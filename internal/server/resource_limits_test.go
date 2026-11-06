package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/micelio/micelio/internal/logs"
	"github.com/micelio/micelio/internal/types"
)

// ── HTTP / WebSocket bounds (server.go) ──

func TestHTTPServerHasConnectionLimits(t *testing.T) {
	s := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout != httpReadHeaderTimeout || s.ReadHeaderTimeout == 0 {
		t.Errorf("ReadHeaderTimeout = %v", s.ReadHeaderTimeout)
	}
	if s.IdleTimeout == 0 || s.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Errorf("IdleTimeout = %v, MaxHeaderBytes = %d", s.IdleTimeout, s.MaxHeaderBytes)
	}
	// Whole-request clocks would cut uploads, exports and /ws.
	if s.ReadTimeout != 0 || s.WriteTimeout != 0 {
		t.Errorf("ReadTimeout/WriteTimeout must stay 0, got %v/%v", s.ReadTimeout, s.WriteTimeout)
	}
}

func shortDeadlines(t *testing.T, d time.Duration) {
	t.Helper()
	oldBody, oldWrite := bodyIdleTimeout, writeIdleTimeout
	bodyIdleTimeout, writeIdleTimeout = d, d
	t.Cleanup(func() { bodyIdleTimeout, writeIdleTimeout = oldBody, oldWrite })
}

// A client that stops sending its body loses the connection; one that
// keeps sending, however slowly overall, does not.
func TestConnDeadlinesBodyProgress(t *testing.T) {
	shortDeadlines(t, 300*time.Millisecond)
	got := make(chan error, 1)
	srv := httptest.NewServer(withConnDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err == nil {
			fmt.Fprintf(w, "%d", n)
		}
		got <- err
	})))
	defer srv.Close()

	// Stalled body: headers promise 100 bytes, 10 arrive, then nothing.
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n0123456789")
	select {
	case err := <-got:
		if err == nil {
			t.Fatal("stalled body was read to the end")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled body held the handler")
	}

	// Slow but steady: 8 chunks 100 ms apart = 800 ms in total, longer
	// than the idle timeout, but never idle for that long.
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 8; i++ {
			pw.Write([]byte("chunk"))
			time.Sleep(100 * time.Millisecond)
		}
		pw.Close()
	}()
	resp, err := http.Post(srv.URL, "text/plain", pr)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "40" {
		t.Errorf("steady upload: got %q, want 40", b)
	}
	<-got
}

// After the body is read, a long handler keeps its request context: the
// read deadline is cleared so net/http's background read cannot cancel it.
func TestConnDeadlinesLongHandlerKeepsContext(t *testing.T) {
	shortDeadlines(t, 100*time.Millisecond)
	srv := httptest.NewServer(withConnDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		time.Sleep(400 * time.Millisecond) // a PDF export, a merge...
		if err := r.Context().Err(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte(strings.Repeat("x", 1<<20))) // more than one writeChunk
	})))
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(b) != 1<<20 {
		t.Fatalf("status %d, %d bytes: %.80s", resp.StatusCode, len(b), b)
	}
}

func wsTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := &Server{clients: map[*wsClient]struct{}{}}
	ts := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	t.Cleanup(ts.Close)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http")
}

// An oversized client frame closes the socket instead of being buffered.
func TestWebSocketReadLimit(t *testing.T) {
	_, u := wsTestServer(t)
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteMessage(websocket.TextMessage, []byte("small")); err != nil {
		t.Fatal(err)
	}
	c.WriteMessage(websocket.BinaryMessage, make([]byte, wsReadLimit*4))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = c.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.CloseMessageTooBig {
		t.Errorf("err = %v, want close 1009 (message too big)", err)
	}
}

