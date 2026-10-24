package crawler

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// robotsMaxRedirects is the number of redirect hops followed when fetching
	// robots.txt (Google follows at least five).
	robotsMaxRedirects = 5
	// robotsMaxBytes is the maximum robots.txt size read (Google enforces 500 KiB).
	robotsMaxBytes = 500 * 1024
)

// RobotsChecker fetches and evaluates robots.txt rules for a single origin.
//
// Status handling follows Google's robots.txt spec:
//   - 2xx: rules are parsed and applied.
//   - 3xx: up to 5 redirects are followed (also across hosts, e.g. http->https,
//     apex->www). More than that is treated like a 404.
//   - 4xx (except 429): no robots.txt, everything is allowed.
//   - 429, 5xx and network errors: the site is treated as fully disallowed for
//     the rest of the crawl (conservative; Google does the same temporarily).
type RobotsChecker struct {
	rules       *robotsRules
	body        string
	sitemapURLs []string
	statusCode  int
	unavailable bool
	fetched     bool
	SkipSSRF    bool // skip SSRF checks (for testing with localhost servers)
	// Client is the HTTP client used to fetch robots.txt. Pass the crawl client
	// so robots.txt goes through the same proxy/Tor/stealth/SSRF settings.
	// Redirects are followed manually, so a no-redirect client is expected.
	Client *http.Client

	selMu    sync.Mutex
	selCache map[string]*robotsGroupSel // user-agent -> selected rules
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

	client := rc.Client
	if client == nil {
		if rc.SkipSSRF {
			client = UnsafeClient(10*time.Second, nil)
		} else {
			client = DefaultClient(10*time.Second, nil)
		}
	}
	// Bound the whole robots fetch (all hops) even if the crawl client has a
	// longer timeout.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rc.fetched = true
	current := robotsURL
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return fmt.Errorf("create robots.txt request: %w", err)
		}
		if userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}
		req.Header.Set("Accept", "text/plain,*/*;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			rc.unavailable = true
			return nil // Non-fatal: network error => treat as unavailable (disallow all)
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			drainAndClose(resp.Body)
			if location == "" {
				rc.statusCode = resp.StatusCode
				break // nothing to follow: treat like 4xx (allow all)
			}
			if hop >= robotsMaxRedirects {
				rc.statusCode = resp.StatusCode
				break // too many redirects: treat like 404 (allow all)
			}
			next := resolveURL(current, location)
			if !rc.SkipSSRF && IsPrivateURL(next) {
				rc.unavailable = true
				rc.statusCode = resp.StatusCode
				return fmt.Errorf("robots.txt redirect to private/loopback address blocked: %s", next)
			}
			current = next
			continue
		}

		rc.statusCode = resp.StatusCode
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, robotsMaxBytes))
		resp.Body.Close()

		switch {
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			rc.unavailable = true
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if readErr != nil {
				rc.unavailable = true
				return nil
			}
			rc.body = string(body)
		}
		// Other 4xx => no robots.txt (allow all)
		break
	}

	rc.parseExtras()
	return nil
}

// parseExtras parses the stored robots.txt body once and caches the rules,
// sitemap URLs and crawl-delay directives.
func (rc *RobotsChecker) parseExtras() {
	rc.rules = parseRobots(rc.body)
	rc.sitemapURLs = rc.rules.sitemaps
	rc.selMu.Lock()
	rc.selCache = nil
	rc.selMu.Unlock()
}

func (rc *RobotsChecker) selection(userAgent string) *robotsGroupSel {
	rc.selMu.Lock()
	defer rc.selMu.Unlock()
	if sel, ok := rc.selCache[userAgent]; ok {
		return sel
	}
	if rc.rules == nil {
		rc.rules = parseRobots(rc.body)
	}
	sel := rc.rules.selectGroups(userAgent)
	if rc.selCache == nil {
		rc.selCache = make(map[string]*robotsGroupSel)
	}
	rc.selCache[userAgent] = sel
	return sel
}

// IsAllowed checks if a URL is allowed for the given user-agent.
// Returns true if robots.txt allows access, false if disallowed.
// If robots.txt is unavailable (429/5xx/network error), returns false
// (fully restricted per Google spec).
func (rc *RobotsChecker) IsAllowed(testURL, userAgent string) bool {
	if !rc.fetched {
		return true // Not initialized
	}
	if rc.unavailable {
		return false
	}
	if rc.body == "" {
		return true // No robots.txt or 4xx
	}
	return rc.selection(userAgent).allowed(testURL)
}

// IsUnavailable returns true when robots.txt could not be fetched (429/5xx/network error).
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

