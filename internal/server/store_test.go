package server

import (
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// newTestStore creates a temp file-based UiStore for testing.
func newTestStore(t *testing.T) *UiStore {
	t.Helper()
	dir := t.TempDir()
	crawlDir := filepath.Join(dir, "crawls")
	os.MkdirAll(crawlDir, 0755)
	dbPath := filepath.Join(dir, "test.db")

	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenCreate|sqlite.OpenReadWrite|sqlite.OpenWAL)
	if err != nil {
		t.Fatal(err)
	}
	s := &UiStore{
		conn:     conn,
		uiDir:    dir,
		crawlDir: crawlDir,
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.seedPresets(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return s
}

func TestCreateAndGetCrawlJob(t *testing.T) {
	s := newTestStore(t)

	config := map[string]interface{}{"url": "https://example.com", "mode": "spider"}
	job, err := s.CreateCrawlJob(config)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if job.Status != "pending" {
		t.Fatalf("expected pending, got %s", job.Status)
	}
	if job.SeedURL != "https://example.com" {
		t.Fatalf("expected seed URL, got %s", job.SeedURL)
	}
	if job.Mode != "spider" {
		t.Fatalf("expected spider mode, got %s", job.Mode)
	}

	// Get by ID
	got, err := s.GetCrawlJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected job, got nil")
	}
	if got.ID != job.ID {
		t.Fatalf("ID mismatch: %s vs %s", got.ID, job.ID)
	}

	// Get non-existent
	got, err = s.GetCrawlJob("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent job")
	}
}

func TestListCrawlJobs(t *testing.T) {
	s := newTestStore(t)

	s.CreateCrawlJob(map[string]interface{}{"url": "https://a.com"})
	s.CreateCrawlJob(map[string]interface{}{"url": "https://b.com"})

	jobs, err := s.ListCrawlJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestUpdateCrawlJob(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	err := s.UpdateCrawlJob(job.ID, map[string]interface{}{
		"status":    "running",
		"pageCount": 42,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetCrawlJob(job.ID)
	if got.Status != "running" {
		t.Fatalf("expected running, got %s", got.Status)
	}
	if got.PageCount != 42 {
		t.Fatalf("expected 42 pages, got %d", got.PageCount)
	}
}

func TestDeleteCrawlJob(t *testing.T) {
	s := newTestStore(t)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	deleted, err := s.DeleteCrawlJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected deletion")
	}

	got, _ := s.GetCrawlJob(job.ID)
	if got != nil {
		t.Fatal("expected nil after delete")
	}

	// Delete non-existent
	deleted, _ = s.DeleteCrawlJob("nonexistent")
	if deleted {
		t.Fatal("expected false for nonexistent")
	}
}

func TestPresets(t *testing.T) {
	s := newTestStore(t)

	presets, err := s.ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 5 {
		t.Fatalf("expected 5 built-in presets, got %d", len(presets))
	}

	// All should be built-in
	for _, p := range presets {
		if !p.BuiltIn {
			t.Fatalf("expected built-in preset: %s", p.Name)
		}
	}

	// Cannot delete built-in
	deleted, _ := s.DeletePreset(presets[0].ID)
	if deleted {
		t.Fatal("should not delete built-in preset")
	}

	// Create custom preset
	custom, err := s.SavePreset("My Preset", map[string]interface{}{"depth": 5})
	if err != nil {
		t.Fatal(err)
	}
	if custom.BuiltIn {
		t.Fatal("custom preset should not be built-in")
	}

	// Delete custom
	deleted, _ = s.DeletePreset(custom.ID)
	if !deleted {
		t.Fatal("should delete custom preset")
	}
}

func TestSettings(t *testing.T) {
	s := newTestStore(t)

	// Get empty settings
	settings, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 0 {
		t.Fatalf("expected empty settings, got %d", len(settings))
	}

	// Update settings
	err = s.UpdateSettings(map[string]interface{}{
		"defaultDepth":       3,
		"defaultConcurrency": 5,
		"psiKey":             "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	settings, _ = s.GetSettings()
	if len(settings) != 3 {
		t.Fatalf("expected 3 settings, got %d", len(settings))
	}
	if settings["psiKey"] != "test-key" {
		t.Fatalf("expected test-key, got %v", settings["psiKey"])
	}

	// Overwrite
	s.UpdateSettings(map[string]interface{}{"psiKey": "updated"})
	settings, _ = s.GetSettings()
	if settings["psiKey"] != "updated" {
		t.Fatalf("expected updated, got %v", settings["psiKey"])
	}
}

func TestSeededPresetsCount(t *testing.T) {
	s := newTestStore(t)

	var count int
	sqlitex.Execute(s.conn, `SELECT COUNT(*) FROM presets WHERE built_in = 1`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		})
	if count != 5 {
		t.Fatalf("expected 5 built-in presets, got %d", count)
	}
}