// Past maxWSClients new sockets are refused; closing one frees its slot,
// and its ping goroutine ends with it.
func TestWebSocketClientCapAndCleanup(t *testing.T) {
	s, u := wsTestServer(t)
	base := runtime.NumGoroutine()
	var conns []*websocket.Conn
	for i := 0; i < maxWSClients; i++ {
		c, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("client %d accepted (err=%v)", maxWSClients+1, err)
	}
	for _, c := range conns {
		c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.wsSlots.Load() != 0 || runtime.NumGoroutine() > base+5 {
		if time.Now().After(deadline) {
			t.Fatalf("after close: %d slots, %d goroutines (base %d): ping goroutines leak",
				s.wsSlots.Load(), runtime.NumGoroutine(), base)
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	c.Close()
}

// ── /pages view cache (pages_view.go) ──

// Segment values the filter ignores share one cache entry, and identical
// concurrent misses run one computation.
func TestPageViewCanonicalSegmentAndSingleFlight(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "unique-1": "", ":x": "", "nope:": "", "nope:1": noSegmentMatch,
		"dir1:blog": "dir1:blog", "rule:Shop": "rule:Shop",
	} {
		if got := canonicalSegment(in); got != want {
			t.Errorf("canonicalSegment(%q) = %q, want %q", in, got, want)
		}
	}

	pages := viewTestPages(2000)
	r := &CrawlResults{Pages: pages, PagesReady: true}
	var calls atomic.Int32
	release := make(chan struct{})
	old := computePageIndices
	computePageIndices = func(p []*types.PageData, pq pageQuery) []int32 {
		calls.Add(1)
		<-release
		return pageIndices(p, pq)
	}
	t.Cleanup(func() { computePageIndices = old })

	var wg sync.WaitGroup
	results := make([][]int32, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := url.Values{"sort": {"title"}, "segment": {fmt.Sprintf("unique-%d", i)}}
			r.mu.RLock()
			results[i] = r.pageView(pageQueryFromValues(q))
			r.mu.RUnlock()
		}(i)
	}
	time.Sleep(100 * time.Millisecond) // let them all queue on the first
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("%d computations for one semantic query, want 1", n)
	}
	for i := range results {
		if len(results[i]) != len(pages) || &results[i][0] != &results[0][0] {
			t.Fatalf("request %d got a different view", i)
		}
	}
	if got := pageIndices(pages, pageQueryFromValues(url.Values{"segment": {"nope:1"}})); len(got) != 0 {
		t.Errorf("unknown dimension matched %d pages", len(got))
	}
}

// ── Legacy word-count histogram (wordcount.go) ──

