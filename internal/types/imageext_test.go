package types

import "testing"

func TestHasImageExt(t *testing.T) {
	for u, want := range map[string]bool{
		"https://e.com/a.jpg":          true,
		"https://e.com/a.JPEG?w=100":   true,
		"https://e.com/a.webp#x":       true,
		"https://e.com/icons/fav.ico":  true,
		"/x.avif":                      true,
		"https://e.com/page":           false,
		"https://e.com/a.html?f=b.png": false,
		"https://e.com/jpg":            false,
		"https://e.com/svg-guide/":     false,
		"https://e.com/download.pdf":   false,
		"":                             false,
	} {
		if got := HasImageExt(u); got != want {
			t.Errorf("HasImageExt(%q) = %v, want %v", u, got, want)
		}
	}
}
