package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// FaceitClient handles all API calls to the Faceit API
type FaceitClient struct {
	httpClient *http.Client
}

// PlayerInfo contains information about a player
type PlayerInfo struct {
	SteamID     string
	Nickname    string
	AudioFile   string
	FaceitLevel int
	FaceitElo   int
	DemoID      string // Track which demo the voice belongs to
	Team        string // Team 1 or Team 2
}

// FaceitResponse represents the response from the Faceit API
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

// NewFaceitClient creates a new Faceit API client
func NewFaceitClient() *FaceitClient {
	return &FaceitClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetPlayerInfo fetches player information from the Faceit API
func (c *FaceitClient) GetPlayerInfo(steamID string) (*FaceitResponse, error) {
	url := fmt.Sprintf("https://www.faceit.com/api/users/v1/users?game=cs2&game_id=%s", steamID)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call faceit API: %w", err)
	}
	defer resp.Body.Close()

	var result FaceitResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode faceit response: %w", err)
	}

	return &result, nil
}

// EnrichPlayerInfo adds Faceit information to a player
func (c *FaceitClient) EnrichPlayerInfo(player *PlayerInfo) error {
	resp, err := c.GetPlayerInfo(player.SteamID)
	if err != nil {
		return err
	}

	if len(resp.Payload) > 0 {
		player.Nickname = resp.Payload[0].Nickname
		player.FaceitLevel = resp.Payload[0].Games.CS2.SkillLevel
		player.FaceitElo = resp.Payload[0].Games.CS2.FaceitElo
	}

	return nil
}
