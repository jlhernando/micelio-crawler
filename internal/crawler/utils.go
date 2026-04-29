// Package crawler implements the core crawl engine.
package crawler

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
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
		params := parsed.Query()
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var sb strings.Builder
		first := true
		for _, k := range keys {
			vals := params[k]
			sort.Strings(vals)
			for _, v := range vals {
				if !first {
					sb.WriteByte('&')
				}
				sb.WriteString(url.QueryEscape(k))
				sb.WriteByte('=')
				sb.WriteString(url.QueryEscape(v))
				first = false
			}
		}
		parsed.RawQuery = sb.String()
	}

	return parsed.String()
}

// NormalizeURLForComparison normalizes a URL for canonical comparison:
// strips www prefix, trailing slash, protocol. Returns a protocol-agnostic key.
func NormalizeURLForComparison(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.TrimRight(rawURL, "/")
	}

	host := strings.TrimPrefix(parsed.Hostname(), "www.")
	path := parsed.Path
	if path == "" {
		path = "/"
	} else if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}

	if parsed.RawQuery != "" {
		params := parsed.Query()
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var sb strings.Builder
		first := true
		for _, k := range keys {
			vals := params[k]
			sort.Strings(vals)
			for _, v := range vals {
				if !first {
					sb.WriteByte('&')
				}
				sb.WriteString(url.QueryEscape(k))
				sb.WriteByte('=')
				sb.WriteString(url.QueryEscape(v))
				first = false
			}
		}
		return fmt.Sprintf("%s%s?%s", host, path, sb.String())
	}

	return host + path
}

// IsPrivateURL checks whether a URL points to a private/internal network address.
// Used for SSRF protection.
func IsPrivateURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	host := parsed.Hostname()

	// Common loopback aliases
	if host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" {
		return true
	}
	// IPv6 loopback
	if host == "::1" || host == "[::1]" {
		return true
	}

	// Cloud metadata endpoint
	if host == "169.254.169.254" {
		return true
	}

	// Try parsing as IP
	ip := net.ParseIP(host)
	if ip != nil {
		// IPv4 private ranges
		if ip4 := ip.To4(); ip4 != nil {
			// 10.0.0.0/8
			if ip4[0] == 10 {
				return true
			}
			// 172.16.0.0/12
			if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
				return true
			}
			// 192.168.0.0/16
			if ip4[0] == 192 && ip4[1] == 168 {
				return true
			}
			// 127.0.0.0/8
			if ip4[0] == 127 {
				return true
			}
			// 169.254.0.0/16 (link-local)
			if ip4[0] == 169 && ip4[1] == 254 {
				return true
			}
			return false
		}
		// IPv6 checks
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
		// IPv6 unique-local (fc00::/7)
		hostLower := strings.ToLower(host)
		if strings.HasPrefix(hostLower, "fc") || strings.HasPrefix(hostLower, "fd") {
			return true
		}
	}

	// String-based checks for bracketed IPv6
	hostLower := strings.ToLower(strings.Trim(host, "[]"))
	if strings.HasPrefix(hostLower, "fe80:") || strings.HasPrefix(hostLower, "fc") || strings.HasPrefix(hostLower, "fd") {
		return true
	}

	return false
}
