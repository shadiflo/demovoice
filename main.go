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

	"time"
)

const (
	uploadDir = "./uploads"
	outputDir = "./output"
	password  = "faceit" // Set your password here
)

// Initialize global clients
var (
	faceitClient  *api.FaceitClient
	matchClient   *api.MatchClient
	metadataStore *storage.MetadataStore
)

func init() {
	// Create required directories
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(outputDir, 0755)

	// Initialize clients
	faceitClient = api.NewFaceitClient()
	matchClient = api.NewMatchClient()
	metadataStore = storage.NewMetadataStore(outputDir)
}

func main() {
	// Handle routes
	http.HandleFunc("/", passwordAuth(handleHome))
	http.HandleFunc("/upload", passwordAuth(handleUpload))
	http.HandleFunc("/faceit/player", handleFaceitPlayer)
	http.HandleFunc("/api/match", passwordAuth(handleMatchInfo))
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

// Modified auth function that only checks password (no username)
func passwordAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check for password in the query string
		providedPassword := r.URL.Query().Get("pw")

		// If password not in query, check cookies
		if providedPassword == "" {
			cookie, err := r.Cookie("auth_password")
			if err == nil {
				providedPassword = cookie.Value
			}
		}

		if providedPassword != password {
			// Display password-only form
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `
				<!DOCTYPE html>
				<html>
				<head>
					<title>Password Required</title>
					<style>
						body {
							font-family: Arial, sans-serif;
							max-width: 400px;
							margin: 100px auto;
							padding: 20px;
							background: #1a1a1a;
							color: #ffffff;
							text-align: center;
						}
						.password-form {
							background: #2d2d2d;
							border-radius: 8px;
							padding: 20px;
							box-shadow: 0 2px 4px rgba(0,0,0,0.2);
						}
						.button {
							background: #4CAF50;
							color: white;
							padding: 10px 20px;
							border: none;
							border-radius: 4px;
							cursor: pointer;
							margin-top: 10px;
							width: 100%;
						}
						input[type="password"] {
							width: 100%;
							padding: 10px;
							margin: 10px 0;
							border-radius: 4px;
							border: 1px solid #444;
							background: #333;
							color: white;
							box-sizing: border-box;
						}
					</style>
				</head>
				<body>
					<div class="password-form">
						<h2>Enter Password</h2>
						<form method="GET">
							<input type="password" name="pw" placeholder="Password" required>
							<input type="submit" value="Login" class="button">
						</form>
					</div>
				</body>
				</html>
			`)
			return
		}

		// Set a cookie with the password to maintain the session
		http.SetCookie(w, &http.Cookie{
			Name:     "auth_password",
			Value:    providedPassword,
			Path:     "/",
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: true,
		})

		handler(w, r)
	}
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

