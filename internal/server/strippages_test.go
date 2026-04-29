package server

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestStripPagesForCacheZerosFields(t *testing.T) {
	pages := []*types.PageData{
		{
			URL:                "https://example.com/page1",
			StatusCode:         200,
			BodyText:           "This is some body text that should be stripped",
			ContentHash:        "abc123",
			SimhashFingerprint: "fingerprint123",
			Anchors:            []types.AnchorData{{Href: "/link1", Text: "Link 1"}},
			Images:             []types.ImageData{{Src: "/img.png"}},
			StructuredData:     []types.StructuredDataEntry{{Type: "Article"}},
			OpenGraph:          map[string]string{"title": "OG Title"},
			TwitterCard:        map[string]string{"card": "summary"},
			Hreflang:           []types.HreflangEntry{{Lang: "en", Href: "https://example.com"}},
			URLIssues:          []string{"issue1"},
			WordCount:          500,
			ResponseTimeMs:     150,
		},
		{
			URL:        "https://example.com/page2",
			StatusCode: 404,
			BodyText:   "Another body text",
			Error:      "not found",
		},
	}

	stripPagesForCache(pages)

	for i, p := range pages {
		if p.BodyText != "" {
			t.Fatalf("page %d: BodyText should be empty, got %q", i, p.BodyText)
		}
		if p.ContentHash != "" {
			t.Fatalf("page %d: ContentHash should be empty", i)
		}
		if p.SimhashFingerprint != "" {
			t.Fatalf("page %d: SimhashFingerprint should be empty", i)
		}
		if p.Anchors != nil {
			t.Fatalf("page %d: Anchors should be nil", i)
		}
		if p.Images != nil {
			t.Fatalf("page %d: Images should be nil", i)
		}
		if p.StructuredData != nil {
			t.Fatalf("page %d: StructuredData should be nil", i)
		}
		if p.OpenGraph != nil {
			t.Fatalf("page %d: OpenGraph should be nil", i)
		}
		if p.TwitterCard != nil {
			t.Fatalf("page %d: TwitterCard should be nil", i)
		}
		if p.Hreflang != nil {
			t.Fatalf("page %d: Hreflang should be nil", i)
		}
		if p.URLIssues != nil {
			t.Fatalf("page %d: URLIssues should be nil", i)
		}
	}

	// Verify preserved fields
	if pages[0].URL != "https://example.com/page1" {
		t.Fatalf("URL should be preserved, got %s", pages[0].URL)
	}
	if pages[0].StatusCode != 200 {
		t.Fatalf("StatusCode should be preserved, got %d", pages[0].StatusCode)
	}
	if pages[0].WordCount != 500 {
		t.Fatalf("WordCount should be preserved, got %d", pages[0].WordCount)
	}
	if pages[0].ResponseTimeMs != 150 {
		t.Fatalf("ResponseTimeMs should be preserved, got %d", pages[0].ResponseTimeMs)
	}
	if pages[1].Error != "not found" {
		t.Fatalf("Error should be preserved, got %s", pages[1].Error)
	}
}

func TestStripPagesForCacheEmpty(t *testing.T) {
	// Should not panic on empty slice
	stripPagesForCache([]*types.PageData{})
}

func TestStripPagesForCacheNil(t *testing.T) {
	// Should not panic on nil slice
	stripPagesForCache(nil)
}

func TestStripPagesForCacheNilPointerFields(t *testing.T) {
	// Pages with nil pointer fields should not cause issues
	pages := []*types.PageData{
		{
			URL:        "https://example.com",
			StatusCode: 200,
			// All pointer/slice fields are already nil/zero
		},
	}

	stripPagesForCache(pages)

	if pages[0].URL != "https://example.com" {
		t.Fatalf("URL should be preserved, got %s", pages[0].URL)
	}
}
