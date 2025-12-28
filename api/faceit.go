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
	downloadClient *http.Client // Separate client with longer timeout for downloads
	apiKey         string
	downloadAPIKey string
}

// PlayerInfo contains information about a player
type PlayerInfo struct {
	SteamID      string
	Nickname     string
	AudioFile    string
	AudioLength  string  // Duration like "1m 23s" or "45s"
	FaceitLevel  int
	FaceitElo    int
	DemoID       string // Track which demo the voice belongs to
	Team         string // Team 1 or Team 2
}

// FaceitResponse represents the response from the Faceit API
type FaceitResponse struct {
	Nickname string `json:"nickname"`
	Games    map[string]struct {
		SkillLevel int `json:"skill_level"`
		FaceitElo  int `json:"faceit_elo"`
	} `json:"games"`
}

// NewFaceitClient creates a new Faceit API client
func NewFaceitClient(apiKey string, downloadAPIKey string) *FaceitClient {
	return &FaceitClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		downloadClient: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for large demo downloads
		},
		apiKey:         apiKey,
		downloadAPIKey: downloadAPIKey,
	}
}

// GetPlayerInfo fetches player information from the Faceit API
func (c *FaceitClient) GetPlayerInfo(steamID string) (*FaceitResponse, error) {
	// Use Open API v4
	url := fmt.Sprintf("https://open.faceit.com/data/v4/players?game=cs2&game_player_id=%s", steamID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call faceit API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("player not found")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("faceit API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

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

	player.Nickname = resp.Nickname
	if cs2, ok := resp.Games["cs2"]; ok {
		player.FaceitLevel = cs2.SkillLevel
		player.FaceitElo = cs2.FaceitElo
	}

	return nil
}

// EnrichPlayersFromMatch updates a list of players with information from the match data
func (c *FaceitClient) EnrichPlayersFromMatch(players []PlayerInfo, matchData *MatchResponse) []PlayerInfo {
	// Create map of GameID (SteamID) -> MatchPlayer
	playerMap := make(map[string]MatchPlayer)

	// Add faction 1 players
	for _, p := range matchData.Payload.Teams.Faction1.Roster {
		playerMap[p.GameID] = p
		// fmt.Printf("🔍 DEBUG: Added mapping %s -> %s\n", p.GameID, p.Nickname)
	}
	// Add faction 2 players
	for _, p := range matchData.Payload.Teams.Faction2.Roster {
		playerMap[p.GameID] = p
		// fmt.Printf("🔍 DEBUG: Added mapping %s -> %s\n", p.GameID, p.Nickname)
	}

	fmt.Printf("🔍 DEBUG: EnrichPlayersFromMatch - Map has %d players\n", len(playerMap))

	// Update players
	count := 0
	for i := range players {
		// fmt.Printf("🔍 DEBUG: Looking for SteamID: %s\n", players[i].SteamID)
		if matchPlayer, ok := playerMap[players[i].SteamID]; ok {
			// Only update if we have a nickname
			if matchPlayer.Nickname != "" {
				players[i].Nickname = matchPlayer.Nickname
				players[i].FaceitLevel = matchPlayer.GameSkillLevel
				players[i].FaceitElo = matchPlayer.Elo
				count++
			}
		} else {
			fmt.Printf("⚠️ DEBUG: SteamID %s not found in match roster\n", players[i].SteamID)
		}
	}
	fmt.Printf("🔍 DEBUG: Enriched %d/%d players from match data\n", count, len(players))
	return players
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
		ID          string   `json:"id"`
		Type        string   `json:"type"`
		Game        string   `json:"game"`
		Region      string   `json:"region"`
		OrganizerID string   `json:"organizerId"`
		DemoURL     []string `json:"demoUrl"` // Added demo URLs
		Teams       struct {
			Faction1 MatchTeam `json:"faction1"`
			Faction2 MatchTeam `json:"faction2"`
		} `json:"teams"`
		EntityCustom struct {
			// Teams removed from here as they are directly under payload
		} `json:"entityCustom"`
	} `json:"payload"`
}

// GetMatchData fetches match room data from Faceit API (public endpoint - no auth needed)
func (c *FaceitClient) GetMatchData(matchID string) (*MatchResponse, error) {
	url := fmt.Sprintf("https://www.faceit.com/api/match/v2/match/%s", matchID)

	fmt.Printf("🔍 DEBUG [GetMatchData]: URL: %s\n", url)

	// This is a public endpoint - don't send Authorization header
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call match API: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("🔍 DEBUG [GetMatchData]: Response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
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

// OpenAPIMatchResponse represents the response from the official Faceit Data API
type OpenAPIMatchResponse struct {
	DemoURL []string `json:"demo_url"`
}

// GetDemoResourceURL fetches the demo resource URL from match data
// Tries multiple sources: official Data API, match API, then constructs fallback
func (c *FaceitClient) GetDemoResourceURL(matchID string) (string, error) {
	fmt.Printf("🔍 DEBUG: Fetching demo URL for match ID: %s\n", matchID)

	// Try 1: Use official Faceit Data API (requires API key)
	if c.apiKey != "" {
		demoURL, err := c.getDemoFromOpenAPI(matchID)
		if err == nil && demoURL != "" {
			fmt.Printf("🔍 DEBUG: Found demo URL from Open API: %s\n", demoURL)
			return demoURL, nil
		}
		fmt.Printf("🔍 DEBUG: Open API failed: %v\n", err)
	}

	// Try 2: Get from internal match API
	matchData, err := c.GetMatchData(matchID)
	if err == nil && len(matchData.Payload.DemoURL) > 0 {
		demoURL := matchData.Payload.DemoURL[0]
		fmt.Printf("🔍 DEBUG: Found demo URL from match API: %s\n", demoURL)
		return demoURL, nil
	}
	if err != nil {
		fmt.Printf("🔍 DEBUG: Match API failed: %v\n", err)
	} else {
		fmt.Printf("🔍 DEBUG: No demo URL in match data\n")
	}

	// Try 3: Construct URL based on known pattern
	// Pattern: https://demos-{region}-faceit-cdn.s3.{region}.backblazeb2.com/cs2/1-{match_id}-1-1.dem.zst
	resourceURL := fmt.Sprintf("https://demos-europe-central-faceit-cdn.s3.eu-central-003.backblazeb2.com/cs2/1-%s-1-1.dem.zst", matchID)
	fmt.Printf("🔍 DEBUG: Using constructed resource URL: %s\n", resourceURL)
	return resourceURL, nil
}

// getDemoFromOpenAPI fetches demo URL from official Faceit Data API
func (c *FaceitClient) getDemoFromOpenAPI(matchID string) (string, error) {
	url := fmt.Sprintf("https://open.faceit.com/data/v4/matches/%s", matchID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("open API returned status %d", resp.StatusCode)
	}

	var result OpenAPIMatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.DemoURL) > 0 {
		return result.DemoURL[0], nil
	}

	return "", fmt.Errorf("no demo URL in response")
}

// GetSignedDemoURL calls the download API to get a signed download URL
func (c *FaceitClient) GetSignedDemoURL(resourceURL string) (string, error) {
	if c.downloadAPIKey == "" {
		return "", fmt.Errorf("download API key not configured - set FACEIT_DOWNLOAD_API_KEY in .env")
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
	keyPreview := c.downloadAPIKey
	if len(keyPreview) > 8 {
		keyPreview = keyPreview[:8]
	}
	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Using download API key: %s...\n", keyPreview)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call download API: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Error response: %s\n", string(bodyBytes))
		return "", fmt.Errorf("download API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var downloadResp DemoDownloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&downloadResp); err != nil {
		return "", fmt.Errorf("failed to decode download response: %w", err)
	}

	if downloadResp.Payload.DownloadURL == "" {
		return "", fmt.Errorf("download API returned empty download URL")
	}

	urlPreview := downloadResp.Payload.DownloadURL
	if len(urlPreview) > 80 {
		urlPreview = urlPreview[:80] + "..."
	}
	fmt.Printf("🔍 DEBUG [GetSignedDemoURL]: Got signed URL: %s\n", urlPreview)

	return downloadResp.Payload.DownloadURL, nil
}

// DownloadDemo downloads a demo file from Faceit and saves it to the specified path
func (c *FaceitClient) DownloadDemo(matchID string, savePath string) error {
	fmt.Printf("📥 Starting demo download for match: %s\n", matchID)

	// Step 1: Get the resource URL from match data
	resourceURL, err := c.GetDemoResourceURL(matchID)
	if err != nil {
		return fmt.Errorf("failed to get demo resource URL: %w", err)
	}
	fmt.Printf("📥 Got resource URL: %s\n", resourceURL)

	// Step 2: Get signed download URL
	signedURL, err := c.GetSignedDemoURL(resourceURL)
	if err != nil {
		return fmt.Errorf("failed to get signed download URL: %w", err)
	}
	fmt.Printf("📥 Got signed URL, starting download...\n")

	// Step 3: Download the demo file using the download client (longer timeout)
	resp, err := c.downloadClient.Get(signedURL)
	if err != nil {
		return fmt.Errorf("failed to download demo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("demo download returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Get content length for progress logging
	contentLength := resp.ContentLength
	fmt.Printf("📥 Download size: %.2f MB\n", float64(contentLength)/(1024*1024))

	// Step 4: Save to file
	out, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save demo file: %w", err)
	}

	fmt.Printf("📥 Downloaded %.2f MB to %s\n", float64(written)/(1024*1024), savePath)
	return nil
}
