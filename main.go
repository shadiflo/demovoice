package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	uploadDir = "./uploads"
	outputDir = "./output"
	password  = "faceit" // Set your password here
)

type PlayerInfo struct {
	SteamID     string
	Nickname    string
	AudioFile   string
	FaceitLevel int
	FaceitElo   int
	DemoID      string // Added field to track which demo the voice belongs to
}

type FaceitResponse struct {
	Payload []struct {
		Nickname string `json:"nickname"`
		Games    struct {
			CS2 struct {
				SkillLevel int    `json:"skill_level"`
				FaceitElo  int    `json:"faceit_elo"`
				GameName   string `json:"game_name"`
			} `json:"cs2"`
		} `json:"games"`
	} `json:"payload"`
}

func main() {
	// Create required directories
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(outputDir, 0755)

	// Handle routes
	http.HandleFunc("/", passwordAuth(handleHome))
	http.HandleFunc("/upload", passwordAuth(handleUpload))
	http.HandleFunc("/faceit/player", handleFaceitPlayer)
	http.Handle("/output/", http.StripPrefix("/output/", http.FileServer(http.Dir(outputDir))))

	fmt.Println("Server started at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
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

	// Fetch player data from Faceit API
	url := fmt.Sprintf("https://www.faceit.com/api/users/v1/users?game=cs2&game_id=%s", steamID)

	resp, err := http.Get(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var result FaceitResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
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
        </style>
    </head>
    <body>
        <div class="upload-box">
            <h1>CS2 Voice Extractor</h1>
            <form action="/upload" method="post" enctype="multipart/form-data">
                <input type="file" name="demo" accept=".dem" required>
                <input type="submit" value="Extract Voices" class="button">
            </form>
        </div>

        {{if .CurrentDemo}}
        <div class="demo-info">
            <strong>Current Demo:</strong> {{.CurrentDemo}}
        </div>
        {{end}}

        {{if .Players}}
        <div class="players-grid">
            {{range .Players}}
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
        {{else}}
        <div class="no-demo">
            No voice data available. Upload a demo file to extract voices.
        </div>
        {{end}}

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

    var players []PlayerInfo
    var allFiles []string

    // Debug mode - show all files in output directory
    showDebug := r.URL.Query().Get("debug") == "1"

    if showDebug {
        files, err := os.ReadDir(outputDir)
        if err == nil {
            for _, file := range files {
                if !file.IsDir() {
                    allFiles = append(allFiles, file.Name())
                }
            }
        }
    }

    if currentDemoID != "" {
        // Read metadata file to get list of players for this demo
        metadataPath := filepath.Join(outputDir, currentDemoID+".json")
        if metadataExists, _ := fileExists(metadataPath); metadataExists {
            metadataFile, err := os.ReadFile(metadataPath)
            if err == nil {
                var metadata struct {
                    Filename string      `json:"filename"`
                    Players  []PlayerInfo `json:"players"`
                }

                if err := json.Unmarshal(metadataFile, &metadata); err == nil {
                    players = metadata.Players
                }
            }
        } else {
            // If metadata file doesn't exist, look for files directly
            files, err := os.ReadDir(outputDir)
            if err == nil {
                for _, file := range files {
                    if !file.IsDir() && strings.Contains(file.Name(), "_"+currentDemoID+".wav") {
                        parts := strings.Split(file.Name(), "_"+currentDemoID+".wav")
                        if len(parts) > 0 {
                            steamID := parts[0]
                            players = append(players, PlayerInfo{
                                SteamID:   steamID,
                                AudioFile: file.Name(),
                                DemoID:    currentDemoID,
                            })
                        }
                    }
                }
            }

            // Create metadata file if it doesn't exist but we found files
            if len(players) > 0 {
                saveMetadata(currentDemoID, getDemoFilename(currentDemoID))
            }
        }
    }

    t := template.Must(template.New("home").Parse(tmpl))
    t.Execute(w, struct{
        Players []PlayerInfo
        CurrentDemo string
        Debug bool
        DemoID string
        Files []string
    }{
        Players: players,
        CurrentDemo: getDemoFilename(currentDemoID),
        Debug: showDebug,
        DemoID: currentDemoID,
        Files: allFiles,
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

// Helper to get demo filename from ID
func getDemoFilename(demoID string) string {
    if demoID == "" {
        return ""
    }

    metadataPath := filepath.Join(outputDir, demoID+".json")
    metadataFile, err := os.ReadFile(metadataPath)
    if err != nil {
        return demoID
    }

    var metadata struct {
        Filename string `json:"filename"`
    }

    if err := json.Unmarshal(metadataFile, &metadata); err != nil {
        return demoID
    }

    return metadata.Filename
}

// Helper to check if a file exists
func fileExists(path string) (bool, error) {
    _, err := os.Stat(path)
    if err == nil {
        return true, nil
    }
    if os.IsNotExist(err) {
        return false, nil
    }
    return false, err
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

    // Save demo metadata
    saveMetadata(demoID, header.Filename)

    // Set a cookie to remember which demo was uploaded
    http.SetCookie(w, &http.Cookie{
        Name:     "current_demo_id",
        Value:    demoID,
        Path:     "/",
        Expires:  time.Now().Add(24 * time.Hour),
    })

    // Clean up
    os.Remove(tempPath)

    // Redirect back to home page
    http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Save metadata about the demo
func saveMetadata(demoID, filename string) error {
    // Read the output directory to find files associated with this demo
    files, err := os.ReadDir(outputDir)
    if err != nil {
        return err
    }

    var players []PlayerInfo
    for _, file := range files {
        if !file.IsDir() && strings.Contains(file.Name(), "_"+demoID+".wav") {
            // Extract steamID from filename (format: steamID_demoID.wav)
            parts := strings.Split(file.Name(), "_"+demoID+".wav")
            if len(parts) > 0 {
                steamID := parts[0]
                players = append(players, PlayerInfo{
                    SteamID:   steamID,
                    AudioFile: file.Name(),
                    DemoID:    demoID,
                })
            }
        }
    }

    // Log for debugging
    log.Printf("Found %d player voices for demo ID %s", len(players), demoID)

    // Save metadata as JSON
    metadata := struct {
        Filename string      `json:"filename"`
        Players  []PlayerInfo `json:"players"`
        UploadTime time.Time `json:"upload_time"`
    }{
        Filename:   filename,
        Players:    players,
        UploadTime: time.Now(),
    }

    metadataBytes, err := json.Marshal(metadata)
    if err != nil {
        return err
    }

    return os.WriteFile(filepath.Join(outputDir, demoID+".json"), metadataBytes, 0644)
}