// CrawlDelay returns the Crawl-delay value (in whole seconds, rounded up) for
// the given user-agent. Returns 0 if no delay is specified.
func (rc *RobotsChecker) CrawlDelay(userAgent string) int {
	d := rc.CrawlDelayDuration(userAgent)
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

// CrawlDelayDuration returns the Crawl-delay for the user-agent's group
// (falling back to the * group). Fractional values like 0.5 are supported.
func (rc *RobotsChecker) CrawlDelayDuration(userAgent string) time.Duration {
	if rc.body == "" {
		return 0
	}
	return rc.selection(userAgent).crawlDelay
}

// IsAllowedByRobots checks if a URL is allowed by robots.txt for a given user-agent.
// Implements Google's matching rules: group selection by product token,
// `*` wildcards, `$` end anchor, matching on path+query, longest match wins and
// Allow wins ties.
func IsAllowedByRobots(robotsTxt, testURL, userAgent string) bool {
	if robotsTxt == "" {
		return true
	}
	return parseRobots(robotsTxt).selectGroups(userAgent).allowed(testURL)
}

// --- Parsing & matching ---

type robotsRule struct {
	pattern string
	allow   bool
}

type robotsGroup struct {
	agents     []string // lower-cased product tokens ("*" for wildcard)
	rules      []robotsRule
	crawlDelay time.Duration
	hasDelay   bool
}

type robotsRules struct {
	groups   []*robotsGroup
	sitemaps []string
}

// robotsGroupSel is the merged rule set that applies to one user-agent.
type robotsGroupSel struct {
	rules      []robotsRule
	crawlDelay time.Duration
}

// parseRobots parses a robots.txt body into groups.
func parseRobots(body string) *robotsRules {
	r := &robotsRules{}
	if body == "" {
		return r
	}
	var cur *robotsGroup
	lastWasAgent := false

	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		val := strings.TrimSpace(line[colon+1:])

		switch key {
		case "user-agent", "useragent", "user agent":
			if !lastWasAgent || cur == nil {
				cur = &robotsGroup{}
				r.groups = append(r.groups, cur)
			}
			if tok := robotsAgentToken(val); tok != "" {
				cur.agents = append(cur.agents, tok)
			}
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if cur == nil {
				continue // rule before any user-agent line: ignored
			}
			cur.rules = append(cur.rules, robotsRule{pattern: normalizeRobotsPattern(val), allow: key == "allow"})
		case "crawl-delay", "crawl delay":
			lastWasAgent = false
			if cur == nil {
				continue
			}
			if secs, err := strconv.ParseFloat(val, 64); err == nil && secs > 0 && !math.IsInf(secs, 0) {
				if secs > 86400 {
					secs = 86400
				}
				cur.crawlDelay = time.Duration(secs * float64(time.Second))
				cur.hasDelay = true
			}
		case "sitemap", "site-map":
			// Sitemap lines are not part of any group and do not end one.
			if val != "" {
				r.sitemaps = append(r.sitemaps, val)
			}
		default:
			// Unknown directives are ignored.
		}
	}
	return r
}

// robotsAgentToken extracts the product token from a robots.txt user-agent
// value: leading [A-Za-z_-] characters, lower-cased. "*" stays "*".
func robotsAgentToken(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "*") {
		return "*"
	}
	end := 0
	for end < len(v) {
		c := v[end]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '-' {
			end++
			continue
		}
		break
	}
	return strings.ToLower(v[:end])
}

// crawlerProductTokens returns the candidate product tokens for the crawler's
// User-Agent string, most specific first. "Micelio/1.0" -> ["micelio"];
// "Mozilla/5.0 (compatible; Googlebot/2.1; ...)" -> ["googlebot", "mozilla"].
func crawlerProductTokens(userAgent string) []string {
	var toks []string
	if i := strings.Index(strings.ToLower(userAgent), "compatible;"); i >= 0 {
		if t := robotsAgentToken(userAgent[i+len("compatible;"):]); t != "" && t != "*" {
			toks = append(toks, t)
		}
	}
	if t := robotsAgentToken(userAgent); t != "" && t != "*" {
		toks = append(toks, t)
	}
	return toks
}

// selectGroups merges the groups that apply to userAgent: all groups naming
// the crawler's product token, otherwise all `*` groups.
func (r *robotsRules) selectGroups(userAgent string) *robotsGroupSel {
	sel := &robotsGroupSel{}
	pick := func(match func(agent string) bool) bool {
		found := false
		for _, g := range r.groups {
			for _, a := range g.agents {
				if match(a) {
					found = true
					sel.rules = append(sel.rules, g.rules...)
					if g.hasDelay && sel.crawlDelay == 0 {
						sel.crawlDelay = g.crawlDelay
					}
					break
				}
			}
		}
		return found
	}
	for _, tok := range crawlerProductTokens(userAgent) {
		if pick(func(a string) bool { return a == tok }) {
			return sel
		}
	}
	pick(func(a string) bool { return a == "*" })
	return sel
}

