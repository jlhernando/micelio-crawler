package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowServer answers nothing until the client gives up: every request it
// gets stays open (an outbound connection) until its context ends.
type slowServer struct {
	*httptest.Server
	active atomic.Int64
	total  atomic.Int64
	first  chan struct{}
	once   sync.Once
}

func newSlowServer(t *testing.T) *slowServer {
	t.Helper()
	s := &slowServer{first: make(chan struct{})}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.total.Add(1)
		s.active.Add(1)
		defer s.active.Add(-1)
		s.once.Do(func() { close(s.first) })
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	t.Cleanup(func() {
		s.CloseClientConnections()
		s.Close()
	})
	return s
}

// host is the slow server's address under a name other than 127.0.0.1, so
// its URLs are external to a site served on 127.0.0.1.
func (s *slowServer) host() string {
	return strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
}

// quiet waits until no request is open on s and no new one starts for a
// while; it fails the test if requests keep coming.
func (s *slowServer) quiet(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for s.active.Load() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d outbound requests still open %s after cancel", s.active.Load(), within)
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := s.total.Load()
	time.Sleep(300 * time.Millisecond)
	if after := s.total.Load(); after != before || s.active.Load() > 0 {
		t.Fatalf("requests kept coming after cancel: %d new, %d open", after-before, s.active.Load())
	}
}

// postCrawlSite serves 6 linked pages; each links to 40 external URLs on ext
// (240 in all) and embeds 20 images on ext.
func postCrawlSite(t *testing.T, ext string) *httptest.Server {
	t.Helper()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		var b strings.Builder
		b.WriteString(`<html><head><title>A test page with a title</title></head><body><h1>Page</h1>`)
		for i := 0; i < 6; i++ {
			fmt.Fprintf(&b, `<a href="/p%d">page %d</a>`, i, i)
		}
		for i := 0; i < 40; i++ {
			fmt.Fprintf(&b, `<a href="%s/out%s/%d">out</a>`, ext, r.URL.Path, i)
		}
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&b, `<img src="%s/img%s/%d.png" alt="x">`, ext, r.URL.Path, i)
		}
		b.WriteString(`</body></html>`)
		fmt.Fprint(w, b.String())
	}))
	t.Cleanup(site.Close)
	return site
}

type eventLog struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (l *eventLog) add(e ProgressEvent) {
	if e.Type == "page" || e.Type == "progress" {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *eventLog) has(crawlID, typ string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		if e.CrawlID == crawlID && e.Type == typ {
			return true
		}
	}
	return false
}

func waitBackground(t *testing.T, m *CrawlManager, within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { m.WaitBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("post-crawl work still running %s after cancel", within)
	}
}

func postAction(t *testing.T, api http.Handler, id, action string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/"+id+"/"+action, nil))
	var got map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got
}

func startPostCrawlTest(t *testing.T, extra map[string]interface{}) (http.Handler, *UiStore, *CrawlManager, *slowServer, string, *eventLog) {
	t.Helper()
	api, s, m := newTestAPI(t)
	m.SkipSSRF = true
	slow := newSlowServer(t)
	site := postCrawlSite(t, slow.host())
	cfg := map[string]interface{}{"seedUrl": site.URL + "/", "limit": 20, "depth": 3, "concurrency": 2, "delay": 0}
	for k, v := range extra {
		cfg[k] = v
	}
	job, err := s.CreateCrawlJob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	events := &eventLog{}
	m.SetProgressCallback(events.add)
	if err := m.StartCrawl(job.ID, cfg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slow.first:
	case <-time.After(20 * time.Second):
		t.Fatal("post-crawl work never reached the slow server")
	}
	return api, s, m, slow, job.ID, events
}

func eventTypes(job *CrawlJob) []string {
	var out []string
	for _, e := range job.Events {
		out = append(out, e.Type)
	}
	return out
}

