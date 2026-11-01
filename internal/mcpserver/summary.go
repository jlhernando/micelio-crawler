package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxSummaryBytes keeps crawl_summary small enough for an agent's context.
const maxSummaryBytes = 4000

// summaryStats is the part of /stats crawl_summary reads. Other fields
// are left out on purpose to keep the summary small.
type summaryStats struct {
	TotalPages        int            `json:"totalPages"`
	StatusCodes       map[string]int `json:"statusCodes"`
	IndexabilityStats struct {
		Reasons      map[string]int `json:"reasons"`
		Indexable    int            `json:"indexable"`
		NonIndexable int            `json:"nonIndexable"`
	} `json:"indexabilityStats"`
	PagesWithoutTitle         int        `json:"pagesWithoutTitle"`
	PagesWithoutDescription   int        `json:"pagesWithoutDescription"`
	PagesWithoutH1            int        `json:"pagesWithoutH1"`
	MultipleH1Count           int        `json:"multipleH1Count"`
	TitleTooLongCount         int        `json:"titleTooLongCount"`
	TitleTooShortCount        int        `json:"titleTooShortCount"`
	DescriptionTooLongCount   int        `json:"descriptionTooLongCount"`
	DuplicateTitleCount       int        `json:"duplicateTitleCount"`
	DuplicateDescriptionCount int        `json:"duplicateDescriptionCount"`
	RobotsBlockedCount        int        `json:"robotsBlockedCount"`
	BrokenLinks               []urlItem  `json:"brokenLinks"`
	TotalBrokenLinks          int        `json:"totalBrokenLinks"`
	LongRedirectChains        []urlItem  `json:"longRedirectChains"`
	TotalLongRedirectChains   int        `json:"totalLongRedirectChains"`
	OrphanPages               []string   `json:"orphanPages"`
	TotalOrphanPages          int        `json:"totalOrphanPages"`
	ThinContentPages          []urlItem  `json:"thinContentPages"`
	TotalThinContentPages     int        `json:"totalThinContentPages"`
	NonHTTPSPages             []string   `json:"nonHttpsPages"`
	TotalNonHTTPSPages        int        `json:"totalNonHttpsPages"`
	MixedContentPages         []string   `json:"mixedContentPages"`
	BrokenExternalLinks       []urlItem  `json:"brokenExternalLinks"`
	DuplicateContentGroups    []struct{} `json:"duplicateContentGroups"`
	Soft404Stats              *struct {
		Total      int       `json:"total"`
		Candidates []urlItem `json:"candidates"`
	} `json:"soft404Stats"`
	Soft404Pages            []string `json:"soft404Pages"`
	ResponseTimePercentiles struct {
		P50 float64 `json:"p50"`
		P90 float64 `json:"p90"`
		P99 float64 `json:"p99"`
	} `json:"responseTimePercentiles"`
	TotalInternalLinks int   `json:"totalInternalLinks"`
	TotalExternalLinks int   `json:"totalExternalLinks"`
	TotalImages        int   `json:"totalImages"`
	ImagesMissingAlt   int   `json:"imagesMissingAlt"`
	CrawlDurationMs    int64 `json:"crawlDurationMs"`
}

type urlItem struct {
	URL string `json:"url"`
}

type summaryTotals struct {
	Pages            int   `json:"pages"`
	Indexable        int   `json:"indexable"`
	NonIndexable     int   `json:"nonIndexable"`
	InternalLinks    int   `json:"internalLinks"`
	ExternalLinks    int   `json:"externalLinks"`
	Images           int   `json:"images"`
	ImagesMissingAlt int   `json:"imagesMissingAlt"`
	CrawlDurationMs  int64 `json:"crawlDurationMs"`
}

type summaryIndexability struct {
	Indexable    int            `json:"indexable"`
	NonIndexable int            `json:"nonIndexable"`
	Reasons      map[string]int `json:"reasons,omitempty" jsonschema:"non-indexable pages per reason (top 8)"`
}

type summaryIssue struct {
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Count   int               `json:"count"`
	Samples []string          `json:"samples"`
	Filter  map[string]string `json:"filter,omitempty" jsonschema:"pass these to pages_query to list every affected page"`
}

type percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

type crawlSummaryOut struct {
	ID                string              `json:"id"`
	SeedURL           string              `json:"seedUrl"`
	Status            string              `json:"status"`
	AnalysisPending   bool                `json:"analysisPending" jsonschema:"true while link intelligence or external link checks still run; some numbers may change"`
	Totals            summaryTotals       `json:"totals"`
	StatusCodes       map[string]int      `json:"statusCodes" jsonschema:"pages per HTTP status code (0 = request failed)"`
	Indexability      summaryIndexability `json:"indexability"`
	Issues            []summaryIssue      `json:"issues" jsonschema:"top 10 issues, most pages first"`
	Soft404Candidates int                 `json:"soft404Candidates"`
	ResponseTimeMs    *percentiles        `json:"responseTimeMs,omitempty"`
}

