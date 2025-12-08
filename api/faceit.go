package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// FaceitClient handles all API calls to the Faceit API
type FaceitClient struct {
	httpClient     *http.Client
	apiKey         string
	downloadAPIKey string
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
func NewFaceitClient(apiKey string, downloadAPIKey string) *FaceitClient {
	return &FaceitClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiKey:         apiKey,
		downloadAPIKey: downloadAPIKey,
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
		ID           string   `json:"id"`
		Type         string   `json:"type"`
		Game         string   `json:"game"`
		Region       string   `json:"region"`
		OrganizerID  string   `json:"organizerId"`
		DemoURL      []string `json:"demoUrl"` // Added demo URLs
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

	fmt.Printf("🔍 DEBUG [GetMatchData]: URL: %s\n", url)
	fmt.Printf("🔍 DEBUG [GetMatchData]: Match ID: %s\n", matchID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add API key if available (optional for public match data)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		fmt.Printf("🔍 DEBUG [GetMatchData]: Using API key\n")
	} else {
		fmt.Printf("🔍 DEBUG [GetMatchData]: No API key, trying without auth\n")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call match API: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("🔍 DEBUG [GetMatchData]: Response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		// Read response body for error details
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("🔍 DEBUG [GetMatchData]: Error response: %s\n", string(bodyBytes))
		return nil, fmt.Errorf("match API returned status %d", resp.StatusCode)
	}

	var result MatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode match response: %w", err)
	}

	fmt.Printf("🔍 DEBUG [GetMatchData]: Successfully decoded match data\n")

	return &result, nil
}

// DemoDownloadRequest represents the request body for demo download API
type DemoDownloadRequest struct {
	ResourceURL string `json:"resource_url"`
}

// DemoDownloadResponse represents the response from demo download API
type DemoDownloadResponse struct {
	Payload struct {
		DownloadURL string `json:"download_url"`
	} `json:"payload"`
}

// MatchDetailsResponse represents detailed match information including demo URL
type MatchDetailsResponse struct {
	DemoURL []string `json:"demo_url"`
}

// GetDemoResourceURL fetches the demo resource URL from match data
// Uses the existing working match API endpoint, or constructs URL as fallback
func (c *FaceitClient) GetDemoResourceURL(matchID string) (string, error) {
	fmt.Printf("🔍 DEBUG: Fetching demo URL for match ID: %s\n", matchID)

	// Try to get the resource URL from match API first
	matchData, err := c.GetMatchData(matchID)
	if err != nil {
		fmt.Printf("🔍 DEBUG: Match API failed, trying to construct resource URL directly: %v\n", err)

		// Fallback: Construct the resource URL based on the known pattern
		// Pattern from network inspection: https://demos-europe-central-faceit-cdn.s3.eu-central-003.backblazeb2.com/cs2/1-{match_id}-1-1.dem.zst
		resourceURL := fmt.Sprintf("https://demos-europe-central-faceit-cdn.s3.eu-central-003.backblazeb2.com/cs2/1-%s-1-1.dem.zst", matchID)
		fmt.Printf("🔍 DEBUG: Constructed resource URL: %s\n", resourceURL)
		return resourceURL, nil
	}

	fmt.Printf("🔍 DEBUG: Match data retrieved successfully\n")
	fmt.Printf("🔍 DEBUG: Demo URLs in response: %v\n", matchData.Payload.DemoURL)

	// Check if demo URL exists
	if len(matchData.Payload.DemoURL) == 0 {
		fmt.Printf("🔍 DEBUG: No demo URL in match data, constructing resource URL\n")
		// Fallback to constructed URL
		resourceURL := fmt.Sprintf("https://demos-europe-central-faceit-cdn.s3.eu-central-003.backblazeb2.com/cs2/1-%s-1-1.dem.zst", matchID)
		fmt.Printf("🔍 DEBUG: Constructed resource URL: %s\n", resourceURL)
		return resourceURL, nil
	}

	demoURL := matchData.Payload.DemoURL[0]
	fmt.Printf("🔍 DEBUG: Found demo resource URL from API: %s\n", demoURL)

	return demoURL, nil
}

// GetSignedDemoURL calls the download API to get a signed download URL
func (c *FaceitClient) GetSignedDemoURL(resourceURL string) (string, error) {
	if c.downloadAPIKey == "" {
		return "", fmt.Errorf("download API key not configured")
	}

	downloadReq := DemoDownloadRequest{
		ResourceURL: resourceURL,
	}

	reqBody, err := json.Marshal(downloadReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Use the endpoint from official docs
	req, err := http.NewRequest("POST", "https://open.faceit.com/download/v2/demos/download", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.downloadAPIKey)
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Calling download API with resource URL: %s\n", resourceURL)
	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Using download API key: %s...\n", c.downloadAPIKey[:8])

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call download API: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Error response: %s\n", string(bodyBytes))
		return "", fmt.Errorf("download API returned status %d", resp.StatusCode)
	}

	var downloadResp DemoDownloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&downloadResp); err != nil {
		return "", fmt.Errorf("failed to decode download response: %w", err)
	}

	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Got signed URL: %s\n", downloadResp.Payload.DownloadURL[:80]+"...")

	return downloadResp.Payload.DownloadURL, nil
}

// DownloadDemo downloads a demo file from Faceit and saves it to the specified path
func (c *FaceitClient) DownloadDemo(matchID string, savePath string) error {
	// Step 1: Get the resource URL from match data
	resourceURL, err := c.GetDemoResourceURL(matchID)
	if err != nil {
		return fmt.Errorf("failed to get demo resource URL: %w", err)
	}

	// Step 2: Get signed download URL
	signedURL, err := c.GetSignedDemoURL(resourceURL)
	if err != nil {
		return fmt.Errorf("failed to get signed download URL: %w", err)
	}

	// Step 3: Download the demo file
	resp, err := c.httpClient.Get(signedURL)
	if err != nil {
		return fmt.Errorf("failed to download demo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("demo download returned status %d", resp.StatusCode)
	}

	// Step 4: Save to file
	out, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save demo file: %w", err)
	}

	return nil
}