// New handler for match information
func handleMatchInfo(w http.ResponseWriter, r *http.Request) {
	matchID := r.URL.Query().Get("match_id")
	demoID := r.URL.Query().Get("demo_id")

	if matchID == "" || demoID == "" {
		http.Error(w, "Both match_id and demo_id are required", http.StatusBadRequest)
		return
	}

	// Get match information
	matchInfo, err := matchClient.GetMatchInfoByID(matchID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting match info: %v", err), http.StatusInternalServerError)
		return
	}

	// Update metadata with match information
	err = metadataStore.EnrichMetadataWithMatchInfo(demoID, matchID, matchInfo)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error updating metadata: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(matchInfo)
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := `
    <!DOCTYPE html>
    <html>
    <head>
        <title>CS2 Voice Extractor</title>
        <style>
            body {
                font-family: Arial, sans-serif;
                max-width: 1200px;
                margin: 20px auto;
                padding: 0 20px;
                background: #1a1a1a;
                color: #ffffff;
            }
            .upload-box {
                background: #2d2d2d;
                border-radius: 8px;
                padding: 20px;
                box-shadow: 0 2px 4px rgba(0,0,0,0.2);
                margin-bottom: 20px;
            }
            .button {
                background: #4CAF50;
                color: white;
                padding: 10px 20px;
                border: none;
                border-radius: 4px;
                cursor: pointer;
                transition: background 0.3s;
            }
            .button:hover {
                background: #45a049;
            }
            .button:disabled {
                background: #666;
                cursor: not-allowed;
            }
            .players-grid {
                display: grid;
                grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
                gap: 15px;
                margin-top: 20px;
            }
            .player-card {
                background: #363636;
                border-radius: 8px;
                padding: 15px;
                box-shadow: 0 2px 4px rgba(0,0,0,0.2);
            }
            .player-info {
                display: flex;
                align-items: center;
                gap: 10px;
                margin-bottom: 10px;
            }
            .faceit-level {
                width: 24px;
                height: 24px;
                border-radius: 50%;
                display: flex;
                align-items: center;
                justify-content: center;
                color: white;
                font-weight: bold;
                font-size: 12px;
            }
            .audio-controls {
                width: 100%;
                margin-top: 10px;
                filter: invert(0.8);
            }
            .steam-id {
                color: #999;
                font-size: 0.8em;
            }
            .faceit-nickname {
                font-weight: bold;
                font-size: 1.1em;
                color: #ffffff;
            }
            .faceit-elo {
                color: #bbb;
                font-size: 0.9em;
            }
            .team-label {
                display: inline-block;
                padding: 2px 6px;
                border-radius: 3px;
                font-size: 0.8em;
                margin-top: 5px;
            }
            .team-1 {
                background: #3498db;
                color: white;
            }
            .team-2 {
                background: #e74c3c;
                color: white;
            }
            .loading {
                color: #999;
                font-style: italic;
            }
            input[type="file"] {
                background: #363636;
                color: #ffffff;
                padding: 10px;
                border-radius: 4px;
                margin-right: 10px;
            }
            .demo-info {
                margin-bottom: 15px;
                padding: 8px 12px;
                background: #444;
                border-radius: 4px;
            }
            .no-demo {
                text-align: center;
                padding: 30px;
                color: #888;
                font-style: italic;
            }
            .processing-overlay {
                position: fixed;
                top: 0;
                left: 0;
                width: 100%;
                height: 100%;
                background: rgba(0, 0, 0, 0.8);
                display: none;
                justify-content: center;
                align-items: center;
                z-index: 1000;
                flex-direction: column;
            }
            .spinner {
                border: 5px solid rgba(0, 0, 0, 0.1);
                width: 50px;
                height: 50px;
                border-radius: 50%;
                border-left-color: #4CAF50;
                animation: spin 1s linear infinite;
                margin-bottom: 20px;
            }
            @keyframes spin {
                0% { transform: rotate(0deg); }
                100% { transform: rotate(360deg); }
            }
            .progress-text {
                color: white;
                margin-top: 15px;
                font-size: 16px;
            }
            .team-section {
                margin-top: 15px;
                border-top: 1px solid #444;
                padding-top: 10px;
            }
            .match-info {
                background: #333;
                padding: 10px;
                border-radius: 4px;
                margin-bottom: 15px;
            }
            .match-id-input {
                display: flex;
                margin-top: 10px;
                gap: 10px;
            }
            .match-id-input input {
                flex-grow: 1;
                padding: 8px;
                background: #444;
                border: 1px solid #555;
                border-radius: 4px;
                color: white;
            }
        </style>
    </head>
    <body>
        <div class="upload-box">
            <h1>CS2 Voice Extractor</h1>
            <form action="/upload" method="post" enctype="multipart/form-data" id="uploadForm">
                <input type="file" name="demo" accept=".dem" required id="demoFile">
                <input type="submit" value="Extract Voices" class="button" id="submitButton">
            </form>
        </div>

        {{if .CurrentDemo}}
        <div class="demo-info">
            <strong>Current Demo:</strong> {{.CurrentDemo.Filename}}
            {{if .CurrentDemo.MatchID}}
            <div class="match-info">
                <strong>Match ID:</strong> {{.CurrentDemo.MatchID}}<br>
                <strong>Map:</strong> {{.CurrentDemo.Map}}<br>
                <strong>Competition:</strong> {{.CurrentDemo.Competition}}
            </div>
            {{else}}
            <div class="match-id-input">
                <input type="text" id="matchIdInput" placeholder="Enter Match ID to link teams">
                <button id="linkMatchButton" class="button">Link Match</button>
            </div>
            {{end}}
        </div>

        <!-- Team 1 Section -->
        <div class="team-section">
            <h2>Team 1</h2>
            <div class="players-grid">
                {{range .Team1Players}}
                <div class="player-card" id="player-{{.SteamID}}">
                    <div class="player-info">
                        <div class="faceit-level" style="background: #999;">-</div>
                        <div>
                            <div class="faceit-nickname">Loading...</div>
                            <div class="steam-id">{{.SteamID}}</div>
                            <div class="faceit-elo"></div>
                            <div class="team-label team-1">Team 1</div>
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
        <div class="team-section">
            <h2>Team 2</h2>
            <div class="players-grid">
                {{range .Team2Players}}
                <div class="player-card" id="player-{{.SteamID}}">
                    <div class="player-info">
                        <div class="faceit-level" style="background: #999;">-</div>
                        <div>
                            <div class="faceit-nickname">Loading...</div>
                            <div class="steam-id">{{.SteamID}}</div>
                            <div class="faceit-elo"></div>
                            <div class="team-label team-2">Team 2</div>
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
        <div class="team-section">
            <h2>Unassigned Players</h2>
            <div class="players-grid">
                {{range .UnassignedPlayers}}
                <div class="player-card" id="player-{{.SteamID}}">
                    <div class="player-info">
                        <div class="faceit-level" style="background: #999;">-</div>
                        <div>
                            <div class="faceit-nickname">Loading...</div>
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
        {{else}}
        <div class="no-demo">
            No voice data available. Upload a demo file to extract voices.
        </div>
        {{end}}

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
            const linkMatchButton = document.getElementById('linkMatchButton');
            const matchIdInput = document.getElementById('matchIdInput');

            // Link match button event
            if (linkMatchButton) {
                linkMatchButton.addEventListener('click', function() {
                    const matchId = matchIdInput.value.trim();
                    if (!matchId) {
                        alert('Please enter a match ID');
                        return;
                    }

                    const demoId = '{{.CurrentDemo.DemoID}}';
                    fetch('/api/match?match_id=' + matchId + '&demo_id=' + demoId)
                        .then(response => {
                            if (!response.ok) {
                                throw new Error('Failed to link match');
                            }
                            return response.json();
                        })
                        .then(data => {
                            // Reload the page to show updated team assignments
                            window.location.reload();
                        })
                        .catch(error => {
                            alert('Error linking match: ' + error.message);
                        });
                });
            }

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
		CurrentDemo      *storage.DemoMetadata
		Team1Players     []api.PlayerInfo
		Team2Players     []api.PlayerInfo
		UnassignedPlayers []api.PlayerInfo
	}{
		CurrentDemo:      currentDemo,
		Team1Players:     team1Players,
		Team2Players:     team2Players,
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
	_, err = metadataStore.SaveMetadata(demoID, header.Filename)
	if err != nil {
		log.Printf("Warning: Failed to save metadata: %v", err)
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
