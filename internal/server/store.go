// Package server implements the HTTP API, WebSocket, and UI persistence layer.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/micelio/micelio/internal/logs"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// CrawlEvent records a lifecycle event (start, pause, resume, stop, complete, etc.).
type CrawlEvent struct {
	Type string `json:"type"` // started, paused, resumed, stopped, completed, cancelled, failed
	At   string `json:"at"`   // ISO8601 timestamp
	Note string `json:"note,omitempty"`
}

// CrawlJob represents a crawl job stored in the database.
type CrawlJob struct {
	Coverage    json.RawMessage        `json:"coverage,omitempty"`
	Config      map[string]interface{} `json:"config"`
	Stats       json.RawMessage        `json:"stats,omitempty"`
	Events      []CrawlEvent           `json:"events,omitempty"`
	CompletedAt *string                `json:"completedAt"`
	DurationMs  *int64                 `json:"durationMs"`
	ID          string                 `json:"id"`
	Status      string                 `json:"status"`
	StartedAt   string                 `json:"startedAt"`
	SeedURL     string                 `json:"seedUrl"`
	Mode        string                 `json:"mode"`
	DBPath      string                 `json:"dbPath"`
	PageCount   int                    `json:"pageCount"`
	ErrorCount  int                    `json:"errorCount"`
}