// issueDef describes one issue: how to count it and where samples come from.
type issueDef struct {
	id, label string
	count     int
	filter    map[string]string // pages_query filter; samples come from /pages
	samples   []string          // samples straight from the stats
}

func listCount(total, n int) int { return max(total, n) }

func urls(items []urlItem) []string {
	out := make([]string, 0, min(len(items), 3))
	for _, it := range items[:min(len(items), 3)] {
		out = append(out, it.URL)
	}
	return out
}

func first3(s []string) []string { return s[:min(len(s), 3)] }

func issueDefs(s summaryStats) []issueDef {
	codes := func(lo, hi int) (n int) {
		for k, v := range s.StatusCodes {
			var c int
			fmt.Sscan(k, &c)
			if c >= lo && c < hi {
				n += v
			}
		}
		return n
	}
	issue := func(id, label string, count int) issueDef {
		return issueDef{id: id, label: label, count: count, filter: map[string]string{"issue": id}}
	}
	soft := 0
	var softSamples []string
	if s.Soft404Stats != nil {
		soft, softSamples = s.Soft404Stats.Total, urls(s.Soft404Stats.Candidates)
	}
	return []issueDef{
		{id: "4xx", label: "pages answering 4xx (not found, gone, forbidden)", count: codes(400, 500), filter: map[string]string{"status": "4xx"}},
		issue("5xx", "pages answering 5xx (server errors)", codes(500, 600)),
		{id: "non-indexable", label: "pages search engines will not index", count: s.IndexabilityStats.NonIndexable, filter: map[string]string{"indexable": "no"}},
		{id: "robots-blocked", label: "internal URLs blocked by robots.txt", count: s.RobotsBlockedCount, filter: map[string]string{"robotsBlocked": "yes"}},
		issue("missing-title", "pages without a title", s.PagesWithoutTitle),
		issue("missing-description", "pages without a meta description", s.PagesWithoutDescription),
		issue("missing-h1", "pages without an h1", s.PagesWithoutH1),
		issue("multiple-h1", "pages with more than one h1", s.MultipleH1Count),
		issue("title-too-long", "titles over 60 characters", s.TitleTooLongCount),
		issue("title-too-short", "titles under 30 characters", s.TitleTooShortCount),
		issue("desc-too-long", "meta descriptions over 160 characters", s.DescriptionTooLongCount),
		issue("duplicate-titles", "pages sharing a title with another page", s.DuplicateTitleCount),
		issue("duplicate-descriptions", "pages sharing a meta description", s.DuplicateDescriptionCount),
		{id: "broken-links", label: "internal links to broken pages", count: listCount(s.TotalBrokenLinks, len(s.BrokenLinks)), samples: urls(s.BrokenLinks)},
		{id: "long-redirect-chains", label: "redirect chains with several hops", count: listCount(s.TotalLongRedirectChains, len(s.LongRedirectChains)), samples: urls(s.LongRedirectChains)},
		{id: "orphan-pages", label: "pages no crawled page links to", count: listCount(s.TotalOrphanPages, len(s.OrphanPages)), samples: first3(s.OrphanPages)},
		{id: "thin-content", label: "pages with very little text", count: listCount(s.TotalThinContentPages, len(s.ThinContentPages)), samples: urls(s.ThinContentPages)},
		{id: "non-https", label: "pages not served over HTTPS", count: listCount(s.TotalNonHTTPSPages, len(s.NonHTTPSPages)), samples: first3(s.NonHTTPSPages)},
		{id: "mixed-content", label: "HTTPS pages loading HTTP resources", count: len(s.MixedContentPages), samples: first3(s.MixedContentPages)},
		{id: "broken-external-links", label: "external links that are broken", count: len(s.BrokenExternalLinks), samples: urls(s.BrokenExternalLinks)},
		{id: "duplicate-content", label: "groups of pages with identical content", count: len(s.DuplicateContentGroups)},
		{id: "soft-404", label: "soft-404 candidates (look like error pages, confirm in Search Console)", count: soft, samples: softSamples, filter: map[string]string{"issue": "soft-404"}},
	}
}

// topIssues returns up to n issues with a count, most pages first.
func topIssues(defs []issueDef, n int) []issueDef {
	var out []issueDef
	for _, d := range defs {
		if d.count > 0 {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].count > out[j].count })
	return out[:min(len(out), n)]
}

// topReasons keeps the n most common non-indexable reasons.
func topReasons(m map[string]int, n int) map[string]int {
	if len(m) <= n {
		return m
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || (m[keys[i]] == m[keys[j]] && keys[i] < keys[j]) })
	out := make(map[string]int, n)
	for _, k := range keys[:n] {
		out[k] = m[k]
	}
	return out
}

