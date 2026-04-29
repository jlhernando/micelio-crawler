package integration

import (
	"bytes"
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

var cruxEndpoint = "https://chromeuxreport.googleapis.com/v1/records:queryRecord"

const (
	cruxMaxPerMinute    = 150
	cruxDefaultConc     = 10
	cruxMaxBodySize     = 1 << 20 // 1 MB
	cruxBackoffDuration = 60 * time.Second
)

// setCruxEndpoint overrides the CrUX endpoint (for testing).
func setCruxEndpoint(ep string) { cruxEndpoint = ep }

// CrUX assessment thresholds.
type CrUXAssessment string

const (
	CrUXGood             CrUXAssessment = "good"
	CrUXNeedsImprovement CrUXAssessment = "needs-improvement"
	CrUXPoor             CrUXAssessment = "poor"
)

// CruxClient fetches Chrome UX Report data with rate limiting and concurrency control.
type CruxClient struct {
	httpClient *http.Client
	apiKey     string
	formFactor types.CrUXFormFactor
	timestamps []time.Time
	conc       int
	mu         sync.Mutex
}

// NewCruxClient creates a new CrUX client.
func NewCruxClient(apiKey string, formFactor types.CrUXFormFactor, concurrency int) *CruxClient {
	if concurrency <= 0 {
		concurrency = cruxDefaultConc
	}
	return &CruxClient{
		apiKey:     apiKey,
		formFactor: formFactor,
		conc:       concurrency,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchBatch fetches CrUX data for multiple URLs with bounded concurrency.
func (c *CruxClient) FetchBatch(ctx context.Context, urls []string) map[string]*types.CruxData {
	results := make(map[string]*types.CruxData, len(urls))
	var mu sync.Mutex

	sem := make(chan struct{}, c.conc)
	var wg sync.WaitGroup

	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(pageURL string) {
			defer func() { <-sem; wg.Done() }()
			data, err := c.fetch(ctx, pageURL)
			if err != nil {
				return // no data for this URL
			}
			mu.Lock()
			results[pageURL] = data
			mu.Unlock()
		}(u)
	}
	wg.Wait()
	return results
}

func (c *CruxClient) fetch(ctx context.Context, pageURL string) (*types.CruxData, error) {
	if err := c.waitForRateLimit(ctx); err != nil {
		return nil, err
	}

	reqBody := map[string]interface{}{
		"url": pageURL,
	}
	if c.formFactor != "" && c.formFactor != types.FormFactorAll {
		reqBody["formFactor"] = string(c.formFactor)
	}

	bodyBytes, _ := json.Marshal(reqBody)
	endpoint := cruxEndpoint + "?key=" + url.QueryEscape(c.apiKey)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, cruxMaxBodySize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no crux data for %s", pageURL)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		select {
		case <-time.After(cruxBackoffDuration):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("crux: rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crux: HTTP %d", resp.StatusCode)
	}

	return parseCruxResponse(respBody, c.formFactor)
}

func (c *CruxClient) waitForRateLimit(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Trim old timestamps
	valid := 0
	for _, ts := range c.timestamps {
		if ts.After(cutoff) {
			c.timestamps[valid] = ts
			valid++
		}
	}
	c.timestamps = c.timestamps[:valid]

	if len(c.timestamps) >= cruxMaxPerMinute {
		sleepUntil := c.timestamps[0].Add(time.Minute)
		c.mu.Unlock()
		select {
		case <-time.After(time.Until(sleepUntil)):
		case <-ctx.Done():
			c.mu.Lock()
			return ctx.Err()
		}
		c.mu.Lock()
		// Re-trim after sleep
		now = time.Now()
		cutoff = now.Add(-time.Minute)
		valid = 0
		for _, ts := range c.timestamps {
			if ts.After(cutoff) {
				c.timestamps[valid] = ts
				valid++
			}
		}
		c.timestamps = c.timestamps[:valid]
	}

	c.timestamps = append(c.timestamps, time.Now())
	return nil
}

func parseCruxResponse(body []byte, formFactor types.CrUXFormFactor) (*types.CruxData, error) {
	var raw struct {
		Record struct {
			Metrics map[string]struct {
				Percentiles struct {
					P75 json.RawMessage `json:"p75"`
				} `json:"percentiles"`
			} `json:"metrics"`
		} `json:"record"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("crux: parse: %w", err)
	}

	data := &types.CruxData{FormFactor: formFactor}
	if formFactor == "" {
		data.FormFactor = types.FormFactorAll
	}

	metrics := raw.Record.Metrics
	data.LCPMs = extractP75Float(metrics, "largest_contentful_paint")
	data.FIDMs = extractP75Float(metrics, "first_input_delay")
	data.INPMs = extractP75Float(metrics, "interaction_to_next_paint")
	data.CLS = extractP75Float(metrics, "cumulative_layout_shift")
	data.TTFBMs = extractP75Float(metrics, "experimental_time_to_first_byte")
	data.FCPMs = extractP75Float(metrics, "first_contentful_paint")

	return data, nil
}

func extractP75Float(metrics map[string]struct {
	Percentiles struct {
		P75 json.RawMessage `json:"p75"`
	} `json:"percentiles"`
}, key string) *float64 {
	m, ok := metrics[key]
	if !ok || m.Percentiles.P75 == nil {
		return nil
	}
	var v float64
	if err := json.Unmarshal(m.Percentiles.P75, &v); err != nil {
		return nil
	}
	return &v
}

// GetCrUXAssessment evaluates a metric value against Google's CWV thresholds.
func GetCrUXAssessment(metric string, value float64) CrUXAssessment {
	thresholds := map[string][2]float64{
		"lcp":  {2500, 4000},
		"fid":  {100, 300},
		"inp":  {200, 500},
		"cls":  {0.1, 0.25},
		"ttfb": {800, 1800},
		"fcp":  {1800, 3000},
	}
	t, ok := thresholds[metric]
	if !ok {
		return CrUXGood
	}
	if value <= t[0] {
		return CrUXGood
	}
	if value <= t[1] {
		return CrUXNeedsImprovement
	}
	return CrUXPoor
}
