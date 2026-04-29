package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPageDataJSONRoundTrip(t *testing.T) {
	title := TextLength{Text: "Test Page", Length: 9}
	desc := TextLength{Text: "A test description", Length: 18}
	canonical := "https://example.com/"
	clickDepth := 2

	page := PageData{
		URL:             "https://example.com/test",
		FinalURL:        "https://example.com/test",
		StatusCode:      200,
		ResponseTimeMs:  150,
		Title:           &title,
		MetaDescription: &desc,
		Canonical:       &canonical,
		Headings: HeadingData{
			H1: []string{"Main Heading"},
			H2: []string{"Sub 1", "Sub 2"},
		},
		InternalLinks: []string{"/about", "/contact"},
		ExternalLinks: []string{"https://other.com"},
		Images: []ImageData{
			{Src: "/img/logo.png", MissingAlt: false, HasAltAttribute: true, AltLength: 4},
		},
		Depth:     1,
		CrawledAt: time.Now().UTC().Truncate(time.Second),
		Hreflang: []HreflangEntry{
			{Lang: "en", Href: "https://example.com/en"},
			{Lang: "es", Href: "https://example.com/es"},
		},
		OpenGraph: map[string]string{
			"og:title": "Test Page",
			"og:type":  "website",
		},
		WordCount:          250,
		ContentHash:        "abc123",
		SimhashFingerprint: "deadbeef",
		Indexability:       IndexabilityData{Indexable: true, Reason: ""},
		Security: SecurityData{
			IsHTTPS: true,
		},
		LinkIntelligence: &LinkIntelligenceData{
			ClickDepth:   &clickDepth,
			InDegree:     5,
			OutDegree:    10,
			HubScore:     0.5,
			AuthorityScore: 0.7,
		},
		PageRank: 3.14,
	}

	data, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded PageData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify key fields survived round-trip
	if decoded.URL != page.URL {
		t.Errorf("URL: got %q, want %q", decoded.URL, page.URL)
	}
	if decoded.StatusCode != page.StatusCode {
		t.Errorf("StatusCode: got %d, want %d", decoded.StatusCode, page.StatusCode)
	}
	if decoded.Title == nil || decoded.Title.Text != "Test Page" {
		t.Errorf("Title: got %v, want %q", decoded.Title, "Test Page")
	}
	if decoded.Canonical == nil || *decoded.Canonical != canonical {
		t.Errorf("Canonical: got %v, want %q", decoded.Canonical, canonical)
	}
	if len(decoded.Headings.H1) != 1 || decoded.Headings.H1[0] != "Main Heading" {
		t.Errorf("H1: got %v, want [Main Heading]", decoded.Headings.H1)
	}
	if len(decoded.InternalLinks) != 2 {
		t.Errorf("InternalLinks: got %d, want 2", len(decoded.InternalLinks))
	}
	if decoded.LinkIntelligence == nil || decoded.LinkIntelligence.ClickDepth == nil || *decoded.LinkIntelligence.ClickDepth != 2 {
		t.Errorf("LinkIntelligence.ClickDepth: got %v, want 2", decoded.LinkIntelligence)
	}
	if decoded.PageRank != 3.14 {
		t.Errorf("PageRank: got %f, want 3.14", decoded.PageRank)
	}
}

func TestCrawlConfigDefaults(t *testing.T) {
	cfg := CrawlConfig{
		SeedURL:     "https://example.com",
		Mode:        ModeSpider,
		MaxDepth:    5,
		MaxPages:    1000,
		Concurrency: 5,
		DelayMs:     200,
		UserAgent:   "Micelio/1.0",
		OutputFormat: FormatJSONL,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded CrawlConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Mode != ModeSpider {
		t.Errorf("Mode: got %q, want %q", decoded.Mode, ModeSpider)
	}
	if decoded.Concurrency != 5 {
		t.Errorf("Concurrency: got %d, want 5", decoded.Concurrency)
	}
}

func TestCrawlStatsJSONRoundTrip(t *testing.T) {
	stats := CrawlStats{
		TotalPages:  500,
		StatusCodes: map[int]int{200: 450, 301: 30, 404: 20},
		DepthDistribution: map[int]int{0: 1, 1: 50, 2: 200, 3: 249},
		ResponseTimePercentiles: PercentileData{P50: 120, P90: 350, P99: 1200},
		IndexabilityStats: IndexabilityStats{
			Indexable:    400,
			NonIndexable: 100,
			Reasons:      map[string]int{"noindex": 50, "canonicalized": 50},
		},
		PageRankScores: map[string]float64{
			"https://example.com/": 10.0,
			"https://example.com/about": 5.5,
		},
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded CrawlStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.TotalPages != 500 {
		t.Errorf("TotalPages: got %d, want 500", decoded.TotalPages)
	}
	if decoded.StatusCodes[200] != 450 {
		t.Errorf("StatusCodes[200]: got %d, want 450", decoded.StatusCodes[200])
	}
	if decoded.PageRankScores["https://example.com/"] != 10.0 {
		t.Errorf("PageRankScores: got %f, want 10.0", decoded.PageRankScores["https://example.com/"])
	}
}

func TestHeadResultJSONRoundTrip(t *testing.T) {
	xRobots := "noindex"
	canonical := "https://example.com/"
	contentLen := int64(12345)

	hr := HeadResult{
		URL:            "https://example.com/page",
		FinalURL:       "https://example.com/page",
		StatusCode:     200,
		ResponseTimeMs: 50,
		ContentType:    "text/html",
		ContentLength:  &contentLen,
		Server:         "nginx",
		XRobotsTag:     &xRobots,
		LinkCanonical:  &canonical,
		HSTS:           true,
		Headers:        map[string]string{"x-custom": "value"},
	}

	data, err := json.Marshal(hr)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded HeadResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ContentLength == nil || *decoded.ContentLength != 12345 {
		t.Errorf("ContentLength: got %v, want 12345", decoded.ContentLength)
	}
	if decoded.XRobotsTag == nil || *decoded.XRobotsTag != "noindex" {
		t.Errorf("XRobotsTag: got %v, want noindex", decoded.XRobotsTag)
	}
}

func TestEnumConstants(t *testing.T) {
	// Verify string enum values match TypeScript
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"ModeSpider", string(ModeSpider), "spider"},
		{"ModeList", string(ModeList), "list"},
		{"ModeSitemap", string(ModeSitemap), "sitemap"},
		{"FormatJSONL", string(FormatJSONL), "jsonl"},
		{"FormatCSV", string(FormatCSV), "csv"},
		{"StatePending", string(StatePending), "pending"},
		{"StateCrawling", string(StateCrawling), "crawling"},
		{"StateCompleted", string(StateCompleted), "completed"},
		{"StateFailed", string(StateFailed), "failed"},
		{"LinkPosContent", string(LinkPosContent), "content"},
		{"LinkPosNavigation", string(LinkPosNavigation), "navigation"},
		{"FormatJSONLD", string(FormatJSONLD), "json-ld"},
		{"FormatMicrodata", string(FormatMicrodata), "microdata"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}
