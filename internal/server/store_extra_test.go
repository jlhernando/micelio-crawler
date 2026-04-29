package server

import (
	"encoding/json"
	"testing"
)

func TestSaveAndLoadCrawlStats(t *testing.T) {
	s := newTestStore(t)

	// Create a crawl job first (foreign key)
	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}

	stats := map[string]interface{}{
		"totalPages":   float64(42),
		"totalErrors":  float64(3),
		"avgResponse":  float64(150.5),
		"statusCodes":  map[string]interface{}{"200": float64(39), "404": float64(3)},
	}
	statsJSON, _ := json.Marshal(stats)

	err = s.SaveCrawlStats(job.ID, statsJSON)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadCrawlStats(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected stats, got nil")
	}
	if loaded["totalPages"] != float64(42) {
		t.Fatalf("expected 42 totalPages, got %v", loaded["totalPages"])
	}
	if loaded["avgResponse"] != float64(150.5) {
		t.Fatalf("expected 150.5, got %v", loaded["avgResponse"])
	}
}

func TestLoadCrawlStatsNonExistent(t *testing.T) {
	s := newTestStore(t)

	loaded, err := s.LoadCrawlStats("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatal("expected nil for nonexistent crawl stats")
	}
}

func TestSaveCrawlStatsOverwrite(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})

	// Save initial stats
	stats1, _ := json.Marshal(map[string]interface{}{"version": float64(1)})
	s.SaveCrawlStats(job.ID, stats1)

	// Overwrite with new stats (INSERT OR REPLACE)
	stats2, _ := json.Marshal(map[string]interface{}{"version": float64(2), "extra": "data"})
	err := s.SaveCrawlStats(job.ID, stats2)
	if err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.LoadCrawlStats(job.ID)
	if loaded["version"] != float64(2) {
		t.Fatalf("expected version 2, got %v", loaded["version"])
	}
	if loaded["extra"] != "data" {
		t.Fatalf("expected extra data, got %v", loaded["extra"])
	}
}

func TestDeleteCrawlJobAlsoDeletesStats(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	statsJSON, _ := json.Marshal(map[string]interface{}{"totalPages": float64(10)})
	s.SaveCrawlStats(job.ID, statsJSON)

	// Verify stats exist
	loaded, _ := s.LoadCrawlStats(job.ID)
	if loaded == nil {
		t.Fatal("expected stats to exist before delete")
	}

	// Delete the job
	deleted, err := s.DeleteCrawlJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected deletion")
	}

	// Stats should also be gone
	loaded, _ = s.LoadCrawlStats(job.ID)
	if loaded != nil {
		t.Fatal("expected stats to be deleted with job")
	}
}

