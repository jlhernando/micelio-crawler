// Package webhook sends HTTP POST notifications on crawl events.
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/crawler"
)

// Payload is the JSON body sent to the webhook URL.
type Payload struct {
	Schedule   *Schedule `json:"schedule,omitempty"`
	Event      string    `json:"event"`
	URL        string    `json:"url"`
	Timestamp  string    `json:"timestamp"`
	Duration   string    `json:"duration"`
	OutputFile string    `json:"outputFile"`
	HTMLReport string    `json:"htmlReport,omitempty"`
	Pages      int       `json:"pages"`
	Errors     int       `json:"errors"`
}

// Schedule holds cron context when the crawl was triggered by a schedule.
type Schedule struct {
	Cron      string `json:"cron"`
	NextRun   string `json:"nextRun"`
	RunNumber int    `json:"runNumber"`
}

// Options configures the webhook target.
type Options struct {
	Headers        map[string]string
	URL            string
	SkipValidation bool // skip SSRF validation (for testing with localhost servers)
}

const (
	maxAttempts = 2
	retryDelay  = 2 * time.Second
	sendTimeout = 10 * time.Second
)

// ValidateURL checks if a webhook URL is safe to dispatch to.
// Returns an error if the URL uses a non-HTTP scheme or targets a private/loopback address.
func ValidateURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("webhook URL must use http:// or https:// scheme")
	}
	if crawler.IsPrivateURL(rawURL) {
		return fmt.Errorf("webhook URL targets a private/loopback address")
	}
	return nil
}

// Send dispatches a webhook POST with retry. Returns true on success.
// Logs warnings on error but never returns an error.
func Send(opts Options, payload Payload) bool {
	if !opts.SkipValidation {
		if err := ValidateURL(opts.URL); err != nil {
			log.Printf("[webhook] blocked: %v (%s)", err, RedactURL(opts.URL))
			return false
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[webhook] marshal error: %v", err)
		return false
	}

	var client *http.Client
	if opts.SkipValidation {
		client = &http.Client{Timeout: sendTimeout}
	} else {
		client = crawler.SafeClientFollowRedirects(sendTimeout)
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, opts.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("[webhook] request error: %v", err)
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Micelio/1.0 Scheduler")
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[webhook] attempt %d failed: %v", attempt+1, err)
		} else {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return true
			}
			log.Printf("[webhook] attempt %d: HTTP %d %s", attempt+1, resp.StatusCode, resp.Status)
		}

		if attempt == 0 {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("[webhook] failed to deliver notification to %s", RedactURL(opts.URL))
	return false
}

// RedactURL strips path and query (may contain tokens) for safe logging.
func RedactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	return fmt.Sprintf("%s://%s/...", parsed.Scheme, parsed.Host)
}
