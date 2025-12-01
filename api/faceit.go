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
	apiKey     string
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
func NewFaceitClient(apiKey string) *FaceitClient {
	return &FaceitClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiKey: apiKey,
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

// MatchPlayer represents a player in a match roster
type MatchPlayer struct {
	ID             string   `json:"id"`
	Nickname       string   `json:"nickname"`
	Avatar         string   `json:"avatar"`
	GameID         string   `json:"gameId"`
	GameName       string   `json:"gameName"`
	Memberships    []string `json:"memberships"`
	Elo            int      `json:"elo"`
	GameSkillLevel int      `json:"gameSkillLevel"`
}

// MatchTeam represents a team in a match
type MatchTeam struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Avatar string        `json:"avatar"`
	Leader string        `json:"leader"`
	Roster []MatchPlayer `json:"roster"`
	Stats  struct {
		WinProbability float64 `json:"winProbability"`
		SkillLevel     struct {
			Average int `json:"average"`
			Range   struct {
				Min int `json:"min"`
				Max int `json:"max"`
			} `json:"range"`
		} `json:"skillLevel"`
		Rating int `json:"rating"`
	} `json:"stats"`
}

// MatchResponse represents the Faceit match API response
type MatchResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Payload struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Game         string `json:"game"`
		Region       string `json:"region"`
		OrganizerID  string `json:"organizerId"`
		EntityCustom struct {
			Teams struct {
				Faction1 MatchTeam `json:"faction1"`
				Faction2 MatchTeam `json:"faction2"`
			} `json:"teams"`
		} `json:"entityCustom"`
	} `json:"payload"`
}

// GetMatchData fetches match room data from Faceit API
func (c *FaceitClient) GetMatchData(matchID string) (*MatchResponse, error) {
	url := fmt.Sprintf("https://www.faceit.com/api/match/v2/match/%s", matchID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add API key if available (optional for public match data)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call match API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("match API returned status %d", resp.StatusCode)
	}

	var result MatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode match response: %w", err)
	}

	return &result, nil
}
