// Package integration implements third-party API clients for SEO data enrichment.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/types"
)

var psiEndpoint = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

const (
	psiMinInterval = 4 * time.Second
	psiMaxBodySize = 1 << 20 // 1 MB
)

// setPSIEndpoint overrides the PSI endpoint (for testing).
func setPSIEndpoint(ep string) { psiEndpoint = ep }

// PSIClient fetches PageSpeed Insights data with rate limiting.
type PSIClient struct {
	lastCall   time.Time
	httpClient *http.Client
	apiKey     string
	mu         sync.Mutex
}

// NewPSIClient creates a new PageSpeed Insights client.
func NewPSIClient(apiKey string) *PSIClient {
	return &PSIClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Fetch retrieves PageSpeed Insights data for a single URL.
func (c *PSIClient) Fetch(ctx context.Context, pageURL string) (*types.PageSpeedData, error) {
	if err := c.rateLimit(ctx); err != nil {
		return nil, err
	}

	params := url.Values{
		"url":      {pageURL},
		"strategy": {"mobile"},
		"category": {"performance"},
	}
	if c.apiKey != "" {
		params.Set("key", c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", psiEndpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("psi: create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("psi: fetch %s: %w", pageURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, psiMaxBodySize))
	if err != nil {
		return nil, fmt.Errorf("psi: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &types.PageSpeedData{Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))}, nil
	}

	return parsePSIResponse(body)
}

// FetchBatch fetches PSI data for multiple URLs sequentially (rate-limited).
func (c *PSIClient) FetchBatch(ctx context.Context, urls []string) map[string]*types.PageSpeedData {
	results := make(map[string]*types.PageSpeedData, len(urls))
	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		data, err := c.Fetch(ctx, u)
		if err != nil {
			results[u] = &types.PageSpeedData{Error: err.Error()}
		} else {
			results[u] = data
		}
	}
	return results
}

func (c *PSIClient) rateLimit(ctx context.Context) error {
	c.mu.Lock()
	elapsed := time.Since(c.lastCall)
	if elapsed < psiMinInterval {
		wait := psiMinInterval - elapsed
		c.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		c.mu.Lock()
	}
	c.lastCall = time.Now()
	c.mu.Unlock()
	return nil
}

func parsePSIResponse(body []byte) (*types.PageSpeedData, error) {
	var raw struct {
		LighthouseResult struct {
			Categories struct {
				Performance struct {
					Score *float64 `json:"score"`
				} `json:"performance"`
			} `json:"categories"`
			Audits map[string]struct {
				NumericValue *float64 `json:"numericValue"`
			} `json:"audits"`
		} `json:"lighthouseResult"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("psi: parse response: %w", err)
	}

	audits := raw.LighthouseResult.Audits
	data := &types.PageSpeedData{}

	if s := raw.LighthouseResult.Categories.Performance.Score; s != nil {
		data.PerformanceScore = *s * 100
	}
	if a, ok := audits["largest-contentful-paint"]; ok && a.NumericValue != nil {
		data.LCP = *a.NumericValue
	}
	if a, ok := audits["first-input-delay"]; ok && a.NumericValue != nil {
		data.FID = *a.NumericValue
	}
	if a, ok := audits["interaction-to-next-paint"]; ok && a.NumericValue != nil {
		data.INP = *a.NumericValue
	}
	if a, ok := audits["cumulative-layout-shift"]; ok && a.NumericValue != nil {
		data.CLS = *a.NumericValue
	}
	if a, ok := audits["server-response-time"]; ok && a.NumericValue != nil {
		data.TTFB = *a.NumericValue
	}
	if a, ok := audits["speed-index"]; ok && a.NumericValue != nil {
		data.SpeedIndex = *a.NumericValue
	}
	if a, ok := audits["total-blocking-time"]; ok && a.NumericValue != nil {
		data.TBT = *a.NumericValue
	}

	return data, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
