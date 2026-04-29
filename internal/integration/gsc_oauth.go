package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	oauthRedirectURI = "http://127.0.0.1:9549/oauth2callback"
	oauthPort        = "127.0.0.1:9549"
	oauthTimeout     = 5 * time.Minute
)

var gscOAuthScopes = []string{"https://www.googleapis.com/auth/webmasters.readonly"}

// OAuthToken represents a stored OAuth2 token.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiryDate   int64  `json:"expiry_date"`
}

// gscConfigDir returns ~/.micelio, creating it if needed.
func gscConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".micelio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// gscTokenPath returns the path to ~/.micelio/gsc-token.json.
func gscTokenPath() (string, error) {
	dir, err := gscConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gsc-token.json"), nil
}

// HasStoredGscToken returns true if a GSC OAuth token file exists.
func HasStoredGscToken() bool {
	p, err := gscTokenPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// DeleteStoredGscToken removes the stored GSC OAuth token file.
func DeleteStoredGscToken() error {
	p, err := gscTokenPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		fmt.Println("No stored token found.")
		return nil
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	fmt.Println("Token deleted: ~/.micelio/gsc-token.json")
	fmt.Println("To fully revoke access, visit: https://myaccount.google.com/permissions")
	return nil
}

// RunGscOAuthFlow runs the interactive OAuth2 flow for GSC authentication.
// It starts a local HTTP server, prints the authorization URL, waits for the
// callback, exchanges the code for tokens, and stores them.
func RunGscOAuthFlow(clientID, clientSecret string) error {
	if clientID == "" {
		clientID = os.Getenv("MICELIO_GSC_CLIENT_ID")
	}
	if clientSecret == "" {
		clientSecret = os.Getenv("MICELIO_GSC_CLIENT_SECRET")
	}

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf(
			"GSC OAuth2 credentials not configured.\n\n" +
				"Set environment variables:\n" +
				"  MICELIO_GSC_CLIENT_ID=your-client-id\n" +
				"  MICELIO_GSC_CLIENT_SECRET=your-client-secret\n\n" +
				"Or pass them as options:\n" +
				"  micelio gsc-auth --client-id <id> --client-secret <secret>\n\n" +
				"To get credentials:\n" +
				"  1. Go to https://console.cloud.google.com/apis/credentials\n" +
				"  2. Create an OAuth 2.0 Client ID (Desktop or Web application)\n" +
				"  3. Add http://localhost:9549/oauth2callback as a redirect URI\n" +
				"  4. Enable the Search Console API for your project")
	}

	// Generate CSRF state token
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("generate state token: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	// Build authorization URL
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {oauthRedirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(gscOAuthScopes, " ")},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	authURL := "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()

	fmt.Print("\nOpen this URL in your browser to authorize Micelio:\n\n")
	fmt.Printf("  %s\n\n", authURL)
	fmt.Print("Waiting for authorization...\n\n")

	// Start local server to receive callback
	code, err := waitForOAuthCallback(state)
	if err != nil {
		return fmt.Errorf("OAuth flow: %w", err)
	}

	// Exchange authorization code for tokens
	token, err := exchangeOAuthCode(clientID, clientSecret, code)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}

	// Save token
	if err := saveGscToken(token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Print("Authorization successful! Token saved to ~/.micelio/gsc-token.json\n\n")

	// List available properties
	if err := listGscProperties(token.AccessToken); err != nil {
		// Non-fatal — token works, just can't list properties
		_ = err
	}

	return nil
}

func waitForOAuthCallback(expectedState string) (string, error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: oauthPort, Handler: mux}

	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "<h1>Authorization failed</h1><p>You can close this window.</p>")
			errCh <- fmt.Errorf("OAuth error: %s", errParam)
			return
		}

		// Verify CSRF state token
		if returnedState := r.URL.Query().Get("state"); returnedState != expectedState {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<h1>Invalid state parameter</h1><p>Authorization rejected for security reasons.</p>")
			errCh <- fmt.Errorf("OAuth state mismatch (possible CSRF)")
			return
		}

		authCode := r.URL.Query().Get("code")
		if authCode != "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "<h1>Authorization successful!</h1><p>You can close this window and return to the terminal.</p>")
			codeCh <- authCode
			return
		}

		w.WriteHeader(http.StatusNotFound)
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	timer := time.NewTimer(oauthTimeout)
	defer timer.Stop()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		_ = srv.Shutdown(context.Background())
		return "", err
	case <-timer.C:
		_ = srv.Shutdown(context.Background())
		return "", fmt.Errorf("OAuth flow timed out after %v", oauthTimeout)
	}

	_ = srv.Shutdown(context.Background())
	return code, nil
}

func exchangeOAuthCode(clientID, clientSecret, code string) (*OAuthToken, error) {
	data := url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {oauthRedirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := http.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	return &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiryDate:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixMilli(),
	}, nil
}

func saveGscToken(token *OAuthToken) error {
	p, err := gscTokenPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// LoadStoredGscToken loads the stored OAuth token and returns a fresh access token,
// refreshing it if expired. Returns ("", nil) if no token is stored.
func LoadStoredGscToken(clientID, clientSecret string) (string, error) {
	p, err := gscTokenPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	var token OAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return "", err
	}

	// Check if token is expired (with 60s buffer)
	if time.Now().UnixMilli() >= token.ExpiryDate-60000 {
		// Refresh the token
		refreshed, err := refreshOAuthToken(clientID, clientSecret, token.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("refresh token: %w", err)
		}
		// Preserve refresh token if not returned
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = token.RefreshToken
		}
		if err := saveGscToken(refreshed); err != nil {
			return "", err
		}
		return refreshed.AccessToken, nil
	}

	return token.AccessToken, nil
}

func refreshOAuthToken(clientID, clientSecret, refreshToken string) (*OAuthToken, error) {
	data := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}

	resp, err := http.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	return &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiryDate:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixMilli(),
	}, nil
}

// listGscProperties lists available Search Console properties.
func listGscProperties(accessToken string) error {
	req, err := http.NewRequest("GET", gscEndpoint+"/sites", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, gscMaxBodySize))
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		SiteEntry []struct {
			SiteURL         string `json:"siteUrl"`
			PermissionLevel string `json:"permissionLevel"`
		} `json:"siteEntry"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}

	if len(result.SiteEntry) > 0 {
		fmt.Print("Available Search Console properties:\n\n")
		for _, site := range result.SiteEntry {
			fmt.Printf("  %s  (%s)\n", site.SiteURL, site.PermissionLevel)
		}
		fmt.Print("\nUse with: micelio crawl <url> --gsc --gsc-property <property-url>\n\n")
	}
	return nil
}
