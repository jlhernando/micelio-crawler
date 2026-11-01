package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/micelio/micelio/internal/server"
)

// Secrets that must never appear in any tool output.
const (
	testToken  = "tok-dashboard-7f3a9c2e51"
	fakeAPIKey = "AIzaFAKE-psi-key-0123456789abcdef"
)

// TestMain points HOME at a temp dir so the dashboard store and crawl DBs
// never touch the real ~/.micelio.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "micelio-mcp-test-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	fx.close()
	os.RemoveAll(home)
	os.Exit(code)
}

// newSite serves a tiny site with one page of each kind the tools explain.
//
//	/          home, links to everything below
//	/a         indexable, long title
//	/b         no meta description, 300-char title
//	/noindex   meta robots noindex
//	/missing   404
//	/old       301 -> /a
//	/c         canonical -> /a
//	/private   blocked by robots.txt
func newSite() *httptest.Server {
	page := func(w http.ResponseWriter, title, head, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><title>%s</title>%s</head><body><h1>%s</h1>%s<p>%s</p></body></html>`,
			title, head, title, body, strings.Repeat("Plain words about the test site. ", 60))
	}
	desc := `<meta name="description" content="A page of the Micelio MCP test site.">`
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
	})
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Home of the Micelio MCP test site", desc,
			`<a href="/a">A</a> <a href="/b">B</a> <a href="/noindex">N</a> <a href="/missing">M</a>
			 <a href="/old">O</a> <a href="/c">C</a> <a href="/private">P</a>`)
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Page A has a rather long title that goes well past sixty characters", desc,
			`<a href="/">Home</a> <a href="/b">B</a>`)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		page(w, strings.Repeat("Bee ", 75), "", `<a href="/a">A</a>`)
	})
	mux.HandleFunc("/noindex", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Hidden page that should stay out of search", desc+`<meta name="robots" content="noindex, follow">`,
			`<a href="/">Home</a>`)
	})
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Page C is a copy of page A for canonical tests", desc+`<link rel="canonical" href="http://`+r.Host+`/a">`,
			`<a href="/a">A</a>`)
	})
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Private page", desc, "")
	})
	mux.HandleFunc("/", http.NotFound)
	return httptest.NewServer(mux)
}

// fixture is a real dashboard (the production API handler) with one
// completed crawl of the test site.
type fixture struct {
	once    sync.Once
	err     error
	site    *httptest.Server
	dash    *httptest.Server
	store   *server.UiStore
	manager *server.CrawlManager
	crawlID string
}

var fx fixture

func (f *fixture) close() {
	if f.manager != nil {
		f.manager.WaitBackground()
		f.manager.Close()
	}
	if f.dash != nil {
		f.dash.Close()
	}
	if f.site != nil {
		f.site.Close()
	}
	if f.store != nil {
		f.store.Close()
	}
}

// requireBearer rejects requests without the test token, like `micelio ui
// --auth-token`.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// realDashboard returns the shared fixture, crawling the test site once.
func realDashboard(t *testing.T) *fixture {
	t.Helper()
	fx.once.Do(func() { fx.err = fx.setup() })
	if fx.err != nil {
		t.Fatalf("fixture: %v", fx.err)
	}
	return &fx
}

func (f *fixture) setup() error {
	store, err := server.OpenUiStore()
	if err != nil {
		return err
	}
	f.store = store
	f.manager = server.NewCrawlManager(store)
	f.manager.SkipSSRF = true // the test site is on 127.0.0.1
	api := server.CreateAPIHandler(store, f.manager, nil)
	f.dash = httptest.NewServer(requireBearer(testToken, api))
	f.site = newSite()

	// A secret in Settings: no tool may ever return it.
	if _, err := f.api("PUT", "/api/settings", map[string]any{"psiKey": fakeAPIKey}); err != nil {
		return err
	}
	body, err := f.api("POST", "/api/crawl/start", map[string]any{
		"mode": "spider", "seedUrl": f.site.URL + "/", "depth": 5, "limit": 50,
		"concurrency": 2, "respectRobots": true, "showBlockedInternal": true,
	})
	if err != nil {
		return err
	}
	var started struct{ ID string }
	json.Unmarshal(body, &started)
	f.crawlID = started.ID
	if err := f.waitDone(f.crawlID, 60*time.Second); err != nil {
		return err
	}
	f.manager.WaitBackground()
	return nil
}

// api calls the dashboard directly (not through MCP).
func (f *fixture) api(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.dash.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("%s %s: %s %s", method, path, resp.Status, out)
	}
	return out, nil
}

func (f *fixture) waitDone(id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := f.store.GetCrawlJob(id)
		if err != nil {
			return err
		}
		if job != nil && (job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled") {
			if job.Status != "completed" {
				return fmt.Errorf("crawl %s ended %s", id, job.Status)
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("crawl %s not done after %s", id, timeout)
}

func (f *fixture) url(path string) string { return f.site.URL + path }

// stubDashboard records what the MCP server sends, for tests that must not
// start real crawls (caps, auth, errors).
type stubDashboard struct {
	srv     *httptest.Server
	mu      sync.Mutex
	auth    []string
	starts  []map[string]any
	stops   []string
	queries []url.Values
	crawls  []map[string]any
}

func newStubDashboard(t *testing.T) *stubDashboard {
	t.Helper()
	s := &stubDashboard{crawls: []map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/crawls", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, map[string]any{"crawls": s.crawls, "total": len(s.crawls)})
	})
	mux.HandleFunc("POST /api/crawl/start", func(w http.ResponseWriter, r *http.Request) {
		var cfg map[string]any
		json.NewDecoder(r.Body).Decode(&cfg)
		s.mu.Lock()
		s.starts = append(s.starts, cfg)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"id": "stub-crawl-1", "status": "running"})
	})
	mux.HandleFunc("POST /api/crawl/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.stops = append(s.stops, r.PathValue("id"))
		s.mu.Unlock()
		writeJSON(w, map[string]any{"status": "stopping"})
	})
	mux.HandleFunc("GET /api/crawl/{id}/pages", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.queries = append(s.queries, r.URL.Query())
		s.mu.Unlock()
		writeJSON(w, map[string]any{"pages": []any{}, "total": 0, "totalAll": 0})
	})
	mux.HandleFunc("GET /api/crawl/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		// An upstream error that quotes the caller's credentials.
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": "proxy refused Authorization: Bearer " + testToken + " (key=" + fakeAPIKey + ")"})
	})
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// connect starts the MCP server on in-memory transports and returns a client
// session. Every tool output seen by the session is kept in outputs.
func connect(t *testing.T, base string, opts Options, copts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	if opts.PollInterval == 0 {
		opts.PollInterval = 200 * time.Millisecond
	}
	srv := New(NewHTTPBackend(base, testToken), opts)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, copts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Wait()
	})
	return cs
}

