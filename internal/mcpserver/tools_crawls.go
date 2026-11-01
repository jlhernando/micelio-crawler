package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// crawlJob is the part of a dashboard crawl job the tools show. The job's
// config is never read into it: it can hold API keys.
type crawlJob struct {
	ID          string         `json:"id"`
	SeedURL     string         `json:"seedUrl"`
	Status      string         `json:"status"`
	Mode        string         `json:"mode"`
	StartedAt   string         `json:"startedAt"`
	CompletedAt *string        `json:"completedAt"`
	DurationMs  *int64         `json:"durationMs"`
	PageCount   int            `json:"pageCount"`
	ErrorCount  int            `json:"errorCount"`
	StatusCodes map[string]int `json:"statusCodes"` // live, while running
	Queued      int            `json:"queued"`
	TotalSeen   int            `json:"totalSeen"`
	Analyzing   bool           `json:"analyzing"` // completed, results not cached yet
}

// finished reports whether a crawl will not change any more.
func finished(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// active reports whether a crawl holds the dashboard's single crawl slot.
func active(status string) bool {
	return status == "running" || status == "paused" || status == "stopping"
}

// ── crawls_list ──

type crawlsListIn struct {
	Site   string `json:"site,omitempty" jsonschema:"only crawls whose start URL contains this text, e.g. example.com"`
	Status string `json:"status,omitempty" jsonschema:"only crawls with this status: running, paused, completed, failed or cancelled"`
	Limit  int    `json:"limit,omitempty" jsonschema:"most crawls to return, newest first (default 20, max 100)"`
}

type crawlRow struct {
	ID          string `json:"id"`
	SeedURL     string `json:"seedUrl"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt,omitempty"`
	Pages       int    `json:"pages"`
	Errors      int    `json:"errors"`
}

type crawlsListOut struct {
	Total  int        `json:"total" jsonschema:"crawls matching the filters"`
	Crawls []crawlRow `json:"crawls"`
}

func (t *tools) listJobs(ctx context.Context) ([]crawlJob, error) {
	var resp struct {
		Crawls []crawlJob `json:"crawls"`
	}
	if err := t.get(ctx, "/api/crawls", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Crawls, nil
}

func (t *tools) crawlsList(ctx context.Context, _ *mcp.CallToolRequest, in crawlsListIn) (*mcp.CallToolResult, crawlsListOut, error) {
	jobs, err := t.listJobs(ctx)
	if err != nil {
		return nil, crawlsListOut{}, err
	}
	limit := clamp(in.Limit, 20, 1, 100)
	site := strings.ToLower(strings.TrimSpace(in.Site))
	out := crawlsListOut{Crawls: []crawlRow{}}
	for _, j := range jobs {
		if site != "" && !strings.Contains(strings.ToLower(j.SeedURL), site) {
			continue
		}
		if in.Status != "" && j.Status != in.Status {
			continue
		}
		out.Total++
		if len(out.Crawls) < limit {
			row := crawlRow{ID: j.ID, SeedURL: j.SeedURL, Status: j.Status, Mode: j.Mode,
				StartedAt: j.StartedAt, Pages: j.PageCount, Errors: j.ErrorCount}
			if j.CompletedAt != nil {
				row.CompletedAt = *j.CompletedAt
			}
			out.Crawls = append(out.Crawls, row)
		}
	}
	return nil, out, nil
}

// clamp returns v, or def when v is not set (<= 0), kept within [lo, hi].
func clamp(v, def, lo, hi int) int {
	if v <= 0 {
		v = def
	}
	return min(max(v, lo), hi)
}

// ── crawl_status ──

type idIn struct {
	ID string `json:"id" jsonschema:"crawl id (from crawls_list)"`
}

type crawlStatusOut struct {
	ID              string         `json:"id"`
	SeedURL         string         `json:"seedUrl"`
	Status          string         `json:"status" jsonschema:"pending, running, paused, stopping, completed, failed or cancelled"`
	Mode            string         `json:"mode"`
	Done            bool           `json:"done" jsonschema:"true once the crawl has ended (completed, failed or cancelled) and its results are ready"`
	Phase           string         `json:"phase" jsonschema:"what the crawl is doing now, in plain words"`
	StartedAt       string         `json:"startedAt"`
	CompletedAt     string         `json:"completedAt,omitempty"`
	DurationMs      int64          `json:"durationMs,omitempty"`
	Pages           int            `json:"pages" jsonschema:"pages crawled so far"`
	Errors          int            `json:"errors"`
	Queued          int            `json:"queued" jsonschema:"URLs waiting to be crawled (running crawls only)"`
	TotalSeen       int            `json:"totalSeen,omitempty"`
	StatusCodes     map[string]int `json:"statusCodes,omitempty" jsonschema:"pages per status class so far (running crawls only)"`
	Analyzing       bool           `json:"analyzing,omitempty" jsonschema:"true while the pages just crawled are analysed; results are not ready yet"`
	AnalysisPending bool           `json:"analysisPending" jsonschema:"true while slow analysis (link intelligence, external link checks) still runs after the crawl"`
	WaitedSec       float64        `json:"waitedSec,omitempty" jsonschema:"seconds crawl_wait waited"`
}

func (t *tools) status(ctx context.Context, id string) (crawlStatusOut, error) {
	if strings.TrimSpace(id) == "" {
		return crawlStatusOut{}, errors.New("id is required (use crawls_list to see crawl ids)")
	}
	var j crawlJob
	if err := t.get(ctx, crawlPath(id, "/status"), nil, &j); err != nil {
		return crawlStatusOut{}, noCrawl(id, err)
	}
	out := crawlStatusOut{
		ID: j.ID, SeedURL: j.SeedURL, Status: j.Status, Mode: j.Mode, Done: finished(j.Status) && !j.Analyzing,
		Analyzing: j.Analyzing,
		StartedAt: j.StartedAt, Pages: j.PageCount, Errors: j.ErrorCount,
		Queued: j.Queued, TotalSeen: j.TotalSeen, StatusCodes: j.StatusCodes,
	}
	if j.CompletedAt != nil {
		out.CompletedAt = *j.CompletedAt
	}
	if j.DurationMs != nil {
		out.DurationMs = *j.DurationMs
	}
	if j.Status == "completed" && !j.Analyzing {
		var st struct {
			AnalysisPending bool `json:"analysisPending"`
		}
		if t.get(ctx, crawlPath(id, "/stats"), nil, &st) == nil {
			out.AnalysisPending = st.AnalysisPending
		}
	}
	out.Phase = phase(out)
	return out, nil
}

func phase(s crawlStatusOut) string {
	switch {
	case s.Analyzing:
		return "crawl finished; analysing the pages, results not ready yet"
	case s.Status == "completed" && s.AnalysisPending:
		return "results ready; background analysis still running"
	case s.Status == "completed":
		return "done"
	case s.Status == "running":
		return "crawling"
	case s.Status == "stopping":
		return "stopping, then analysing the pages crawled so far"
	default:
		return s.Status
	}
}

func (t *tools) crawlStatus(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, crawlStatusOut, error) {
	out, err := t.status(ctx, in.ID)
	return nil, out, err
}

// ── crawl_start ──

// Limits an agent cannot raise.
const (
	defaultLimit       = 500
	maxLimit           = 10000
	defaultConcurrency = 3
	maxConcurrency     = 5
	defaultDelayMs     = 1000
	minDelayMs         = 500
	maxDelayMs         = 60000
	defaultDepth       = 10
	maxDepth           = 100
)

type crawlStartIn struct {
	URL         string   `json:"url,omitempty" jsonschema:"start URL (spider mode) or sitemap URL (sitemap mode); http or https"`
	Mode        string   `json:"mode,omitempty" jsonschema:"spider (follow links, default), list (crawl only the given urls) or sitemap"`
	URLs        []string `json:"urls,omitempty" jsonschema:"URLs to crawl in list mode, or sitemap URLs in sitemap mode"`
	Limit       int      `json:"limit,omitempty" jsonschema:"most pages to crawl (default 500, max 10000)"`
	Depth       int      `json:"depth,omitempty" jsonschema:"how many clicks from the start URL to follow (default 10, max 100)"`
	Concurrency int      `json:"concurrency,omitempty" jsonschema:"parallel requests (default 3, max 5)"`
	DelayMs     *int     `json:"delayMs,omitempty" jsonschema:"pause between requests to the site in ms (default 1000, min 500)"`
}

type crawlStartOut struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	SeedURL     string `json:"seedUrl,omitempty"`
	URLs        int    `json:"urls,omitempty" jsonschema:"number of URLs given in list or sitemap mode"`
	Limit       int    `json:"limit"`
	Depth       int    `json:"depth"`
	Concurrency int    `json:"concurrency"`
	DelayMs     int    `json:"delayMs"`
	// Adjusted lists every value changed to fit the caps, so the agent
	// knows the crawl is not exactly the one it asked for.
	Adjusted []string `json:"adjusted,omitempty" jsonschema:"values changed to fit the safety caps"`
	Message  string   `json:"message"`
}

// webURL checks that u is an absolute http(s) URL.
func webURL(u string) error {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", truncate(u, 100))
	}
	return nil
}

