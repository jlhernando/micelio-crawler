package types

import "testing"

func TestIsHTMLPage(t *testing.T) {
	title := &TextLength{Text: "Hello", Length: 5}
	for _, c := range []struct {
		name string
		p    PageData
		want bool
	}{
		{"html 200", PageData{URL: "https://ex.com/", StatusCode: 200, ContentType: "text/html"}, true},
		{"html charset", PageData{URL: "https://ex.com/", StatusCode: 200, ContentType: "text/html; charset=utf-8"}, true},
		{"html upper case", PageData{URL: "https://ex.com/", StatusCode: 200, ContentType: " Text/HTML ;charset=UTF-8"}, true},
		{"xhtml 200", PageData{URL: "https://ex.com/", StatusCode: 200, ContentType: "application/xhtml+xml"}, true},
		{"html 203", PageData{URL: "https://ex.com/", StatusCode: 203, ContentType: "text/html"}, true},
		{"image", PageData{URL: "https://ex.com/a.jpg", StatusCode: 200, ContentType: "image/jpeg"}, false},
		{"image without ext", PageData{URL: "https://ex.com/img?id=1", StatusCode: 200, ContentType: "image/jpeg"}, false},
		{"pdf", PageData{URL: "https://ex.com/a.pdf", StatusCode: 200, ContentType: "application/pdf"}, false},
		{"html 301", PageData{URL: "https://ex.com/old", StatusCode: 301, ContentType: "text/html"}, false},
		{"html 304", PageData{URL: "https://ex.com/", StatusCode: 304}, false},
		{"html 404", PageData{URL: "https://ex.com/x", StatusCode: 404, ContentType: "text/html"}, false},
		{"html 500", PageData{URL: "https://ex.com/x", StatusCode: 500, ContentType: "text/html"}, false},
		{"blocked", PageData{URL: "https://ex.com/p", StatusCode: 200, ContentType: "text/html", RobotsBlocked: true}, false},
		{"fetch error", PageData{URL: "https://ex.com/p", StatusCode: 0}, false},
		// Old rows (no Content-Type stored).
		{"old row page", PageData{URL: "https://ex.com/page", StatusCode: 200}, true},
		{"old row image", PageData{URL: "https://ex.com/wp-content/a.JPG?v=2", StatusCode: 200}, false},
		{"old row pdf", PageData{URL: "https://ex.com/doc.pdf", StatusCode: 200}, false},
		{"old row pdf with title", PageData{URL: "https://ex.com/doc.pdf", StatusCode: 200, Title: title}, true},
	} {
		if got := c.p.IsHTMLPage(); got != c.want {
			t.Errorf("%s: IsHTMLPage() = %v, want %v", c.name, got, c.want)
		}
	}
	var nilPage *PageData
	if nilPage.IsHTMLPage() {
		t.Error("nil page counted as HTML")
	}
}

func TestMediaType(t *testing.T) {
	for in, want := range map[string]string{
		"text/html":                  "text/html",
		"TEXT/HTML; charset=utf-8":   "text/html",
		" application/xhtml+xml ; q": "application/xhtml+xml",
		"":                           "",
	} {
		if got := MediaType(in); got != want {
			t.Errorf("MediaType(%q) = %q, want %q", in, got, want)
		}
	}
}
