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

const (
	uploadDir = "./uploads"
	outputDir = "./output"
	tempFileLifetime = 5 * time.Minute // Files will be deleted after 5 minutes
)

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
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: Error loading .env file: %v", err)
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

	// Clean existing files in output directory on startup
	cleanExistingFiles()

	// Start background cleanup routine for temporary files
	go startTempFileCleanup()

	// Start background cleanup for uploaded demos
	go startUploadedDemoCleanup()
}

func main() {
	// Handle routes (removed password auth)
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/download-from-url", handleDownloadFromURL)
	http.HandleFunc("/faceit/player", handleFaceitPlayer)
	http.HandleFunc("/faceit/match", handleFaceitMatch)
	http.Handle("/output/", http.StripPrefix("/output/", http.FileServer(http.Dir(outputDir))))
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

func handleHome(w http.ResponseWriter, r *http.Request) {
	// Get current demo ID from session
	currentDemoID := getCurrentDemoID(r)

	var currentDemo *storage.DemoMetadata
	var playersJSON string

	if currentDemoID != "" {
		// Try to load metadata for this demo
		metadata, err := metadataStore.LoadMetadata(currentDemoID)
		if err == nil {
			currentDemo = metadata
			// Convert players to JSON for JavaScript
			playersBytes, _ := json.Marshal(metadata.Players)
			playersJSON = string(playersBytes)
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
		CurrentDemo *storage.DemoMetadata
		PlayersJSON template.JS
	}{
		CurrentDemo: currentDemo,
		PlayersJSON: template.JS(playersJSON),
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

	// Create a unique ID for this demo upload (using a simple timestamp)
	demoID := fmt.Sprintf("demo_%d", time.Now().UnixNano())

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

	// Register uploaded demo for cleanup
	registerUploadedDemo(header.Filename)

	// Process the demo file with the demo ID
	err = ProcessDemo(tempPath, demoID)
	if err != nil {
		http.Error(w, "Error processing demo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Save demo metadata using our new metadata store
	metadata, err := metadataStore.SaveMetadata(demoID, header.Filename)
	if err != nil {
		log.Printf("Warning: Failed to save metadata: %v", err)
	} else {
		// Register all generated audio files as temporary
		for _, player := range metadata.Players {
			registerTempFile(player.AudioFile)
		}
		
		// Also register the metadata file itself
		registerTempFile(demoID + ".json")
	}

	// Set a cookie to remember which demo was uploaded
	http.SetCookie(w, &http.Cookie{
		Name:    "current_demo_id",
		Value:   demoID,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})

	// Clean up
	os.Remove(tempPath)

	// Redirect back to home page
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	// URL format: https://www.faceit.com/en/cs2/room/1-MATCH_ID
	matchID := extractMatchIDFromURL(matchroomURL)
	if matchID == "" {
		http.Error(w, "Invalid matchroom URL format", http.StatusBadRequest)
		return
	}

	log.Printf("Downloading demo for match ID: %s", matchID)

	// Create a unique demo ID
	demoID := fmt.Sprintf("demo_%d", time.Now().UnixNano())

	// Download the demo file (CS2 demos are .dem.zst compressed)
	demoPath := filepath.Join(uploadDir, fmt.Sprintf("%s.dem.zst", matchID))
	err := faceitClient.DownloadDemo(matchID, demoPath)
	if err != nil {
		log.Printf("Error downloading demo: %v", err)
		http.Error(w, "Error downloading demo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Demo downloaded successfully to: %s", demoPath)

	// Register the downloaded demo for cleanup
	registerUploadedDemo(fmt.Sprintf("%s.dem.zst", matchID))

	// Process the demo file
	err = ProcessDemo(demoPath, demoID)
	if err != nil {
		log.Printf("Error processing demo: %v", err)
		http.Error(w, "Error processing demo: "+err.Error(), http.StatusInternalServerError)
		// Clean up the downloaded file
		os.Remove(demoPath)
		return
	}

	// Save demo metadata
	metadata, err := metadataStore.SaveMetadata(demoID, matchID+".dem.zst")
	if err != nil {
		log.Printf("Warning: Failed to save metadata: %v", err)
	} else {
		// Store the match ID in metadata for reference
		metadata.MatchID = matchID

		// Register all generated audio files as temporary
		for _, player := range metadata.Players {
			registerTempFile(player.AudioFile)
		}

		// Also register the metadata file itself
		registerTempFile(demoID + ".json")
	}

	// Set a cookie to remember which demo was downloaded
	http.SetCookie(w, &http.Cookie{
		Name:    "current_demo_id",
		Value:   demoID,
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	})

	log.Printf("Demo processing complete for match: %s", matchID)

	// Redirect back to home page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// extractMatchIDFromURL extracts the match ID from a Faceit matchroom URL
// Supports formats like:
// - https://www.faceit.com/en/cs2/room/1-MATCH_ID
// - https://www.faceit.com/en/cs2/room/1-MATCH_ID/scoreboard
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

	// The format is "1-MATCH_ID", so we need to remove the "1-" prefix
	if strings.HasPrefix(roomPart, "1-") {
		return strings.TrimPrefix(roomPart, "1-")
	}

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