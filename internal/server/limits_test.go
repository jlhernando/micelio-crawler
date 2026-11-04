package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"zombiezen.com/go/sqlite"
)

func TestDefaultLimitsFitDocumentedWorkloads(t *testing.T) {
	lim := limits()
	if lim.MaxCrawlPages < 250_000 {
		t.Errorf("page cap %d below the 250K stress test", lim.MaxCrawlPages)
	}
	if lim.LogUploadBytes < 3*65*gib/10 {
		t.Errorf("log upload cap %d below three 6.5 GB files", lim.LogUploadBytes)
	}
	if lim.LogAnalyses < 1 || lim.LogPending < lim.LogAnalyses {
		t.Errorf("log queue %d/%d", lim.LogAnalyses, lim.LogPending)
	}
}

// A small gzip that expands past the cap is refused, not written out.
func TestImportJSONLDecompressionCap(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{"url":"https://a.example/","statusCode":200}` + "\n"))
	zw.Write(bytes.Repeat([]byte(" "), 4<<20)) // 4 MB of padding compresses to a few KB
	zw.Close()
	src := filepath.Join(t.TempDir(), "bomb.jsonl.gz")
	os.WriteFile(src, buf.Bytes(), 0o644)
	dst := filepath.Join(t.TempDir(), "out.db")
	err := jsonlToCrawlDB(src, "gzip", dst, importLimit{maxDecompressed: 1 << 20})
	if !errors.Is(err, errImportTooLarge) {
		t.Fatalf("err = %v, want errImportTooLarge", err)
	}
	// Without a cap (CLI) the same file imports.
	if err := jsonlToCrawlDB(src, "gzip", filepath.Join(t.TempDir(), "ok.db"), importLimit{}); err != nil {
		t.Fatalf("uncapped: %v", err)
	}
}

func TestImportJSONLPageCapAndSchemes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	three := write("three.jsonl", `{"url":"https://a.example/1"}`+"\n"+`{"url":"https://a.example/2"}`+"\n"+`{"url":"https://a.example/3"}`+"\n")
	if err := jsonlToCrawlDB(three, "jsonl", filepath.Join(dir, "a.db"), importLimit{maxPages: 2}); !errors.Is(err, errImportTooLarge) {
		t.Errorf("page cap: err = %v", err)
	}
	file := write("file.jsonl", `{"url":"file:///etc/passwd","statusCode":200}`+"\n")
	if err := jsonlToCrawlDB(file, "jsonl", filepath.Join(dir, "b.db"), importLimit{}); !errors.Is(err, ErrNotCrawlFile) {
		t.Errorf("file:// URL: err = %v, want ErrNotCrawlFile", err)
	}
}

func TestReadLimitedLine(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 100)+"\nshort\n"), 16)
	if _, err := readLimitedLine(br, 50); !errors.Is(err, errImportTooLarge) {
		t.Errorf("long line: err = %v", err)
	}
	br = bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 40)+"\n"), 16)
	if line, err := readLimitedLine(br, 50); err != nil || len(line) != 41 {
		t.Errorf("line of 41 bytes: %d, %v", len(line), err)
	}
}

func TestCapReader(t *testing.T) {
	if b, err := io.ReadAll(&capReader{r: strings.NewReader("12345"), left: 5}); err != nil || string(b) != "12345" {
		t.Errorf("exact size: %q %v", b, err)
	}
	if _, err := io.ReadAll(&capReader{r: strings.NewReader("123456"), left: 5}); !errors.Is(err, errImportTooLarge) {
		t.Errorf("over size: err = %v", err)
	}
}

func TestImportCrawlDBRejectsNonWebURLs(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	src := filepath.Join(t.TempDir(), "file.db")
	cs, err := storage.NewCrawlStore(src)
	if err != nil {
		t.Fatal(err)
	}
	cs.SaveConfig(types.CrawlConfig{SeedURL: "https://a.example/"})
	cs.InsertPage(&types.PageData{URL: "https://a.example/", StatusCode: 200})
	cs.InsertPage(&types.PageData{URL: "file:///etc/passwd", StatusCode: 200})
	cs.Close()
	if _, err := m.ImportCrawl(context.Background(), src, ImportOptions{}); !errors.Is(err, ErrNotCrawlFile) {
		t.Errorf("err = %v, want ErrNotCrawlFile", err)
	}
}

func TestImportUploadTooLarge(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	mux := http.NewServeMux()
	registerImportRoutes(mux, s, m)
	req := httptest.NewRequest(http.MethodPost, "/api/crawls/import", strings.NewReader("x"))
	req.ContentLength = limits().ImportBytes + 1
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", rec.Code)
	}
	if left, _ := filepath.Glob(filepath.Join(s.crawlDir, "upload-*")); len(left) > 0 {
		t.Errorf("upload file created: %v", left)
	}
}

func logUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "access.log")
	fw.Write([]byte(`1.2.3.4 - - [10/Oct/2026:13:55:36 +0000] "GET / HTTP/1.1" 200 12 "-" "Googlebot/2.1"` + "\n"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/logs/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestLogUploadLimits(t *testing.T) {
	api, s, m := newTestAPI(t)
	// Log analysis writes URL stats on a second connection.
	uc, err := sqlite.OpenConn(filepath.Join(s.uiDir, "test.db"), sqlite.OpenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	s.urlConn = uc
	t.Cleanup(func() { uc.Close() })

	req := logUploadRequest(t)
	req.ContentLength = limits().LogUploadBytes + 1
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload: status %d, want 413", rec.Code)
	}

	// The queue is full: refused before any byte is stored.
	m.logQueue.mu.Lock()
	m.logQueue.pending = limits().LogPending
	m.logQueue.mu.Unlock()
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, logUploadRequest(t))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("full queue: status %d, want 429", rec.Code)
	}
	if jobs, _ := s.ListLogJobs(); len(jobs) != 0 {
		t.Errorf("refused uploads created %d log jobs", len(jobs))
	}

	// A refused upload gives its place back; an accepted one holds it until
	// its analysis ends.
	m.logQueue.mu.Lock()
	m.logQueue.pending = 0
	m.logQueue.mu.Unlock()
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, logUploadRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.logQueue.mu.Lock()
		p := m.logQueue.pending
		m.logQueue.mu.Unlock()
		if p == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue place not released (pending %d)", p)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLogQueueBoundsConcurrency(t *testing.T) {
	var q logQueue
	n := limits().LogAnalyses + 2
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		if !q.reserve() {
			t.Fatalf("reserve %d refused", i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.run(func() {
				cur := running.Add(1)
				for {
					old := peak.Load()
					if cur <= old || peak.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				running.Add(-1)
			})
		}()
	}
	wg.Wait()
	if int(peak.Load()) > limits().LogAnalyses {
		t.Errorf("peak %d analyses at once, limit %d", peak.Load(), limits().LogAnalyses)
	}
	if q.pending != 0 {
		t.Errorf("pending = %d after all runs", q.pending)
	}
}