// startConfig builds the dashboard crawl config, with the MCP caps applied
// whatever the agent asked for.
func startConfig(in crawlStartIn) (map[string]any, crawlStartOut, error) {
	out := crawlStartOut{
		Mode:        in.Mode,
		Limit:       clamp(in.Limit, defaultLimit, 1, maxLimit),
		Depth:       clamp(in.Depth, defaultDepth, 1, maxDepth),
		Concurrency: clamp(in.Concurrency, defaultConcurrency, 1, maxConcurrency),
		DelayMs:     defaultDelayMs,
	}
	if in.DelayMs != nil {
		out.DelayMs = min(max(*in.DelayMs, minDelayMs), maxDelayMs)
	}
	adjust := func(name string, asked, got int, rule string) {
		if asked > 0 && asked != got {
			out.Adjusted = append(out.Adjusted, fmt.Sprintf("%s %d -> %d (%s)", name, asked, got, rule))
		}
	}
	adjust("limit", in.Limit, out.Limit, fmt.Sprintf("max %d", maxLimit))
	adjust("depth", in.Depth, out.Depth, fmt.Sprintf("max %d", maxDepth))
	adjust("concurrency", in.Concurrency, out.Concurrency, fmt.Sprintf("max %d", maxConcurrency))
	if in.DelayMs != nil && *in.DelayMs != out.DelayMs {
		out.Adjusted = append(out.Adjusted, fmt.Sprintf("delayMs %d -> %d (min %d, max %d)", *in.DelayMs, out.DelayMs, minDelayMs, maxDelayMs))
	}
	if out.Mode == "" {
		out.Mode = "spider"
	}
	cfg := map[string]any{
		"mode":                out.Mode,
		"limit":               out.Limit,
		"depth":               out.Depth,
		"concurrency":         out.Concurrency,
		"delay":               out.DelayMs,
		"respectRobots":       true,
		"jsRendering":         false,
		"adaptiveRate":        true,
		"showBlockedInternal": true,
		"checkExternal":       false,
	}
	urls := in.URLs
	switch out.Mode {
	case "spider":
		if err := webURL(in.URL); err != nil {
			return nil, out, fmt.Errorf("spider mode needs url: %w", err)
		}
		out.SeedURL = strings.TrimSpace(in.URL)
		cfg["seedUrl"] = out.SeedURL
		return cfg, out, nil
	case "list":
		if len(urls) == 0 {
			return nil, out, errors.New("list mode needs urls")
		}
		if len(urls) > out.Limit {
			out.Adjusted = append(out.Adjusted, fmt.Sprintf("urls %d -> %d (limit %d)", len(urls), out.Limit, out.Limit))
			urls = urls[:out.Limit]
		}
		if err := checkURLs(urls); err != nil {
			return nil, out, err
		}
		out.URLs = len(urls)
		cfg["urlList"] = strings.Join(urls, "\n")
		return cfg, out, nil
	case "sitemap":
		if in.URL != "" {
			urls = append([]string{in.URL}, urls...)
		}
		if len(urls) == 0 {
			return nil, out, errors.New("sitemap mode needs url (the sitemap) or urls")
		}
		if len(urls) > 50 {
			out.Adjusted = append(out.Adjusted, fmt.Sprintf("sitemaps %d -> 50 (max 50)", len(urls)))
			urls = urls[:50]
		}
		if err := checkURLs(urls); err != nil {
			return nil, out, err
		}
		out.URLs = len(urls)
		cfg["sitemapUrls"] = urls
		return cfg, out, nil
	default:
		return nil, out, fmt.Errorf("mode must be spider, list or sitemap, not %q", truncate(out.Mode, 30))
	}
}

