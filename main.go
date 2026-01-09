package main

import (
	"demovoice/api"
	"demovoice/storage"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

var (
	uploadDir        string
	outputDir        string
	tempFileLifetime = 2 * time.Hour // Files will be deleted after 2 hours
)

func getExecutableDir() string {
	ex, err := os.Executable()
	if err != nil {
		log.Printf("Warning: Could not get executable path, using current directory: %v", err)
		return "."
	}
	return filepath.Dir(ex)
}


// Initialize global clients
var (
	faceitClient   *api.FaceitClient
	matchClient    *api.MatchClient
	metadataStore  *storage.MetadataStore
	tempFiles      = make(map[string]time.Time) // Track temporary files and their creation time
	tempFilesMutex sync.RWMutex                 // Protect the tempFiles map
	uploadedDemos  = make(map[string]time.Time) // Track uploaded demo files
	demosMutex     sync.RWMutex                 // Protect the uploadedDemos map
)

func init() {
	// Get the directory where the executable is located
	execDir := getExecutableDir()
	uploadDir = filepath.Join(execDir, "uploads")
	outputDir = filepath.Join(execDir, "output")

	log.Printf("Working directory: %s", execDir)
	log.Printf("Upload directory: %s", uploadDir)
	log.Printf("Output directory: %s", outputDir)

	// Load .env file from executable directory
	envPath := filepath.Join(execDir, ".env")
	if err := godotenv.Load(envPath); err != nil {
		log.Printf("Warning: Error loading .env file from %s: %v", envPath, err)
	}

	// Create required directories
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(outputDir, 0755)

	// Get Faceit API keys from environment
	faceitAPIKey := os.Getenv("FACEIT_API_KEY")
	if faceitAPIKey == "" {
		log.Printf("Warning: FACEIT_API_KEY not set in .env file")
	}

	faceitDownloadAPIKey := os.Getenv("FACEIT_DOWNLOAD_API_KEY")
	if faceitDownloadAPIKey == "" {
		log.Printf("Warning: FACEIT_DOWNLOAD_API_KEY not set in .env file - demo download from URLs will not work")
	}

	// Initialize clients
	faceitClient = api.NewFaceitClient(faceitAPIKey, faceitDownloadAPIKey)
	matchClient = api.NewMatchClient()
	metadataStore = storage.NewMetadataStore(outputDir)

	// Don't clean existing files on startup - let TTL handle cleanup
	// This prevents deleting files if server restarts
	// cleanExistingFiles()

	// Start background cleanup routine for temporary files
	go startTempFileCleanup()

	// Start background cleanup for uploaded demos
	go startUploadedDemoCleanup()
}

func main() {
	// Handle routes (removed password auth)
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/api/upload", handleAPIUpload) // JSON API for external services
	http.HandleFunc("/download-from-url", handleDownloadFromURL)
	http.HandleFunc("/faceit/player", handleFaceitPlayer)
	http.HandleFunc("/faceit/match", handleFaceitMatch)
	http.HandleFunc("/status", handleStatus)
	http.Handle("/output/", http.StripPrefix("/output/", corsHandler(http.FileServer(http.Dir(outputDir)))))
	http.Handle("/icons/", http.StripPrefix("/icons/", http.FileServer(http.Dir("./icons"))))

	// Configure server with extended timeouts for large file uploads
	server := &http.Server{
		Addr:         ":9000",
		ReadTimeout:  30 * time.Minute, // Long timeout for large uploads
		WriteTimeout: 30 * time.Minute, // Long timeout for processing
		IdleTimeout:  120 * time.Second,
	}

	fmt.Println("Server started at http://localhost:9000")
	log.Fatal(server.ListenAndServe())
}

func handleFaceitPlayer(w http.ResponseWriter, r *http.Request) {
	steamID := r.URL.Query().Get("steamid")
	if steamID == "" {
		http.Error(w, "Steam ID is required", http.StatusBadRequest)
		return
	}

	// Use our new FaceitClient
	response, err := faceitClient.GetPlayerInfo(steamID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func handleFaceitMatch(w http.ResponseWriter, r *http.Request) {
	matchID := r.URL.Query().Get("matchid")
	if matchID == "" {
		http.Error(w, "Match ID is required", http.StatusBadRequest)
		return
	}

	// Get match data from Faceit API
	response, err := faceitClient.GetMatchData(matchID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// StatusResponse contains the current status of a demo
type StatusResponse struct {
	Status  string           `json:"status"`
	DemoID  string           `json:"demo_id"`
	MatchID string           `json:"match_id"`
	Players []api.PlayerInfo `json:"players"`
	ChatLog string           `json:"chat_log,omitempty"`
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers for API access
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	demoID := r.URL.Query().Get("demo_id")
	if demoID == "" {
		// Try to get from cookie
		demoCookie, err := r.Cookie("current_demo_id")
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Demo ID is required"})
			return
		}
		demoID = demoCookie.Value
	}

	metadata, err := metadataStore.LoadMetadata(demoID)
	if err != nil {
		// Demo not found or still initializing
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StatusResponse{
			Status: "initializing",
			DemoID: demoID,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusResponse{
		Status:  metadata.Status,
		DemoID:  metadata.DemoID,
		MatchID: metadata.MatchID,
		Players: metadata.Players,
		ChatLog: metadata.ChatLog,
	})
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	// Check if demo_id is in the URL (e.g., /?demo_id=123)
	queryDemoID := r.URL.Query().Get("demo_id")
	if queryDemoID != "" {
		// Set the session cookie automatically
		http.SetCookie(w, &http.Cookie{
			Name:     "current_demo_id",
			Value:    queryDemoID,
			Path:     "/",
			MaxAge:   3600 * 2, // 2 hours
			HttpOnly: false,    // Set to false if your JS needs to read it
		})

		// Redirect to / to clean up the URL
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Get current demo ID from session
	currentDemoID := getCurrentDemoID(r)

	var currentDemo *storage.DemoMetadata
	var playersJSON string
	var cachedMatchData string

	if currentDemoID != "" {
		// Try to load metadata for this demo
		metadata, err := metadataStore.LoadMetadata(currentDemoID)
		if err == nil {
			currentDemo = metadata
			// Convert players to JSON for JavaScript
			playersBytes, _ := json.Marshal(metadata.Players)
			playersJSON = string(playersBytes)
			// Pass cached match data if available
			cachedMatchData = metadata.MatchDataJSON
		}
	}

	// Parse template file
	tmpl, err := template.ParseFiles("templates/home.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
		return
	}

	tmpl.Execute(w, struct {
		CurrentDemo     *storage.DemoMetadata
		PlayersJSON     string
		CachedMatchData string
	}{
		CurrentDemo:     currentDemo,
		PlayersJSON:     playersJSON,
		CachedMatchData: cachedMatchData,
	})
}

// Helper to get the demo ID from the session cookie
func getCurrentDemoID(r *http.Request) string {
	demoCookie, err := r.Cookie("current_demo_id")
	if err != nil {
		return ""
	}
	return demoCookie.Value
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the uploaded file
	file, header, err := r.FormFile("demo")
	if err != nil {
		http.Error(w, "Error receiving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Extract match ID from filename for instant team loading
	matchID := storage.ExtractMatchIDFromFilename(header.Filename)

	// Check if we already have this demo processed (cache lookup)
	if matchID != "" {
		existingDemo, err := metadataStore.FindDemoByMatchID(matchID, tempFileLifetime)
		if err == nil && existingDemo != nil {
			log.Printf("🎯 Cache HIT! Reusing existing demo %s for match %s", existingDemo.DemoID, matchID)

			// Set cookie to existing demo
			http.SetCookie(w, &http.Cookie{
				Name:    "current_demo_id",
				Value:   existingDemo.DemoID,
				Path:    "/",
				Expires: time.Now().Add(24 * time.Hour),
			})

			// Redirect immediately - no need to reprocess!
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	// Create a unique ID for this demo upload (cache miss - need to process)
	demoID := fmt.Sprintf("demo_%d", time.Now().UnixNano())
	var matchDataJSON string

	// If we found a match ID, prefetch match data for faster UI loading
	if matchID != "" {
		log.Printf("Cache MISS - Processing new demo for match ID: %s", matchID)
		matchData, err := faceitClient.GetMatchData(matchID)
		if err != nil {
			log.Printf("Warning: Could not prefetch match data: %v", err)
		} else {
			matchDataBytes, _ := json.Marshal(matchData)
			matchDataJSON = string(matchDataBytes)
			log.Printf("Prefetched match data for faster loading")
		}
	}

	// Create temporary file for processing
	tempPath := filepath.Join(uploadDir, header.Filename)
	tempFile, err := os.Create(tempPath)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}
	defer tempFile.Close()

	// Copy uploaded file to temporary location
	_, err = io.Copy(tempFile, file)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}

	// Create initial metadata with "processing" status and cached match data
	initialMetadata := &storage.DemoMetadata{
		DemoID:        demoID,
		Filename:      header.Filename,
		MatchID:       matchID,
		Status:        "processing",
		UploadTime:    time.Now(),
		Players:       []api.PlayerInfo{},
		MatchDataJSON: matchDataJSON,
	}
	metadataStore.UpdateMetadata(initialMetadata)
	// Also register the metadata file so it gets cleaned up
	registerTempFile(demoID + ".json")

	// Set cookie immediately
	http.SetCookie(w, &http.Cookie{
		Name:    "current_demo_id",
		Value:   demoID,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})

	// Process in background
	go func() {
		// Register uploaded demo for cleanup
		registerUploadedDemo(header.Filename)

		// Process the demo file
		playerTeams, err := ProcessDemo(tempPath, demoID)
		if err != nil {
			log.Printf("Error processing demo %s: %v", demoID, err)
			// Update status to failed
			initialMetadata, _ := metadataStore.LoadMetadata(demoID)
			if initialMetadata != nil {
				initialMetadata.Status = "failed"
				metadataStore.UpdateMetadata(initialMetadata)
			}
			return
		}

		// Save demo metadata (scans files and populates players)
		// This will set Status to "completed" as per our change in metadata.go
		metadata, err := metadataStore.SaveMetadata(demoID, header.Filename)
		if err != nil {
			log.Printf("Warning: Failed to save metadata: %v", err)
		} else {
			// Save team information now that metadata exists
			saveTeamMetadata(demoID, playerTeams)

			// Try to fetch match data first if we have a match ID
			var matchData *api.MatchResponse
			if metadata.MatchID != "" {
				log.Printf("Fetching match data for uploaded demo with match ID: %s", metadata.MatchID)
				md, err := faceitClient.GetMatchData(metadata.MatchID)
				if err == nil {
					matchData = md
					// Update MatchDataJSON
					matchDataBytes, _ := json.Marshal(matchData)
					metadata.MatchDataJSON = string(matchDataBytes)
					log.Printf("Successfully enriched uploaded demo with match data")

					// Use match data to enrich players
					metadata.Players = faceitClient.EnrichPlayersFromMatch(metadata.Players, matchData)
				} else {
					log.Printf("Warning: Failed to fetch match data for uploaded demo: %v", err)
				}
			}

			// Enrich player data with Faceit information (nickname, ELO, level)
			// Fallback to individual API calls if nickname is still missing
			for i := range metadata.Players {
				if metadata.Players[i].Nickname == "" {
					if err := faceitClient.EnrichPlayerInfo(&metadata.Players[i]); err != nil {
						log.Printf("Warning: Failed to enrich player %s: %v", metadata.Players[i].SteamID, err)
					}
				}
			}

			// Save enriched metadata
			metadataStore.UpdateMetadata(metadata)

			// Register all generated audio files as temporary
			for _, player := range metadata.Players {
				registerTempFile(player.AudioFile)
			}
		}

		// Clean up uploaded file
		os.Remove(tempPath)
	}()

	// Redirect back to home page immediately
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// corsHandler wraps a handler to add CORS headers
func corsHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		h.ServeHTTP(w, r)
	})
}

// setCORSHeaders adds CORS headers to the response
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// APIUploadResponse is the JSON response for API uploads
type APIUploadResponse struct {
	Success bool   `json:"success"`
	DemoID  string `json:"demo_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleAPIUpload handles demo uploads via JSON API (for external services like faceitgpt.com)
func handleAPIUpload(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	// Handle preflight
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(APIUploadResponse{Success: false, Error: "Method not allowed"})
		return
	}

	// Verify API key
	expectedAPIKey := os.Getenv("API_KEY")
	if expectedAPIKey != "" {
		providedKey := r.Header.Get("X-API-Key")
		if providedKey != expectedAPIKey {
			log.Printf("⚠️  API Upload rejected: Invalid or missing API key")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(APIUploadResponse{Success: false, Error: "Unauthorized: Invalid API key"})
			return
		}
	}

	// Parse the uploaded file
	file, header, err := r.FormFile("demo")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIUploadResponse{Success: false, Error: "Error receiving file: " + err.Error()})
		return
	}
	defer file.Close()

	log.Printf("📥 API Upload received: %s (size: %d bytes)", header.Filename, header.Size)

	// Extract match ID from filename for caching
	matchID := storage.ExtractMatchIDFromFilename(header.Filename)

	// Check cache
	if matchID != "" {
		existingDemo, err := metadataStore.FindDemoByMatchID(matchID, tempFileLifetime)
		if err == nil && existingDemo != nil {
			log.Printf("🎯 API Cache HIT! Reusing existing demo %s for match %s", existingDemo.DemoID, matchID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIUploadResponse{
				Success: true,
				DemoID:  existingDemo.DemoID,
				Status:  existingDemo.Status,
			})
			return
		}
	}

	// Create a unique ID for this demo upload
	demoID := fmt.Sprintf("demo_%d", time.Now().UnixNano())

	// Create temporary file for processing
	tempPath := filepath.Join(uploadDir, header.Filename)
	tempFile, err := os.Create(tempPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIUploadResponse{Success: false, Error: "Error saving file"})
		return
	}
	defer tempFile.Close()

	// Copy uploaded file to temporary location
	_, err = io.Copy(tempFile, file)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIUploadResponse{Success: false, Error: "Error saving file"})
		return
	}

	// Create initial metadata with "processing" status
	initialMetadata := &storage.DemoMetadata{
		DemoID:     demoID,
		Filename:   header.Filename,
		MatchID:    matchID,
		Status:     "processing",
		UploadTime: time.Now(),
		Players:    []api.PlayerInfo{},
	}
	metadataStore.UpdateMetadata(initialMetadata)
	registerTempFile(demoID + ".json")

	log.Printf("📥 API Upload started processing: %s -> %s", header.Filename, demoID)

	// Process in background
	go func() {
		registerUploadedDemo(header.Filename)

		// Process the demo file
		playerTeams, err := ProcessDemo(tempPath, demoID)
		if err != nil {
			log.Printf("❌ API Upload error processing demo %s: %v", demoID, err)
			initialMetadata, _ := metadataStore.LoadMetadata(demoID)
			if initialMetadata != nil {
				initialMetadata.Status = "failed"
				metadataStore.UpdateMetadata(initialMetadata)
			}
			return
		}

		// Save demo metadata
		metadata, err := metadataStore.SaveMetadata(demoID, header.Filename)
		if err != nil {
			log.Printf("Warning: Failed to save metadata: %v", err)
		} else {
			saveTeamMetadata(demoID, playerTeams)

			// Enrich player data
			for i := range metadata.Players {
				if metadata.Players[i].Nickname == "" {
					faceitClient.EnrichPlayerInfo(&metadata.Players[i])
				}
			}
			metadataStore.UpdateMetadata(metadata)

			for _, player := range metadata.Players {
				registerTempFile(player.AudioFile)
			}
		}

		log.Printf("✅ API Upload completed: %s", demoID)
		os.Remove(tempPath)
	}()

	// Return immediately with demo_id for status polling
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(APIUploadResponse{
		Success: true,
		DemoID:  demoID,
		Status:  "processing",
	})
}

func handleDownloadFromURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the matchroom URL from form
	matchroomURL := r.FormValue("matchroom_url")
	if matchroomURL == "" {
		http.Error(w, "Matchroom URL is required", http.StatusBadRequest)
		return
	}

	// Extract match ID from URL
	matchID := extractMatchIDFromURL(matchroomURL)
	if matchID == "" {
		http.Error(w, "Invalid matchroom URL format", http.StatusBadRequest)
		return
	}

	// Check if we already have this demo processed (cache lookup)
	existingDemo, err := metadataStore.FindDemoByMatchID(matchID, tempFileLifetime)
	if err == nil && existingDemo != nil {
		log.Printf("🎯 Cache HIT! Reusing existing demo %s for match %s", existingDemo.DemoID, matchID)

		// Set cookie to existing demo
		http.SetCookie(w, &http.Cookie{
			Name:    "current_demo_id",
			Value:   existingDemo.DemoID,
			Path:    "/",
			Expires: time.Now().Add(24 * time.Hour),
		})

		// Redirect immediately - no need to reprocess!
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	log.Printf("Cache MISS - Starting async download for match ID: %s", matchID)

	// Create a unique demo ID
	demoID := fmt.Sprintf("demo_%d", time.Now().UnixNano())
	demoFilename := fmt.Sprintf("%s.dem.zst", matchID)

	// Fetch match data immediately for faster UI loading
	var matchDataJSON string
	matchData, err := faceitClient.GetMatchData(matchID)
	if err != nil {
		log.Printf("Warning: Could not prefetch match data: %v", err)
	} else {
		matchDataBytes, _ := json.Marshal(matchData)
		matchDataJSON = string(matchDataBytes)
		log.Printf("Prefetched match data for faster loading")
	}

	// Create initial metadata with "processing" status and cached match data
	initialMetadata := &storage.DemoMetadata{
		DemoID:        demoID,
		MatchID:       matchID,
		Filename:      demoFilename,
		Status:        "processing",
		UploadTime:    time.Now(),
		Players:       []api.PlayerInfo{},
		MatchDataJSON: matchDataJSON,
	}
	metadataStore.UpdateMetadata(initialMetadata)
	registerTempFile(demoID + ".json")

	// Set cookie immediately
	http.SetCookie(w, &http.Cookie{
		Name:    "current_demo_id",
		Value:   demoID,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})

	// Process in background
	go func() {
		// Download the demo file
		demoPath := filepath.Join(uploadDir, demoFilename)
		err := faceitClient.DownloadDemo(matchID, demoPath)
		if err != nil {
			log.Printf("Error downloading demo %s: %v", matchID, err)
			// Update status to failed
			initialMetadata.Status = "failed"
			metadataStore.UpdateMetadata(initialMetadata)
			return
		}

		log.Printf("Demo downloaded successfully to: %s", demoPath)

		// Register the downloaded demo for cleanup
		registerUploadedDemo(demoFilename)

		// Process the demo file
		playerTeams, err := ProcessDemo(demoPath, demoID)
		if err != nil {
			log.Printf("Error processing demo: %v", err)
			// Update status to failed
			initialMetadata.Status = "failed"
			metadataStore.UpdateMetadata(initialMetadata)
			// Clean up the downloaded file
			os.Remove(demoPath)
			return
		}

		// Save demo metadata
		metadata, err := metadataStore.SaveMetadata(demoID, demoFilename)
		if err != nil {
			log.Printf("Warning: Failed to save metadata: %v", err)
		} else {
			// Restore MatchID if missing (though Filename should have it)
			if metadata.MatchID == "" {
				metadata.MatchID = matchID
				metadataStore.UpdateMetadata(metadata)
			}

			// Save team information now that metadata exists
			saveTeamMetadata(demoID, playerTeams)

			// Enrich player data with Faceit information (nickname, ELO, level)
			// Use existing matchData if available
			if matchData != nil {
				metadata.Players = faceitClient.EnrichPlayersFromMatch(metadata.Players, matchData)
			}

			for i := range metadata.Players {
				if metadata.Players[i].Nickname == "" {
					if err := faceitClient.EnrichPlayerInfo(&metadata.Players[i]); err != nil {
						log.Printf("Warning: Failed to enrich player %s: %v", metadata.Players[i].SteamID, err)
					}
				}
			}

			// Save enriched metadata
			metadataStore.UpdateMetadata(metadata)

			// Register all generated audio files as temporary
			for _, player := range metadata.Players {
				registerTempFile(player.AudioFile)
			}
		}

		log.Printf("Demo processing complete for match: %s", matchID)
	}()

	// Redirect back to home page immediately
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// extractMatchIDFromURL extracts the match ID from a Faceit matchroom URL
func extractMatchIDFromURL(url string) string {
	// Remove trailing slashes
	url = strings.TrimRight(url, "/")

	// Split by "room/"
	parts := strings.Split(url, "room/")
	if len(parts) < 2 {
		return ""
	}

	// Get the part after "room/"
	roomPart := parts[1]

	// Remove any path after the match ID (like /scoreboard)
	roomPart = strings.Split(roomPart, "/")[0]

	// Return full match ID including the "1-" prefix - Faceit API needs it
	return roomPart
}

// startTempFileCleanup runs a background goroutine that deletes old temporary files
func startTempFileCleanup() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cleanupExpiredFiles()
		}
	}
}

// cleanupExpiredFiles removes files older than tempFileLifetime
func cleanupExpiredFiles() {
	tempFilesMutex.Lock()
	defer tempFilesMutex.Unlock()

	now := time.Now()
	for filename, creationTime := range tempFiles {
		if now.Sub(creationTime) > tempFileLifetime {
			filePath := filepath.Join(outputDir, filename)

			// Delete the audio file
			if err := os.Remove(filePath); err != nil {
				log.Printf("Warning: Failed to delete temporary file %s: %v", filename, err)
			} else {
				log.Printf("Deleted temporary file: %s (age: %v)", filename, now.Sub(creationTime))
			}

			// Delete associated metadata file if it exists
			if strings.HasSuffix(filename, ".wav") {
				demoID := extractDemoIDFromFilename(filename)
				if demoID != "" {
					metadataPath := filepath.Join(outputDir, demoID+".json")
					os.Remove(metadataPath) // Ignore error, file might not exist
				}
			}

			// Remove from tracking
			delete(tempFiles, filename)
		}
	}
}

// extractDemoIDFromFilename extracts demo ID from filename like "steamid_demo_timestamp.wav"
func extractDemoIDFromFilename(filename string) string {
	parts := strings.Split(filename, "_")
	if len(parts) >= 3 {
		// Find the part that starts with "demo_"
		for i, part := range parts {
			if part == "demo" && i+1 < len(parts) {
				return "demo_" + parts[i+1]
			}
		}
	}
	return ""
}

// registerTempFile adds a file to the temporary files tracking
func registerTempFile(filename string) {
	tempFilesMutex.Lock()
	defer tempFilesMutex.Unlock()
	tempFiles[filename] = time.Now()
	log.Printf("Registered temporary file: %s (will be deleted in %v)", filename, tempFileLifetime)
}

// cleanExistingFiles removes all existing files in the output directory on startup
func cleanExistingFiles() {
	files, err := os.ReadDir(outputDir)
	if err != nil {
		log.Printf("Warning: Could not read output directory for cleanup: %v", err)
		return
	}

	deletedCount := 0
	for _, file := range files {
		if !file.IsDir() {
			filePath := filepath.Join(outputDir, file.Name())
			if err := os.Remove(filePath); err != nil {
				log.Printf("Warning: Failed to delete existing file %s: %v", file.Name(), err)
			} else {
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		log.Printf("Cleaned up %d existing files from output directory", deletedCount)
	}
}

// registerUploadedDemo adds a demo file to the tracking for auto-cleanup
func registerUploadedDemo(filename string) {
	demosMutex.Lock()
	defer demosMutex.Unlock()
	uploadedDemos[filename] = time.Now()
	log.Printf("Registered uploaded demo: %s (will be deleted in %v)", filename, tempFileLifetime)
}

// startUploadedDemoCleanup runs a background goroutine that deletes old demo files
func startUploadedDemoCleanup() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cleanupExpiredDemos()
		}
	}
}

// cleanupExpiredDemos removes uploaded demo files older than tempFileLifetime
func cleanupExpiredDemos() {
	demosMutex.Lock()
	defer demosMutex.Unlock()

	now := time.Now()
	for filename, uploadTime := range uploadedDemos {
		if now.Sub(uploadTime) > tempFileLifetime {
			filePath := filepath.Join(uploadDir, filename)

			if err := os.Remove(filePath); err != nil {
				log.Printf("Warning: Failed to delete demo file %s: %v", filename, err)
			} else {
				log.Printf("Deleted uploaded demo: %s (age: %v)", filename, now.Sub(uploadTime))
			}

			// Remove from tracking
			delete(uploadedDemos, filename)
		}
	}
}
