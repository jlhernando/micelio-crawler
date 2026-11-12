package types

import "strings"

// imageExts are path endings treated as image files whatever referenced them.
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico", ".bmp", ".tif", ".tiff", ".jxl", ".heic"}

// HasImageExt reports a URL whose path (query and fragment ignored) ends in
// an image file extension.
func HasImageExt(rawURL string) bool {
	p := rawURL
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if len(p) > 6 {
		p = p[len(p)-6:] // longest extension is 5 bytes plus the dot
	}
	p = strings.ToLower(p)
	for _, ext := range imageExts {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}
