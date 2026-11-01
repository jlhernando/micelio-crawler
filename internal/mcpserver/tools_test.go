package mcpserver

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	readTools  = []string{"crawl_status", "crawl_summary", "crawls_list", "pages_query", "url_explain", "url_links"}
	crawlTools = []string{"crawl_start", "crawl_stop", "crawl_wait"}
)

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func names(tools map[string]*mcp.Tool) []string {
	var out []string
	for n := range tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestToolListReadOnlyByDefault(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{}, nil)
	tools := listTools(t, cs)
	if got := names(tools); !slices.Equal(got, readTools) {
		t.Fatalf("tools = %v, want %v", got, readTools)
	}
	for _, n := range readTools {
		tool := tools[n]
		if tool.Description == "" {
			t.Errorf("%s: no description", n)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: want ReadOnlyHint", n)
		}
	}
}

func TestCrawlToolsOnlyWithAllowCrawl(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{AllowCrawl: true}, nil)
	tools := listTools(t, cs)
	want := append(slices.Clone(readTools), crawlTools...)
	sort.Strings(want)
	if got := names(tools); !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	start := tools["crawl_start"].Annotations
	if start == nil || start.ReadOnlyHint || start.OpenWorldHint == nil || !*start.OpenWorldHint ||
		start.DestructiveHint == nil || *start.DestructiveHint {
		t.Errorf("crawl_start annotations = %+v", start)
	}
	stop := tools["crawl_stop"].Annotations
	if stop == nil || stop.DestructiveHint == nil || *stop.DestructiveHint {
		t.Errorf("crawl_stop annotations = %+v", stop)
	}
	if wait := tools["crawl_wait"].Annotations; wait == nil || !wait.ReadOnlyHint {
		t.Errorf("crawl_wait annotations = %+v", wait)
	}
}

func TestCrawlsList(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)

	var got struct {
		Total  int              `json:"total"`
		Crawls []map[string]any `json:"crawls"`
	}
	callOK(t, cs, "crawls_list", nil, &got)
	if got.Total < 1 || len(got.Crawls) < 1 {
		t.Fatalf("crawls_list = %+v", got)
	}
	var row map[string]any
	for _, c := range got.Crawls {
		if c["id"] == f.crawlID {
			row = c
		}
	}
	if row == nil {
		t.Fatalf("fixture crawl %s not listed: %+v", f.crawlID, got.Crawls)
	}
	for _, k := range []string{"id", "seedUrl", "status", "startedAt", "pages", "errors"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row missing %q: %v", k, row)
		}
	}
	if _, ok := row["config"]; ok {
		t.Error("crawls_list must not return the crawl config")
	}
	if row["status"] != "completed" || row["pages"].(float64) < 5 {
		t.Errorf("row = %v", row)
	}

	// Filters: a site that matches nothing, and a status.
	callOK(t, cs, "crawls_list", map[string]any{"site": "no-such-site.example"}, &got)
	if len(got.Crawls) != 0 {
		t.Errorf("site filter kept %d crawls", len(got.Crawls))
	}
	callOK(t, cs, "crawls_list", map[string]any{"site": "127.0.0.1", "status": "completed", "limit": 1}, &got)
	if len(got.Crawls) != 1 {
		t.Errorf("limit 1 returned %d crawls", len(got.Crawls))
	}
}

func TestCrawlStatus(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)
	var got map[string]any
	callOK(t, cs, "crawl_status", map[string]any{"id": f.crawlID}, &got)
	if got["status"] != "completed" || got["done"] != true {
		t.Errorf("status = %v", got)
	}
	for _, k := range []string{"id", "seedUrl", "pages", "errors", "queued", "analysisPending", "phase"} {
		if _, ok := got[k]; !ok {
			t.Errorf("crawl_status missing %q: %v", k, got)
		}
	}
	if _, ok := got["config"]; ok {
		t.Error("crawl_status must not return the crawl config")
	}
	if msg := callErr(t, cs, "crawl_status", map[string]any{"id": "no-such-crawl"}); !strings.Contains(msg, "not found") {
		t.Errorf("unknown crawl error = %q", msg)
	}
}

