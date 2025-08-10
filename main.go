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
)

const (
	uploadDir = "./uploads"
	outputDir = "./output"
	tempFileLifetime = 5 * time.Minute // Files will be deleted after 5 minutes
)

// Initialize global clients
var (
	faceitClient  *api.FaceitClient
	matchClient   *api.MatchClient
	metadataStore *storage.MetadataStore
	tempFiles     = make(map[string]time.Time) // Track temporary files and their creation time
	tempFilesMutex sync.RWMutex                // Protect the tempFiles map
)

func init() {
	// Create required directories
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(outputDir, 0755)

	// Initialize clients
	faceitClient = api.NewFaceitClient()
	matchClient = api.NewMatchClient()
	metadataStore = storage.NewMetadataStore(outputDir)

	// Clean existing files in output directory on startup
	cleanExistingFiles()

	// Start background cleanup routine for temporary files
	go startTempFileCleanup()
}

func main() {
	// Handle routes (removed password auth)
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/faceit/player", handleFaceitPlayer)
	http.Handle("/output/", http.StripPrefix("/output/", http.FileServer(http.Dir(outputDir))))

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

func handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := `
    <!DOCTYPE html>
    <html>
    <head>
        <title>CS2 Voice Extractor</title>
        <style>
            * {
                margin: 0;
                padding: 0;
                box-sizing: border-box;
            }
            body {
                font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
                background: linear-gradient(135deg, #0f1419 0%, #1a1f2e 100%);
                color: #e2e8f0;
                min-height: 100vh;
                line-height: 1.6;
            }
            .container {
                max-width: 1400px;
                margin: 0 auto;
                padding: 2rem;
            }
            .header {
                text-align: center;
                margin-bottom: 3rem;
            }
            .header h1 {
                font-size: 2.5rem;
                font-weight: 700;
                background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
                -webkit-background-clip: text;
                -webkit-text-fill-color: transparent;
                background-clip: text;
                margin-bottom: 0.5rem;
            }
            .header p {
                color: #94a3b8;
                font-size: 1.1rem;
            }
            .upload-section {
                background: rgba(30, 41, 59, 0.6);
                backdrop-filter: blur(20px);
                border: 1px solid rgba(148, 163, 184, 0.1);
                border-radius: 16px;
                padding: 2rem;
                margin-bottom: 3rem;
                box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.3);
            }
            .upload-form {
                display: flex;
                gap: 1rem;
                align-items: center;
                flex-wrap: wrap;
            }
            .file-input {
                flex: 1;
                min-width: 200px;
                padding: 0.75rem 1rem;
                background: rgba(51, 65, 85, 0.8);
                border: 2px dashed rgba(148, 163, 184, 0.3);
                border-radius: 8px;
                color: #e2e8f0;
                cursor: pointer;
                transition: all 0.3s ease;
            }
            .file-input:hover {
                border-color: #667eea;
                background: rgba(51, 65, 85, 1);
            }
            .btn-primary {
                background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
                color: white;
                padding: 0.75rem 1.5rem;
                border: none;
                border-radius: 8px;
                font-weight: 600;
                cursor: pointer;
                transition: all 0.3s ease;
                box-shadow: 0 4px 15px rgba(102, 126, 234, 0.3);
            }
            .btn-primary:hover {
                transform: translateY(-2px);
                box-shadow: 0 8px 25px rgba(102, 126, 234, 0.4);
            }
            .btn-primary:disabled {
                background: #64748b;
                cursor: not-allowed;
                transform: none;
                box-shadow: none;
            }
            .teams-container {
                display: grid;
                grid-template-columns: repeat(auto-fit, minmax(400px, 1fr));
                gap: 2rem;
                margin-top: 2rem;
            }
            .team-section {
                background: rgba(30, 41, 59, 0.4);
                backdrop-filter: blur(10px);
                border: 1px solid rgba(148, 163, 184, 0.1);
                border-radius: 12px;
                padding: 1.5rem;
                box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.2);
            }
            .team-header {
                display: flex;
                align-items: center;
                gap: 0.75rem;
                margin-bottom: 1.5rem;
                padding-bottom: 0.75rem;
                border-bottom: 2px solid rgba(148, 163, 184, 0.1);
            }
            .team-icon {
                width: 12px;
                height: 12px;
                border-radius: 50%;
            }
            .team-1 .team-icon {
                background: linear-gradient(135deg, #06b6d4, #0891b2);
            }
            .team-2 .team-icon {
                background: linear-gradient(135deg, #f59e0b, #d97706);
            }
            .unassigned .team-icon {
                background: linear-gradient(135deg, #6b7280, #4b5563);
            }
            .team-title {
                font-size: 1.25rem;
                font-weight: 600;
                color: #f1f5f9;
            }
            .players-grid {
                display: grid;
                gap: 1rem;
            }
            .player-card {
                background: rgba(51, 65, 85, 0.3);
                border: 1px solid rgba(148, 163, 184, 0.1);
                border-radius: 10px;
                padding: 1.25rem;
                transition: all 0.3s ease;
            }
            .player-card:hover {
                transform: translateY(-2px);
                background: rgba(51, 65, 85, 0.5);
                border-color: rgba(102, 126, 234, 0.3);
                box-shadow: 0 10px 25px rgba(0, 0, 0, 0.2);
            }
            .player-header {
                display: flex;
                align-items: center;
                gap: 0.75rem;
                margin-bottom: 1rem;
            }
            .faceit-level {
                width: 32px;
                height: 32px;
                border-radius: 6px;
                display: flex;
                align-items: center;
                justify-content: center;
                color: white;
                font-weight: 700;
                font-size: 0.875rem;
                box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
            }
            .player-info {
                flex: 1;
            }
            .faceit-nickname {
                font-weight: 600;
                font-size: 1.1rem;
                color: #f1f5f9;
                margin-bottom: 0.25rem;
            }
            .steam-id {
                color: #94a3b8;
                font-size: 0.8rem;
                font-family: 'Monaco', 'Menlo', monospace;
            }
            .faceit-elo {
                color: #cbd5e1;
                font-size: 0.875rem;
                margin-top: 0.25rem;
            }
            .audio-controls {
                width: 100%;
                margin-top: 1rem;
                border-radius: 6px;
                background: rgba(30, 41, 59, 0.6);
            }
            .audio-controls::-webkit-media-controls-panel {
                background: rgba(30, 41, 59, 0.8);
            }
            .demo-info {
                background: rgba(30, 41, 59, 0.6);
                border: 1px solid rgba(148, 163, 184, 0.1);
                border-radius: 12px;
                padding: 1.5rem;
                margin-bottom: 2rem;
                backdrop-filter: blur(10px);
            }
            .demo-info h3 {
                color: #f1f5f9;
                margin-bottom: 1rem;
                font-size: 1.2rem;
                font-weight: 600;
            }
            .no-demo {
                text-align: center;
                padding: 4rem 2rem;
                color: #64748b;
                background: rgba(30, 41, 59, 0.4);
                border-radius: 12px;
                border: 2px dashed rgba(148, 163, 184, 0.2);
            }
            .no-demo h3 {
                font-size: 1.5rem;
                margin-bottom: 0.5rem;
                color: #94a3b8;
            }
            .loading-placeholder {
                color: #94a3b8;
                font-style: italic;
                animation: pulse 2s infinite;
            }
            @keyframes pulse {
                0%, 100% { opacity: 1; }
                50% { opacity: 0.5; }
            }
            .processing-overlay {
                position: fixed;
                top: 0;
                left: 0;
                width: 100%;
                height: 100%;
                background: rgba(15, 20, 25, 0.95);
                backdrop-filter: blur(10px);
                display: none;
                justify-content: center;
                align-items: center;
                z-index: 1000;
                flex-direction: column;
            }
            .spinner {
                width: 60px;
                height: 60px;
                border: 4px solid rgba(102, 126, 234, 0.2);
                border-top: 4px solid #667eea;
                border-radius: 50%;
                animation: spin 1s linear infinite;
                margin-bottom: 1.5rem;
            }
            @keyframes spin {
                0% { transform: rotate(0deg); }
                100% { transform: rotate(360deg); }
            }
            .progress-text {
                color: #e2e8f0;
                font-size: 1.1rem;
                text-align: center;
                margin: 0.5rem 0;
            }
        </style>
    </head>
    <body>
        <div class="container">
            <div class="header">
                <h1>CS2 Voice Extractor</h1>
                <p>Extract and analyze voice communications from Counter-Strike 2 demo files</p>
            </div>
            
            <div class="upload-section">
                <form action="/upload" method="post" enctype="multipart/form-data" id="uploadForm" class="upload-form">
                    <input type="file" name="demo" accept=".dem" required id="demoFile" class="file-input">
                    <button type="submit" class="btn-primary" id="submitButton">Extract Voices</button>
                </form>
            </div>

            {{if .CurrentDemo}}
            <div class="demo-info">
                <h3>📁 Current Demo: {{.CurrentDemo.Filename}}</h3>
                <p style="color: #fbbf24; font-size: 0.9rem; margin-top: 0.5rem;">
                    ⏱️ Voice files are temporary and will be automatically deleted after 5 minutes
                </p>
            </div>

            <div class="teams-container">
                <!-- Team 1 Section -->
                <div class="team-section team-1">
                    <div class="team-header">
                        <div class="team-icon"></div>
                        <h2 class="team-title">Team 1 ({{len .Team1Players}})</h2>
                    </div>
                    <div class="players-grid">
                        {{range .Team1Players}}
                        <div class="player-card" id="player-{{.SteamID}}">
                            <div class="player-header">
                                <div class="faceit-level" style="background: #6b7280;">-</div>
                                <div class="player-info">
                                    <div class="faceit-nickname loading-placeholder">Loading...</div>
                                    <div class="steam-id">{{.SteamID}}</div>
                                    <div class="faceit-elo"></div>
                                </div>
                            </div>
                            <audio controls class="audio-controls">
                                <source src="/output/{{.AudioFile}}" type="audio/wav">
                                Your browser does not support the audio element.
                            </audio>
                        </div>
                        {{end}}
                    </div>
                </div>

                <!-- Team 2 Section -->
                <div class="team-section team-2">
                    <div class="team-header">
                        <div class="team-icon"></div>
                        <h2 class="team-title">Team 2 ({{len .Team2Players}})</h2>
                    </div>
                    <div class="players-grid">
                        {{range .Team2Players}}
                        <div class="player-card" id="player-{{.SteamID}}">
                            <div class="player-header">
                                <div class="faceit-level" style="background: #6b7280;">-</div>
                                <div class="player-info">
                                    <div class="faceit-nickname loading-placeholder">Loading...</div>
                                    <div class="steam-id">{{.SteamID}}</div>
                                    <div class="faceit-elo"></div>
                                </div>
                            </div>
                            <audio controls class="audio-controls">
                                <source src="/output/{{.AudioFile}}" type="audio/wav">
                                Your browser does not support the audio element.
                            </audio>
                        </div>
                        {{end}}
                    </div>
                </div>

                <!-- Unassigned Players Section -->
                {{if .UnassignedPlayers}}
                <div class="team-section unassigned" style="grid-column: 1 / -1;">
                    <div class="team-header">
                        <div class="team-icon"></div>
                        <h2 class="team-title">Unassigned Players ({{len .UnassignedPlayers}})</h2>
                    </div>
                    <div class="players-grid" style="grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));">
                        {{range .UnassignedPlayers}}
                        <div class="player-card" id="player-{{.SteamID}}">
                            <div class="player-header">
                                <div class="faceit-level" style="background: #6b7280;">-</div>
                                <div class="player-info">
                                    <div class="faceit-nickname loading-placeholder">Loading...</div>
                                    <div class="steam-id">{{.SteamID}}</div>
                                    <div class="faceit-elo"></div>
                                </div>
                            </div>
                            <audio controls class="audio-controls">
                                <source src="/output/{{.AudioFile}}" type="audio/wav">
                                Your browser does not support the audio element.
                            </audio>
                        </div>
                        {{end}}
                    </div>
                </div>
                {{end}}
            </div>
            {{else}}
            <div class="no-demo">
                <h3>🎯 Ready to Extract</h3>
                <p>Upload a CS2 demo file (.dem) to extract voice communications and see player analysis</p>
            </div>
            {{end}}
        </div>

        <!-- Processing overlay -->
        <div class="processing-overlay" id="processingOverlay">
            <div class="spinner"></div>
            <div class="progress-text">Processing demo file...</div>
            <div class="progress-text" id="progressDetail">This may take a few minutes for large files</div>
        </div>

        <script>
        function getLevelColor(level) {
            const colors = {
                1: '#EEE', 2: '#4CAF50', 3: '#8BC34A',
                4: '#CDDC39', 5: '#FFC107', 6: '#FF9800',
                7: '#FF5722', 8: '#F44336', 9: '#E91E63',
                10: '#9C27B0'
            };
            return colors[level] || '#999';
        }

        document.addEventListener('DOMContentLoaded', function() {
            const uploadForm = document.getElementById('uploadForm');
            const processingOverlay = document.getElementById('processingOverlay');
            const submitButton = document.getElementById('submitButton');
            const demoFileInput = document.getElementById('demoFile');
            const progressDetail = document.getElementById('progressDetail');

            // Show loading overlay when form is submitted
            uploadForm.addEventListener('submit', function(e) {
                if (demoFileInput.files.length > 0) {
                    const fileSize = demoFileInput.files[0].size;
                    const fileSizeMB = (fileSize / (1024 * 1024)).toFixed(2);

                    processingOverlay.style.display = 'flex';
                    submitButton.disabled = true;

                    progressDetail.textContent = "Processing " + fileSizeMB + " MB demo file. This may take " + (fileSizeMB > 100 ? "several minutes" : "a few minutes") + ".";

                    // Add status update every 5 seconds
                    let seconds = 0;
                    const processingTimer = setInterval(function() {
                        seconds += 5;
                        progressDetail.textContent = "Still processing... (" + seconds + "s elapsed)";
                    }, 5000);

                    // Store timer in sessionStorage so we can clear it if page reloads
                    sessionStorage.setItem('processingTimer', processingTimer);
                }
            });

            // Check if we just came back from processing (page reload)
            if (sessionStorage.getItem('processing') === 'true') {
                processingOverlay.style.display = 'none';
                sessionStorage.removeItem('processing');

                // Clear any existing timer
                const oldTimer = sessionStorage.getItem('processingTimer');
                if (oldTimer) {
                    clearInterval(parseInt(oldTimer));
                    sessionStorage.removeItem('processingTimer');
                }
            }

            // Load Faceit data for each player
            const players = document.querySelectorAll('.player-card');
            players.forEach(function(playerCard) {
                const steamId = playerCard.id.split('-')[1];
                fetch('/faceit/player?steamid=' + steamId)
                    .then(response => response.json())
                    .then(data => {
                        if (data.payload && data.payload.length > 0) {
                            const player = data.payload[0];
                            const cs2Data = player.games.cs2;
                            if (cs2Data) {
                                const level = cs2Data.skill_level;
                                const elo = cs2Data.faceit_elo;
                                const nickname = player.nickname;

                                playerCard.querySelector('.faceit-nickname').textContent = nickname;
                                playerCard.querySelector('.faceit-level').textContent = level;
                                playerCard.querySelector('.faceit-level').style.background = getLevelColor(level);
                                playerCard.querySelector('.faceit-elo').textContent = elo + ' ELO';
                            }
                        } else {
                            playerCard.querySelector('.faceit-nickname').textContent = 'Player not found';
                            playerCard.querySelector('.faceit-level').textContent = '-';
                        }
                    })
                    .catch(error => {
                        console.error('Error fetching player data:', error);
                        playerCard.querySelector('.faceit-nickname').textContent = 'Error loading data';
                    });
            });
        });
        </script>
    </body>
    </html>
    `

	// Get current demo ID from session
	currentDemoID := getCurrentDemoID(r)

	var currentDemo *storage.DemoMetadata
	var team1Players []api.PlayerInfo
	var team2Players []api.PlayerInfo
	var unassignedPlayers []api.PlayerInfo

	if currentDemoID != "" {
		// Try to load metadata for this demo
		metadata, err := metadataStore.LoadMetadata(currentDemoID)
		if err == nil {
			currentDemo = metadata

			// Separate players by team
			for _, player := range metadata.Players {
				if player.Team == "Team 1" {
					team1Players = append(team1Players, player)
				} else if player.Team == "Team 2" {
					team2Players = append(team2Players, player)
				} else {
					unassignedPlayers = append(unassignedPlayers, player)
				}
			}
		}
	}

	t := template.Must(template.New("home").Parse(tmpl))
	t.Execute(w, struct {
		CurrentDemo       *storage.DemoMetadata
		Team1Players      []api.PlayerInfo
		Team2Players      []api.PlayerInfo
		UnassignedPlayers []api.PlayerInfo
	}{
		CurrentDemo:       currentDemo,
		Team1Players:      team1Players,
		Team2Players:      team2Players,
		UnassignedPlayers: unassignedPlayers,
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