// Package crawler implements the core crawl engine.
package crawler

import (
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/urlutil"
)

// FormatError extracts a human-readable error message.
func FormatError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// FormatDateYmd formats a time as YYYY-MM-DD.
func FormatDateYmd(t time.Time) string {
	return t.Format("2006-01-02")
}

// NormalizeURL normalizes a URL for consistent comparison:
// strip hash, trailing slash, normalize default ports, sort query params.
// Returns empty string if invalid.
func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}

	// Strip fragment
	parsed.Fragment = ""
	parsed.RawFragment = ""

	// Normalize default ports
	host := parsed.Hostname()
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		parsed.Host = host
	}

	// Host names are case-insensitive (RFC 3986 §3.2.2)
	parsed.Host = strings.ToLower(parsed.Host)

	// Normalize empty path to "/"
	if parsed.Path == "" {
		parsed.Path = "/"
	}

	// Strip trailing slash (but keep root "/")
	if len(parsed.Path) > 1 && strings.HasSuffix(parsed.Path, "/") {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}

	// Sort query parameters
	if parsed.RawQuery != "" {
		parsed.RawQuery = urlutil.SortedQuery(parsed.Query())
	}

	return parsed.String()
}

// normalizeURL strips one trailing slash for consistent lookup.
func normalizeURL(u string) string { return urlutil.TrimTrailingSlash(u) }

// NormalizeURLForComparison normalizes a URL for canonical comparison:
// strips www prefix, trailing slash, protocol. Returns a protocol-agnostic key
// with query parameters sorted.
func NormalizeURLForComparison(rawURL string) string {
	return urlutil.ComparisonKey(rawURL, true)
}

// IsPrivateURL checks whether a URL's host is a literal private address or
// a name for this computer (localhost). Host names are checked again when
// they are resolved (SafeDialer, ssrfSafeDialer). Used for SSRF protection.
func IsPrivateURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && blockedIP(ip)
}