func checkURLs(urls []string) error {
	for i, u := range urls {
		if err := webURL(u); err != nil {
			return err
		}
		urls[i] = strings.TrimSpace(u)
	}
	return nil
}

func (t *tools) crawlStart(ctx context.Context, _ *mcp.CallToolRequest, in crawlStartIn) (*mcp.CallToolResult, crawlStartOut, error) {
	cfg, out, err := startConfig(in)
	if err != nil {
		return nil, out, err
	}
	// The dashboard runs one crawl at a time.
	jobs, err := t.listJobs(ctx)
	if err != nil {
		return nil, out, err
	}
	for _, j := range jobs {
		if active(j.Status) {
			return nil, out, busyError(j.ID, j.SeedURL)
		}
	}
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := t.b.Do(ctx, "POST", "/api/crawl/start", nil, cfg, &resp); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && strings.HasPrefix(ae.Message, "crawl already running: ") {
			return nil, out, busyError(strings.TrimPrefix(ae.Message, "crawl already running: "), "")
		}
		return nil, out, err
	}
	out.ID, out.Status = resp.ID, resp.Status
	out.Message = "Crawl started. Call crawl_wait with this id until done is true, then crawl_summary."
	if len(out.Adjusted) > 0 {
		out.Message = "Crawl started with safety caps applied: " + strings.Join(out.Adjusted, "; ") +
			". Tell the user the crawl differs from what they asked. " + out.Message[len("Crawl started. "):]
	}
	return nil, out, nil
}

