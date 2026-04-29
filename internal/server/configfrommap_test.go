package server

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestConfigFromMapDefaults(t *testing.T) {
	cfg := configFromMap(map[string]interface{}{})

	if cfg.Concurrency != 5 {
		t.Fatalf("expected default concurrency 5, got %d", cfg.Concurrency)
	}
	if cfg.MaxPages != 1000 {
		t.Fatalf("expected default maxPages 1000, got %d", cfg.MaxPages)
	}
	if cfg.MaxDepth != 10 {
		t.Fatalf("expected default maxDepth 10, got %d", cfg.MaxDepth)
	}
	if cfg.Mode != types.ModeSpider {
		t.Fatalf("expected default mode spider, got %s", cfg.Mode)
	}
}

func TestConfigFromMapAllFields(t *testing.T) {
	m := map[string]interface{}{
		"seedUrl":          "https://example.com",
		"maxPages":         float64(500),
		"maxDepth":         float64(3),
		"concurrency":      float64(10),
		"mode":             "list",
		"checkExternal":    true,
		"ngrams":           true,
		"linkIntelligence": true,
		"psi":              true,
		"htmlReport":       true,
		"embeddings":       true,
		"embeddingKey":     "key-123",
		"liMaxSuggestions": float64(200),
		"language":         "en",
		"userAgent":        "TestBot/1.0",
		"respectRobots":    true,
		"pageWeight":       true,
	}

	cfg := configFromMap(m)

	if cfg.SeedURL != "https://example.com" {
		t.Fatalf("expected seed URL, got %s", cfg.SeedURL)
	}
	if cfg.MaxPages != 500 {
		t.Fatalf("expected 500 maxPages, got %d", cfg.MaxPages)
	}
	if cfg.MaxDepth != 3 {
		t.Fatalf("expected 3 maxDepth, got %d", cfg.MaxDepth)
	}
	if cfg.Concurrency != 10 {
		t.Fatalf("expected 10 concurrency, got %d", cfg.Concurrency)
	}
	if cfg.Mode != "list" {
		t.Fatalf("expected list mode, got %s", string(cfg.Mode))
	}
	if !cfg.CheckExternal {
		t.Fatal("expected checkExternal true")
	}
	if !cfg.Ngrams {
		t.Fatal("expected ngrams true")
	}
	if !cfg.LinkIntelligence {
		t.Fatal("expected linkIntelligence true")
	}
	if !cfg.PSI {
		t.Fatal("expected psi true")
	}
	if !cfg.HTMLReport {
		t.Fatal("expected htmlReport true")
	}
	if !cfg.Embeddings {
		t.Fatal("expected embeddings true")
	}
	if cfg.EmbeddingKey != "key-123" {
		t.Fatalf("expected embedding key, got %s", cfg.EmbeddingKey)
	}
	if cfg.LIMaxSuggestions != 200 {
		t.Fatalf("expected 200, got %d", cfg.LIMaxSuggestions)
	}
	if cfg.Language != "en" {
		t.Fatalf("expected en, got %s", cfg.Language)
	}
	if cfg.UserAgent != "TestBot/1.0" {
		t.Fatalf("expected TestBot/1.0, got %s", cfg.UserAgent)
	}
	if !cfg.RespectRobots {
		t.Fatal("expected respectRobots true")
	}
	if !cfg.PageWeight {
		t.Fatal("expected pageWeight true")
	}
}

func TestConfigFromMapDashboardAliases(t *testing.T) {
	// Dashboard sends "url", "limit", "depth" — configFromMap should remap
	m := map[string]interface{}{
		"url":   "https://test.com",
		"limit": float64(50),
		"depth": float64(2),
	}

	cfg := configFromMap(m)

	if cfg.SeedURL != "https://test.com" {
		t.Fatalf("expected url alias to seedUrl, got %s", cfg.SeedURL)
	}
	if cfg.MaxPages != 50 {
		t.Fatalf("expected limit alias to maxPages 50, got %d", cfg.MaxPages)
	}
	if cfg.MaxDepth != 2 {
		t.Fatalf("expected depth alias to maxDepth 2, got %d", cfg.MaxDepth)
	}
}

func TestConfigFromMapAliasesDoNotOverride(t *testing.T) {
	// If both alias and canonical field exist, canonical wins
	m := map[string]interface{}{
		"url":      "https://alias.com",
		"seedUrl":  "https://canonical.com",
		"limit":    float64(10),
		"maxPages": float64(99),
		"depth":    float64(1),
		"maxDepth": float64(7),
	}

	cfg := configFromMap(m)

	if cfg.SeedURL != "https://canonical.com" {
		t.Fatalf("canonical seedUrl should win, got %s", cfg.SeedURL)
	}
	if cfg.MaxPages != 99 {
		t.Fatalf("canonical maxPages should win, got %d", cfg.MaxPages)
	}
	if cfg.MaxDepth != 7 {
		t.Fatalf("canonical maxDepth should win, got %d", cfg.MaxDepth)
	}
}

func TestConfigFromMapPartialFields(t *testing.T) {
	m := map[string]interface{}{
		"seedUrl":  "https://partial.com",
		"maxPages": float64(200),
		// maxDepth and concurrency not set — should get defaults
	}

	cfg := configFromMap(m)

	if cfg.SeedURL != "https://partial.com" {
		t.Fatalf("expected partial.com, got %s", cfg.SeedURL)
	}
	if cfg.MaxPages != 200 {
		t.Fatalf("expected 200 maxPages, got %d", cfg.MaxPages)
	}
	if cfg.MaxDepth != 10 {
		t.Fatalf("expected default maxDepth 10, got %d", cfg.MaxDepth)
	}
	if cfg.Concurrency != 5 {
		t.Fatalf("expected default concurrency 5, got %d", cfg.Concurrency)
	}
}

func TestConfigFromMapNilMap(t *testing.T) {
	// nil map should not panic and return defaults
	cfg := configFromMap(nil)

	if cfg.Concurrency != 5 {
		t.Fatalf("expected default concurrency 5, got %d", cfg.Concurrency)
	}
	if cfg.MaxPages != 1000 {
		t.Fatalf("expected default maxPages 1000, got %d", cfg.MaxPages)
	}
}

func TestConfigFromMapInvalidTypes(t *testing.T) {
	// configFromMap uses JSON marshal/unmarshal, so wrong types should be handled gracefully
	m := map[string]interface{}{
		"maxPages":    "not-a-number", // string instead of float64
		"concurrency": true,           // bool instead of float64
	}

	cfg := configFromMap(m)

	// Invalid types should be zero-valued, then defaults kick in
	if cfg.MaxPages != 1000 {
		t.Fatalf("expected default maxPages due to bad type, got %d", cfg.MaxPages)
	}
	if cfg.Concurrency != 5 {
		t.Fatalf("expected default concurrency due to bad type, got %d", cfg.Concurrency)
	}
}
