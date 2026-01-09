package storage

import (
	"demovoice/api"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MetadataStore handles saving and loading metadata for demo files
type MetadataStore struct {
	OutputDir string
}

// DemoMetadata contains metadata about a processed demo file
type DemoMetadata struct {
	DemoID        string           `json:"demo_id"`
	Filename      string           `json:"filename"`
	Status        string           `json:"status"` // "processing", "completed", "failed"
	Players       []api.PlayerInfo `json:"players"`
	UploadTime    time.Time        `json:"upload_time"`
	MatchID       string           `json:"match_id,omitempty"`
	Map           string           `json:"map,omitempty"`
	Competition   string           `json:"competition,omitempty"`
	MatchDataJSON string           `json:"match_data_json,omitempty"` // Cached match data for faster loading
	ChatLog       string           `json:"chat_log,omitempty"`        // Filename of the chat log
}

// NewMetadataStore creates a new metadata store
func NewMetadataStore(outputDir string) *MetadataStore {
	return &MetadataStore{
		OutputDir: outputDir,
	}
}

// SaveMetadata saves metadata about a processed demo
func (s *MetadataStore) SaveMetadata(demoID, filename string) (*DemoMetadata, error) {
	// Read the output directory to find files associated with this demo
	files, err := os.ReadDir(s.OutputDir)
	if err != nil {
		return nil, err
	}

	var players []api.PlayerInfo
	for _, file := range files {
		if !file.IsDir() && strings.Contains(file.Name(), "_"+demoID+".wav") {
			// Extract steamID from filename (format: steamID_demoID.wav)
			parts := strings.Split(file.Name(), "_"+demoID+".wav")
			if len(parts) > 0 {
				steamID := parts[0]

				// Calculate audio duration
				audioLength := getWavDuration(filepath.Join(s.OutputDir, file.Name()))

				players = append(players, api.PlayerInfo{
					SteamID:     steamID,
					AudioFile:   file.Name(),
					AudioLength: audioLength,
					DemoID:      demoID,
				})
			}
		}
	}

	// Log for debugging
	log.Printf("Found %d player voices for demo ID %s", len(players), demoID)

	// Check for chat logs
	var chatLog string
	chatLogPath := demoID + "_chat.txt"
	if _, err := os.Stat(filepath.Join(s.OutputDir, chatLogPath)); err == nil {
		chatLog = chatLogPath
	}

	// Extract match ID from filename if possible
	// Format: 1-51dcaf59-f8aa-4df1-b20e-168f4b590c52-1-1.dem
	matchID := ExtractMatchIDFromFilename(filename)

	// Save metadata as JSON
	metadata := DemoMetadata{
		DemoID:     demoID,
		Filename:   filename,
		Players:    players,
		UploadTime: time.Now(),
		MatchID:    matchID,
		Status:     "completed",
		ChatLog:    chatLog,
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}

	metadataPath := filepath.Join(s.OutputDir, demoID+".json")
	if err := os.WriteFile(metadataPath, metadataBytes, 0644); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// LoadMetadata loads metadata for a specific demo ID
func (s *MetadataStore) LoadMetadata(demoID string) (*DemoMetadata, error) {
	if demoID == "" {
		return nil, fmt.Errorf("empty demo ID")
	}

	metadataPath := filepath.Join(s.OutputDir, demoID+".json")
	metadataFile, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, err
	}

	var metadata DemoMetadata
	if err := json.Unmarshal(metadataFile, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// ListAllDemos returns a list of all demos in the system
func (s *MetadataStore) ListAllDemos() ([]DemoMetadata, error) {
	files, err := os.ReadDir(s.OutputDir)
	if err != nil {
		return nil, err
	}

	var demos []DemoMetadata
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") && !strings.HasPrefix(file.Name(), "player_") {
			demoID := strings.TrimSuffix(file.Name(), ".json")
			metadata, err := s.LoadMetadata(demoID)
			if err == nil {
				demos = append(demos, *metadata)
			}
		}
	}

	return demos, nil
}

// EnrichMetadataWithMatchInfo adds match information to the demo metadata
func (s *MetadataStore) EnrichMetadataWithMatchInfo(demoID string, matchID string, matchInfo *api.MatchInfo) error {
	metadata, err := s.LoadMetadata(demoID)
	if err != nil {
		return err
	}

	metadata.MatchID = matchID
	metadata.Map = matchInfo.Map
	metadata.Competition = matchInfo.Competition

	// Update players with team information
	playerMap := make(map[string]*api.PlayerInfo)
	for i := range metadata.Players {
		playerMap[metadata.Players[i].SteamID] = &metadata.Players[i]
	}

	for _, steamID := range matchInfo.Team1 {
		if player, ok := playerMap[steamID]; ok {
			player.Team = "Team 1"
		}
	}

	for _, steamID := range matchInfo.Team2 {
		if player, ok := playerMap[steamID]; ok {
			player.Team = "Team 2"
		}
	}

	// Save updated metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	metadataPath := filepath.Join(s.OutputDir, demoID+".json")
	return os.WriteFile(metadataPath, metadataBytes, 0644)
}

// UpdateMetadata saves an existing metadata object back to disk
func (s *MetadataStore) UpdateMetadata(metadata *DemoMetadata) error {
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	metadataPath := filepath.Join(s.OutputDir, metadata.DemoID+".json")
	return os.WriteFile(metadataPath, metadataBytes, 0644)
}

// FindDemoByMatchID finds an existing demo for a specific match ID
// Returns the demo if found and not expired (within 2 hours), otherwise returns nil
func (s *MetadataStore) FindDemoByMatchID(matchID string, maxAge time.Duration) (*DemoMetadata, error) {
	if matchID == "" {
		return nil, nil
	}

	files, err := os.ReadDir(s.OutputDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") && !strings.HasPrefix(file.Name(), "player_") {
			demoID := strings.TrimSuffix(file.Name(), ".json")
			metadata, err := s.LoadMetadata(demoID)
			if err != nil {
				continue // Skip corrupted metadata files
			}

			// Check if this demo matches the match ID
			if metadata.MatchID == matchID {
				// Check if it's still fresh (not expired)
				age := now.Sub(metadata.UploadTime)
				if age <= maxAge {
					log.Printf("Found existing demo for match %s: %s (age: %v)", matchID, demoID, age)
					return metadata, nil
				} else {
					log.Printf("Found expired demo for match %s: %s (age: %v, max: %v)", matchID, demoID, age, maxAge)
				}
			}
		}
	}

	return nil, nil // Not found
}

