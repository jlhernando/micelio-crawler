// Package webhook sends HTTP POST notifications on crawl events.
package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
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
	Reason     string    `json:"reason,omitempty"` // why a run was skipped or stopped
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
			// The error quotes the URL, which may hold a token.
			log.Printf("[webhook] invalid webhook URL %s", RedactURL(opts.URL))
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Micelio/1.0 Scheduler")
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[webhook] attempt %d failed: %s", attempt+1, redactErr(err))
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
// Something that does not parse as a URL with a host is not shown at all.
func RedactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "[invalid URL]"
	}
	return fmt.Sprintf("%s://%s/...", parsed.Scheme, parsed.Host)
}

// urlInText finds URLs inside an error message.
var urlInText = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"']+`)

// redactErr renders a delivery error without the URL's path and query:
// http.Client wraps every failure (DNS, TLS, timeout, redirect) in a
// *url.Error that quotes the full request URL, and webhook URLs carry
// their token there (Slack: in the path).
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + " " + RedactURL(ue.URL) + ": " + redactErr(ue.Err)
	}
	return urlInText.ReplaceAllStringFunc(err.Error(), RedactURL)
}