func TestWordCountBucketsComputedOncePerPageSet(t *testing.T) {
	var calls atomic.Int32
	old := buildWordCountBuckets
	buildWordCountBuckets = func(p []*types.PageData) *types.WordCountBuckets {
		calls.Add(1)
		return old(p)
	}
	t.Cleanup(func() { buildWordCountBuckets = old })

	results := &CrawlResults{
		Stats:      map[string]interface{}{"totalPages": 2},
		Pages:      []*types.PageData{{WordCount: 50}, {WordCount: 2500}},
		PagesReady: true,
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results.mu.RLock()
			defer results.mu.RUnlock()
			stats := capStatsForAPI(results.Stats)
			backfillWordCountBuckets(stats, results)
			if _, ok := stats["wordCountBuckets"]; !ok {
				t.Error("missing buckets")
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("histogram built %d times for one page set, want 1", n)
	}
	// New pages: rebuilt once.
	results.Pages = []*types.PageData{{WordCount: 10}}
	stats := capStatsForAPI(results.Stats)
	backfillWordCountBuckets(stats, results)
	if b := stats["wordCountBuckets"].(*types.WordCountBuckets); b.Pages != 1 || calls.Load() != 2 {
		t.Errorf("after reload: %+v, %d builds", b, calls.Load())
	}
}

// ── Export web checks (export_web.go) ──

// Concurrent exports of one uncached crawl make one CrUX/PageSpeed/CDN
// lookup, not one each.
func TestWebChecksSingleFlight(t *testing.T) {
	_, s, m := newTestAPI(t)
	var calls atomic.Int32
	release := make(chan struct{})
	old := fetchWebChecks
	fetchWebChecks = func(context.Context, *http.Client, string, string, string) (*types.CWVReport, *types.CDNReport) {
		calls.Add(1)
		<-release
		return &types.CWVReport{Source: "crux"}, &types.CDNReport{Provider: "X"}
	}
	t.Cleanup(func() {
		fetchWebChecks = old
		webCache.Lock()
		webCache.m = map[string]webCheck{}
		webCache.Unlock()
	})
	var wg sync.WaitGroup
	var got atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cwv, cdn := webChecks(context.Background(), "sf-crawl", "https://ex.com/", s, m); cwv != nil && cdn != nil {
				got.Add(1)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 || got.Load() != 10 {
		t.Errorf("%d lookups, %d answers; want 1 lookup, 10 answers", calls.Load(), got.Load())
	}
	// A caller that gives up while waiting does not hang.
	webCache.Lock()
	webCache.m = map[string]webCheck{}
	webCache.Unlock()
	release2 := make(chan struct{})
	fetchWebChecks = func(context.Context, *http.Client, string, string, string) (*types.CWVReport, *types.CDNReport) {
		<-release2
		return nil, nil
	}
	leader := make(chan struct{})
	go func() { webChecks(context.Background(), "sf-crawl", "https://ex.com/", s, m); close(leader) }()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if cwv, _ := webChecks(ctx, "sf-crawl", "https://ex.com/", s, m); cwv != nil {
		t.Error("cancelled waiter got an answer")
	}
	close(release2)
	<-leader
}

// ── Log merge (store.go, api.go) ──

func TestIterateLogURLStatsBatched(t *testing.T) {
	s := newTestStore(t)
	rows := make([]logs.URLStat, 7)
	for i := range rows {
		rows[i] = logs.URLStat{Path: fmt.Sprintf("/p%d", i), Hits: int64(i + 1), BotHits: 1, Status: 200}
	}
	if err := s.StreamLogURLStats("job-a", func(fn func(logs.URLStat) error) error {
		for _, u := range rows {
			if err := fn(u); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertLogURLStatsBatch("job-legacy", rows); err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"job-a", "job-legacy"} {
		seen := map[string]bool{}
		// fn uses the store while iterating: the merge writer does too.
		err := s.IterateLogURLStatsBatched(job, 3, func(u logs.URLStat) error {
			if seen[u.Path] {
				return fmt.Errorf("%s twice", u.Path)
			}
			seen[u.Path] = true
			_, err := s.CountLogURLStats(job)
			return err
		})
		if err != nil || len(seen) != len(rows) {
			t.Errorf("%s: %d rows, err %v", job, len(seen), err)
		}
	}
}

// The merge endpoint streams log URLs from the per-job table.
func TestLogMergeEndpointStreams(t *testing.T) {
	api, s, m := newTestAPI(t)
	lj, err := s.CreateLogJob("access.log", 10)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(logs.LogStats{})
	s.SaveLogStats(lj.ID, raw)
	s.StreamLogURLStats(lj.ID, func(fn func(logs.URLStat) error) error {
		for _, p := range []string{"/a", "/b", "/orphan"} {
			fn(logs.URLStat{Path: p, Hits: 3, BotHits: 3, Status: 200})
		}
		return nil
	})
	job, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://ex.com/"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cacheResultLocked(job.ID, &CrawlResults{Stats: map[string]interface{}{}, PagesReady: true, Pages: []*types.PageData{
		{URL: "https://ex.com/a", StatusCode: 200}, {URL: "https://ex.com/b", StatusCode: 200},
		{URL: "https://ex.com/c", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}}, // ghost: indexable, no traffic
	}}, 3)
	m.mu.Unlock()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/logs/"+lj.ID+"/merge", strings.NewReader(`{"crawlId":"`+job.ID+`"}`)))
	var sum logs.MergeSummary
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &sum) != nil {
		t.Fatalf("merge %d: %s", rec.Code, rec.Body.String())
	}
	if sum.LogURLsTotal != 3 || sum.OrphanCount != 1 || sum.GhostCount != 1 || sum.CappedURLs {
		t.Errorf("summary %+v", sum)
	}
}

// ── CLI import limits (limits.go) ──

func TestLimitImport(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "crawl.jsonl.gz")
	os.WriteFile(small, []byte("x"), 0o644)
	opts, err := LimitImport(small, ImportOptions{LinkIntelligence: true})
	if err != nil {
		t.Fatal(err)
	}
	lim := limits()
	if !opts.LinkIntelligence || opts.limit.maxDecompressed != lim.ImportDecompressedBytes || opts.limit.maxPages != lim.MaxCrawlPages {
		t.Errorf("limits not applied: %+v", opts)
	}
	big := filepath.Join(dir, "big.db")
	f, _ := os.Create(big)
	f.Truncate(lim.ImportBytes + 1) // sparse: no real disk used
	f.Close()
	if _, err := LimitImport(big, ImportOptions{}); !errors.Is(err, errImportTooLarge) {
		t.Errorf("oversized file: err = %v", err)
	}
}
