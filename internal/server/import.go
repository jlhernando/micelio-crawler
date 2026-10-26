package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Importing lets a crawl made elsewhere (for example `micelio crawl` on a
// small cloud server) be opened in the dashboard. The crawl database written
// by the CLI has the same format as the dashboard's, so an import copies it,
// registers a History entry and runs the same post-crawl analysis a dashboard
// crawl gets. JSONL output (plain, .gz or .zst) is converted to a database
// first.

// ImportOptions tunes an import.
type ImportOptions struct {
	// Name is the file name shown in the crawl's events.
	Name string
	// LinkIntelligence forces link analysis even if the crawl ran without it.
	LinkIntelligence bool
}

// ErrNotCrawlFile means the file is neither a Micelio crawl database nor
// Micelio JSONL output.
var ErrNotCrawlFile = errors.New("not a Micelio crawl database or JSONL file")

// pendingImport is a validated import whose History entry exists.
type pendingImport struct {
	job     *CrawlJob
	cfg     *types.CrawlConfig
	srcDB   string
	cleanup func() // removes a temporary database made from JSONL
	name    string
}

// ImportCrawl imports a crawl file and runs the core analysis before it
// returns. Link intelligence (if enabled) continues in the background (see
// WaitBackground).
func (m *CrawlManager) ImportCrawl(ctx context.Context, path string, opts ImportOptions) (*CrawlJob, error) {
	p, err := m.prepareImport(path, opts)
	if err != nil {
		return nil, err
	}
	if err := m.runImport(ctx, p); err != nil {
		return p.job, err
	}
	if job, err := m.store.GetCrawlJob(p.job.ID); err == nil && job != nil {
		return job, nil
	}
	return p.job, nil
}

// StartImport validates path and creates its History entry, then copies and
// analyses it in the background. onDone (may be nil) runs when that ends.
func (m *CrawlManager) StartImport(path string, opts ImportOptions, onDone func()) (*CrawlJob, error) {
	p, err := m.prepareImport(path, opts)
	if err != nil {
		return nil, err
	}
	m.background.Add(1)
	go func() {
		defer m.background.Done()
		if onDone != nil {
			defer onDone()
		}
		if err := m.runImport(m.ctx, p); err != nil {
			log.Printf("import %s: %v", p.job.ID, err)
		}
	}()
	return p.job, nil
}

func (m *CrawlManager) prepareImport(path string, opts ImportOptions) (*pendingImport, error) {
	if opts.Name == "" {
		opts.Name = filepath.Base(path)
	}
	kind, err := sniffCrawlFile(path)
	if err != nil {
		return nil, err
	}
	p := &pendingImport{srcDB: path, cleanup: func() {}, name: opts.Name}
	if kind != "sqlite" {
		tmp, err := os.CreateTemp(m.store.crawlDir, "import-*.db")
		if err != nil {
			return nil, err
		}
		tmp.Close()
		p.srcDB = tmp.Name()
		p.cleanup = func() {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				os.Remove(p.srcDB + suffix)
			}
		}
		if err := jsonlToCrawlDB(path, kind, p.srcDB); err != nil {
			p.cleanup()
			return nil, err
		}
	}
	cfg, err := inspectCrawlDB(p.srcDB)
	if err != nil {
		p.cleanup()
		return nil, err
	}
	if opts.LinkIntelligence {
		cfg.LinkIntelligence = true
	}
	p.cfg = cfg
	p.job, err = m.store.CreateCrawlJob(importedConfigMap(cfg, opts.Name))
	if err != nil {
		p.cleanup()
		return nil, fmt.Errorf("create crawl: %w", err)
	}
	return p, nil
}

