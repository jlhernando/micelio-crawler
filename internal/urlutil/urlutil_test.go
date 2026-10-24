package urlutil

import "testing"

// These tables pin the keys produced by the copies this package replaced
// (GA4/Plausible path matching, extract canonical comparison, crawler
// comparison and lookup maps, reporter URL matching). Lookups across stored
// crawls depend on them, so a change here is a behaviour change.

func TestTrimTrailingSlash(t *testing.T) {
	for in, want := range map[string]string{
		"":                            "",
		"/":                           "/",
		"//":                          "/",
		"///":                         "//",
		"https://example.com":         "https://example.com",
		"https://example.com/":        "https://example.com",
		"https://example.com/a//":     "https://example.com/a/",
		"https://example.com/a/#frag": "https://example.com/a/#frag",
		"https://example.com/a/?q=1":  "https://example.com/a/?q=1",
	} {
		if got := TrimTrailingSlash(in); got != want {
			t.Errorf("TrimTrailingSlash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrimTrailingSlashes(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"/":                          "",
		"///":                        "",
		"https://example.com/":       "https://example.com",
		"https://example.com/blog//": "https://example.com/blog",
		"https://example.com/a/?q=1": "https://example.com/a/?q=1",
		"https://example.com/%zz/":   "https://example.com/%zz",
	} {
		if got := TrimTrailingSlashes(in); got != want {
			t.Errorf("TrimTrailingSlashes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "/",
		"https://example.com":            "/",
		"https://example.com/":           "/",
		"https://www.Example.com/Blog/":  "/blog",
		"https://example.com/blog//":     "/blog",
		"https://example.com/a/b/?z=1":   "/a/b",
		"https://example.com/caf%C3%A9/": "/café",
		"https://example.com/a%2Fb/":     "/a/b",
		"https://example.com/a/#frag":    "/a",
		"///":                            "",
		"relative/path/":                 "relative/path",
		"mailto:a@b.com":                 "/",
		"%zz":                            "%zz",
		"https://example.com/%zz/":       "https://example.com/%zz/",
	} {
		if got := PathKey(in); got != want {
			t.Errorf("PathKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComparisonKey(t *testing.T) {
	tests := []struct {
		in             string
		raw, sortedKey string
	}{
		{"", "/", "/"},
		{"https://example.com", "example.com/", "example.com/"},
		{"https://www.example.com/", "example.com/", "example.com/"},
		{"https://www.Example.com/Blog/", "Example.com/Blog", "Example.com/Blog"},
		{"https://example.com/blog//", "example.com/blog", "example.com/blog"},
		{"https://example.com/a/b/?z=1&a=2&a=1", "example.com/a/b?z=1&a=2&a=1", "example.com/a/b?a=1&a=2&z=1"},
		{"http://WWW.example.com:8080/path/?q=a+b&p=%20x", "WWW.example.com/path?q=a+b&p=%20x", "WWW.example.com/path?p=+x&q=a+b"},
		{"https://example.com/a?b=&c", "example.com/a?b=&c", "example.com/a?b=&c="},
		{"https://example.com/caf%C3%A9/", "example.com/café", "example.com/café"},
		{"https://example.com/a/#frag", "example.com/a", "example.com/a"},
		{"?x=1", "/?x=1", "/?x=1"},
		{"///", "", ""},
		{"https://example.com/%zz/", "https://example.com/%zz", "https://example.com/%zz"},
	}
	for _, tt := range tests {
		if got := ComparisonKey(tt.in, false); got != tt.raw {
			t.Errorf("ComparisonKey(%q, false) = %q, want %q", tt.in, got, tt.raw)
		}
		if got := ComparisonKey(tt.in, true); got != tt.sortedKey {
			t.Errorf("ComparisonKey(%q, true) = %q, want %q", tt.in, got, tt.sortedKey)
		}
	}
}