func TestUpdateCrawlJobEmptyUpdates(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})

	// Empty updates should be a no-op
	err := s.UpdateCrawlJob(job.ID, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}

	// Nil updates should also be a no-op
	err = s.UpdateCrawlJob(job.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCrawlJobInvalidKeys(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})

	// Invalid keys should be silently ignored
	err := s.UpdateCrawlJob(job.ID, map[string]interface{}{
		"invalidKey": "value",
		"anotherBad": 123,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Job should be unchanged
	got, _ := s.GetCrawlJob(job.ID)
	if got.Status != "pending" {
		t.Fatalf("expected pending, got %s", got.Status)
	}
}

func TestUpdateCrawlJobAllValidFields(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})

	completedAt := "2025-01-15T10:30:00Z"
	err := s.UpdateCrawlJob(job.ID, map[string]interface{}{
		"status":      "completed",
		"completedAt": completedAt,
		"pageCount":   100,
		"errorCount":  5,
		"durationMs":  int64(12345),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetCrawlJob(job.ID)
	if got.Status != "completed" {
		t.Fatalf("expected completed, got %s", got.Status)
	}
	if got.CompletedAt == nil || *got.CompletedAt != completedAt {
		t.Fatalf("expected completedAt %s, got %v", completedAt, got.CompletedAt)
	}
	if got.PageCount != 100 {
		t.Fatalf("expected 100, got %d", got.PageCount)
	}
	if got.ErrorCount != 5 {
		t.Fatalf("expected 5, got %d", got.ErrorCount)
	}
	if got.DurationMs == nil || *got.DurationMs != 12345 {
		t.Fatalf("expected 12345 durationMs, got %v", got.DurationMs)
	}
}

func TestCreateCrawlJobSeedURLFromURL(t *testing.T) {
	s := newTestStore(t)

	// When "url" is provided but not "seedUrl", seedURL should be extracted from "url"
	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://fromurl.com"})
	if err != nil {
		t.Fatal(err)
	}
	if job.SeedURL != "https://fromurl.com" {
		t.Fatalf("expected seedURL from url field, got %s", job.SeedURL)
	}
}

func TestCreateCrawlJobSeedURLFromSeedUrl(t *testing.T) {
	s := newTestStore(t)

	job, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://fromseed.com"})
	if err != nil {
		t.Fatal(err)
	}
	if job.SeedURL != "https://fromseed.com" {
		t.Fatalf("expected seedURL from seedUrl field, got %s", job.SeedURL)
	}
}

func TestCreateCrawlJobDefaultMode(t *testing.T) {
	s := newTestStore(t)

	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Mode != "spider" {
		t.Fatalf("expected default mode spider, got %s", job.Mode)
	}
}

func TestCreateCrawlJobCustomMode(t *testing.T) {
	s := newTestStore(t)

	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com", "mode": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Mode != "list" {
		t.Fatalf("expected list mode, got %s", job.Mode)
	}
}

func TestCreateCrawlJobDBPath(t *testing.T) {
	s := newTestStore(t)

	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if job.DBPath == "" {
		t.Fatal("expected non-empty DBPath")
	}
	// DBPath should end with the job ID + .db
	expected := job.ID + ".db"
	if len(job.DBPath) < len(expected) || job.DBPath[len(job.DBPath)-len(expected):] != expected {
		t.Fatalf("expected DBPath to end with %s, got %s", expected, job.DBPath)
	}
}

func TestListCrawlJobsOrderByStartedAt(t *testing.T) {
	s := newTestStore(t)

	// Create jobs — they should come back newest first
	job1, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://first.com"})
	job2, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://second.com"})

	jobs, err := s.ListCrawlJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	// Newest first
	if jobs[0].ID != job2.ID {
		t.Fatalf("expected newest job first, got %s", jobs[0].ID)
	}
	if jobs[1].ID != job1.ID {
		t.Fatalf("expected oldest job second, got %s", jobs[1].ID)
	}
}

func TestListCrawlJobsEmpty(t *testing.T) {
	s := newTestStore(t)

	jobs, err := s.ListCrawlJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestUpdateSettingsEmpty(t *testing.T) {
	s := newTestStore(t)

	// Empty settings should be a no-op
	err := s.UpdateSettings(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}

	// Nil should also be a no-op
	err = s.UpdateSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateCrawlJobConfigPersistence(t *testing.T) {
	s := newTestStore(t)

	config := map[string]interface{}{
		"url":            "https://example.com",
		"maxPages":       float64(500),
		"checkExternal":  true,
		"customField":    "value",
	}

	job, err := s.CreateCrawlJob(config)
	if err != nil {
		t.Fatal(err)
	}

	// Retrieve and check config is fully persisted
	got, _ := s.GetCrawlJob(job.ID)
	if got.Config["url"] != "https://example.com" {
		t.Fatalf("expected url in config, got %v", got.Config["url"])
	}
	if got.Config["maxPages"] != float64(500) {
		t.Fatalf("expected maxPages in config, got %v", got.Config["maxPages"])
	}
	if got.Config["checkExternal"] != true {
		t.Fatalf("expected checkExternal in config, got %v", got.Config["checkExternal"])
	}
}