// The bug: once a crawl had fetched its pages, cancel answered "cancelled"
// (or "no active crawl") but the external link check went on for every
// external URL, holding hundreds of outbound connections until the app
// restarted. Cancel must stop it at once. The results shown already stay
// (job "completed"), with what was checked so far and a note.
func TestCancelStopsExternalCheckAfterResults(t *testing.T) {
	api, s, m, slow, id, events := startPostCrawlTest(t, map[string]interface{}{"checkExternal": true, "linkIntelligence": true})
	if m.GetCachedResults(id) == nil {
		t.Fatal("external check started before the results were cached")
	}
	code, body := postAction(t, api, id, "cancel")
	if code != http.StatusOK {
		t.Fatalf("cancel during external check: %d %v", code, body)
	}
	slow.quiet(t, 3*time.Second)
	waitBackground(t, m, 5*time.Second)

	job, _ := s.GetCrawlJob(id)
	if job.Status != "completed" {
		t.Errorf("status = %q, want completed (results were already shown)", job.Status)
	}
	if !strings.Contains(strings.Join(eventTypes(job), ","), "cancelled") {
		t.Errorf("no cancelled event: %v", eventTypes(job))
	}
	if !events.has(id, "analysis_complete") {
		t.Error("no analysis_complete event: the Results page would wait forever")
	}
	res := m.GetCachedResults(id)
	if res == nil {
		t.Fatal("results dropped")
	}
	res.mu.RLock()
	defer res.mu.RUnlock()
	if res.AnalysisPending {
		t.Error("analysis still pending")
	}
	if n, _ := res.Stats["externalLinksUnchecked"].(float64); n == 0 {
		t.Errorf("externalLinksUnchecked = %v, want the URLs left unchecked", res.Stats["externalLinksUnchecked"])
	}
	if note, _ := res.Stats["analysisInterrupted"].(string); !strings.Contains(note, "cancel") {
		t.Errorf("analysisInterrupted = %q, want a cancel note", note)
	}
	// Nothing left to cancel or stop.
	if code, _ := postAction(t, api, id, "cancel"); code != http.StatusBadRequest {
		t.Errorf("second cancel after the work ended: %d, want 400", code)
	}
}

// Stop & Analyze during the external check stops the network work and
// finishes the analysis (link intelligence) with what was fetched.
func TestStopDuringExternalCheckFinishesAnalysis(t *testing.T) {
	api, s, m, slow, id, events := startPostCrawlTest(t, map[string]interface{}{"checkExternal": true, "linkIntelligence": true})
	code, body := postAction(t, api, id, "stop")
	if code != http.StatusOK || body["status"] != "stopping" {
		t.Fatalf("stop during external check: %d %v", code, body)
	}
	slow.quiet(t, 3*time.Second)
	waitBackground(t, m, 5*time.Second)

	job, _ := s.GetCrawlJob(id)
	if job.Status != "completed" {
		t.Errorf("status = %q, want completed", job.Status)
	}
	if !events.has(id, "analysis_complete") {
		t.Error("no analysis_complete event")
	}
	res := m.GetCachedResults(id)
	if res == nil {
		t.Fatal("results dropped")
	}
	res.mu.RLock()
	defer res.mu.RUnlock()
	if res.Stats["linkIntelligenceStats"] == nil {
		t.Error("link intelligence did not run after stop")
	}
	if n, _ := res.Stats["externalLinksUnchecked"].(float64); n == 0 {
		t.Errorf("externalLinksUnchecked = %v, want the URLs left unchecked", res.Stats["externalLinksUnchecked"])
	}
	if note, _ := res.Stats["analysisInterrupted"].(string); !strings.Contains(note, "stop") {
		t.Errorf("analysisInterrupted = %q, want a stop note", note)
	}
}

