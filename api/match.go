package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MatchClient handles all API calls for match data
type MatchClient struct {
	httpClient *http.Client
}

// MatchInfo contains information about a match
type MatchInfo struct {
	MatchID     string
	Map         string
	Team1       []string // SteamIDs of team 1 players
	Team2       []string // SteamIDs of team 2 players
	Date        time.Time
	Competition string
}

// NewMatchClient creates a new match API client
func NewMatchClient() *MatchClient {
	return &MatchClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetMatchInfoByID fetches match information by match ID
// Note: This is a placeholder. Replace with your actual API endpoint and response structure
func (c *MatchClient) GetMatchInfoByID(matchID string) (*MatchInfo, error) {
	// Replace with your actual API endpoint
	url := fmt.Sprintf("https://your-api-endpoint.com/matches/%s", matchID)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call match API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read match API response: %w", err)
	}

	var result MatchInfo
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode match response: %w", err)
	}

	return &result, nil
}

// AssignTeams assigns teams to player information based on match data
func (c *MatchClient) AssignTeams(matchInfo *MatchInfo, players []*PlayerInfo) {
	// Create a map for quick lookup
	team1Map := make(map[string]bool)
	team2Map := make(map[string]bool)

	for _, steamID := range matchInfo.Team1 {
		team1Map[steamID] = true
	}

	for _, steamID := range matchInfo.Team2 {
		team2Map[steamID] = true
	}

	// Assign teams to players
	for _, player := range players {
		if team1Map[player.SteamID] {
			player.Team = "Team 1"
		} else if team2Map[player.SteamID] {
			player.Team = "Team 2"
		} else {
			player.Team = "Unknown"
		}
	}
}
