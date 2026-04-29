package crawler

import (
	"fmt"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/", "https://example.com/"},
		{"https://example.com/path/", "https://example.com/path"},
		{"https://example.com:443/path", "https://example.com/path"},
		{"http://example.com:80/path", "http://example.com/path"},
		{"https://example.com/path#fragment", "https://example.com/path"},
		{"https://example.com/path?b=2&a=1", "https://example.com/path?a=1&b=2"},
		{"ftp://example.com/", ""},
		{"not a url", ""},
		{"https://example.com", "https://example.com/"},
	}

	for _, tt := range tests {
		got := NormalizeURL(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeURLForComparison(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://www.example.com/path", "example.com/path"},
		{"http://example.com/path/", "example.com/path"},
		{"https://example.com/", "example.com/"},
		{"https://example.com/path?b=2&a=1", "example.com/path?a=1&b=2"},
	}

	for _, tt := range tests {
		got := NormalizeURLForComparison(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeURLForComparison(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsPrivateURL(t *testing.T) {
	private := []string{
		"http://localhost/",
		"http://127.0.0.1/",
		"http://0.0.0.0/",
		"http://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://172.31.255.255/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
	}
	public := []string{
		"https://example.com/",
		"https://8.8.8.8/",
		"https://172.32.0.1/",
		"https://11.0.0.1/",
	}

	for _, u := range private {
		if !IsPrivateURL(u) {
			t.Errorf("IsPrivateURL(%q) = false, want true", u)
		}
	}
	for _, u := range public {
		if IsPrivateURL(u) {
			t.Errorf("IsPrivateURL(%q) = true, want false", u)
		}
	}
}

func TestFormatError(t *testing.T) {
	if got := FormatError(nil); got != "" {
		t.Errorf("FormatError(nil) = %q, want empty", got)
	}
	err := fmt.Errorf("test error")
	if got := FormatError(err); got != "test error" {
		t.Errorf("FormatError(err) = %q, want %q", got, "test error")
	}
}