func (m *CrawlManager) runImport(ctx context.Context, p *pendingImport) error {
	defer p.cleanup()
	job := p.job
	fail := func(err error) error {
		m.store.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "failed"})
		m.store.AddCrawlEvent(job.ID, "failed", "import: "+err.Error())
		return err
	}

	// VACUUM INTO writes a consistent, compact copy and never touches the
	// source file (it may still be open or read-only).
	if err := copyCrawlDB(p.srcDB, job.DBPath); err != nil {
		return fail(fmt.Errorf("copy database: %w", err))
	}
	cs, err := storage.NewCrawlStore(job.DBPath)
	if err != nil {
		return fail(fmt.Errorf("open copy: %w", err))
	}
	pages, err := cs.GetAllPages()
	if err != nil {
		cs.Close()
		return fail(fmt.Errorf("read pages: %w", err))
	}

	errorCount := 0
	for _, pg := range pages {
		if pg.StatusCode >= 400 || (pg.StatusCode == 0 && pg.Error != "" && !pg.RobotsBlocked) {
			errorCount++
		}
	}
	now := time.Now().UTC()
	updates := map[string]interface{}{
		"status": "completed", "pageCount": len(pages), "errorCount": errorCount,
		"completedAt": now.Format(time.RFC3339),
	}
	var durationMs int64
	if started, _ := cs.GetMeta("startedAt"); started != "" {
		if t0, err := time.Parse(time.RFC3339, started); err == nil {
			updates["startedAt"] = started
			if done, _ := cs.GetMeta("completedAt"); done != "" {
				if t1, err := time.Parse(time.RFC3339, done); err == nil && !t1.Before(t0) {
					updates["completedAt"] = done
					durationMs = t1.Sub(t0).Milliseconds()
					updates["durationMs"] = durationMs
				}
			}
		}
	}
	if err := m.store.UpdateCrawlJob(job.ID, updates); err != nil {
		log.Printf("import %s: update job: %v", job.ID, err)
	}
	m.store.AddCrawlEvent(job.ID, "imported", fmt.Sprintf("%s · %d pages", p.name, len(pages)))

	postCfg := analysis.PostProcessConfigFromCrawl(*p.cfg)
	postCfg.CrawlDurationMs = durationMs
	// Anchor stats are aggregated during a crawl; stored pages keep anchors
	// only when it ran with --full-anchors.
	var anchors *analysis.AnchorAggregator
	for _, pg := range pages {
		if len(pg.Anchors) > 0 {
			if anchors == nil {
				anchors = analysis.NewAnchorAggregator()
			}
			anchors.AddPage(pg.URL, pg.StatusCode, pg.Anchors)
			pg.Anchors = nil
		}
	}
	if anchors != nil {
		postCfg.AnchorAggregator = anchors
	}
	postCfg.InternalLinksIter = cs.IterateInternalLinks
	if raw, _ := cs.GetMeta(crawler.Soft404ProbeMetaKey); raw != "" {
		json.Unmarshal([]byte(raw), &postCfg.Soft404Probes)
	}
	m.analyzeAndCache(analysisRun{
		ctx: ctx, crawlID: job.ID, job: job, config: *p.cfg, pages: pages,
		postCfg: postCfg, crawlStore: cs, closeLinks: func() { cs.Close() },
		elapsed: durationMs, skipNotify: true,
	})
	return nil
}

// sniffCrawlFile returns "sqlite", "gzip", "zstd" or "jsonl".
func sniffCrawlFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("SQLite format 3\x00")):
		return "sqlite", nil
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return "gzip", nil
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return "zstd", nil
	case len(bytes.TrimLeft(head, " \t\r\n")) > 0 && bytes.TrimLeft(head, " \t\r\n")[0] == '{':
		return "jsonl", nil
	}
	return "", ErrNotCrawlFile
}

// jsonlToCrawlDB writes the pages of a JSONL crawl output into a new crawl
// database. JSONL has no crawl config, so the start URL is the shallowest
// page.
func jsonlToCrawlDB(path, kind, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	switch kind {
	case "gzip":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotCrawlFile, err)
		}
		defer gz.Close()
		r = gz
	case "zstd":
		zr, err := zstd.NewReader(f)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotCrawlFile, err)
		}
		defer zr.Close()
		r = zr
	}

	cs, err := storage.NewCrawlStore(dst)
	if err != nil {
		return err
	}
	defer cs.Close()

	br := bufio.NewReaderSize(r, 1<<20)
	seed, seedDepth, pages := "", -1, 0
	var first, last time.Time
	for {
		line, readErr := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var pg types.PageData
			if err := json.Unmarshal(line, &pg); err != nil || pg.URL == "" {
				if pages == 0 {
					return ErrNotCrawlFile
				}
				return fmt.Errorf("line %d: not a page", pages+1)
			}
			if seedDepth < 0 || pg.Depth < seedDepth {
				seed, seedDepth = pg.URL, pg.Depth
			}
			if t := pg.CrawledAt; !t.IsZero() {
				if first.IsZero() || t.Before(first) {
					first = t
				}
				if t.After(last) {
					last = t
				}
			}
			if err := cs.InsertPage(&pg); err != nil {
				return err
			}
			pages++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if pages == 0 {
		return fmt.Errorf("%w: it has no pages", ErrNotCrawlFile)
	}
	if err := cs.SaveConfig(types.CrawlConfig{SeedURL: seed, Mode: types.ModeSpider, MaxPages: pages}); err != nil {
		return err
	}
	if err := cs.SetStatus("complete"); err != nil {
		return err
	}
	// The crawl's real time span comes from the pages, not from this import.
	if !first.IsZero() {
		cs.SaveMeta("startedAt", first.UTC().Format(time.RFC3339))
		cs.SaveMeta("completedAt", last.UTC().Format(time.RFC3339))
	}
	return nil
}