// Page weight runs before the results exist (or, with background page
// weight, after): cancel must abort its resource requests either way, and
// the job must end in a clear state.
func TestCancelStopsPageWeight(t *testing.T) {
	api, s, m, slow, id, _ := startPostCrawlTest(t, map[string]interface{}{"pageWeight": true})
	code, body := postAction(t, api, id, "cancel")
	if code != http.StatusOK {
		t.Fatalf("cancel during page weight: %d %v", code, body)
	}
	slow.quiet(t, 3*time.Second)
	waitBackground(t, m, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for m.GetStatus() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a := m.GetStatus(); a != nil {
		t.Fatalf("crawl slot still held by %s", a.ID)
	}
	job, _ := s.GetCrawlJob(id)
	switch job.Status {
	case "cancelled":
		if m.GetCachedResults(id) != nil {
			t.Error("cancelled before results, yet results were cached")
		}
	case "completed":
		if m.GetCachedResults(id) == nil {
			t.Error("completed without results")
		}
	default:
		t.Errorf("status = %q, want cancelled or completed", job.Status)
	}
	if m.IsAnalyzing(id) {
		t.Error("still marked analyzing")
	}
}

// Stop & Analyze while page weight sizes resources: the requests stop, the
// analysis finishes and the results say page weight is partial.
func TestStopDuringPageWeightFinishesAnalysis(t *testing.T) {
	api, s, m, slow, id, _ := startPostCrawlTest(t, map[string]interface{}{"pageWeight": true})
	code, body := postAction(t, api, id, "stop")
	if code != http.StatusOK || body["status"] != "stopping" {
		t.Fatalf("stop during page weight: %d %v", code, body)
	}
	slow.quiet(t, 3*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for m.GetCachedResults(id) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	waitBackground(t, m, 5*time.Second)
	job, _ := s.GetCrawlJob(id)
	if job.Status != "completed" {
		t.Errorf("status = %q, want completed", job.Status)
	}
	res := m.GetCachedResults(id)
	if res == nil {
		t.Fatal("no results after stop")
	}
	res.mu.RLock()
	defer res.mu.RUnlock()
	if note, _ := res.Stats["analysisInterrupted"].(string); note == "" {
		t.Error("no note that page weight was cut short")
	}
}

// Cancel while the core analysis runs (results not cached yet): no results
// are cached, the job ends "cancelled", no "complete" event, slot free.
func TestCancelDuringCoreAnalysis(t *testing.T) {
	api, s, m := newTestAPI(t)
	m.SkipSSRF = true
	site := postCrawlSite(t, "http://localhost:1")
	cfg := map[string]interface{}{"seedUrl": site.URL + "/", "limit": 5, "depth": 2, "concurrency": 1, "delay": 0}
	job, _ := s.CreateCrawlJob(cfg)
	events := &eventLog{}
	m.SetProgressCallback(events.add)
	cancelled := make(chan [2]any, 1)
	m.testBeforeCache = func(id string) {
		code, body := postAction(t, api, id, "cancel")
		cancelled <- [2]any{code, body}
	}
	if err := m.StartCrawl(job.ID, cfg); err != nil {
		t.Fatal(err)
	}
	var got [2]any
	select {
	case got = <-cancelled:
	case <-time.After(20 * time.Second):
		t.Fatal("analysis never reached the cache step")
	}
	if got[0] != http.StatusOK {
		t.Fatalf("cancel during core analysis: %v %v", got[0], got[1])
	}
	waitBackground(t, m, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for m.GetStatus() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := s.GetCrawlJob(job.ID)
	if j.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", j.Status)
	}
	if m.GetCachedResults(job.ID) != nil {
		t.Error("results cached after cancel")
	}
	if events.has(job.ID, "complete") {
		t.Error("complete emitted after cancel")
	}
	if !events.has(job.ID, "cancelled") {
		t.Error("no cancelled event")
	}
	if m.IsAnalyzing(job.ID) || m.GetStatus() != nil {
		t.Error("crawl still analyzing or holding the slot")
	}
}
