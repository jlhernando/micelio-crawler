package extract

import (
	"strings"
	"testing"
)

func TestGooglebotTruncationCheck_SmallPage(t *testing.T) {
	html := "<html><head><title>Hello</title></head><body><h1>World</h1></body></html>"
	result := GooglebotTruncationCheck(html)
	if result != nil {
		t.Errorf("expected nil for small page, got %v", result)
	}
}

func TestGooglebotTruncationCheck_LargePage_NoTruncation(t *testing.T) {
	// Build a page >2MB where all critical elements are in the first 100 bytes
	head := `<html><head><title>Test</title><meta name="description" content="desc"><link rel="canonical" href="/test"><meta name="robots" content="index"></head><body><h1>Heading</h1><main><article>`
	// Pad with filler to exceed 2MB
	filler := strings.Repeat("<p>Lorem ipsum dolor sit amet.</p>\n", 70000)
	tail := `</article></main></body></html>`
	html := head + filler + tail

	if len(html) <= googlebotHTMLLimit {
		t.Fatalf("test page should be >2MB, got %d bytes", len(html))
	}

	result := GooglebotTruncationCheck(html)

	// title, meta desc, canonical, meta robots, h1, main open, article open should NOT be flagged
	// (they're all in the head before 2MB)
	for _, r := range result {
		if strings.HasPrefix(r, "title") || strings.HasPrefix(r, "meta description") ||
			strings.HasPrefix(r, "canonical") || strings.HasPrefix(r, "meta robots") ||
			strings.HasPrefix(r, "h1") || strings.HasPrefix(r, "main content open") ||
			strings.HasPrefix(r, "article open") {
			t.Errorf("element should not be flagged (in first <2MB): %s", r)
		}
	}

	// article close and main close should be flagged (they're after the filler)
	foundArticleClose := false
	foundMainClose := false
	for _, r := range result {
		if strings.HasPrefix(r, "article close") {
			foundArticleClose = true
		}
		if strings.HasPrefix(r, "main content close") {
			foundMainClose = true
		}
	}
	if !foundArticleClose {
		t.Error("expected 'article close' to be flagged as truncated")
	}
	if !foundMainClose {
		t.Error("expected 'main content close' to be flagged as truncated")
	}
}

func TestGooglebotTruncationCheck_LargePage_TitleAfterCutoff(t *testing.T) {
	// Build a page >2MB where <title> appears after the 2MB mark
	filler := strings.Repeat("x", googlebotHTMLLimit+100)
	html := "<html><head>" + filler + `<title>Late Title</title></head><body><h1>Late H1</h1></body></html>`

	result := GooglebotTruncationCheck(html)
	if result == nil {
		t.Fatal("expected truncation results for page with late title")
	}

	foundTitle := false
	foundH1 := false
	for _, r := range result {
		if strings.HasPrefix(r, "title") {
			foundTitle = true
		}
		if strings.HasPrefix(r, "h1") {
			foundH1 = true
		}
	}
	if !foundTitle {
		t.Error("expected 'title' to be flagged as truncated")
	}
	if !foundH1 {
		t.Error("expected 'h1' to be flagged as truncated")
	}
}

func TestGooglebotTruncationCheck_LastInternalLink(t *testing.T) {
	// Early links + filler + late links
	earlyLinks := `<html><body><a href="/page1">Link 1</a><a href="/page2">Link 2</a>`
	filler := strings.Repeat("<p>Content filler text padding.</p>\n", 65000)
	lateLinks := `<a href="/page3">Link 3</a></body></html>`
	html := earlyLinks + filler + lateLinks

	if len(html) <= googlebotHTMLLimit {
		t.Fatalf("test page should be >2MB, got %d bytes", len(html))
	}

	result := GooglebotTruncationCheck(html)
	foundLastLink := false
	for _, r := range result {
		if strings.HasPrefix(r, "last internal link") {
			foundLastLink = true
		}
	}
	if !foundLastLink {
		t.Error("expected 'last internal link' to be flagged (last <a href is after 2MB)")
	}
}

func TestGooglebotTruncationCheck_CaseInsensitive(t *testing.T) {
	// Use uppercase HTML tags
	filler := strings.Repeat("x", googlebotHTMLLimit+100)
	html := "<HTML><HEAD>" + filler + `<TITLE>Late</TITLE></HEAD></HTML>`

	result := GooglebotTruncationCheck(html)
	foundTitle := false
	for _, r := range result {
		if strings.HasPrefix(r, "title") {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Error("expected case-insensitive match for <TITLE>")
	}
}