// SavedPreset represents a saved crawl preset.
type SavedPreset struct {
	Config    map[string]interface{} `json:"config"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	CreatedAt string                 `json:"createdAt"`
	UpdatedAt string                 `json:"updatedAt"`
	BuiltIn   bool                   `json:"builtIn"`
}

// UiStore manages SQLite persistence for the web UI.
type UiStore struct {
	conn     *sqlite.Conn
	uiDir    string
	crawlDir string
	mu       sync.Mutex
	// urlConn is a separate connection for URL stat bulk writes so they
	// don't block the main API (which uses conn + mu). SQLite WAL mode
	// allows concurrent reads + one writer across connections.
	urlConn *sqlite.Conn
	urlMu   sync.Mutex
	// unlock releases the dashboard's lock on uiDir (NewUiStore only).
	unlock func()
}

// NewUiStore opens or creates the UI database at ~/.micelio/ui/ for the
// dashboard server. It fails when another dashboard already serves that
// folder: owner (pid and address) is shown to the next one that tries.
func NewUiStore(owner string) (*UiStore, error) {
	s, err := OpenUiStore()
	if err != nil {
		return nil, err
	}
	unlock, err := lockUIDir(s.uiDir, owner)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.unlock = unlock
	s.recoverStaleJobs()
	s.removeOrphanCrawlFiles()
	scrubLegacyCrawlConfigs(s.crawlDir)
	return s, nil
}

// OpenUiStore opens the dashboard database without recovering stale jobs, for
// tools (like `micelio import`) that may run next to a live dashboard whose
// crawls are really running.
func OpenUiStore() (*UiStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	uiDir := filepath.Join(home, ".micelio", "ui")
	crawlDir := filepath.Join(uiDir, "crawls")
	if err := os.MkdirAll(crawlDir, 0o700); err != nil {
		return nil, fmt.Errorf("create crawl dir: %w", err)
	}
	// Crawl databases hold crawled responses and uploads: owner-only, also
	// for a directory made by an older version.
	os.Chmod(crawlDir, 0o700)

	dbPath := filepath.Join(uiDir, "micelio-ui.db")
	// Open without OpenWAL so page_size PRAGMA takes effect on new databases.
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Performance PRAGMAs — page_size must be set before WAL and before any tables.
	sqlitex.Execute(conn, "PRAGMA page_size = 8192;", nil)
	sqlitex.Execute(conn, "PRAGMA journal_mode = WAL;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { return nil },
	})
	sqlitex.Execute(conn, "PRAGMA synchronous = NORMAL;", nil)
	sqlitex.Execute(conn, "PRAGMA cache_size = -8000;", nil) // 8K pages × 8KB = 64 MB
	sqlitex.Execute(conn, "PRAGMA temp_store = MEMORY;", nil)

	// Second connection for URL stat bulk writes — prevents long UPSERT
	// transactions from blocking API reads on the main connection.
	urlConn, err := sqlite.OpenConn(dbPath, sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open url conn: %w", err)
	}
	sqlitex.Execute(urlConn, "PRAGMA journal_mode = WAL;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { return nil },
	})
	sqlitex.Execute(urlConn, "PRAGMA synchronous = NORMAL;", nil)
	sqlitex.Execute(urlConn, "PRAGMA cache_size = -64000;", nil)

	// Settings and crawl configs hold credentials (SMTP password, API keys,
	// cookies, auth headers): only the owner may read the database.
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		os.Chmod(p, 0o600)
	}

	s := &UiStore{conn: conn, urlConn: urlConn, uiDir: uiDir, crawlDir: crawlDir}
	if err := s.migrate(); err != nil {
		conn.Close()
		urlConn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := s.seedPresets(); err != nil {
		conn.Close()
		urlConn.Close()
		return nil, fmt.Errorf("seed presets: %w", err)
	}
	return s, nil
}

func (s *UiStore) migrate() error {
	if err := sqlitex.ExecuteScript(s.conn, `
		CREATE TABLE IF NOT EXISTS crawl_jobs (
			id TEXT PRIMARY KEY,
			config TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			started_at TEXT NOT NULL,
			completed_at TEXT,
			page_count INTEGER NOT NULL DEFAULT 0,
			error_count INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER,
			seed_url TEXT NOT NULL,
			mode TEXT NOT NULL DEFAULT 'spider',
			db_path TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_crawl_jobs_status ON crawl_jobs(status);
		CREATE INDEX IF NOT EXISTS idx_crawl_jobs_started ON crawl_jobs(started_at);

		CREATE TABLE IF NOT EXISTS crawl_coverage (
			crawl_id TEXT PRIMARY KEY,
			summary_json TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS presets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			config TEXT NOT NULL,
			built_in INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS crawl_stats (
			crawl_id TEXT PRIMARY KEY REFERENCES crawl_jobs(id),
			stats_json TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS log_jobs (
			id TEXT PRIMARY KEY,
			filename TEXT NOT NULL,
			format TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL,
			completed_at TEXT,
			file_size INTEGER,
			total_lines INTEGER,
			processed_lines INTEGER DEFAULT 0,
			duration_ms INTEGER,
			upload_ms INTEGER,
			parse_ms INTEGER,
			analysis_ms INTEGER,
			error_msg TEXT,
			files TEXT DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_log_jobs_created ON log_jobs(created_at);

		CREATE TABLE IF NOT EXISTS log_stats (
			job_id TEXT PRIMARY KEY REFERENCES log_jobs(id),
			stats_json TEXT NOT NULL
		);

		-- Per-URL hit stats from log analysis. Stored separately from
		-- log_stats so the (huge) per-URL set doesn't bloat the JSON blob
		-- that gets parsed every time the overview page opens.
		-- WITHOUT ROWID is more compact for compound primary keys.
		CREATE TABLE IF NOT EXISTS log_url_stats (
			job_id     TEXT NOT NULL,
			path       TEXT NOT NULL,
			hits       INTEGER NOT NULL,
			bot_hits   INTEGER NOT NULL,
			human_hits INTEGER NOT NULL,
			top_bot    TEXT,
			status     INTEGER,
			PRIMARY KEY (job_id, path)
		) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS idx_log_url_stats_job_bothits
			ON log_url_stats(job_id, bot_hits DESC);

		-- Per-URL merged result of log×crawl: a cache with one table per
		-- (log, crawl) pair (see mergeTable), rebuilt by each Run Merge and
		-- queried by the dashboard's Merge tab. This lists the tables so
		-- deleting a log job or a crawl can drop its pairs. Older versions
		-- kept every pair in one shared table, log_merge_pages: it is still
		-- read as a fallback and dropped once empty (deleteLegacyMerge).
		CREATE TABLE IF NOT EXISTS log_merge_tables (
			log_id     TEXT NOT NULL,
			crawl_id   TEXT NOT NULL,
			table_name TEXT NOT NULL,
			PRIMARY KEY (log_id, crawl_id)
		);
		CREATE INDEX IF NOT EXISTS idx_log_merge_tables_crawl ON log_merge_tables(crawl_id);
	`, nil); err != nil {
		return err
	}
	// Idempotent column add for existing DBs created before multi-file support.
	// SQLite errors if the column already exists; ignore that specific case.
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_jobs ADD COLUMN files TEXT DEFAULT ''`, nil)
	// Phase timing columns for log analysis.
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_jobs ADD COLUMN upload_ms INTEGER`, nil)
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_jobs ADD COLUMN parse_ms INTEGER`, nil)
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_jobs ADD COLUMN analysis_ms INTEGER`, nil)
	// Add bytes column to per-URL stats (response size tracking).
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_url_stats ADD COLUMN bytes INTEGER NOT NULL DEFAULT 0`, nil)
	// Add log_bytes column to legacy merged pages (response size from logs);
	// fails harmlessly when there is no legacy table.
	_ = sqlitex.Execute(s.conn, `ALTER TABLE log_merge_pages ADD COLUMN log_bytes INTEGER NOT NULL DEFAULT 0`, nil)
	// An empty legacy shared merge table is not needed any more.
	if err := dropLegacyMergeIfEmpty(s.conn); err != nil {
		return err
	}
	// Add events column for crawl lifecycle tracking.
	_ = sqlitex.Execute(s.conn, `ALTER TABLE crawl_jobs ADD COLUMN events TEXT NOT NULL DEFAULT '[]'`, nil)
	return nil
}

// recoverStaleJobs marks log jobs stuck in "processing" and crawl jobs stuck
// in "running" as failed on startup — their background goroutines are gone.
func (s *UiStore) recoverStaleJobs() {
	now := time.Now().UTC().Format(time.RFC3339)
	_ = sqlitex.Execute(s.conn,
		`UPDATE log_jobs SET status = 'failed', error_msg = 'interrupted: server restarted', completed_at = ? WHERE status IN ('processing', 'deleting')`,
		&sqlitex.ExecOptions{Args: []interface{}{now}})
	_ = sqlitex.Execute(s.conn,
		`UPDATE crawl_jobs SET status = 'failed', completed_at = ? WHERE status IN ('running', 'paused')`,
		&sqlitex.ExecOptions{Args: []interface{}{now}})
}

func (s *UiStore) seedPresets() error {
	builtins := []struct {
		config map[string]interface{}
		name   string
	}{
		{name: "Quick Scan", config: map[string]interface{}{
			"depth": 1, "limit": 100, "concurrency": 3, "htmlReport": true,
		}},
		{name: "Full Audit", config: map[string]interface{}{
			"depth": 30, "limit": 1000000, "concurrency": 5, "delay": 1000,
			"adaptiveRate": true, "discoverSitemaps": true,
			"ngrams": true, "embeddings": true,
			"linkIntelligence": true, "liMaxSuggestions": 100,
			"checkExternal": true, "htmlReport": true,
		}},
		{name: "Content Audit", config: map[string]interface{}{
			"depth": 3, "limit": 2000, "concurrency": 3,
			"ngrams": true, "embeddings": true, "htmlReport": true,
		}},
		{name: "Technical Audit", config: map[string]interface{}{
			"depth": 3, "limit": 2000, "concurrency": 3,
			"checkExternal": true, "linkIntelligence": true,
			"pageWeight": true, "htmlReport": true,
		}},
		{name: "Internal Linking Audit", config: map[string]interface{}{
			"depth": 10, "limit": 50000, "concurrency": 3,
			"linkIntelligence": true, "liMaxSuggestions": 200,
			"embeddings": true, "ngrams": true, "sitemapOut": true,
			"checkExternal": true, "htmlReport": true,
		}},
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, b := range builtins {
		configJSON, _ := json.Marshal(b.config)
		err := sqlitex.Execute(s.conn,
			`INSERT OR IGNORE INTO presets (id, name, config, built_in, created_at, updated_at)
			 VALUES (?, ?, ?, 1, ?, ?)`,
			&sqlitex.ExecOptions{Args: []interface{}{uuid.New().String(), b.name, string(configJSON), now, now}})
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateCrawlJob creates a new crawl job and returns it.
func (s *UiStore) CreateCrawlJob(config map[string]interface{}) (*CrawlJob, error) {
	return s.createCrawlJob(config, "")
}

// CreateCrawlJobWithDB creates a new crawl job that reuses an existing crawl database.
func (s *UiStore) CreateCrawlJobWithDB(config map[string]interface{}, dbPath string) (*CrawlJob, error) {
	return s.createCrawlJob(config, dbPath)
}

func (s *UiStore) createCrawlJob(config map[string]interface{}, existingDBPath string) (*CrawlJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	seedURL := ""
	if u, ok := config["seedUrl"].(string); ok {
		seedURL = u
	} else if u, ok := config["url"].(string); ok {
		seedURL = u
	}
	mode := "spider"
	if m, ok := config["mode"].(string); ok {
		mode = m
	}
	dbPath := existingDBPath
	if dbPath == "" {
		dbPath = filepath.Join(s.crawlDir, id+".db")
	}

	err = sqlitex.Execute(s.conn,
		`INSERT INTO crawl_jobs (id, config, status, started_at, page_count, error_count, seed_url, mode, db_path)
		 VALUES (?, ?, 'pending', ?, 0, 0, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{id, string(configJSON), now, seedURL, mode, dbPath}})
	if err != nil {
		return nil, err
	}

	return &CrawlJob{
		ID: id, Config: config, Status: "pending", StartedAt: now,
		PageCount: 0, ErrorCount: 0, SeedURL: seedURL, Mode: mode, DBPath: dbPath,
	}, nil
}

// GetCrawlJob retrieves a crawl job by ID.
func (s *UiStore) GetCrawlJob(id string) (*CrawlJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var job *CrawlJob
	err := sqlitex.Execute(s.conn, `SELECT id, config, status, started_at, completed_at, page_count, error_count, duration_ms, seed_url, mode, db_path, events FROM crawl_jobs WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				job = rowToJob(stmt)
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if job != nil {
		err = sqlitex.Execute(s.conn, `SELECT summary_json FROM crawl_coverage WHERE crawl_id = ?`, &sqlitex.ExecOptions{
			Args: []interface{}{id}, ResultFunc: func(stmt *sqlite.Stmt) error { job.Coverage = json.RawMessage(stmt.ColumnText(0)); return nil },
		})
		if err != nil {
			return nil, err
		}
	}
	return job, nil
}

// ListCrawlJobs returns all crawl jobs ordered by start time (newest first).
func (s *UiStore) ListCrawlJobs() ([]*CrawlJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jobs []*CrawlJob
	err := sqlitex.Execute(s.conn, `SELECT id, config, status, started_at, completed_at, page_count, error_count, duration_ms, seed_url, mode, db_path, events FROM crawl_jobs ORDER BY started_at DESC`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				jobs = append(jobs, rowToJob(stmt))
				return nil
			},
		})
	return jobs, err
}

// ListCrawlJobsWithStats returns all crawl jobs with their persisted stats (if any).
func (s *UiStore) ListCrawlJobsWithStats() ([]*CrawlJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jobs []*CrawlJob
	err := sqlitex.Execute(s.conn,
		`SELECT cj.id, cj.config, cj.status, cj.started_at, cj.completed_at, cj.page_count, cj.error_count, cj.duration_ms, cj.seed_url, cj.mode, cj.db_path, cj.events, cs.stats_json
		 FROM crawl_jobs cj LEFT JOIN crawl_stats cs ON cj.id = cs.crawl_id
		 ORDER BY cj.started_at DESC`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				job := rowToJob(stmt)
				if stmt.ColumnType(12) != sqlite.TypeNull {
					job.Stats = json.RawMessage(stmt.ColumnText(12))
				}
				jobs = append(jobs, job)
				return nil
			},
		})
	return jobs, err
}

// UpdateCrawlJob updates specific fields of a crawl job.
func (s *UiStore) UpdateCrawlJob(id string, updates map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(updates) == 0 {
		return nil
	}
	validCols := map[string]string{
		"status": "status", "completedAt": "completed_at",
		"pageCount": "page_count", "errorCount": "error_count",
		"durationMs": "duration_ms", "startedAt": "started_at",
	}

	var setClauses []string
	var args []interface{}
	for key, val := range updates {
		col, ok := validCols[key]
		if !ok {
			continue
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}
	if len(setClauses) == 0 {
		return nil
	}
	args = append(args, id)
	query := fmt.Sprintf("UPDATE crawl_jobs SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	return sqlitex.Execute(s.conn, query, &sqlitex.ExecOptions{Args: args})
}

// SaveCrawlCoverage persists a snapshot independently of page statistics, including
// zero-page crawls and interrupted executions. NULL/absence means not recorded.
func (s *UiStore) SaveCrawlCoverage(id string, coverage interface{}) error {
	data, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn, `INSERT INTO crawl_coverage(crawl_id, summary_json) VALUES (?, ?) ON CONFLICT(crawl_id) DO UPDATE SET summary_json = excluded.summary_json`, &sqlitex.ExecOptions{Args: []interface{}{id, string(data)}})
}

// DeleteCrawlJob deletes a crawl job and its associated stats by ID.
func (s *UiStore) DeleteCrawlJob(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := sqlitex.Execute(s.conn, `DELETE FROM crawl_coverage WHERE crawl_id = ?`, &sqlitex.ExecOptions{Args: []interface{}{id}}); err != nil {
		return false, err
	}
	// Drop this crawl's cached merges. Its rows in a legacy shared table go
	// with their log job: deleting them by crawl alone scans the whole table.
	if err := dropMergeTables(s.conn, `crawl_id = ?`, id); err != nil {
		return false, err
	}
	// Delete associated stats first (foreign key target)
	if err := sqlitex.Execute(s.conn, `DELETE FROM crawl_stats WHERE crawl_id = ?`,
		&sqlitex.ExecOptions{Args: []interface{}{id}}); err != nil {
		log.Printf("[store] warning: failed to delete stats for crawl %s: %v", id, err)
	}
	err := sqlitex.Execute(s.conn, `DELETE FROM crawl_jobs WHERE id = ?`,
		&sqlitex.ExecOptions{Args: []interface{}{id}})
	if err != nil {
		return false, err
	}
	return s.conn.Changes() > 0, nil
}

// ListPresets returns all presets (built-in first, then alphabetical).
func (s *UiStore) ListPresets() ([]*SavedPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var presets []*SavedPreset
	err := sqlitex.Execute(s.conn,
		`SELECT id, name, config, built_in, created_at, updated_at FROM presets ORDER BY built_in DESC, name ASC`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var cfg map[string]interface{}
				_ = json.Unmarshal([]byte(stmt.ColumnText(2)), &cfg)
				presets = append(presets, &SavedPreset{
					ID: stmt.ColumnText(0), Name: stmt.ColumnText(1),
					Config: cfg, BuiltIn: stmt.ColumnInt(3) != 0,
					CreatedAt: stmt.ColumnText(4), UpdatedAt: stmt.ColumnText(5),
				})
				return nil
			},
		})
	return presets, err
}

