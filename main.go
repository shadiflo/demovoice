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
            .players-container {
                display: grid;
                grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
                gap: 1.5rem;
                margin-top: 2rem;
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
                overflow: hidden;
                background: #6b7280;
            }
            .faceit-level img {
                width: 100%;
                height: 100%;
                object-fit: cover;
                border-radius: 4px;
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
            .loading-container {
                background: rgba(30, 41, 59, 0.9);
                border: 1px solid rgba(102, 126, 234, 0.3);
                border-radius: 16px;
                padding: 3rem 2.5rem;
                text-align: center;
                max-width: 400px;
                box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
            }
            .spinner-container {
                position: relative;
                margin-bottom: 2rem;
            }
            .spinner {
                width: 80px;
                height: 80px;
                border: 4px solid rgba(102, 126, 234, 0.1);
                border-radius: 50%;
                position: relative;
                animation: spin 2s linear infinite;
            }
            .spinner::before {
                content: '';
                position: absolute;
                top: -4px;
                left: -4px;
                right: -4px;
                bottom: -4px;
                border: 4px solid transparent;
                border-top: 4px solid #667eea;
                border-radius: 50%;
                animation: spin 1.5s linear infinite reverse;
            }
            .spinner::after {
                content: '';
                position: absolute;
                top: 6px;
                left: 6px;
                right: 6px;
                bottom: 6px;
                border: 3px solid rgba(102, 126, 234, 0.2);
                border-top: 3px solid #764ba2;
                border-radius: 50%;
                animation: spin 1s linear infinite;
            }
            @keyframes spin {
                0% { transform: rotate(0deg); }
                100% { transform: rotate(360deg); }
            }
            .loading-title {
                color: #f1f5f9;
                font-size: 1.5rem;
                font-weight: 700;
                margin-bottom: 1rem;
                background: linear-gradient(135deg, #667eea, #764ba2);
                -webkit-background-clip: text;
                -webkit-text-fill-color: transparent;
                background-clip: text;
            }
            .progress-text {
                color: #cbd5e1;
                font-size: 1rem;
                margin-bottom: 0.75rem;
                line-height: 1.5;
            }
            .progress-detail {
                color: #94a3b8;
                font-size: 0.9rem;
                font-style: italic;
                margin-bottom: 1rem;
            }
            .progress-bar {
                width: 100%;
                height: 8px;
                background: rgba(51, 65, 85, 0.6);
                border-radius: 4px;
                overflow: hidden;
                margin-bottom: 1.5rem;
            }
            .progress-bar-fill {
                height: 100%;
                background: linear-gradient(90deg, #667eea, #764ba2);
                border-radius: 4px;
                width: 0%;
                transition: width 0.3s ease;
                animation: shimmer 1.5s infinite;
            }
            @keyframes shimmer {
                0% { transform: translateX(-100%); }
                100% { transform: translateX(100%); }
            }
            .loading-stats {
                display: flex;
                justify-content: space-between;
                margin-top: 1rem;
                padding-top: 1rem;
                border-top: 1px solid rgba(148, 163, 184, 0.2);
            }
            .stat-item {
                text-align: center;
            }
            .stat-number {
                color: #667eea;
                font-size: 1.2rem;
                font-weight: 700;
                display: block;
            }
            .stat-label {
                color: #94a3b8;
                font-size: 0.8rem;
                margin-top: 0.25rem;
            }
            .processing-steps {
                list-style: none;
                padding: 0;
                margin: 1.5rem 0;
                text-align: left;
            }
            .processing-steps li {
                color: #94a3b8;
                font-size: 0.9rem;
                margin-bottom: 0.5rem;
                padding-left: 1.5rem;
                position: relative;
            }
            .processing-steps li::before {
                content: '⏳';
                position: absolute;
                left: 0;
                top: 0;
            }
            .processing-steps li.completed::before {
                content: '✅';
            }
            .processing-steps li.active {
                color: #e2e8f0;
                font-weight: 600;
            }
            .processing-steps li.active::before {
                content: '🔄';
                animation: spin 1s linear infinite;
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

            <div class="players-container">
                {{range .AllPlayers}}
                <div class="player-card" id="player-{{.SteamID}}">
                    <div class="player-header">
                        <div class="faceit-level">
                            <img id="level-img-{{.SteamID}}" src="/icons/faceit1.png" alt="Level" style="display: none;">
                            <span id="level-text-{{.SteamID}}">-</span>
                        </div>
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
            {{else}}
            <div class="no-demo">
                <h3>🎯 Ready to Extract</h3>
                <p>Upload a CS2 demo file (.dem) to extract voice communications and see player analysis</p>
            </div>
            {{end}}
        </div>

        <!-- Processing overlay -->
        <div class="processing-overlay" id="processingOverlay">
            <div class="loading-container">
                <div class="spinner-container">
                    <div class="spinner"></div>
                </div>
                
                <div class="loading-title">Processing Demo</div>
                
                <div class="progress-text" id="mainProgress">Analyzing demo file...</div>
                <div class="progress-detail" id="progressDetail">This may take a few minutes for large files</div>
                
                <div class="progress-bar">
                    <div class="progress-bar-fill" id="progressBarFill"></div>
                </div>
                
                <ul class="processing-steps" id="processingSteps">
                    <li id="step1">📁 Reading demo file</li>
                    <li id="step2">🔍 Parsing game data</li>
                    <li id="step3">🎤 Extracting voice data</li>
                    <li id="step4">⚡ Parallel processing</li>
                    <li id="step5">🎵 Generating audio files</li>
                    <li id="step6">✨ Finalizing</li>
                </ul>
                
                <div class="loading-stats">
                    <div class="stat-item">
                        <span class="stat-number" id="fileSize">0</span>
                        <div class="stat-label">MB</div>
                    </div>
                    <div class="stat-item">
                        <span class="stat-number" id="elapsedTime">0</span>
                        <div class="stat-label">Seconds</div>
                    </div>
                    <div class="stat-item">
                        <span class="stat-number" id="currentStep">1</span>
                        <div class="stat-label">of 6</div>
                    </div>
                </div>
            </div>
        </div>

        <script>
        function setFaceitLevel(playerCard, level) {
            const levelImg = playerCard.querySelector('[id^="level-img-"]');
            const levelText = playerCard.querySelector('[id^="level-text-"]');
            
            if (level >= 1 && level <= 10) {
                levelImg.src = '/icons/faceit' + level + '.png';
                levelImg.style.display = 'block';
                levelText.style.display = 'none';
            } else {
                levelImg.style.display = 'none';
                levelText.style.display = 'block';
                levelText.textContent = '-';
            }
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
                    const fileSizeMB = (fileSize / (1024 * 1024)).toFixed(1);

                    processingOverlay.style.display = 'flex';
                    submitButton.disabled = true;

                    // Initialize loading interface
                    document.getElementById('fileSize').textContent = fileSizeMB;
                    document.getElementById('elapsedTime').textContent = '0';
                    document.getElementById('currentStep').textContent = '1';
                    document.getElementById('progressBarFill').style.width = '0%';

                    // Start the processing animation
                    startProcessingAnimation();
                }
            });

            function startProcessingAnimation() {
                let currentStepIndex = 0;
                let seconds = 0;
                const steps = ['step1', 'step2', 'step3', 'step4', 'step5', 'step6'];
                const stepMessages = [
                    'Reading demo file...',
                    'Parsing game events and data...',
                    'Extracting voice communications...',
                    'Processing with parallel optimization...',
                    'Generating individual audio files...',
                    'Finalizing and cleaning up...'
                ];

                function updateStep() {
                    // Mark previous step as completed
                    if (currentStepIndex > 0) {
                        document.getElementById(steps[currentStepIndex - 1]).classList.remove('active');
                        document.getElementById(steps[currentStepIndex - 1]).classList.add('completed');
                    }

                    // Mark current step as active
                    if (currentStepIndex < steps.length) {
                        document.getElementById(steps[currentStepIndex]).classList.add('active');
                        document.getElementById('mainProgress').textContent = stepMessages[currentStepIndex];
                        document.getElementById('currentStep').textContent = (currentStepIndex + 1).toString();
                        
                        // Update progress bar
                        const progress = ((currentStepIndex + 1) / steps.length) * 100;
                        document.getElementById('progressBarFill').style.width = progress + '%';
                        
                        currentStepIndex++;
                    }
                }

                // Start with first step
                updateStep();

                // Update step every 8-15 seconds (randomized for realism)
                const stepTimer = setInterval(function() {
                    if (currentStepIndex < steps.length) {
                        updateStep();
                    } else {
                        // All steps completed, show final messages
                        document.getElementById('mainProgress').textContent = 'Almost done...';
                        document.getElementById('progressDetail').textContent = 'Wrapping up the final details';
                    }
                }, Math.random() * 7000 + 8000); // 8-15 seconds

                // Update elapsed time every second
                const timeTimer = setInterval(function() {
                    seconds++;
                    document.getElementById('elapsedTime').textContent = seconds.toString();
                    
                    // Add some encouraging messages based on time
                    if (seconds === 30) {
                        document.getElementById('progressDetail').textContent = 'Processing is going well...';
                    } else if (seconds === 60) {
                        document.getElementById('progressDetail').textContent = 'Large demo files take more time, please be patient';
                    } else if (seconds === 120) {
                        document.getElementById('progressDetail').textContent = 'Still working hard on your demo...';
                    }
                }, 1000);

                // Store timers in sessionStorage so we can clear them if page reloads
                sessionStorage.setItem('stepTimer', stepTimer);
                sessionStorage.setItem('timeTimer', timeTimer);
            }

            // Check if we just came back from processing (page reload)
            if (sessionStorage.getItem('processing') === 'true') {
                processingOverlay.style.display = 'none';
                sessionStorage.removeItem('processing');

                // Clear any existing timers
                const oldStepTimer = sessionStorage.getItem('stepTimer');
                const oldTimeTimer = sessionStorage.getItem('timeTimer');
                if (oldStepTimer) {
                    clearInterval(parseInt(oldStepTimer));
                    sessionStorage.removeItem('stepTimer');
                }
                if (oldTimeTimer) {
                    clearInterval(parseInt(oldTimeTimer));
                    sessionStorage.removeItem('timeTimer');
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
                                playerCard.querySelector('.faceit-nickname').classList.remove('loading-placeholder');
                                setFaceitLevel(playerCard, level);
                                playerCard.querySelector('.faceit-elo').textContent = elo + ' ELO';
                            }
                        } else {
                            playerCard.querySelector('.faceit-nickname').textContent = 'Player not found';
                            playerCard.querySelector('.faceit-nickname').classList.remove('loading-placeholder');
                            setFaceitLevel(playerCard, 0); // Will show '-'
                        }
                    })
                    .catch(error => {
                        console.error('Error fetching player data:', error);
                        playerCard.querySelector('.faceit-nickname').textContent = 'Error loading data';
                        playerCard.querySelector('.faceit-nickname').classList.remove('loading-placeholder');
                        setFaceitLevel(playerCard, 0); // Will show '-'
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
	var allPlayers []api.PlayerInfo

	if currentDemoID != "" {
		// Try to load metadata for this demo
		metadata, err := metadataStore.LoadMetadata(currentDemoID)
		if err == nil {
			currentDemo = metadata
			allPlayers = metadata.Players
		}
	}

	t := template.Must(template.New("home").Parse(tmpl))
	t.Execute(w, struct {
		CurrentDemo *storage.DemoMetadata
		AllPlayers  []api.PlayerInfo
	}{
		CurrentDemo: currentDemo,
		AllPlayers:  allPlayers,
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