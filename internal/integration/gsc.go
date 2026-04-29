package integration

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/micelio/micelio/internal/types"
)

var gscEndpoint = "https://searchconsole.googleapis.com/webmasters/v3"

const (
	gscBatchSize   = 25000
	gscMaxBodySize = 4 << 20 // 4 MB
	gscTokenURL    = "https://oauth2.googleapis.com/token"
	gscScope       = "https://www.googleapis.com/auth/webmasters.readonly"
)

// setGscEndpoint overrides the GSC endpoint (for testing).
func setGscEndpoint(ep string) { gscEndpoint = ep }

// GscClient fetches Google Search Console data using a service account.
type GscClient struct {
	tokenExpiry time.Time
	httpClient  *http.Client
	privateKey  *rsa.PrivateKey
	property    string
	clientEmail string
	tokenURL    string
	cachedToken string
	days        int
	tokenMu     sync.Mutex
}

// GscKeyFile represents the service account JSON key file structure.
type GscKeyFile struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// NewGscClient creates a new GSC client from a service account key file JSON.
func NewGscClient(keyJSON []byte, property string, days int) (*GscClient, error) {
	var kf GscKeyFile
	if err := json.Unmarshal(keyJSON, &kf); err != nil {
		return nil, fmt.Errorf("gsc: parse key file: %w", err)
	}

	block, _ := pem.Decode([]byte(kf.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("gsc: invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("gsc: parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("gsc: key is not RSA")
	}

	tokenURL := kf.TokenURI
	if tokenURL == "" {
		tokenURL = gscTokenURL
	}

	if days <= 0 {
		days = 90
	}

	return &GscClient{
		property:    property,
		days:        days,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		clientEmail: kf.ClientEmail,
		privateKey:  rsaKey,
		tokenURL:    tokenURL,
	}, nil
}

// FetchBatch fetches GSC data for the given URLs, returning a map of URL → GscData.
func (c *GscClient) FetchBatch(ctx context.Context, urls []string) (map[string]*types.GscData, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("gsc: auth: %w", err)
	}

	results := make(map[string]*types.GscData, len(urls))
	// Build reverse map: normalized URL → original URL
	normalizedToOriginal := make(map[string]string, len(urls))
	for _, u := range urls {
		normalizedToOriginal[normalizeGscURL(u)] = u
	}

	startDate := time.Now().AddDate(0, 0, -(c.days + 3)).Format("2006-01-02") // 3-day lag
	endDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	startRow := 0
	for {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		query := map[string]interface{}{
			"startDate":  startDate,
			"endDate":    endDate,
			"dimensions": []string{"page"},
			"rowLimit":   gscBatchSize,
			"startRow":   startRow,
		}

		rows, err := c.querySearchAnalytics(ctx, token, query)
		if err != nil {
			return results, err
		}

		for _, row := range rows {
			normalizedURL := normalizeGscURL(row.Key)
			if originalURL, ok := normalizedToOriginal[normalizedURL]; ok {
				results[originalURL] = &types.GscData{
					Impressions: int(row.Impressions),
					Clicks:      int(row.Clicks),
					CTR:         row.CTR,
					Position:    row.Position,
				}
			}
		}

		if len(rows) < gscBatchSize {
			break
		}
		startRow += gscBatchSize
	}

	return results, nil
}

// FetchAIOverviewData queries GSC for AI Overview appearances using the
// searchAppearance dimension filtered to AI_OVERVIEW type.
func (c *GscClient) FetchAIOverviewData(ctx context.Context, urls []string) (map[string]*types.AIVisibilityData, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("gsc: auth: %w", err)
	}

	results := make(map[string]*types.AIVisibilityData, len(urls))
	normalizedToOriginal := make(map[string]string, len(urls))
	for _, u := range urls {
		normalizedToOriginal[normalizeGscURL(u)] = u
	}

	startDate := time.Now().AddDate(0, 0, -(c.days + 3)).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	// Query for pages with AI_OVERVIEW search appearance
	startRow := 0
	for {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		query := map[string]interface{}{
			"startDate":  startDate,
			"endDate":    endDate,
			"dimensions": []string{"page", "query"},
			"dimensionFilterGroups": []map[string]interface{}{
				{
					"filters": []map[string]interface{}{
						{
							"dimension":  "searchAppearance",
							"operator":   "equals",
							"expression": "AI_OVERVIEW",
						},
					},
				},
			},
			"rowLimit": gscBatchSize,
			"startRow": startRow,
		}

		rows, err := c.querySearchAnalytics(ctx, token, query)
		if err != nil {
			// AI Overview data may not be available — return what we have
			return results, err
		}

		for _, row := range rows {
			parts := strings.SplitN(row.Key, "\t", 2)
			if len(parts) < 2 {
				continue
			}
			pageURL := parts[0]
			queryText := parts[1]

			normalizedURL := normalizeGscURL(pageURL)
			originalURL, ok := normalizedToOriginal[normalizedURL]
			if !ok {
				continue
			}

			data, exists := results[originalURL]
			if !exists {
				data = &types.AIVisibilityData{InAIOverview: true}
				results[originalURL] = data
			}
			data.AIImpressions += int(row.Impressions)
			data.AIClicks += int(row.Clicks)
			data.Queries = append(data.Queries, types.AIOverviewQuery{
				Query:       queryText,
				Impressions: int(row.Impressions),
				Clicks:      int(row.Clicks),
				CTR:         row.CTR,
				Position:    row.Position,
			})
		}

		if len(rows) < gscBatchSize {
			break
		}
		startRow += gscBatchSize
	}

	// Compute per-URL CTR
	for _, data := range results {
		if data.AIImpressions > 0 {
			data.AICTR = float64(data.AIClicks) / float64(data.AIImpressions)
		}
	}

	return results, nil
}

type gscRow struct {
	Key         string // tab-joined dimension keys (e.g. page URL, or page\tquery for multi-dimension)
	Impressions float64
	Clicks      float64
	CTR         float64
	Position    float64
}

func (c *GscClient) querySearchAnalytics(ctx context.Context, token string, query map[string]interface{}) ([]gscRow, error) {
	bodyBytes, _ := json.Marshal(query)
	endpoint := gscEndpoint + "/sites/" + url.QueryEscape(c.property) + "/searchAnalytics/query"

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, gscMaxBodySize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gsc: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result struct {
		Rows []struct {
			Keys        []string `json:"keys"`
			Impressions float64  `json:"impressions"`
			Clicks      float64  `json:"clicks"`
			CTR         float64  `json:"ctr"`
			Position    float64  `json:"position"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("gsc: parse: %w", err)
	}

	rows := make([]gscRow, 0, len(result.Rows))
	for _, r := range result.Rows {
		if len(r.Keys) == 0 {
			continue
		}
		rows = append(rows, gscRow{
			Key:         strings.Join(r.Keys, "\t"),
			Impressions: r.Impressions,
			Clicks:      r.Clicks,
			CTR:         r.CTR,
			Position:    r.Position,
		})
	}
	return rows, nil
}

func (c *GscClient) getToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   c.clientEmail,
		"scope": gscScope,
		"aud":   c.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("gsc: sign JWT: %w", err)
	}

	// Exchange JWT for access token
	data := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gsc: token exchange failed: %s", truncate(string(body), 200))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("gsc: parse token: %w", err)
	}

	c.cachedToken = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)
	return c.cachedToken, nil
}

func normalizeGscURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	// Remove trailing slash
	p := u.Path
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
	}
	u.Path = p
	u.Fragment = ""
	return strings.ToLower(u.String())
}