// SavePreset creates a custom preset.
func (s *UiStore) SavePreset(name string, config map[string]interface{}) (*SavedPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	configJSON, _ := json.Marshal(config)
	err := sqlitex.Execute(s.conn,
		`INSERT INTO presets (id, name, config, built_in, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{id, name, string(configJSON), now, now}})
	if err != nil {
		return nil, err
	}
	return &SavedPreset{ID: id, Name: name, Config: config, BuiltIn: false, CreatedAt: now, UpdatedAt: now}, nil
}

// DeletePreset deletes a custom preset (built-in presets cannot be deleted).
func (s *UiStore) DeletePreset(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := sqlitex.Execute(s.conn, `DELETE FROM presets WHERE id = ? AND built_in = 0`,
		&sqlitex.ExecOptions{Args: []interface{}{id}})
	if err != nil {
		return false, err
	}
	return s.conn.Changes() > 0, nil
}

// GetSettings returns all settings as a key-value map.
func (s *UiStore) GetSettings() (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := make(map[string]interface{})
	err := sqlitex.Execute(s.conn, `SELECT key, value FROM settings`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				key := stmt.ColumnText(0)
				raw := stmt.ColumnText(1)
				var parsed interface{}
				if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
					parsed = raw
				}
				settings[key] = parsed
				return nil
			},
		})
	return settings, err
}

// UpdateSettings batch-upserts settings within a single transaction.
func (s *UiStore) UpdateSettings(settings map[string]interface{}) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(settings) == 0 {
		return nil
	}

	// err is the named result so a failed COMMIT reaches the caller.
	endFn, err := sqlitex.ImmediateTransaction(s.conn)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer endFn(&err)

	for key, val := range settings {
		valJSON, _ := json.Marshal(val)
		if err = sqlitex.Execute(s.conn,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			&sqlitex.ExecOptions{Args: []interface{}{key, string(valJSON)}}); err != nil {
			return err
		}
	}
	return nil
}

// SaveCrawlStats persists the stats JSON blob for a completed crawl.
func (s *UiStore) SaveCrawlStats(crawlID string, statsJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO crawl_stats (crawl_id, stats_json) VALUES (?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{crawlID, string(statsJSON)}})
}

// LoadCrawlStats retrieves the persisted stats for a crawl.
func (s *UiStore) LoadCrawlStats(crawlID string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var statsMap map[string]interface{}
	err := sqlitex.Execute(s.conn,
		`SELECT stats_json FROM crawl_stats WHERE crawl_id = ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{crawlID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				return json.Unmarshal([]byte(stmt.ColumnText(0)), &statsMap)
			},
		})
	return statsMap, err
}

// AddCrawlEvent appends a lifecycle event to a crawl job's events array.
func (s *UiStore) AddCrawlEvent(crawlID string, eventType, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Read current events
	var eventsJSON string
	_ = sqlitex.Execute(s.conn, `SELECT events FROM crawl_jobs WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{crawlID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				eventsJSON = stmt.ColumnText(0)
				return nil
			},
		})
	var events []CrawlEvent
	if eventsJSON != "" {
		_ = json.Unmarshal([]byte(eventsJSON), &events)
	}
	events = append(events, CrawlEvent{
		Type: eventType,
		At:   time.Now().UTC().Format(time.RFC3339),
		Note: note,
	})
	newJSON, _ := json.Marshal(events)
	return sqlitex.Execute(s.conn, `UPDATE crawl_jobs SET events = ? WHERE id = ?`,
		&sqlitex.ExecOptions{Args: []interface{}{string(newJSON), crawlID}})
}

// DeleteCrawlJobsByDBPath deletes all crawl jobs sharing a DB path except the given ID.
func (s *UiStore) DeleteCrawlJobsByDBPath(keepID, dbPath string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	err := sqlitex.Execute(s.conn, `DELETE FROM crawl_jobs WHERE db_path = ? AND id != ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{dbPath, keepID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				deleted++
				return nil
			},
		})
	// Also clean up orphaned stats
	_ = sqlitex.Execute(s.conn, `DELETE FROM crawl_stats WHERE crawl_id NOT IN (SELECT id FROM crawl_jobs)`, nil)
	_ = dropMergeTables(s.conn, `crawl_id NOT IN (SELECT id FROM crawl_jobs)`)
	return deleted, err
}

// Close closes the database connection.
func (s *UiStore) Close() error {
	err := s.conn.Close()
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
	return err
}

func rowToJob(stmt *sqlite.Stmt) *CrawlJob {
	job := &CrawlJob{
		ID:         stmt.ColumnText(0),
		Status:     stmt.ColumnText(2),
		StartedAt:  stmt.ColumnText(3),
		PageCount:  stmt.ColumnInt(5),
		ErrorCount: stmt.ColumnInt(6),
		SeedURL:    stmt.ColumnText(8),
		Mode:       stmt.ColumnText(9),
		DBPath:     stmt.ColumnText(10),
	}

	// Parse config JSON
	var cfg map[string]interface{}
	_ = json.Unmarshal([]byte(stmt.ColumnText(1)), &cfg)
	job.Config = cfg

	// Nullable fields
	if stmt.ColumnType(4) != sqlite.TypeNull {
		v := stmt.ColumnText(4)
		job.CompletedAt = &v
	}
	if stmt.ColumnType(7) != sqlite.TypeNull {
		v := stmt.ColumnInt64(7)
		job.DurationMs = &v
	}
	// Parse events JSON (column 11)
	if stmt.ColumnCount() > 11 && stmt.ColumnType(11) != sqlite.TypeNull {
		evJSON := stmt.ColumnText(11)
		if evJSON != "" && evJSON != "[]" {
			_ = json.Unmarshal([]byte(evJSON), &job.Events)
		}
	}
	return job
}

// ── Log Analysis persistence ──

type LogJob struct {
	CompletedAt    *string `json:"completedAt"`
	DurationMs     *int64  `json:"durationMs"`
	UploadMs       *int64  `json:"uploadMs,omitempty"`
	ParseMs        *int64  `json:"parseMs,omitempty"`
	AnalysisMs     *int64  `json:"analysisMs,omitempty"`
	ErrorMsg       *string `json:"errorMsg"`
	ID             string  `json:"id"`
	Filename       string  `json:"filename"`
	Format         string  `json:"format"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"createdAt"`
	FileSize       int64   `json:"fileSize"`
	TotalLines     int64   `json:"totalLines"`
	ProcessedLines int64   `json:"processedLines"`
	// Files is the per-file breakdown for multi-file jobs. Empty (nil) for
	// single-file jobs — the top-level Filename/FileSize/TotalLines still apply.
	Files []LogJobFile `json:"files,omitempty"`
}