func TestCrawlSummary(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)
	var got struct {
		ID     string `json:"id"`
		Totals struct {
			Pages     int `json:"pages"`
			Indexable int `json:"indexable"`
		} `json:"totals"`
		StatusCodes  map[string]int `json:"statusCodes"`
		Indexability struct {
			Indexable    int            `json:"indexable"`
			NonIndexable int            `json:"nonIndexable"`
			Reasons      map[string]int `json:"reasons"`
		} `json:"indexability"`
		Issues []struct {
			ID      string   `json:"id"`
			Label   string   `json:"label"`
			Count   int      `json:"count"`
			Samples []string `json:"samples"`
		} `json:"issues"`
		Soft404Candidates *int `json:"soft404Candidates"`
	}
	text := callOK(t, cs, "crawl_summary", map[string]any{"id": f.crawlID}, &got)
	if len(text) > 4096 {
		t.Errorf("crawl_summary is %d bytes, want <= 4096", len(text))
	}
	if got.ID != f.crawlID || got.Totals.Pages < 5 {
		t.Errorf("totals = %+v", got)
	}
	if got.StatusCodes["200"] < 3 || got.StatusCodes["404"] < 1 {
		t.Errorf("statusCodes = %v", got.StatusCodes)
	}
	if got.Indexability.Indexable < 1 || got.Indexability.NonIndexable < 1 {
		t.Errorf("indexability = %+v", got.Indexability)
	}
	if len(got.Issues) == 0 || len(got.Issues) > 10 {
		t.Fatalf("issues = %+v", got.Issues)
	}
	for i, is := range got.Issues {
		if is.ID == "" || is.Label == "" || is.Count < 1 || len(is.Samples) > 3 {
			t.Errorf("issue %+v", is)
		}
		if i > 0 && is.Count > got.Issues[i-1].Count {
			t.Errorf("issues not sorted by count: %+v", got.Issues)
		}
	}
	// The 300-char title on /b must show up as too long, with /b as sample.
	var long bool
	for _, is := range got.Issues {
		if is.ID == "title-too-long" {
			long = slices.Contains(is.Samples, f.url("/b"))
		}
	}
	if !long {
		t.Errorf("title-too-long with /b sample missing: %+v", got.Issues)
	}
	if got.Soft404Candidates == nil {
		t.Error("soft404Candidates missing")
	}
}

func TestPagesQuery(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)

	var got struct {
		Total int              `json:"total"`
		Rows  []map[string]any `json:"rows"`
	}
	callOK(t, cs, "pages_query", map[string]any{"id": f.crawlID}, &got)
	if got.Total < 5 || len(got.Rows) != got.Total {
		t.Fatalf("total %d, rows %d", got.Total, len(got.Rows))
	}
	defaults := []string{"depth", "indexable", "inlinks", "status", "title", "url"}
	for _, r := range got.Rows {
		var keys []string
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if !slices.Equal(keys, defaults) {
			t.Fatalf("default row keys = %v, want %v", keys, defaults)
		}
		if title, _ := r["title"].(string); len([]rune(title)) > 201 {
			t.Errorf("title not truncated: %d runes", len([]rune(title)))
		}
	}

	// Field selection and a filter.
	got.Rows = nil // json reuses maps in a slice it decodes into
	callOK(t, cs, "pages_query", map[string]any{"id": f.crawlID, "status": "4xx", "fields": []string{"url", "status"}}, &got)
	if got.Total != 1 || len(got.Rows) != 1 || got.Rows[0]["url"] != f.url("/missing") || len(got.Rows[0]) != 2 {
		t.Errorf("4xx rows = %+v", got.Rows)
	}
	got.Rows = nil
	callOK(t, cs, "pages_query", map[string]any{"id": f.crawlID, "indexable": "no", "fields": []string{"url", "indexabilityReason"}}, &got)
	if got.Total < 2 {
		t.Errorf("non-indexable rows = %+v", got.Rows)
	}
	got.Rows = nil
	callOK(t, cs, "pages_query", map[string]any{"id": f.crawlID, "limit": 2, "sort": "url"}, &got)
	if len(got.Rows) != 2 || got.Total < 5 {
		t.Errorf("limit 2: total %d rows %d", got.Total, len(got.Rows))
	}
	if msg := callErr(t, cs, "pages_query", map[string]any{"id": f.crawlID, "fields": []string{"url", "sourceHtml"}}); !strings.Contains(msg, "sourceHtml") {
		t.Errorf("unknown field error = %q", msg)
	}
}

