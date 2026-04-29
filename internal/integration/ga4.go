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

var ga4Endpoint = "https://analyticsdata.googleapis.com/v1beta"

const (
	ga4BatchSize   = 100000
	ga4MaxBodySize = 4 << 20 // 4 MB
	ga4TokenURL    = "https://oauth2.googleapis.com/token"
	ga4Scope       = "https://www.googleapis.com/auth/analytics.readonly"
)

// setGa4Endpoint overrides the GA4 endpoint (for testing).
func setGa4Endpoint(ep string) { ga4Endpoint = ep }

// GA4Client fetches Google Analytics 4 data using a service account.
type GA4Client struct {
	tokenExpiry time.Time
	httpClient  *http.Client
	privateKey  *rsa.PrivateKey
	propertyID  string
	clientEmail string
	tokenURL    string
	cachedToken string
	days        int
	tokenMu     sync.Mutex
}

// NewGA4Client creates a new GA4 client from a service account key file JSON.
func NewGA4Client(keyJSON []byte, propertyID string, days int) (*GA4Client, error) {
	var kf GscKeyFile // same structure
	if err := json.Unmarshal(keyJSON, &kf); err != nil {
		return nil, fmt.Errorf("ga4: parse key file: %w", err)
	}

	block, _ := pem.Decode([]byte(kf.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("ga4: invalid private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ga4: parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ga4: key is not RSA")
	}

	tokenURL := kf.TokenURI
	if tokenURL == "" {
		tokenURL = ga4TokenURL
	}

	if days <= 0 {
		days = 90
	}

	return &GA4Client{
		propertyID:  propertyID,
		days:        days,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		clientEmail: kf.ClientEmail,
		privateKey:  rsaKey,
		tokenURL:    tokenURL,
	}, nil
}

// FetchBatch fetches GA4 data for multiple URLs, returning a map of URL → Ga4Data.
func (c *GA4Client) FetchBatch(ctx context.Context, urls []string) (map[string]*types.Ga4Data, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("ga4: auth: %w", err)
	}

	// Build path → URL map (multiple URLs may map to same path)
	pathToURLs := make(map[string][]string, len(urls))
	for _, u := range urls {
		p := normalizeGA4Path(u)
		pathToURLs[p] = append(pathToURLs[p], u)
	}

	results := make(map[string]*types.Ga4Data, len(urls))

	startDate := time.Now().AddDate(0, 0, -c.days).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	offset := 0
	for {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		rows, total, err := c.runReport(ctx, token, startDate, endDate, offset)
		if err != nil {
			return results, err
		}

		for _, row := range rows {
			normalizedPath := normalizeGA4Path("https://example.com" + row.Path)
			if originalURLs, ok := pathToURLs[normalizedPath]; ok {
				for _, u := range originalURLs {
					results[u] = row.Data
				}
			}
		}

		offset += len(rows)
		if offset >= total || len(rows) == 0 {
			break
		}
	}

	return results, nil
}

type ga4Row struct {
	Data *types.Ga4Data
	Path string
}

func (c *GA4Client) runReport(ctx context.Context, token, startDate, endDate string, offset int) ([]ga4Row, int, error) {
	query := map[string]interface{}{
		"dateRanges": []map[string]string{{
			"startDate": startDate,
			"endDate":   endDate,
		}},
		"dimensions": []map[string]string{{"name": "pagePath"}},
		"metrics": []map[string]string{
			{"name": "sessions"},
			{"name": "screenPageViews"},
			{"name": "bounceRate"},
			{"name": "conversions"},
			{"name": "activeUsers"},
			{"name": "engagementRate"},
			{"name": "averageSessionDuration"},
		},
		"limit":  ga4BatchSize,
		"offset": offset,
	}

	bodyBytes, _ := json.Marshal(query)
	endpoint := ga4Endpoint + "/properties/" + c.propertyID + ":runReport"

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, ga4MaxBodySize))
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("ga4: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result struct {
		Rows []struct {
			DimensionValues []struct {
				Value string `json:"value"`
			} `json:"dimensionValues"`
			MetricValues []struct {
				Value string `json:"value"`
			} `json:"metricValues"`
		} `json:"rows"`
		RowCount int `json:"rowCount"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, 0, fmt.Errorf("ga4: parse: %w", err)
	}

	rows := make([]ga4Row, 0, len(result.Rows))
	for _, r := range result.Rows {
		if len(r.DimensionValues) == 0 || len(r.MetricValues) < 7 {
			continue
		}
		rows = append(rows, ga4Row{
			Path: r.DimensionValues[0].Value,
			Data: &types.Ga4Data{
				Sessions:           parseIntFromString(r.MetricValues[0].Value),
				Pageviews:          parseIntFromString(r.MetricValues[1].Value),
				BounceRate:         parseFloatFromString(r.MetricValues[2].Value),
				Conversions:        parseIntFromString(r.MetricValues[3].Value),
				ActiveUsers:        parseIntFromString(r.MetricValues[4].Value),
				EngagementRate:     parseFloatFromString(r.MetricValues[5].Value),
				AvgSessionDuration: parseFloatFromString(r.MetricValues[6].Value),
			},
		})
	}

	return rows, result.RowCount, nil
}

func (c *GA4Client) getToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   c.clientEmail,
		"scope": ga4Scope,
		"aud":   c.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("ga4: sign JWT: %w", err)
	}

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
		return "", fmt.Errorf("ga4: token exchange failed: %s", truncate(string(body), 200))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("ga4: parse token: %w", err)
	}

	c.cachedToken = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)
	return c.cachedToken, nil
}

func normalizeGA4Path(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	// Remove trailing slash (except root)
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
	}
	// Decode percent-encoding
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	return strings.ToLower(p)
}

func parseIntFromString(s string) int {
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

func parseFloatFromString(s string) float64 {
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}