// inspectCrawlDB checks that path is a Micelio crawl database with pages and
// returns its crawl config. It opens the file read-only.
func inspectCrawlDB(path string) (*types.CrawlConfig, error) {
	conn, err := sqlite.OpenConn(path, sqlite.OpenReadOnly)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotCrawlFile, err)
	}
	defer conn.Close()

	var raw string
	var pages int
	err = sqlitex.Execute(conn, `SELECT value FROM crawl_meta WHERE key = 'config'`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { raw = stmt.ColumnText(0); return nil },
	})
	if err == nil {
		err = sqlitex.Execute(conn, `SELECT COUNT(*) FROM pages`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error { pages = stmt.ColumnInt(0); return nil },
		})
	}
	if err != nil || raw == "" {
		return nil, ErrNotCrawlFile
	}
	if pages == 0 {
		return nil, fmt.Errorf("%w: it has no pages", ErrNotCrawlFile)
	}
	var cfg types.CrawlConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("%w: bad config: %v", ErrNotCrawlFile, err)
	}
	if cfg.SeedURL == "" && len(cfg.URLs) == 0 && len(cfg.SitemapURLs) == 0 {
		return nil, fmt.Errorf("%w: no start URL", ErrNotCrawlFile)
	}
	return &cfg, nil
}

func copyCrawlDB(src, dst string) error {
	os.Remove(dst)
	conn, err := sqlite.OpenConn(src, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer conn.Close()
	return sqlitex.Execute(conn, `VACUUM INTO ?`, &sqlitex.ExecOptions{Args: []any{dst}})
}

// importedConfigMap turns the stored crawl config into the dashboard's job
// config. Credentials are dropped: the imported crawl does not need them.
func importedConfigMap(cfg *types.CrawlConfig, name string) map[string]interface{} {
	data, _ := json.Marshal(cfg)
	m := map[string]interface{}{}
	json.Unmarshal(data, &m)
	for k := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "password") || strings.Contains(lk, "credential") || lk == "cookies" {
			delete(m, k)
		}
	}
	delete(m, "dbPath")
	delete(m, "outputPath")
	if cfg.SeedURL == "" {
		switch {
		case len(cfg.URLs) > 0:
			m["seedUrl"] = cfg.URLs[0]
		case len(cfg.SitemapURLs) > 0:
			m["seedUrl"] = cfg.SitemapURLs[0]
		}
	}
	m["imported"] = map[string]interface{}{"file": name, "at": time.Now().UTC().Format(time.RFC3339)}
	return m
}

// registerImportRoutes adds POST /api/crawls/import. The body is the raw file
// (crawl database or JSONL, optionally .gz/.zst), streamed to disk, so size
// does not matter. ?name= sets the shown file name and ?linkIntelligence=1
// adds link analysis. The crawl appears in History right away and is
// analysed in the background.
func registerImportRoutes(mux *http.ServeMux, store *UiStore, manager *CrawlManager) {
	mux.HandleFunc("POST /api/crawls/import", func(w http.ResponseWriter, r *http.Request) {
		tmp, err := os.CreateTemp(store.crawlDir, "upload-*.part")
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		remove := func() {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				os.Remove(tmp.Name() + suffix)
			}
		}
		_, copyErr := io.Copy(tmp, r.Body)
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil {
			remove()
			jsonError(w, "upload failed", http.StatusBadRequest)
			return
		}
		name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
		if name == "." || name == "/" {
			name = ""
		}
		if name == "" {
			name = "uploaded crawl"
		}
		opts := ImportOptions{Name: name, LinkIntelligence: r.URL.Query().Get("linkIntelligence") == "1"}
		job, err := manager.StartImport(tmp.Name(), opts, remove)
		if err != nil {
			remove()
			code := http.StatusInternalServerError
			if errors.Is(err, ErrNotCrawlFile) {
				code = http.StatusBadRequest
			}
			jsonError(w, err.Error(), code)
			return
		}
		jsonOK(w, job)
	})
}