// LogJobFile describes one file within a multi-file log analysis job.
type LogJobFile struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	BytesRead int64  `json:"bytesRead,omitempty"`
	Lines     int64  `json:"lines,omitempty"`
	Status    string `json:"status"` // uploading, parsing, completed, failed
	Error     string `json:"error,omitempty"`
}

func (s *UiStore) CreateLogJob(filename string, fileSize int64) (*LogJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	err := sqlitex.Execute(s.conn,
		`INSERT INTO log_jobs (id, filename, status, created_at, file_size) VALUES (?, ?, 'processing', ?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{id, filename, now, fileSize}})
	if err != nil {
		return nil, err
	}
	return &LogJob{ID: id, Filename: filename, Status: "processing", CreatedAt: now, FileSize: fileSize}, nil
}

func (s *UiStore) UpdateLogJob(id string, updates map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := map[string]string{
		"status": "status", "format": "format", "completedAt": "completed_at",
		"totalLines": "total_lines", "processedLines": "processed_lines",
		"durationMs": "duration_ms", "errorMsg": "error_msg",
		"uploadMs": "upload_ms", "parseMs": "parse_ms", "analysisMs": "analysis_ms",
		"fileSize": "file_size",
		"filename": "filename",
		"files":    "files", // JSON-encoded []LogJobFile
	}
	var cols []string
	var args []interface{}
	for k, v := range updates {
		col, ok := allowed[k]
		if !ok {
			continue
		}
		// Auto-marshal []LogJobFile to JSON.
		if k == "files" {
			if files, ok := v.([]LogJobFile); ok {
				b, err := json.Marshal(files)
				if err != nil {
					return err
				}
				v = string(b)
			}
		}
		cols = append(cols, col+" = ?")
		args = append(args, v)
	}
	if len(cols) == 0 {
		return nil
	}
	args = append(args, id)
	return sqlitex.Execute(s.conn,
		fmt.Sprintf("UPDATE log_jobs SET %s WHERE id = ?", strings.Join(cols, ", ")),
		&sqlitex.ExecOptions{Args: args})
}

func (s *UiStore) ListLogJobs() ([]LogJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jobs []LogJob
	err := sqlitex.Execute(s.conn,
		`SELECT id, filename, format, status, created_at, completed_at, file_size, total_lines, processed_lines, duration_ms, error_msg, files, upload_ms, parse_ms, analysis_ms
		 FROM log_jobs ORDER BY created_at DESC LIMIT 100`,
		&sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error {
			j := LogJob{
				ID:             stmt.ColumnText(0),
				Filename:       stmt.ColumnText(1),
				Format:         stmt.ColumnText(2),
				Status:         stmt.ColumnText(3),
				CreatedAt:      stmt.ColumnText(4),
				FileSize:       stmt.ColumnInt64(6),
				TotalLines:     stmt.ColumnInt64(7),
				ProcessedLines: stmt.ColumnInt64(8),
			}
			if stmt.ColumnType(5) != sqlite.TypeNull {
				v := stmt.ColumnText(5)
				j.CompletedAt = &v
			}
			if stmt.ColumnType(9) != sqlite.TypeNull {
				v := stmt.ColumnInt64(9)
				j.DurationMs = &v
			}
			if stmt.ColumnType(10) != sqlite.TypeNull {
				v := stmt.ColumnText(10)
				j.ErrorMsg = &v
			}
			if stmt.ColumnType(11) != sqlite.TypeNull {
				if raw := stmt.ColumnText(11); raw != "" {
					_ = json.Unmarshal([]byte(raw), &j.Files)
				}
			}
			if stmt.ColumnType(12) != sqlite.TypeNull {
				v := stmt.ColumnInt64(12)
				j.UploadMs = &v
			}
			if stmt.ColumnType(13) != sqlite.TypeNull {
				v := stmt.ColumnInt64(13)
				j.ParseMs = &v
			}
			if stmt.ColumnType(14) != sqlite.TypeNull {
				v := stmt.ColumnInt64(14)
				j.AnalysisMs = &v
			}
			jobs = append(jobs, j)
			return nil
		}})
	return jobs, err
}

func (s *UiStore) GetLogJob(id string) (*LogJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var job *LogJob
	err := sqlitex.Execute(s.conn,
		`SELECT id, filename, format, status, created_at, completed_at, file_size, total_lines, processed_lines, duration_ms, error_msg, files, upload_ms, parse_ms, analysis_ms
		 FROM log_jobs WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				job = &LogJob{
					ID:             stmt.ColumnText(0),
					Filename:       stmt.ColumnText(1),
					Format:         stmt.ColumnText(2),
					Status:         stmt.ColumnText(3),
					CreatedAt:      stmt.ColumnText(4),
					FileSize:       stmt.ColumnInt64(6),
					TotalLines:     stmt.ColumnInt64(7),
					ProcessedLines: stmt.ColumnInt64(8),
				}
				if stmt.ColumnType(5) != sqlite.TypeNull {
					v := stmt.ColumnText(5)
					job.CompletedAt = &v
				}
				if stmt.ColumnType(9) != sqlite.TypeNull {
					v := stmt.ColumnInt64(9)
					job.DurationMs = &v
				}
				if stmt.ColumnType(10) != sqlite.TypeNull {
					v := stmt.ColumnText(10)
					job.ErrorMsg = &v
				}
				if stmt.ColumnType(11) != sqlite.TypeNull {
					if raw := stmt.ColumnText(11); raw != "" {
						_ = json.Unmarshal([]byte(raw), &job.Files)
					}
				}
				if stmt.ColumnType(12) != sqlite.TypeNull {
					v := stmt.ColumnInt64(12)
					job.UploadMs = &v
				}
				if stmt.ColumnType(13) != sqlite.TypeNull {
					v := stmt.ColumnInt64(13)
					job.ParseMs = &v
				}
				if stmt.ColumnType(14) != sqlite.TypeNull {
					v := stmt.ColumnInt64(14)
					job.AnalysisMs = &v
				}
				return nil
			},
		})
	return job, err
}

func (s *UiStore) DeleteLogJob(id string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// err is the named result: the caller deletes the uploaded log files
	// on nil, so a failed COMMIT must not look like success.
	endFn, txErr := sqlitex.ImmediateTransaction(s.conn)
	if txErr != nil {
		return txErr
	}
	defer endFn(&err)

	if err = sqlitex.Execute(s.conn, `DELETE FROM log_stats WHERE job_id = ?`, &sqlitex.ExecOptions{Args: []interface{}{id}}); err != nil {
		return err
	}

	// Drop per-job URL stats table — instant regardless of row count.
	_ = sqlitex.Execute(s.conn, fmt.Sprintf("DROP TABLE IF EXISTS %s", logURLTable(id)), nil)

	// Legacy: also clean up shared table if it has rows for this job (pre-migration data).
	_ = sqlitex.Execute(s.conn, `DELETE FROM log_url_stats WHERE job_id = ?`, &sqlitex.ExecOptions{Args: []interface{}{id}})

	// Drop this log's cached merges, and its rows in a legacy shared table.
	if err = dropMergeTables(s.conn, `log_id = ?`, id); err != nil {
		return err
	}
	if err = deleteLegacyMerge(s.conn, `log_id = ?`, id); err != nil {
		return err
	}

	err = sqlitex.Execute(s.conn, `DELETE FROM log_jobs WHERE id = ?`, &sqlitex.ExecOptions{Args: []interface{}{id}})
	return err
}

func (s *UiStore) SaveLogStats(jobID string, statsJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO log_stats (job_id, stats_json) VALUES (?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{jobID, string(statsJSON)}})
}

func (s *UiStore) LoadLogStats(jobID string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw json.RawMessage
	err := sqlitex.Execute(s.conn,
		`SELECT stats_json FROM log_stats WHERE job_id = ?`,
		&sqlitex.ExecOptions{
			Args: []interface{}{jobID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				raw = json.RawMessage(stmt.ColumnText(0))
				return nil
			},
		})
	return raw, err
}

// SaveLogURLStats writes the full per-URL hit stats to log_url_stats in one
// transaction with a prepared statement. Replaces any existing rows for the job.
//
// Performance note: SQLite single-row inserts cap at ~1-2K/sec; with prepared
// statement + transaction we hit ~100K-300K/sec. For a 5M-URL job that's
// roughly 20-50 seconds. Caller is expected to surface that as a phase.
// logURLTable returns the per-job table name for URL stats. Each job gets its
// own table so deletion is an instant DROP TABLE instead of scanning millions
// of rows. Table name: url_stats_<hex> where hex is the job UUID with hyphens
// removed (safe for SQL identifiers).
func logURLTable(jobID string) string {
	return "url_stats_" + strings.ReplaceAll(jobID, "-", "")
}

