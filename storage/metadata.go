package storage

import (
	"demovoice/api"
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
	DemoID      string           `json:"demo_id"`
	Filename    string           `json:"filename"`
	Players     []api.PlayerInfo `json:"players"`
	UploadTime  time.Time        `json:"upload_time"`
	MatchID     string           `json:"match_id,omitempty"`
	Map         string           `json:"map,omitempty"`
	Competition string           `json:"competition,omitempty"`
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
				players = append(players, api.PlayerInfo{
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
	metadata := DemoMetadata{
		DemoID:     demoID,
		Filename:   filename,
		Players:    players,
		UploadTime: time.Now(),
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