var (
	outputsMu sync.Mutex
	outputs   []string // every tool result text, checked for secrets
)

// call runs a tool and returns its result and text content.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	return callWith(t, cs, &mcp.CallToolParams{Name: name, Arguments: args})
}

func callWith(t *testing.T, cs *mcp.ClientSession, params *mcp.CallToolParams) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), params)
	if err != nil {
		t.Fatalf("%s: protocol error: %v", params.Name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		sb.WriteString("\n")
		sb.Write(b)
	}
	outputsMu.Lock()
	outputs = append(outputs, sb.String())
	outputsMu.Unlock()
	return res, sb.String()
}

// callOK runs a tool that must succeed and decodes its JSON output.
func callOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, text := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s(%v): tool error: %s", name, args, text)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: no content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: first content is %T", name, res.Content[0])
	}
	if out != nil {
		if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
			t.Fatalf("%s: decode %q: %v", name, tc.Text, err)
		}
	}
	return tc.Text
}

// callErr runs a tool that must fail with a tool error (not a protocol error).
func callErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, text := call(t, cs, name, args)
	if !res.IsError {
		t.Fatalf("%s(%v): want tool error, got %s", name, args, text)
	}
	return text
}

// jsonUnmarshalFirst decodes the first text content of a tool result.
func jsonUnmarshalFirst(res *mcp.CallToolResult, out any) error {
	if len(res.Content) == 0 {
		return fmt.Errorf("no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return fmt.Errorf("content is %T", res.Content[0])
	}
	return json.Unmarshal([]byte(tc.Text), out)
}
