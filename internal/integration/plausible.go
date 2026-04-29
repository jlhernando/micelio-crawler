package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/types"
)

const (
	plausibleDefaultHost = "https://plausible.io"
	plausibleChunkSize   = 100
	plausibleMaxRetries  = 3
	plausibleMaxBodySize = 2 << 20 // 2 MB
)

// PlausibleClient fetches analytics data from Plausible.
type PlausibleClient struct {
	httpClient *http.Client
	apiKey     string
	siteID     string
	host       string
	days       int
}

// NewPlausibleClient creates a new Plausible Analytics client.
func NewPlausibleClient(apiKey, siteID string, days int, host string) *PlausibleClient {
	if host == "" {
		host = plausibleDefaultHost
	}
	if days <= 0 {
		days = 30
	}
	return &PlausibleClient{
		apiKey:     apiKey,
		siteID:     siteID,
		host:       strings.TrimRight(host, "/"),
		days:       days,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchBatch fetches Plausible data for multiple URLs, chunked in batches.
func (c *PlausibleClient) FetchBatch(ctx context.Context, urls []string) (map[string]*types.PlausibleData, error) {
	results := make(map[string]*types.PlausibleData, len(urls))

	// Build path → URL mapping
	pathMap := make(map[string]string, len(urls))
	var paths []string
	for _, u := range urls {
		p := normalizePlausiblePath(u)
		pathMap[p] = u
		paths = append(paths, p)
	}

	// Process in chunks
	for i := 0; i < len(paths); i += plausibleChunkSize {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		end := i + plausibleChunkSize
		if end > len(paths) {
			end = len(paths)
		}
		chunk := paths[i:end]

		data, err := c.fetchChunk(ctx, chunk)
		if err != nil {
			continue
		}

		for path, d := range data {
			if originalURL, ok := pathMap[path]; ok {
				results[originalURL] = d
			}
		}
	}

	return results, nil
}

func (c *PlausibleClient) fetchChunk(ctx context.Context, paths []string) (map[string]*types.PlausibleData, error) {
	dateFrom := time.Now().AddDate(0, 0, -c.days).Format("2006-01-02")
	dateTo := time.Now().Format("2006-01-02")

	query := map[string]interface{}{
		"site_id":    c.siteID,
		"metrics":    []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration", "time_on_page", "scroll_depth"},
		"date_range": []string{dateFrom, dateTo},
		"dimensions": []string{"event:page"},
		"filters":    []interface{}{[]interface{}{"is", "event:page", paths}},
	}

	bodyBytes, _ := json.Marshal(query)

	var resp *http.Response
	var err error
	for attempt := 0; attempt < plausibleMaxRetries; attempt++ {
		endpoint := c.host + "/api/v2/query"
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		break
	}

	if resp == nil {
		return nil, fmt.Errorf("plausible: all retries failed")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, plausibleMaxBodySize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plausible: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	return parsePlausibleResponse(respBody)
}

func parsePlausibleResponse(body []byte) (map[string]*types.PlausibleData, error) {
	var raw struct {
		Results []struct {
			Dimensions []string  `json:"dimensions"`
			Metrics    []float64 `json:"metrics"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("plausible: parse: %w", err)
	}

	results := make(map[string]*types.PlausibleData, len(raw.Results))
	for _, row := range raw.Results {
		if len(row.Dimensions) == 0 || len(row.Metrics) < 5 {
			continue
		}
		path := row.Dimensions[0]
		d := &types.PlausibleData{
			Visitors:      int(row.Metrics[0]),
			Visits:        int(row.Metrics[1]),
			Pageviews:     int(row.Metrics[2]),
			BounceRate:    row.Metrics[3],
			VisitDuration: row.Metrics[4],
		}
		if d.Visits > 0 {
			d.ViewsPerVisit = float64(d.Pageviews) / float64(d.Visits)
		}
		if len(row.Metrics) > 5 {
			v := row.Metrics[5]
			d.TimeOnPage = &v
		}
		if len(row.Metrics) > 6 {
			v := row.Metrics[6]
			d.ScrollDepth = &v
		}
		results[path] = d
	}

	return results, nil
}

func normalizePlausiblePath(rawURL string) string {
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