func (t *tools) crawlSummary(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, crawlSummaryOut, error) {
	st, err := t.status(ctx, in.ID)
	if err != nil {
		return nil, crawlSummaryOut{}, err
	}
	if st.Analyzing {
		return nil, crawlSummaryOut{}, fmt.Errorf("crawl %s finished and its pages are being analysed; call crawl_wait until done is true", in.ID)
	}
	if st.Status != "completed" {
		return nil, crawlSummaryOut{}, fmt.Errorf("crawl %s is %s; the summary is ready once it completes (use crawl_wait)", in.ID, st.Status)
	}
	var resp struct {
		Stats           summaryStats `json:"stats"`
		PageCount       int          `json:"pageCount"`
		AnalysisPending bool         `json:"analysisPending"`
	}
	if err := t.get(ctx, crawlPath(in.ID, "/stats"), nil, &resp); err != nil {
		return nil, crawlSummaryOut{}, noCrawl(in.ID, err)
	}
	s := resp.Stats
	out := crawlSummaryOut{
		ID: in.ID, SeedURL: st.SeedURL, Status: st.Status, AnalysisPending: resp.AnalysisPending,
		Totals: summaryTotals{
			Pages: max(s.TotalPages, resp.PageCount), Indexable: s.IndexabilityStats.Indexable,
			NonIndexable: s.IndexabilityStats.NonIndexable, InternalLinks: s.TotalInternalLinks,
			ExternalLinks: s.TotalExternalLinks, Images: s.TotalImages, ImagesMissingAlt: s.ImagesMissingAlt,
			CrawlDurationMs: s.CrawlDurationMs,
		},
		StatusCodes: s.StatusCodes,
		Indexability: summaryIndexability{
			Indexable: s.IndexabilityStats.Indexable, NonIndexable: s.IndexabilityStats.NonIndexable,
			Reasons: topReasons(s.IndexabilityStats.Reasons, 8),
		},
		Issues:            []summaryIssue{},
		Soft404Candidates: len(s.Soft404Pages),
	}
	if out.StatusCodes == nil {
		out.StatusCodes = map[string]int{}
	}
	if s.Soft404Stats != nil {
		out.Soft404Candidates = s.Soft404Stats.Total
	}
	if p := s.ResponseTimePercentiles; p.P50 > 0 || p.P90 > 0 {
		out.ResponseTimeMs = &percentiles{P50: p.P50, P90: p.P90, P99: p.P99}
	}
	// All sample lookups share one budget. Once one fails (the dashboard is
	// still loading pages, or the budget ran out) the rest are skipped: the
	// counts matter more than the samples.
	sampleCtx, cancel := context.WithTimeout(ctx, t.sampleBudget())
	defer cancel()
	sampling := true
	for _, d := range topIssues(issueDefs(s), 10) {
		samples := d.samples
		if samples == nil && d.filter != nil && sampling {
			var ok bool
			samples, ok = t.issueSamples(sampleCtx, in.ID, d.filter)
			sampling = ok
		}
		is := summaryIssue{ID: d.id, Label: d.label, Count: d.count, Samples: []string{}, Filter: d.filter}
		for _, u := range samples {
			is.Samples = append(is.Samples, truncate(u, 200))
		}
		out.Issues = append(out.Issues, is)
	}
	fitSummary(&out)
	return nil, out, nil
}

// issueSamples returns up to 3 URLs matching a pages_query filter. ok is
// false when the lookup failed, so the caller stops asking.
func (t *tools) issueSamples(ctx context.Context, id string, filter map[string]string) (urls []string, ok bool) {
	q := url.Values{"limit": {"3"}}
	for k, v := range filter {
		q.Set(k, v)
	}
	resp, err := t.pages(ctx, id, q)
	if err != nil {
		return nil, false
	}
	for _, p := range resp.Pages[:min(len(resp.Pages), 3)] {
		urls = append(urls, p.URL)
	}
	return urls, true
}

func (t *tools) sampleBudget() time.Duration {
	if t.opts.SampleBudget > 0 {
		return t.opts.SampleBudget
	}
	return 10 * time.Second
}

// fitSummary drops samples, then reasons, until the summary fits.
func fitSummary(out *crawlSummaryOut) {
	size := func() int {
		b, _ := json.Marshal(out)
		return len(b)
	}
	for keep := 2; keep >= 0 && size() > maxSummaryBytes; keep-- {
		for i := range out.Issues {
			out.Issues[i].Samples = out.Issues[i].Samples[:min(len(out.Issues[i].Samples), keep)]
		}
	}
	if size() > maxSummaryBytes {
		out.Indexability.Reasons = topReasons(out.Indexability.Reasons, 3)
	}
	if size() > maxSummaryBytes {
		codes := make(map[string]int)
		for k, v := range out.StatusCodes {
			if len(codes) < 12 || strings.HasPrefix(k, "2") {
				codes[k] = v
			}
		}
		out.StatusCodes = codes
	}
}