// createLogURLTable creates the per-job URL stats table if it doesn't exist.
// Uses a regular ROWID table (not WITHOUT ROWID) because long URL paths
// (avg 221 bytes) cause massive B-tree space amplification with WITHOUT ROWID
// (internal nodes duplicate the full key). ROWID tables store paths only in
// leaf nodes, reducing disk usage from ~32GB to ~5GB for 26M rows.
func (s *UiStore) createLogURLTable(conn *sqlite.Conn, table string) error {
	return sqlitex.ExecuteScript(conn, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			path       TEXT NOT NULL,
			hits       INTEGER NOT NULL,
			bot_hits   INTEGER NOT NULL,
			human_hits INTEGER NOT NULL,
			top_bot    TEXT,
			status     INTEGER,
			bytes      INTEGER NOT NULL DEFAULT 0
		);
	`, table), nil)
}

func (s *UiStore) SaveLogURLStats(jobID string, urls []logs.URLStat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Drop per-job table (instant) + clean legacy shared table.
	_ = sqlitex.Execute(s.conn, fmt.Sprintf("DROP TABLE IF EXISTS %s", logURLTable(jobID)), nil)
	_ = sqlitex.Execute(s.conn, `DELETE FROM log_url_stats WHERE job_id = ?`,
		&sqlitex.ExecOptions{Args: []interface{}{jobID}})
	// urls is always nil in current usage (clearing prior data before BulkInsert).
	return nil
}

// BulkInsertLogURLStats writes URL stats using the dedicated urlConn in batched
// transactions (100K rows per commit). Much faster than SaveLogURLStats for large
// datasets because: (1) uses urlConn so API reads aren't blocked, (2) smaller
// transactions reduce WAL pressure, (3) no ON CONFLICT overhead.
// Caller must ensure prior data for this jobID has already been cleared.
// StreamLogURLStats creates a per-job URL table and streams sorted URL stats
// into it via a callback. No intermediate []URLStat slice is materialized —
// saves ~2.5GB for 26M URLs. Commits every 100K rows.
func (s *UiStore) StreamLogURLStats(jobID string, stream func(fn func(logs.URLStat) error) error) error {
	table := logURLTable(jobID)
	s.urlMu.Lock()
	defer s.urlMu.Unlock()

	_ = sqlitex.Execute(s.urlConn, fmt.Sprintf("DROP TABLE IF EXISTS %s", table), nil)
	if err := s.createLogURLTable(s.urlConn, table); err != nil {
		return err
	}

	const batchSize = 100_000
	insertSQL := fmt.Sprintf(
		`INSERT INTO %s (path, hits, bot_hits, human_hits, top_bot, status, bytes) VALUES (?, ?, ?, ?, ?, ?, ?)`, table)

	var (
		count    int
		stmt     *sqlite.Stmt
		endFn    func(*error)
		txErr    error
		writeErr error
	)

	beginTx := func() error {
		endFn, txErr = sqlitex.ImmediateTransaction(s.urlConn)
		if txErr != nil {
			return txErr
		}
		var prepErr error
		stmt, _, prepErr = s.urlConn.PrepareTransient(insertSQL)
		if prepErr != nil {
			endFn(&prepErr)
			return prepErr
		}
		return nil
	}

	commitTx := func() error {
		if stmt != nil {
			stmt.Finalize()
			stmt = nil
		}
		if endFn != nil {
			endFn(&writeErr)
			endFn = nil
		}
		return writeErr
	}

	if err := beginTx(); err != nil {
		return err
	}

	streamErr := stream(func(u logs.URLStat) error {
		stmt.BindText(1, u.Path)
		stmt.BindInt64(2, u.Hits)
		stmt.BindInt64(3, u.BotHits)
		stmt.BindInt64(4, u.HumanHits)
		if u.TopBot != "" {
			stmt.BindText(5, u.TopBot)
		} else {
			stmt.BindNull(5)
		}
		if u.Status > 0 {
			stmt.BindInt64(6, int64(u.Status))
		} else {
			stmt.BindNull(6)
		}
		stmt.BindInt64(7, u.Bytes)
		if _, err := stmt.Step(); err != nil {
			writeErr = err
			return err
		}
		stmt.Reset()
		stmt.ClearBindings()
		count++
		if count%batchSize == 0 {
			if err := commitTx(); err != nil {
				return err
			}
			if err := beginTx(); err != nil {
				return err
			}
		}
		return nil
	})

	if err := commitTx(); err != nil && streamErr == nil {
		return err
	}
	return streamErr
}

// IterateLogURLStats streams every URL stats row for a job through fn. Fn may
// return an error to abort iteration. Memory-friendly: rows aren't buffered.
func (s *UiStore) IterateLogURLStats(jobID string, fn func(logs.URLStat) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	table := logURLTable(jobID)
	// Try per-job table first, fall back to legacy shared table.
	query := fmt.Sprintf(`SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes FROM %s`, table)
	resultFn := func(stmt *sqlite.Stmt) error {
		u := logs.URLStat{
			Path:      stmt.ColumnText(0),
			Hits:      stmt.ColumnInt64(1),
			BotHits:   stmt.ColumnInt64(2),
			HumanHits: stmt.ColumnInt64(3),
			Status:    int(stmt.ColumnInt64(5)),
			Bytes:     stmt.ColumnInt64(6),
		}
		if stmt.ColumnType(4) != sqlite.TypeNull {
			u.TopBot = stmt.ColumnText(4)
		}
		return fn(u)
	}
	err := sqlitex.Execute(s.conn, query, &sqlitex.ExecOptions{ResultFunc: resultFn})
	if err != nil {
		// Fall back to legacy shared table.
		return sqlitex.Execute(s.conn,
			`SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes FROM log_url_stats WHERE job_id = ?`,
			&sqlitex.ExecOptions{Args: []interface{}{jobID}, ResultFunc: resultFn})
	}
	return nil
}

// CountLogURLStats returns how many URL rows are stored for a job.
func (s *UiStore) CountLogURLStats(jobID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	countFn := func(stmt *sqlite.Stmt) error {
		count = stmt.ColumnInt64(0)
		return nil
	}
	// Try per-job table first.
	err := sqlitex.Execute(s.conn, fmt.Sprintf("SELECT COUNT(*) FROM %s", logURLTable(jobID)),
		&sqlitex.ExecOptions{ResultFunc: countFn})
	if err != nil {
		// Fall back to legacy shared table.
		err = sqlitex.Execute(s.conn, `SELECT COUNT(*) FROM log_url_stats WHERE job_id = ?`,
			&sqlitex.ExecOptions{Args: []interface{}{jobID}, ResultFunc: countFn})
	}
	return count, err
}

// UpsertLogURLStatsBatch inserts or merges a batch of URL stats using UPSERT.
// Uses a dedicated SQLite connection (urlConn) so bulk writes don't block API
// reads on the main connection.
func (s *UiStore) UpsertLogURLStatsBatch(jobID string, urls []logs.URLStat) (commitErr error) {
	if len(urls) == 0 {
		return nil
	}
	s.urlMu.Lock()
	defer s.urlMu.Unlock()

	endFn, txErr := sqlitex.ImmediateTransaction(s.urlConn)
	if txErr != nil {
		return txErr
	}
	defer func() { endFn(&commitErr) }()

	stmt, _, prepErr := s.urlConn.PrepareTransient(`
		INSERT INTO log_url_stats (job_id, path, hits, bot_hits, human_hits, top_bot, status, bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, path) DO UPDATE SET
			hits       = hits + excluded.hits,
			bot_hits   = bot_hits + excluded.bot_hits,
			human_hits = human_hits + excluded.human_hits,
			bytes      = bytes + excluded.bytes,
			status     = excluded.status`)
	if prepErr != nil {
		commitErr = prepErr
		return prepErr
	}
	defer stmt.Finalize()

	for _, u := range urls {
		stmt.BindText(1, jobID)
		stmt.BindText(2, u.Path)
		stmt.BindInt64(3, u.Hits)
		stmt.BindInt64(4, u.BotHits)
		stmt.BindInt64(5, u.HumanHits)
		if u.TopBot != "" {
			stmt.BindText(6, u.TopBot)
		} else {
			stmt.BindNull(6)
		}
		if u.Status > 0 {
			stmt.BindInt64(7, int64(u.Status))
		} else {
			stmt.BindNull(7)
		}
		stmt.BindInt64(8, u.Bytes)
		if _, err := stmt.Step(); err != nil {
			commitErr = err
			return err
		}
		stmt.Reset()
		stmt.ClearBindings()
	}
	return nil
}

// QueryTopLogURLs returns the top N URLs by hits from the per-job URL stats table.
func (s *UiStore) QueryTopLogURLs(jobID string, limit int) ([]logs.URLStat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []logs.URLStat
	resultFn := func(stmt *sqlite.Stmt) error {
		u := logs.URLStat{
			Path:      stmt.ColumnText(0),
			Hits:      stmt.ColumnInt64(1),
			BotHits:   stmt.ColumnInt64(2),
			HumanHits: stmt.ColumnInt64(3),
			Status:    int(stmt.ColumnInt64(5)),
			Bytes:     stmt.ColumnInt64(6),
		}
		if stmt.ColumnType(4) != sqlite.TypeNull {
			u.TopBot = stmt.ColumnText(4)
		}
		out = append(out, u)
		return nil
	}
	// Try per-job table first.
	query := fmt.Sprintf(`SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes FROM %s ORDER BY hits DESC LIMIT ?`, logURLTable(jobID))
	err := sqlitex.Execute(s.conn, query, &sqlitex.ExecOptions{Args: []interface{}{limit}, ResultFunc: resultFn})
	if err != nil {
		// Fall back to legacy shared table.
		out = nil
		err = sqlitex.Execute(s.conn,
			`SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes FROM log_url_stats WHERE job_id = ? ORDER BY hits DESC LIMIT ?`,
			&sqlitex.ExecOptions{Args: []interface{}{jobID, limit}, ResultFunc: resultFn})
	}
	return out, err
}

// mergeURLBatch is how many log URL rows the merge holds at once.
const mergeURLBatch = 50_000

// mergeSlot lets one log×crawl merge run at a time; others wait.
var mergeSlot = make(chan struct{}, 1)

// IterateLogURLStatsBatched streams a job's URL rows through fn in batches
// of batch rows. Unlike IterateLogURLStats it holds no lock while fn runs
// and reads on the URL connection (urlConn), so fn may use the store (the
// merge writes its pairs table through s.conn meanwhile) and memory stays
// at one batch however many URLs the log has.
func (s *UiStore) IterateLogURLStatsBatched(jobID string, batch int, fn func(logs.URLStat) error) error {
	if batch <= 0 {
		batch = mergeURLBatch
	}
	table := logURLTable(jobID)
	s.urlMu.Lock()
	perJob := false
	err := sqlitex.Execute(s.urlConn, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`,
		&sqlitex.ExecOptions{Args: []interface{}{table}, ResultFunc: func(*sqlite.Stmt) error { perJob = true; return nil }})
	s.urlMu.Unlock()
	if err != nil {
		return err
	}

	// Keyset pagination: per-job tables by rowid, the legacy shared
	// table by its (job_id, path) primary key.
	var query string
	if perJob {
		query = fmt.Sprintf(`SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes, rowid FROM %s
			WHERE rowid > ? ORDER BY rowid LIMIT ?`, table)
	} else {
		query = `SELECT path, hits, bot_hits, human_hits, top_bot, status, bytes, path FROM log_url_stats
			WHERE job_id = ? AND path > ? ORDER BY path LIMIT ?`
	}
	var lastRowID int64
	lastPath := ""
	rows := make([]logs.URLStat, 0, batch)
	for {
		rows = rows[:0]
		resultFn := func(stmt *sqlite.Stmt) error {
			u := logs.URLStat{
				Path:      stmt.ColumnText(0),
				Hits:      stmt.ColumnInt64(1),
				BotHits:   stmt.ColumnInt64(2),
				HumanHits: stmt.ColumnInt64(3),
				Status:    int(stmt.ColumnInt64(5)),
				Bytes:     stmt.ColumnInt64(6),
			}
			if stmt.ColumnType(4) != sqlite.TypeNull {
				u.TopBot = stmt.ColumnText(4)
			}
			if perJob {
				lastRowID = stmt.ColumnInt64(7)
			} else {
				lastPath = u.Path
			}
			rows = append(rows, u)
			return nil
		}
		args := []interface{}{lastRowID, batch}
		if !perJob {
			args = []interface{}{jobID, lastPath, batch}
		}
		s.urlMu.Lock()
		err := sqlitex.Execute(s.urlConn, query, &sqlitex.ExecOptions{Args: args, ResultFunc: resultFn})
		s.urlMu.Unlock()
		if err != nil {
			return err
		}
		for i := range rows {
			if err := fn(rows[i]); err != nil {
				return err
			}
		}
		if len(rows) < batch {
			return nil
		}
	}
}