func busyError(id, seed string) error {
	if seed != "" {
		seed = " of " + seed
	}
	return fmt.Errorf("a crawl is already running (id %s%s); the dashboard runs one at a time. Wait for it with crawl_wait or stop it with crawl_stop", id, seed)
}

// ── crawl_wait ──

type crawlWaitIn struct {
	ID         string `json:"id" jsonschema:"crawl id (from crawl_start or crawls_list)"`
	TimeoutSec int    `json:"timeoutSec,omitempty" jsonschema:"most seconds to wait before returning (default 45, max 50); call again if done is false"`
}

func (t *tools) crawlWait(ctx context.Context, req *mcp.CallToolRequest, in crawlWaitIn) (*mcp.CallToolResult, crawlStatusOut, error) {
	timeout := time.Duration(clamp(in.TimeoutSec, 45, 1, 50)) * time.Second
	start := time.Now()
	var token any
	if req != nil && req.Params != nil {
		token = req.Params.GetProgressToken()
	}
	lastPages := -1
	for {
		st, err := t.status(ctx, in.ID)
		if err != nil {
			return nil, crawlStatusOut{}, err
		}
		if token != nil && st.Pages > lastPages && req.Session != nil {
			lastPages = st.Pages
			req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      float64(st.Pages),
				Message:       fmt.Sprintf("%d pages crawled, %d queued (%s)", st.Pages, st.Queued, st.Phase),
			})
		}
		if st.Done || time.Since(start)+t.opts.PollInterval > timeout {
			st.WaitedSec = time.Since(start).Round(100 * time.Millisecond).Seconds()
			return nil, st, nil
		}
		select {
		case <-ctx.Done():
			return nil, crawlStatusOut{}, ctx.Err()
		case <-time.After(t.opts.PollInterval):
		}
	}
}

// ── crawl_stop ──

type crawlStopOut struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (t *tools) crawlStop(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, crawlStopOut, error) {
	if strings.TrimSpace(in.ID) == "" {
		return nil, crawlStopOut{}, errors.New("id is required")
	}
	var resp struct {
		Status string `json:"status"`
	}
	if err := t.b.Do(ctx, "POST", crawlPath(in.ID, "/stop"), nil, nil, &resp); err != nil {
		return nil, crawlStopOut{}, err
	}
	return nil, crawlStopOut{ID: in.ID, Status: resp.Status,
		Message: "Stopping: no new pages are fetched and the pages crawled so far are analysed. Call crawl_wait until done is true."}, nil
}

func (t *tools) addCrawlTools(s *mcp.Server) {
	add(s, t, &mcp.Tool{
		Name: "crawl_start",
		Description: "Start a crawl in the Micelio dashboard and return its id at once. " +
			"Safety caps always apply: at most 10000 pages, 5 parallel requests, at least 500 ms between requests, robots.txt respected, no JavaScript rendering. " +
			"Only one crawl runs at a time.",
		Annotations: &mcp.ToolAnnotations{Title: "Start a crawl", OpenWorldHint: boolPtr(true), DestructiveHint: boolPtr(false)},
	}, t.crawlStart)
	add(s, t, &mcp.Tool{
		Name:        "crawl_wait",
		Description: "Wait (up to 50 s) for a crawl to finish, reporting progress. Returns its status and done=true when it has ended; call again while done is false.",
		Annotations: readOnly("Wait for a crawl"),
	}, t.crawlWait)
	add(s, t, &mcp.Tool{
		Name:        "crawl_stop",
		Description: "Stop a running crawl gracefully: no new pages are fetched and the pages crawled so far are analysed (Stop & Analyze). Nothing is deleted.",
		Annotations: &mcp.ToolAnnotations{Title: "Stop a crawl", OpenWorldHint: boolPtr(false), DestructiveHint: boolPtr(false)},
	}, t.crawlStop)
}
