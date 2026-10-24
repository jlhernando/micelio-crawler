// Package urlutil holds the small URL key helpers shared by the crawler,
// extractor, analysis and integration packages.
//
// Each helper produces a lookup key. The variants differ on purpose (how many
// slashes are trimmed, whether the query is sorted), and callers depend on
// those exact keys, so do not merge them.
package urlutil

import (
	"net/url"
	"sort"
	"strings"
)

// TrimTrailingSlash removes one trailing slash, keeping a lone "/".
// "https://a.com/x/" → "https://a.com/x", "/" → "/", "a//" → "a/".
func TrimTrailingSlash(u string) string {
	if len(u) > 1 && u[len(u)-1] == '/' {
		return u[:len(u)-1]
	}
	return u
}

// TrimTrailingSlashes removes every trailing slash, including a lone "/"
// (which becomes ""). Used by the reporter's URL-matching keys.
func TrimTrailingSlashes(u string) string {
	return strings.TrimRight(u, "/")
}

// PathKey returns the lower-cased, percent-decoded path of rawURL without a
// trailing slash ("/" for the root). Analytics APIs (GA4, Plausible) report
// pages by path, so this is how crawl URLs are matched to their rows.
// Unparseable input is returned unchanged.
func PathKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
	}
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	return strings.ToLower(p)
}

// ComparisonKey returns a protocol-agnostic key for comparing two URLs:
// host without "www.", path without trailing slashes ("/" for the root), and
// the query. With sortQuery the query parameters are sorted and re-encoded so
// parameter order does not matter; without it the raw query is kept as is.
// Unparseable input has its trailing slashes trimmed.
func ComparisonKey(rawURL string, sortQuery bool) string {
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
	if parsed.RawQuery == "" {
		return host + path
	}
	if !sortQuery {
		return host + path + "?" + parsed.RawQuery
	}
	return host + path + "?" + SortedQuery(parsed.Query())
}

// SortedQuery encodes params with keys and each key's values sorted, so
// parameter order does not change the result.
func SortedQuery(params url.Values) string {
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
	return sb.String()
}
