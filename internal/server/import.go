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
	"github.com/micelio/micelio/internal/secrets"
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
	// limit bounds what the import may expand into; zero values mean no
	// limit. Dashboard uploads and the CLI (LimitImport) set it.
	limit importLimit
}

// importLimit bounds what an import may expand into.
type importLimit struct {
	maxDecompressed int64 // bytes read from a (decompressed) JSONL stream
	maxPages        int
}

// errImportTooLarge means an import went past its limits.
var errImportTooLarge = errors.New("crawl file is too large to import")

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
		if err := jsonlToCrawlDB(path, kind, p.srcDB, opts.limit); err != nil {
			p.cleanup()
			return nil, err
		}
	}
	cfg, err := inspectCrawlDB(p.srcDB, opts.limit.maxPages)
	if err != nil {
		p.cleanup()
		return nil, err
	}
	if opts.LinkIntelligence {
		cfg.LinkIntelligence = true
	}
	// Credentials in an imported file (from an older version, or made by
	// hand) are never used: no API key reaches the analysis.
	if cfg, err = configWithoutSecrets(cfg); err != nil {
		p.cleanup()
		return nil, fmt.Errorf("%w: bad config: %v", ErrNotCrawlFile, err)
	}
	p.cfg = cfg
	p.job, err = m.store.CreateCrawlJob(importedConfigMap(cfg, opts.Name))
	if err != nil {
		p.cleanup()
		return nil, fmt.Errorf("create crawl: %w", err)
	}
	// Held until runImport ends, so a delete cannot remove the job while
	// its DB is copied and analysed. The job is new: nothing deletes it yet.
	release := m.retainJob(p.job.ID)
	cleanup := p.cleanup
	p.cleanup = func() { cleanup(); release() }
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
	// The copy keeps no credentials the source file may hold.
	if err := cs.ScrubConfig(); err != nil {
		cs.Close()
		return fail(fmt.Errorf("scrub config: %w", err))
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
	// "completed" comes before the analysis: say results are not ready.
	m.setAnalyzing(job.ID, true)
	defer m.setAnalyzing(job.ID, false)
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
	postCfg.SitemapHreflang, _ = cs.LoadSitemapHreflang()
	if raw, _ := cs.GetMeta(crawler.AgentReadinessMetaKey); raw != "" {
		json.Unmarshal([]byte(raw), &postCfg.AgentReadiness)
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
func jsonlToCrawlDB(path, kind, dst string, lim importLimit) error {
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
	if lim.maxDecompressed > 0 {
		r = &capReader{r: r, left: lim.maxDecompressed}
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
		line, readErr := readLimitedLine(br, maxImportLineBytes)
		if errors.Is(readErr, errImportTooLarge) {
			return fmt.Errorf("line %d: %w", pages+1, readErr)
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var pg types.PageData
			if err := json.Unmarshal(line, &pg); err != nil || pg.URL == "" {
				if pages == 0 {
					return ErrNotCrawlFile
				}
				return fmt.Errorf("line %d: not a page", pages+1)
			}
			if !isWebURL(pg.URL) {
				return fmt.Errorf("%w: line %d: page URL must be http(s)", ErrNotCrawlFile, pages+1)
			}
			if lim.maxPages > 0 && pages >= lim.maxPages {
				return fmt.Errorf("%w: more than %d pages", errImportTooLarge, lim.maxPages)
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

// readLimitedLine reads one line (with its newline) of at most max bytes.
func readLimitedLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			return nil, errImportTooLarge
		}
		line = append(line, chunk...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

// capReader fails with errImportTooLarge once more than left bytes are read,
// so a small compressed file cannot expand without bound.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		// One more byte tells a stream that ends exactly at the cap from
		// one that goes on.
		var b [1]byte
		if n, _ := c.r.Read(b[:]); n > 0 {
			return 0, errImportTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// inspectCrawlDB checks that path is a Micelio crawl database with pages and
// returns its crawl config. It opens the file read-only. maxPages > 0 caps
// the page count.
func inspectCrawlDB(path string, maxPages int) (*types.CrawlConfig, error) {
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
	if maxPages > 0 && pages > maxPages {
		return nil, fmt.Errorf("%w: %d pages, the limit is %d", errImportTooLarge, pages, maxPages)
	}
	// Only web pages: an imported file:// URL must never reach Chrome
	// (Universe thumbnails) or any other fetch.
	var nonWeb int
	if err := sqlitex.Execute(conn, `SELECT COUNT(*) FROM pages WHERE lower(substr(url, 1, 7)) <> 'http://' AND lower(substr(url, 1, 8)) <> 'https://'`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { nonWeb = stmt.ColumnInt(0); return nil },
	}); err != nil {
		return nil, ErrNotCrawlFile
	}
	if nonWeb > 0 {
		return nil, fmt.Errorf("%w: %d page URLs are not http(s)", ErrNotCrawlFile, nonWeb)
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

// configWithoutSecrets returns cfg without credentials (see
// secrets.Drop).
func configWithoutSecrets(cfg *types.CrawlConfig) (*types.CrawlConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if data, err = secrets.ScrubJSON(data); err != nil {
		return nil, err
	}
	var out types.CrawlConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func copyCrawlDB(src, dst string) error {
	os.Remove(dst)
	// VACUUM INTO accepts an empty file: create it owner-only first.
	f, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	conn, err := sqlite.OpenConn(src, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer conn.Close()
	return sqlitex.Execute(conn, `VACUUM INTO ?`, &sqlitex.ExecOptions{Args: []any{dst}})
}

// importedConfigMap turns the stored crawl config into the dashboard's job
// config. Credentials are dropped at any depth (customHeaders such as
// Authorization, cookies, keys, proxy passwords): the imported crawl does not
// need them.
func importedConfigMap(cfg *types.CrawlConfig, name string) map[string]interface{} {
	data, _ := json.Marshal(cfg)
	m := map[string]interface{}{}
	json.Unmarshal(data, &m)
	dropConfigSecrets(m)
	delete(m, "customHeaders")
	delete(m, "proxy")
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
// (crawl database or JSONL, optionally .gz/.zst), streamed to disk. Its size,
// decompressed size and page count are capped (see limits.go). ?name= sets the shown file name and ?linkIntelligence=1
// adds link analysis. The crawl appears in History right away and is
// analysed in the background.
func registerImportRoutes(mux *http.ServeMux, store *UiStore, manager *CrawlManager) {
	mux.HandleFunc("POST /api/crawls/import", func(w http.ResponseWriter, r *http.Request) {
		lim := limits()
		if r.ContentLength > lim.ImportBytes {
			jsonError(w, fmt.Sprintf("crawl file is larger than the %d GB import limit (MICELIO_MAX_IMPORT_GB)", lim.ImportBytes/gib), http.StatusRequestEntityTooLarge)
			return
		}
		body := http.MaxBytesReader(w, r.Body, lim.ImportBytes)
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
		_, copyErr := io.Copy(tmp, body)
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil {
			remove()
			var tooBig *http.MaxBytesError
			if errors.As(copyErr, &tooBig) {
				jsonError(w, fmt.Sprintf("crawl file is larger than the %d GB import limit (MICELIO_MAX_IMPORT_GB)", lim.ImportBytes/gib), http.StatusRequestEntityTooLarge)
				return
			}
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
		opts := ImportOptions{Name: name, LinkIntelligence: r.URL.Query().Get("linkIntelligence") == "1",
			limit: importLimit{maxDecompressed: lim.ImportDecompressedBytes, maxPages: lim.MaxCrawlPages}}
		job, err := manager.StartImport(tmp.Name(), opts, remove)
		if err != nil {
			remove()
			code := http.StatusInternalServerError
			if errors.Is(err, ErrNotCrawlFile) {
				code = http.StatusBadRequest
			} else if errors.Is(err, errImportTooLarge) {
				code = http.StatusRequestEntityTooLarge
			}
			jsonError(w, err.Error(), code)
			return
		}
		jsonOK(w, redactedJob(job))
	})
}