// ExtractMatchIDFromFilename extracts the Faceit match ID from a demo filename
// Format: 1-51dcaf59-f8aa-4df1-b20e-168f4b590c52-1-1.dem or 1-51dcaf59-f8aa-4df1-b20e-168f4b590c52.dem.zst
// Returns: 1-51dcaf59-f8aa-4df1-b20e-168f4b590c52
func ExtractMatchIDFromFilename(filename string) string {
	// Remove various extensions
	name := strings.TrimSuffix(filename, ".dem.zst")
	name = strings.TrimSuffix(name, ".dem")

	// Split by '-'
	parts := strings.Split(name, "-")

	// Faceit match IDs have format: 1-UUID (UUID has 5 parts separated by hyphens)
	// So full match ID is: 1-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (6 parts total)
	if len(parts) >= 6 {
		// Return full match ID with "1-" prefix: 1-uuid-parts
		return strings.Join(parts[0:6], "-")
	}

	return ""
}

// getWavDuration reads a WAV file and returns the duration as a formatted string
func getWavDuration(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return "?"
	}
	defer file.Close()

	// Read WAV header (44 bytes minimum)
	header := make([]byte, 44)
	n, err := file.Read(header)
	if err != nil || n < 44 {
		return "?"
	}

	// Verify RIFF header
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return "?"
	}

	// Extract audio format parameters
	// Bytes 24-27: Sample Rate (little endian)
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	if sampleRate == 0 {
		return "?"
	}

	// Get file size to calculate duration
	fileInfo, err := file.Stat()
	if err != nil {
		return "?"
	}

	// WAV data size = file size - header size (44 bytes)
	// Duration = data size / (sample rate * channels * bytes per sample)
	// For our files: 1 channel, 32 bits (4 bytes) per sample
	dataSize := fileInfo.Size() - 44
	bytesPerSample := 4 // 32-bit samples
	channels := 1

	totalSamples := dataSize / int64(bytesPerSample*channels)
	durationSeconds := float64(totalSamples) / float64(sampleRate)

	// Format duration as "1m 23s" or "45s"
	minutes := int(durationSeconds) / 60
	seconds := int(durationSeconds) % 60

	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