// allowed reports whether testURL may be fetched under the selected rules.
func (sel *robotsGroupSel) allowed(testURL string) bool {
	parsed, err := url.Parse(testURL)
	if err != nil {
		return true
	}
	target := parsed.EscapedPath()
	if target == "" {
		target = "/"
	}
	if target == "/robots.txt" {
		return true // robots.txt itself is always allowed
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		target += "?" + parsed.RawQuery
	}
	target = normalizeRobotsPercent(target)

	bestLen := -1
	bestAllow := true
	for _, r := range sel.rules {
		if r.pattern == "" {
			continue // empty Disallow/Allow: no restriction
		}
		if !robotsPatternMatch(target, r.pattern) {
			continue
		}
		n := len(r.pattern)
		if n > bestLen || (n == bestLen && r.allow && !bestAllow) {
			bestLen = n
			bestAllow = r.allow
		}
	}
	return bestAllow
}

// robotsPatternMatch reports whether pattern matches path from the start.
// `*` matches any sequence of characters; a trailing `$` anchors the end.
func robotsPatternMatch(path, pattern string) bool {
	// pos holds the sorted set of path offsets reachable after consuming the
	// pattern prefix processed so far.
	pos := make([]int, 1, len(path)+1)
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '$' && i == len(pattern)-1 {
			return pos[len(pos)-1] == len(path)
		}
		if c == '*' {
			start := pos[0]
			pos = pos[:0]
			for j := start; j <= len(path); j++ {
				pos = append(pos, j)
			}
			continue
		}
		n := 0
		for _, p := range pos {
			if p < len(path) && path[p] == c {
				pos[n] = p + 1
				n++
			}
		}
		if n == 0 {
			return false
		}
		pos = pos[:n]
	}
	return true
}

// normalizeRobotsPattern percent-encodes non-ASCII bytes and upper-cases
// existing escapes so patterns compare equal to escaped URL paths.
func normalizeRobotsPattern(p string) string {
	return normalizeRobotsPercent(p)
}

func normalizeRobotsPercent(s string) string {
	needs := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 || c == ' ' || (c == '%' && i+2 < len(s) && (isLowerHex(s[i+1]) || isLowerHex(s[i+2]))) {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x80 || c == ' ':
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte('%')
			b.WriteByte(upperHex(s[i+1]))
			b.WriteByte(upperHex(s[i+2]))
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isLowerHex(c byte) bool { return c >= 'a' && c <= 'f' }

func upperHex(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 'A'
	}
	return c
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

// --- Per-origin registry ---

// robotsRegistry lazily fetches and caches robots.txt per origin
// (scheme://host[:port]) so every host the crawl touches (seed, post-redirect
// host, --allowed-domains) is checked against its own robots.txt.
type robotsRegistry struct {
	mu        sync.Mutex
	entries   map[string]*robotsEntry
	userAgent string
	newRC     func() *RobotsChecker
	onLoad    func(origin string, rc *RobotsChecker)
}

type robotsEntry struct {
	once sync.Once
	rc   *RobotsChecker
}

func newRobotsRegistry(userAgent string, newRC func() *RobotsChecker, onLoad func(string, *RobotsChecker)) *robotsRegistry {
	return &robotsRegistry{
		entries:   make(map[string]*robotsEntry),
		userAgent: userAgent,
		newRC:     newRC,
		onLoad:    onLoad,
	}
}

func robotsOrigin(rawURL string) string {
	p, err := url.Parse(rawURL)
	if err != nil || p.Host == "" {
		return ""
	}
	return strings.ToLower(p.Scheme) + "://" + strings.ToLower(p.Host)
}

// add registers an already-initialized checker for origin.
func (r *robotsRegistry) add(origin string, rc *RobotsChecker) {
	e := &robotsEntry{rc: rc}
	e.once.Do(func() {})
	r.mu.Lock()
	r.entries[origin] = e
	r.mu.Unlock()
	if r.onLoad != nil {
		r.onLoad(origin, rc)
	}
}

// get returns the checker for rawURL's origin, fetching robots.txt on first use.
func (r *robotsRegistry) get(ctx context.Context, rawURL string) *RobotsChecker {
	origin := robotsOrigin(rawURL)
	if origin == "" {
		return nil
	}
	r.mu.Lock()
	e, ok := r.entries[origin]
	if !ok {
		e = &robotsEntry{}
		r.entries[origin] = e
	}
	r.mu.Unlock()
	e.once.Do(func() {
		rc := r.newRC()
		if err := rc.Init(ctx, origin+"/", r.userAgent); err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] robots.txt %s: %v\n", origin, err)
		}
		e.rc = rc
		if r.onLoad != nil {
			r.onLoad(origin, rc)
		}
	})
	return e.rc
}

// IsAllowed checks rawURL against the robots.txt of its own origin.
func (r *robotsRegistry) IsAllowed(ctx context.Context, rawURL string) bool {
	rc := r.get(ctx, rawURL)
	if rc == nil {
		return true
	}
	return rc.IsAllowed(rawURL, r.userAgent)
}