// The agent cannot ask the dashboard for more than 200 rows.
func TestPagesQueryLimitCap(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{}, nil)
	callOK(t, cs, "pages_query", map[string]any{"id": "x", "limit": 5000}, nil)
	callOK(t, cs, "pages_query", map[string]any{"id": "x"}, nil)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.queries) != 2 {
		t.Fatalf("queries = %v", stub.queries)
	}
	if l := stub.queries[0].Get("limit"); l != "200" {
		t.Errorf("limit sent = %s, want 200", l)
	}
	if l := stub.queries[1].Get("limit"); l != "25" {
		t.Errorf("default limit sent = %s, want 25", l)
	}
}

func TestURLExplain(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)

	type explain struct {
		URL        string   `json:"url"`
		Indexable  bool     `json:"indexable"`
		Reasons    []string `json:"reasons"`
		StatusCode int      `json:"statusCode"`
		MetaRobots string   `json:"metaRobots"`
		Depth      int      `json:"depth"`
		Inlinks    int      `json:"inlinks"`
	}
	var a explain
	callOK(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/a")}, &a)
	if !a.Indexable || a.StatusCode != 200 || a.Inlinks < 1 {
		t.Errorf("/a = %+v", a)
	}

	var missing explain
	callOK(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/missing")}, &missing)
	if missing.Indexable || missing.StatusCode != 404 || !anyContains(missing.Reasons, "404") {
		t.Errorf("/missing = %+v", missing)
	}

	var noindex explain
	callOK(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/noindex")}, &noindex)
	if noindex.Indexable || !strings.Contains(noindex.MetaRobots, "noindex") || !anyContains(noindex.Reasons, "noindex") {
		t.Errorf("/noindex = %+v", noindex)
	}

	var canon explain
	callOK(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/c")}, &canon)
	if !anyContains(canon.Reasons, "canonical") {
		t.Errorf("/c reasons = %v", canon.Reasons)
	}

	// A redirect is not indexed itself, whatever its target is.
	var old explain
	callOK(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/old")}, &old)
	if old.Indexable || !anyContains(old.Reasons, "301") || !anyContains(old.Reasons, f.url("/a")) {
		t.Errorf("/old = %+v", old)
	}

	if msg := callErr(t, cs, "url_explain", map[string]any{"id": f.crawlID, "url": f.url("/never-crawled")}); !strings.Contains(msg, "not") {
		t.Errorf("unknown url error = %q", msg)
	}
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(strings.ToLower(s), strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func TestURLLinks(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{}, nil)
	var got struct {
		URL       string   `json:"url"`
		Direction string   `json:"direction"`
		Total     int      `json:"total"`
		Links     []string `json:"links"`
	}
	callOK(t, cs, "url_links", map[string]any{"id": f.crawlID, "url": f.url("/a"), "direction": "in"}, &got)
	if got.Direction != "in" || !slices.Contains(got.Links, f.url("/")) || !slices.Contains(got.Links, f.url("/b")) {
		t.Errorf("inlinks(/a) = %+v", got)
	}
	callOK(t, cs, "url_links", map[string]any{"id": f.crawlID, "url": f.url("/"), "direction": "out"}, &got)
	if got.Direction != "out" || !slices.Contains(got.Links, f.url("/a")) || !slices.Contains(got.Links, f.url("/missing")) {
		t.Errorf("outlinks(/) = %+v", got)
	}
	callOK(t, cs, "url_links", map[string]any{"id": f.crawlID, "url": f.url("/"), "direction": "out", "limit": 2}, &got)
	if len(got.Links) != 2 || got.Total < 6 {
		t.Errorf("limit 2: total %d links %v", got.Total, got.Links)
	}
	callErr(t, cs, "url_links", map[string]any{"id": f.crawlID, "url": f.url("/"), "direction": "sideways"})
}

// Whatever the agent asks for, the dashboard gets the safe caps.
func TestCrawlStartCaps(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{AllowCrawl: true}, nil)
	var got map[string]any
	callOK(t, cs, "crawl_start", map[string]any{
		"url": "https://example.com/", "limit": 1000000, "concurrency": 50, "delayMs": 0, "depth": 3,
	}, &got)
	if got["id"] != "stub-crawl-1" {
		t.Errorf("crawl_start = %v", got)
	}
	callOK(t, cs, "crawl_start", map[string]any{"url": "https://example.com/"}, nil)
	callOK(t, cs, "crawl_start", map[string]any{"mode": "list", "urls": []string{"https://example.com/x", "https://example.com/y"}}, nil)

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.starts) != 3 {
		t.Fatalf("starts = %v", stub.starts)
	}
	capped, defaults, list := stub.starts[0], stub.starts[1], stub.starts[2]
	check := func(name string, cfg map[string]any, key string, want any) {
		t.Helper()
		if cfg[key] != want {
			t.Errorf("%s: %s = %v, want %v", name, key, cfg[key], want)
		}
	}
	check("capped", capped, "limit", float64(10000))
	check("capped", capped, "concurrency", float64(5))
	check("capped", capped, "delay", float64(500))
	check("capped", capped, "depth", float64(3))
	check("capped", capped, "seedUrl", "https://example.com/")
	check("capped", capped, "mode", "spider")
	check("capped", capped, "respectRobots", true)
	check("capped", capped, "jsRendering", false)
	check("capped", capped, "adaptiveRate", true)
	check("defaults", defaults, "limit", float64(500))
	check("defaults", defaults, "concurrency", float64(3))
	check("defaults", defaults, "delay", float64(1000))
	check("list", list, "mode", "list")
	check("list", list, "urlList", "https://example.com/x\nhttps://example.com/y")

	for _, bad := range []map[string]any{
		{"url": "ftp://example.com/"},
		{"url": "javascript:alert(1)"},
		{"mode": "list"},
		{"mode": "teleport", "url": "https://example.com/"},
	} {
		res, text := call(t, cs, "crawl_start", bad)
		if !res.IsError {
			t.Errorf("crawl_start(%v) accepted: %s", bad, text)
		}
	}
}

func TestCrawlStartWhileRunning(t *testing.T) {
	stub := newStubDashboard(t)
	stub.crawls = []map[string]any{{"id": "busy-42", "status": "running", "seedUrl": "https://busy.example/"}}
	cs := connect(t, stub.srv.URL, Options{AllowCrawl: true}, nil)
	msg := callErr(t, cs, "crawl_start", map[string]any{"url": "https://example.com/"})
	if !strings.Contains(msg, "busy-42") {
		t.Errorf("error = %q, want the running crawl's id", msg)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.starts) != 0 {
		t.Error("crawl_start called the dashboard while a crawl was running")
	}
}

func TestCrawlStop(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{AllowCrawl: true}, nil)
	var got map[string]any
	callOK(t, cs, "crawl_stop", map[string]any{"id": "abc"}, &got)
	if got["status"] != "stopping" {
		t.Errorf("crawl_stop = %v", got)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if !slices.Equal(stub.stops, []string{"abc"}) {
		t.Errorf("stops = %v", stub.stops)
	}
}

func TestAuthTokenSent(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{}, nil)
	callOK(t, cs, "crawls_list", nil, nil)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.auth) == 0 || stub.auth[0] != "Bearer "+testToken {
		t.Errorf("Authorization headers = %v", stub.auth)
	}
}

// Dashboard errors come back as tool errors with credentials scrubbed.
func TestErrorsAreScrubbedToolErrors(t *testing.T) {
	stub := newStubDashboard(t)
	cs := connect(t, stub.srv.URL, Options{}, nil)
	msg := callErr(t, cs, "crawl_status", map[string]any{"id": "x"})
	if strings.Contains(msg, testToken) || strings.Contains(msg, fakeAPIKey) {
		t.Errorf("error leaks a secret: %q", msg)
	}
	if !strings.Contains(msg, "proxy refused") {
		t.Errorf("error lost its message: %q", msg)
	}

	// Missing required arguments are tool errors too.
	callErr(t, cs, "url_explain", map[string]any{"id": "x"})
}

func TestScrub(t *testing.T) {
	for in, banned := range map[string]string{
		"Authorization: Bearer abc.def-123":     "abc.def-123",
		"GET /x?token=s3cr3t-value&a=1":         "s3cr3t-value",
		"api_key=AIzaSyD-very-secret-key-value": "AIzaSyD",
		"key: sk-ant-api03-abcdefghijklmnop":    "sk-ant-api03",
	} {
		if out := scrub(in); strings.Contains(out, banned) {
			t.Errorf("scrub(%q) = %q", in, out)
		}
	}
	if out := scrub("crawl not found"); out != "crawl not found" {
		t.Errorf("scrub changed a plain message: %q", out)
	}
}

// Run every read tool against the real dashboard (which has a secret in
// Settings) and check no output ever contains a secret.
func TestNoSecretsInOutputs(t *testing.T) {
	f := realDashboard(t)
	cs := connect(t, f.dash.URL, Options{AllowCrawl: true}, nil)
	id := f.crawlID
	call(t, cs, "crawls_list", nil)
	call(t, cs, "crawl_status", map[string]any{"id": id})
	call(t, cs, "crawl_summary", map[string]any{"id": id})
	call(t, cs, "pages_query", map[string]any{"id": id, "fields": allFieldNames()})
	call(t, cs, "url_explain", map[string]any{"id": id, "url": f.url("/")})
	call(t, cs, "url_links", map[string]any{"id": id, "url": f.url("/a"), "direction": "in"})
	call(t, cs, "crawl_wait", map[string]any{"id": id, "timeoutSec": 1})

	outputsMu.Lock()
	defer outputsMu.Unlock()
	if len(outputs) == 0 {
		t.Fatal("no outputs recorded")
	}
	for _, out := range outputs {
		if strings.Contains(out, fakeAPIKey) || strings.Contains(out, testToken) {
			t.Fatalf("secret in tool output: %s", out)
		}
		if strings.Contains(out, "<html") || strings.Contains(out, "<body") {
			t.Fatalf("raw HTML in tool output: %.300s", out)
		}
	}
}

// crawl_start -> crawl_wait -> crawl_summary on the local site, through the
// real dashboard.
func TestEndToEndCrawl(t *testing.T) {
	f := realDashboard(t)
	var mu sync.Mutex
	var progress []float64
	cs := connect(t, f.dash.URL, Options{AllowCrawl: true}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			progress = append(progress, req.Params.Progress)
			mu.Unlock()
		},
	})

	var started struct {
		ID string `json:"id"`
	}
	callOK(t, cs, "crawl_start", map[string]any{"url": f.url("/"), "limit": 20, "concurrency": 2}, &started)
	if started.ID == "" {
		t.Fatal("no crawl id")
	}

	var waited struct {
		Status string `json:"status"`
		Done   bool   `json:"done"`
		Pages  int    `json:"pages"`
	}
	deadline := time.Now().Add(90 * time.Second)
	for !waited.Done && time.Now().Before(deadline) {
		params := &mcp.CallToolParams{Name: "crawl_wait", Arguments: map[string]any{"id": started.ID, "timeoutSec": 20}}
		params.SetProgressToken("wait-1")
		res, text := callWith(t, cs, params)
		if res.IsError {
			t.Fatalf("crawl_wait: %s", text)
		}
		if err := jsonUnmarshalFirst(res, &waited); err != nil {
			t.Fatal(err)
		}
	}
	if !waited.Done || waited.Status != "completed" || waited.Pages < 5 {
		t.Fatalf("crawl_wait = %+v", waited)
	}
	mu.Lock()
	n := len(progress)
	mu.Unlock()
	if n == 0 {
		t.Error("no progress notifications")
	}
	f.manager.WaitBackground()

	var sum struct {
		Totals struct {
			Pages int `json:"pages"`
		} `json:"totals"`
	}
	callOK(t, cs, "crawl_summary", map[string]any{"id": started.ID}, &sum)
	if sum.Totals.Pages < 5 {
		t.Errorf("summary pages = %d", sum.Totals.Pages)
	}
}
