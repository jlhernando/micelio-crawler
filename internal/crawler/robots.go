package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RobotsChecker fetches and evaluates robots.txt rules.
type RobotsChecker struct {
	crawlDelays map[string]int
	body        string
	sitemapURLs []string
	statusCode  int
	unavailable bool
	fetched     bool
	SkipSSRF    bool // skip SSRF checks (for testing with localhost servers)
}

// Init fetches and parses robots.txt for the given site URL.
func (rc *RobotsChecker) Init(ctx context.Context, siteURL, userAgent string) error {
	parsed, err := url.Parse(siteURL)
	if err != nil {
		return fmt.Errorf("parse site URL: %w", err)
	}

	robotsURL := fmt.Sprintf("%s://%s/robots.txt", parsed.Scheme, parsed.Host)

	// Block private/loopback URLs to prevent SSRF
	if !rc.SkipSSRF && IsPrivateURL(robotsURL) {
		return fmt.Errorf("robots.txt URL targets a private/loopback address")
	}

	var client *http.Client
	if rc.SkipSSRF {
		client = &http.Client{Timeout: 10 * time.Second}
	} else {
		client = DefaultClient(10*time.Second, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return fmt.Errorf("create robots.txt request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		rc.unavailable = true
		rc.fetched = true
		return nil // Non-fatal: treat as unavailable
	}
	defer resp.Body.Close()

	rc.statusCode = resp.StatusCode

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		rc.unavailable = true
		rc.fetched = true
		return nil // Non-fatal: treat as unavailable on read error
	}

	if resp.StatusCode == 200 {
		rc.body = string(body)
	} else if resp.StatusCode >= 500 {
		rc.unavailable = true
	}
	// 4xx → treat as no robots.txt (allow all)

	rc.fetched = true
	rc.parseExtras()
	return nil
}

// parseExtras extracts sitemap URLs and crawl-delay directives.
func (rc *RobotsChecker) parseExtras() {
	if rc.body == "" {
		return
	}

	rc.crawlDelays = make(map[string]int)
	var currentAgents []string
	lastWasAgent := false

	for _, line := range strings.Split(rc.body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if m := MatchDirective(trimmed, "User-agent"); m != "" {
			if !lastWasAgent {
				currentAgents = nil
			}
			currentAgents = append(currentAgents, strings.ToLower(m))
			lastWasAgent = true
			continue
		}
		lastWasAgent = false

		if m := MatchDirective(trimmed, "Sitemap"); m != "" {
			rc.sitemapURLs = append(rc.sitemapURLs, m)
			continue
		}

		if m := MatchDirective(trimmed, "Crawl-delay"); m != "" {
			if delay, err := strconv.Atoi(m); err == nil && delay > 0 {
				for _, agent := range currentAgents {
					rc.crawlDelays[agent] = delay
				}
			}
		}
	}
}

// IsAllowed checks if a URL is allowed for the given user-agent.
// Returns true if robots.txt allows access, false if disallowed.
// If robots.txt is unavailable (5xx), returns false (fully restricted per Google spec).
func (rc *RobotsChecker) IsAllowed(testURL, userAgent string) bool {
	if !rc.fetched {
		return true // Not initialized
	}
	if rc.unavailable {
		return false // 5xx = fully restricted
	}
	if rc.body == "" {
		return true // No robots.txt or 4xx
	}
	return IsAllowedByRobots(rc.body, testURL, userAgent)
}

// IsUnavailable returns true when robots.txt returned a 5xx status.
func (rc *RobotsChecker) IsUnavailable() bool {
	return rc.unavailable
}

// StatusCode returns the HTTP status code of the robots.txt fetch.
func (rc *RobotsChecker) StatusCode() int {
	return rc.statusCode
}

// SitemapURLs returns sitemap URLs declared in robots.txt.
func (rc *RobotsChecker) SitemapURLs() []string {
	return rc.sitemapURLs
}

// CrawlDelay returns the Crawl-delay value (in seconds) for the given user-agent.
// Returns 0 if no delay is specified.
func (rc *RobotsChecker) CrawlDelay(userAgent string) int {
	if rc.crawlDelays == nil {
		return 0
	}
	// Check agent-specific first
	if delay, ok := rc.crawlDelays[strings.ToLower(userAgent)]; ok {
		return delay
	}
	// Fallback to wildcard
	if delay, ok := rc.crawlDelays["*"]; ok {
		return delay
	}
	return 0
}

// IsAllowedByRobots checks if a URL is allowed by robots.txt for a given user-agent.
// Longest matching path wins (most specific rule).
func IsAllowedByRobots(robotsTxt, testURL, userAgent string) bool {
	if robotsTxt == "" {
		return true
	}

	parsed, err := url.Parse(testURL)
	if err != nil {
		return true
	}
	path := parsed.Path
	if path == "" {
		path = "/"
	}

	type rule struct {
		path  string
		allow bool
	}

	agentRules := make(map[string][]rule)
	var currentAgents []string
	lastWasAgent := false

	for _, line := range strings.Split(robotsTxt, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := MatchDirective(trimmed, "User-agent"); m != "" {
			if !lastWasAgent {
				currentAgents = nil
			}
			currentAgents = append(currentAgents, strings.ToLower(m))
			lastWasAgent = true
			continue
		}
		lastWasAgent = false
		if len(currentAgents) == 0 {
			continue
		}
		if m := MatchDirective(trimmed, "Allow"); m != "" {
			for _, agent := range currentAgents {
				agentRules[agent] = append(agentRules[agent], rule{allow: true, path: m})
			}
		}
		if m := MatchDirective(trimmed, "Disallow"); m != "" {
			for _, agent := range currentAgents {
				agentRules[agent] = append(agentRules[agent], rule{allow: false, path: m})
			}
		}
	}

	checkRules := func(rules []rule) *bool {
		var bestMatch *rule
		bestLen := -1
		for i := range rules {
			r := &rules[i]
			if r.path == "" {
				continue
			}
			if strings.HasPrefix(path, r.path) && len(r.path) > bestLen {
				bestMatch = r
				bestLen = len(r.path)
			}
		}
		if bestMatch != nil {
			return &bestMatch.allow
		}
		return nil
	}

	// Check user-agent specific rules first
	if rules, ok := agentRules[strings.ToLower(userAgent)]; ok {
		if result := checkRules(rules); result != nil {
			return *result
		}
	}
	// Fallback to wildcard
	if rules, ok := agentRules["*"]; ok {
		if result := checkRules(rules); result != nil {
			return *result
		}
	}

	return true // Default allow
}

// MatchDirective extracts the value from a robots.txt directive line.
func MatchDirective(line, directive string) string {
	if len(line) < len(directive)+1 {
		return ""
	}
	if !strings.EqualFold(line[:len(directive)], directive) {
		return ""
	}
	rest := line[len(directive):]
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	return strings.TrimSpace(rest[1:])
}
