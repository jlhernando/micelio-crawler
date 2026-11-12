package types

import (
	"path"
	"strings"
)

// MediaType returns the lower-cased media type of a Content-Type header value
// without parameters: "Text/HTML; charset=utf-8" -> "text/html".
func MediaType(contentType string) string {
	mt, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(mt))
}

// IsHTMLMediaType reports text/html or application/xhtml+xml (any case, any
// parameters).
func IsHTMLMediaType(contentType string) bool {
	switch MediaType(contentType) {
	case "text/html", "application/xhtml+xml":
		return true
	}
	return false
}

// nonHTMLExts are file endings that are never pages. Only used for rows
// without a Content-Type (servers that send none, very old crawls).
var nonHTMLExts = map[string]bool{
	".pdf": true, ".zip": true, ".gz": true, ".rar": true, ".7z": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".csv": true, ".txt": true, ".json": true, ".xml": true, ".rss": true,
	".css": true, ".js": true, ".mjs": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp3": true, ".mp4": true, ".m4a": true, ".wav": true, ".ogg": true, ".webm": true, ".mov": true, ".avi": true,
}

// IsHTMLPage reports whether on-page checks (missing/too long title, missing
// meta description, missing/multiple H1, duplicates) apply to p: a fetched,
// not robots-blocked URL with a 2xx status that served HTML (text/html or
// application/xhtml+xml). Images, PDFs and other files, redirects and errors
// never count.
//
// A row with an empty Content-Type (server sent none, or a very old crawl)
// counts as HTML when it has a title, meta description or H1, or when its
// path does not end in an image/document/asset extension.
func (p *PageData) IsHTMLPage() bool {
	if p == nil || p.RobotsBlocked || p.StatusCode < 200 || p.StatusCode > 299 {
		return false
	}
	if p.ContentType != "" {
		return IsHTMLMediaType(p.ContentType)
	}
	if (p.Title != nil && p.Title.Text != "") || (p.MetaDescription != nil && p.MetaDescription.Text != "") || len(p.Headings.H1) > 0 {
		return true
	}
	if HasImageExt(p.URL) {
		return false
	}
	u := p.URL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return !nonHTMLExts[strings.ToLower(path.Ext(u))]
}