// ── Merged-page cache (one table per log×crawl pair) ────────────────────────
//
// Each (log, crawl) merge lives in its own plain table, like the per-job
// url_stats_<id> tables: a re-run drops and rebuilds only that table (fast
// appends, then one index build over its own rows), and other merges are
// never touched. log_merge_tables lists the tables so deleting a log job or
// a crawl can drop its pairs. Merges written by older versions stay in the
// shared legacy table log_merge_pages and are read from there until the pair
// is re-run or deleted; the legacy table is dropped once it is empty.

const legacyMergeTable = "log_merge_pages"

// mergeTable returns the table name for a (logID, crawlID) pair. The IDs are
// hashed (length-prefixed so "ab"+"c" and "a"+"bc" differ) and never put into
// SQL themselves: any ID gives a safe identifier.
func mergeTable(logID, crawlID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%s", len(logID), logID, crawlID)))
	return "log_merge_" + hex.EncodeToString(sum[:16])
}

// validMergeTable guards names read back from log_merge_tables before they
// go into a DROP statement.
func validMergeTable(name string) bool {
	const prefix = "log_merge_"
	if len(name) != len(prefix)+32 || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, c := range name[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func createMergeTable(conn *sqlite.Conn, table string) error {
	return sqlitex.Execute(conn, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		url             TEXT NOT NULL,
		segment         TEXT NOT NULL,
		reason          TEXT,
		log_hits        INTEGER NOT NULL DEFAULT 0,
		log_bot_hits    INTEGER NOT NULL DEFAULT 0,
		log_human_hits  INTEGER NOT NULL DEFAULT 0,
		log_top_bot     TEXT,
		log_status      INTEGER,
		crawl_status    INTEGER,
		crawl_indexable INTEGER,
		crawl_canonical TEXT,
		crawl_depth     INTEGER,
		crawl_inlinks   INTEGER,
		crawl_word_count INTEGER,
		has_noindex     INTEGER,
		in_crawl        INTEGER,
		in_logs         INTEGER,
		log_bytes       INTEGER NOT NULL DEFAULT 0
	)`, table), nil)
}

func createMergeIndexes(conn *sqlite.Conn, table string) error {
	for _, ddl := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS %[1]s_url ON %[1]s(url)`,
		`CREATE INDEX IF NOT EXISTS %[1]s_segment ON %[1]s(segment, log_bot_hits DESC)`,
		`CREATE INDEX IF NOT EXISTS %[1]s_bot_hits ON %[1]s(log_bot_hits DESC)`,
	} {
		if err := sqlitex.Execute(conn, fmt.Sprintf(ddl, table), nil); err != nil {
			return err
		}
	}
	return nil
}

// registerMergeTable records the pair's table so deletes can find it.
func registerMergeTable(conn *sqlite.Conn, logID, crawlID, table string) error {
	return sqlitex.Execute(conn,
		`INSERT OR REPLACE INTO log_merge_tables (log_id, crawl_id, table_name) VALUES (?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []interface{}{logID, crawlID, table}})
}

// hasMergeTable reports whether the pair has its own table.
func hasMergeTable(conn *sqlite.Conn, logID, crawlID string) (bool, error) {
	found := false
	err := sqlitex.Execute(conn,
		`SELECT 1 FROM log_merge_tables WHERE log_id = ? AND crawl_id = ?`,
		&sqlitex.ExecOptions{
			Args:       []interface{}{logID, crawlID},
			ResultFunc: func(*sqlite.Stmt) error { found = true; return nil },
		})
	return found, err
}

func legacyMergeExists(conn *sqlite.Conn) (bool, error) {
	found := false
	err := sqlitex.Execute(conn,
		`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`,
		&sqlitex.ExecOptions{
			Args:       []interface{}{legacyMergeTable},
			ResultFunc: func(*sqlite.Stmt) error { found = true; return nil },
		})
	return found, err
}

// deleteLegacyMerge removes the legacy rows matching where (log_id and
// crawl_id lead the legacy key and indexes) and drops the legacy table once
// it holds no rows.
func deleteLegacyMerge(conn *sqlite.Conn, where string, args ...interface{}) error {
	exists, err := legacyMergeExists(conn)
	if err != nil || !exists {
		return err
	}
	if err := sqlitex.Execute(conn, `DELETE FROM `+legacyMergeTable+` WHERE `+where,
		&sqlitex.ExecOptions{Args: args}); err != nil {
		return err
	}
	return dropLegacyMergeIfEmpty(conn)
}

func dropLegacyMergeIfEmpty(conn *sqlite.Conn) error {
	exists, err := legacyMergeExists(conn)
	if err != nil || !exists {
		return err
	}
	empty := true
	if err := sqlitex.Execute(conn, `SELECT 1 FROM `+legacyMergeTable+` LIMIT 1`,
		&sqlitex.ExecOptions{ResultFunc: func(*sqlite.Stmt) error { empty = false; return nil }}); err != nil {
		return err
	}
	if !empty {
		return nil
	}
	return sqlitex.Execute(conn, `DROP TABLE `+legacyMergeTable, nil)
}

// dropMergeTables drops every pair table whose log_merge_tables row matches
// where, and those rows, all or nothing.
func dropMergeTables(conn *sqlite.Conn, where string, args ...interface{}) (err error) {
	defer sqlitex.Save(conn)(&err)
	var tables []string
	if err = sqlitex.Execute(conn, `SELECT table_name FROM log_merge_tables WHERE `+where,
		&sqlitex.ExecOptions{Args: args, ResultFunc: func(stmt *sqlite.Stmt) error {
			tables = append(tables, stmt.ColumnText(0))
			return nil
		}}); err != nil {
		return err
	}
	for _, t := range tables {
		if !validMergeTable(t) {
			return fmt.Errorf("invalid merge table name %q", t)
		}
		if err = sqlitex.Execute(conn, "DROP TABLE IF EXISTS "+t, nil); err != nil {
			return err
		}
	}
	return sqlitex.Execute(conn, `DELETE FROM log_merge_tables WHERE `+where, &sqlitex.ExecOptions{Args: args})
}

// bindMergeRow binds one merged page to an insert built from mergeInsertSQL.
func bindMergeRow(stmt *sqlite.Stmt, mp *logs.MergedPage) {
	boolOrNull := func(idx int, v bool, present bool) {
		switch {
		case !present:
			stmt.BindNull(idx)
		case v:
			stmt.BindInt64(idx, 1)
		default:
			stmt.BindInt64(idx, 0)
		}
	}
	intOrNull := func(idx int, v int) {
		if v == 0 {
			stmt.BindNull(idx)
			return
		}
		stmt.BindInt64(idx, int64(v))
	}
	textOrNull := func(idx int, v string) {
		if v == "" {
			stmt.BindNull(idx)
			return
		}
		stmt.BindText(idx, v)
	}
	stmt.BindText(1, mp.URL)
	stmt.BindText(2, string(mp.Segment))
	textOrNull(3, mp.Reason)
	stmt.BindInt64(4, mp.LogHits)
	stmt.BindInt64(5, mp.LogBotHits)
	stmt.BindInt64(6, mp.LogHumanHits)
	textOrNull(7, mp.LogTopBot)
	intOrNull(8, mp.LogStatus)
	intOrNull(9, mp.CrawlStatus)
	boolOrNull(10, mp.CrawlIndexable, mp.InCrawl)
	textOrNull(11, mp.CrawlCanonical)
	intOrNull(12, mp.CrawlDepth)
	intOrNull(13, mp.CrawlInlinks)
	intOrNull(14, mp.CrawlWordCount)
	boolOrNull(15, mp.HasNoindex, mp.InCrawl)
	boolOrNull(16, mp.InCrawl, true)
	boolOrNull(17, mp.InLogs, true)
	stmt.BindInt64(18, mp.LogBytes)
}

// mergeInsertSQL takes the verb (INSERT / INSERT OR REPLACE) and the table.
const mergeInsertSQL = `%s INTO %s (
	url, segment, reason,
	log_hits, log_bot_hits, log_human_hits, log_top_bot, log_status,
	crawl_status, crawl_indexable, crawl_canonical, crawl_depth,
	crawl_inlinks, crawl_word_count, has_noindex, in_crawl, in_logs,
	log_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// WriteMergePagesBulk writes ALL merged pages in a single transaction.
// This is 10-50× faster than per-batch transactions because SQLite only
// syncs the WAL once (at COMMIT), not 1000+ times.
// It rebuilds only the (logID, crawlID) pair's own table: merges of other
// pairs stay cached and are not re-indexed. On error it keeps reading pages
// until the channel is closed, so a producer sending on it never blocks
// forever.
// The onProgress callback is called every progressInterval rows.
func (s *UiStore) WriteMergePagesBulk(logID, crawlID string, pages <-chan logs.MergedPage, onProgress func(written int64)) (commitErr error) {
	defer func() {
		if commitErr != nil {
			for range pages {
			}
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()

	endFn, txErr := sqlitex.ImmediateTransaction(s.conn)
	if txErr != nil {
		return txErr
	}
	defer func() { endFn(&commitErr) }()

	// A fresh plain table with no indexes: each INSERT is an O(1) append.
	// The indexes are built once at the end, over this pair's rows only.
	table := mergeTable(logID, crawlID)
	if commitErr = sqlitex.Execute(s.conn, "DROP TABLE IF EXISTS "+table, nil); commitErr != nil {
		return commitErr
	}
	if commitErr = createMergeTable(s.conn, table); commitErr != nil {
		return commitErr
	}
	if commitErr = registerMergeTable(s.conn, logID, crawlID, table); commitErr != nil {
		return commitErr
	}
	// This pair's rows written by an older version are replaced too.
	if commitErr = deleteLegacyMerge(s.conn, `log_id = ? AND crawl_id = ?`, logID, crawlID); commitErr != nil {
		return commitErr
	}

	stmt, _, prepErr := s.conn.PrepareTransient(fmt.Sprintf(mergeInsertSQL, "INSERT", table))
	if prepErr != nil {
		commitErr = prepErr
		return prepErr
	}
	defer stmt.Finalize()

	var written int64
	for mp := range pages {
		bindMergeRow(stmt, &mp)
		if _, err := stmt.Step(); err != nil {
			commitErr = err
			return err
		}
		stmt.Reset()
		stmt.ClearBindings()
		written++
		if onProgress != nil && written%50_000 == 0 {
			onProgress(written)
		}
	}
	if onProgress != nil {
		onProgress(written)
	}

	commitErr = createMergeIndexes(s.conn, table)
	return commitErr
}

// ClearMergePages drops all cached merged rows for a given (logID, crawlID) pair.
func (s *UiStore) ClearMergePages(logID, crawlID string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer sqlitex.Save(s.conn)(&err)
	if err = dropMergeTables(s.conn, `log_id = ? AND crawl_id = ?`, logID, crawlID); err != nil {
		return err
	}
	return deleteLegacyMerge(s.conn, `log_id = ? AND crawl_id = ?`, logID, crawlID)
}

// SaveMergePagesBatch writes a batch of merged pages in one transaction,
// adding them to (or replacing by URL in) the pair's table.
// Caller is expected to call this in chunks (e.g. 5K rows) during a streaming
// merge so memory stays bounded.
func (s *UiStore) SaveMergePagesBatch(logID, crawlID string, pages []logs.MergedPage) (commitErr error) {
	if len(pages) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	endFn, txErr := sqlitex.ImmediateTransaction(s.conn)
	if txErr != nil {
		return txErr
	}
	defer func() { endFn(&commitErr) }()

	table := mergeTable(logID, crawlID)
	has, err := hasMergeTable(s.conn, logID, crawlID)
	if err != nil {
		commitErr = err
		return err
	}
	if !has {
		if commitErr = createMergeTable(s.conn, table); commitErr != nil {
			return commitErr
		}
		if commitErr = createMergeIndexes(s.conn, table); commitErr != nil {
			return commitErr
		}
		if commitErr = registerMergeTable(s.conn, logID, crawlID, table); commitErr != nil {
			return commitErr
		}
		// Readers now use the pair's table: drop its legacy rows so they
		// are not kept hidden forever.
		if commitErr = deleteLegacyMerge(s.conn, `log_id = ? AND crawl_id = ?`, logID, crawlID); commitErr != nil {
			return commitErr
		}
	}

	stmt, _, prepErr := s.conn.PrepareTransient(fmt.Sprintf(mergeInsertSQL, "INSERT OR REPLACE", table))
	if prepErr != nil {
		commitErr = prepErr
		return prepErr
	}
	defer stmt.Finalize()

	for i := range pages {
		bindMergeRow(stmt, &pages[i])
		if _, err := stmt.Step(); err != nil {
			commitErr = err
			return err
		}
		stmt.Reset()
		stmt.ClearBindings()
	}
	return nil
}

// MergeQueryOpts narrows / orders / paginates a merged-pages query.
type MergeQueryOpts struct {
	Segment           string // e.g. "crawl_waste"; empty = all
	URLSearch         string // LIKE %x% on url
	ReasonSearch      string // LIKE %x% on reason
	TopBotSearch      string // LIKE %x% on log_top_bot
	LogStatusFilter   string // "200", "404", "2xx", "3xx", "4xx", "5xx", "" = any
	CrawlStatusFilter string
	IndexableFilter   string // "yes", "no", "" = any
	// Coverage maps Venn-style regions to segment combinations:
	//   "crawl_only" → segment = uncrawled_indexable (ghosts)
	//   "logs_only"  → segment = orphan_crawled
	//   "both"       → everything else (matched URLs)
	// Empty = no coverage filter applied.
	Coverage string
	SortBy   string // logical column name (see sortColumns map)
	Order    string // "asc" or "desc"
	Page     int    // 1-based
	PageSize int    // capped at 500
}

// statusFilterClause translates a "200" / "404" / "2xx" / "3xx" filter token
// into a SQL fragment + bound args. Returns ("", nil) if the token is invalid
// or empty (no filter applied).
func statusFilterClause(column, token string) (string, []interface{}) {
	if token == "" {
		return "", nil
	}
	t := strings.ToLower(strings.TrimSpace(token))
	if len(t) == 3 && (t[1] == 'x' || t[1] == 'X') && (t[2] == 'x' || t[2] == 'X') {
		switch t[0] {
		case '1', '2', '3', '4', '5':
			low := int64(t[0]-'0') * 100
			return column + " >= ? AND " + column + " < ?", []interface{}{low, low + 100}
		}
	}
	// Specific code like "200" / "404"
	if n, err := strconv.Atoi(t); err == nil && n >= 100 && n < 600 {
		return column + " = ?", []interface{}{int64(n)}
	}
	return "", nil
}

// QueryMergePages returns a paginated/sorted/filtered slice of merged pages
// plus the total row count after filtering.
func (s *UiStore) QueryMergePages(logID, crawlID string, opts MergeQueryOpts) ([]logs.MergedPage, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Whitelist sortable columns to prevent SQL injection.
	sortColumns := map[string]string{
		"url":            "url",
		"segment":        "segment",
		"logHits":        "log_hits",
		"logBotHits":     "log_bot_hits",
		"logHumanHits":   "log_human_hits",
		"logBytes":       "log_bytes",
		"logStatus":      "log_status",
		"crawlStatus":    "crawl_status",
		"crawlDepth":     "crawl_depth",
		"crawlInlinks":   "crawl_inlinks",
		"crawlWordCount": "crawl_word_count",
	}
	sortCol, ok := sortColumns[opts.SortBy]
	if !ok {
		sortCol = "log_bot_hits"
	}
	order := "DESC"
	if strings.ToLower(opts.Order) == "asc" {
		order = "ASC"
	}
	pageSize := opts.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	page := opts.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// The pair's own table, else rows an older version wrote into the
	// shared legacy table, else nothing cached yet.
	table := mergeTable(logID, crawlID)
	var whereClauses []string
	var args []interface{}
	has, err := hasMergeTable(s.conn, logID, crawlID)
	if err != nil {
		return nil, 0, err
	}
	if !has {
		legacy, err := legacyMergeExists(s.conn)
		if err != nil {
			return nil, 0, err
		}
		if !legacy {
			return nil, 0, nil
		}
		table = legacyMergeTable
		whereClauses = []string{"log_id = ?", "crawl_id = ?"}
		args = []interface{}{logID, crawlID}
	}
	if opts.Segment != "" {
		whereClauses = append(whereClauses, "segment = ?")
		args = append(args, opts.Segment)
	}
	if opts.URLSearch != "" {
		whereClauses = append(whereClauses, "url LIKE ?")
		args = append(args, "%"+opts.URLSearch+"%")
	}
	if opts.ReasonSearch != "" {
		whereClauses = append(whereClauses, "reason LIKE ?")
		args = append(args, "%"+opts.ReasonSearch+"%")
	}
	if opts.TopBotSearch != "" {
		whereClauses = append(whereClauses, "log_top_bot LIKE ?")
		args = append(args, "%"+opts.TopBotSearch+"%")
	}
	if c, a := statusFilterClause("log_status", opts.LogStatusFilter); c != "" {
		whereClauses = append(whereClauses, c)
		args = append(args, a...)
	}
	if c, a := statusFilterClause("crawl_status", opts.CrawlStatusFilter); c != "" {
		whereClauses = append(whereClauses, c)
		args = append(args, a...)
	}
	switch strings.ToLower(opts.IndexableFilter) {
	case "yes", "true", "1":
		whereClauses = append(whereClauses, "crawl_indexable = 1")
	case "no", "false", "0":
		whereClauses = append(whereClauses, "crawl_indexable = 0")
	}
	switch strings.ToLower(opts.Coverage) {
	case "crawl_only":
		whereClauses = append(whereClauses, "segment = 'uncrawled_indexable'")
	case "logs_only":
		whereClauses = append(whereClauses, "segment = 'orphan_crawled'")
	case "both":
		whereClauses = append(whereClauses, "segment NOT IN ('uncrawled_indexable','orphan_crawled')")
	}
	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Count first.
	var total int64
	if err := sqlitex.Execute(s.conn,
		`SELECT COUNT(*) FROM `+table+whereSQL,
		&sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				total = stmt.ColumnInt64(0)
				return nil
			},
		}); err != nil {
		return nil, 0, err
	}

	// Then page.
	pageQuery := `SELECT
		url, segment, reason,
		log_hits, log_bot_hits, log_human_hits, log_top_bot, log_status,
		crawl_status, crawl_indexable, crawl_canonical, crawl_depth,
		crawl_inlinks, crawl_word_count, has_noindex, in_crawl, in_logs,
		log_bytes
		FROM ` + table + whereSQL +
		` ORDER BY ` + sortCol + ` ` + order + `, url ASC LIMIT ? OFFSET ?`
	pageArgs := append(append([]interface{}{}, args...), pageSize, offset)

	var rows []logs.MergedPage
	err = sqlitex.Execute(s.conn, pageQuery, &sqlitex.ExecOptions{
		Args: pageArgs,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			mp := logs.MergedPage{
				URL:          stmt.ColumnText(0),
				Segment:      logs.PageSegment(stmt.ColumnText(1)),
				LogHits:      stmt.ColumnInt64(3),
				LogBotHits:   stmt.ColumnInt64(4),
				LogHumanHits: stmt.ColumnInt64(5),
			}
			if stmt.ColumnType(2) != sqlite.TypeNull {
				mp.Reason = stmt.ColumnText(2)
			}
			if stmt.ColumnType(6) != sqlite.TypeNull {
				mp.LogTopBot = stmt.ColumnText(6)
			}
			if stmt.ColumnType(7) != sqlite.TypeNull {
				mp.LogStatus = int(stmt.ColumnInt64(7))
			}
			if stmt.ColumnType(8) != sqlite.TypeNull {
				mp.CrawlStatus = int(stmt.ColumnInt64(8))
			}
			if stmt.ColumnType(9) != sqlite.TypeNull {
				mp.CrawlIndexable = stmt.ColumnInt64(9) != 0
			}
			if stmt.ColumnType(10) != sqlite.TypeNull {
				mp.CrawlCanonical = stmt.ColumnText(10)
			}
			if stmt.ColumnType(11) != sqlite.TypeNull {
				mp.CrawlDepth = int(stmt.ColumnInt64(11))
			}
			if stmt.ColumnType(12) != sqlite.TypeNull {
				mp.CrawlInlinks = int(stmt.ColumnInt64(12))
			}
			if stmt.ColumnType(13) != sqlite.TypeNull {
				mp.CrawlWordCount = int(stmt.ColumnInt64(13))
			}
			if stmt.ColumnType(14) != sqlite.TypeNull {
				mp.HasNoindex = stmt.ColumnInt64(14) != 0
			}
			if stmt.ColumnType(15) != sqlite.TypeNull {
				mp.InCrawl = stmt.ColumnInt64(15) != 0
			}
			if stmt.ColumnType(16) != sqlite.TypeNull {
				mp.InLogs = stmt.ColumnInt64(16) != 0
			}
			mp.LogBytes = stmt.ColumnInt64(17)
			rows = append(rows, mp)
			return nil
		},
	})
	return rows, total, err
}

// UpdateCrawlJobConfig replaces a crawl's stored config (live tuning keeps
// the new delay, concurrency and limit for a later resume).
func (s *UiStore) UpdateCrawlJobConfig(id string, config map[string]interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn, `UPDATE crawl_jobs SET config = ? WHERE id = ?`, &sqlitex.ExecOptions{Args: []interface{}{string(data), id}})
}
