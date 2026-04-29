package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"

	"github.com/micelio/micelio/internal/types"
)

// DiffResult represents the comparison between two crawl result sets.
type DiffResult struct {
	FieldSummary   map[string]int `json:"fieldSummary"`
	AddedURLs      []string       `json:"addedUrls"`
	RemovedURLs    []string       `json:"removedUrls"`
	ChangedURLs    []URLDiff      `json:"changedUrls"`
	Lifecycle      *LifecycleSummary `json:"lifecycle,omitempty"`
	OldCount       int            `json:"oldCount"`
	NewCount       int            `json:"newCount"`
	UnchangedCount int            `json:"unchangedCount"`
}

// LifecycleSummary holds enriched URL lifecycle data for disappeared/new URLs.
type LifecycleSummary struct {
	DisappearedWithTraffic []URLTrafficInfo `json:"disappearedWithTraffic"`
	NewWithTraffic         []URLTrafficInfo `json:"newWithTraffic"`
	DisappearedReasons     map[string]int   `json:"disappearedReasons"`
}

// URLTrafficInfo holds traffic metrics for a URL.
type URLTrafficInfo struct {
	URL         string `json:"url"`
	Clicks      int    `json:"clicks,omitempty"`
	Impressions int    `json:"impressions,omitempty"`
	Sessions    int    `json:"sessions,omitempty"`
	Visitors    int    `json:"visitors,omitempty"`
	StatusCode  int    `json:"statusCode,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// URLDiff represents changes for a single URL between crawls.
type URLDiff struct {
	URL     string      `json:"url"`
	Changes []FieldDiff `json:"changes"`
}

// FieldDiff represents a single field change.
type FieldDiff struct {
	OldValue interface{} `json:"oldValue"`
	NewValue interface{} `json:"newValue"`
	Field    string      `json:"field"`
}

// ComputeDiff compares two sets of crawl pages by URL.
func ComputeDiff(oldPages, newPages []*types.PageData) *DiffResult {
	oldMap := make(map[string]*types.PageData, len(oldPages))
	for _, p := range oldPages {
		oldMap[p.URL] = p
	}
	newMap := make(map[string]*types.PageData, len(newPages))
	for _, p := range newPages {
		newMap[p.URL] = p
	}

	result := &DiffResult{
		OldCount:     len(oldPages),
		NewCount:     len(newPages),
		FieldSummary: make(map[string]int),
	}

	// Find added and changed
	for url, newPage := range newMap {
		oldPage, exists := oldMap[url]
		if !exists {
			result.AddedURLs = append(result.AddedURLs, url)
			continue
		}
		changes := diffPage(oldPage, newPage)
		if len(changes) > 0 {
			result.ChangedURLs = append(result.ChangedURLs, URLDiff{URL: url, Changes: changes})
			for _, c := range changes {
				result.FieldSummary[c.Field]++
			}
		} else {
			result.UnchangedCount++
		}
	}

	// Find removed
	for url := range oldMap {
		if _, exists := newMap[url]; !exists {
			result.RemovedURLs = append(result.RemovedURLs, url)
		}
	}

	// Sort for deterministic output
	sort.Strings(result.AddedURLs)
	sort.Strings(result.RemovedURLs)
	sort.Slice(result.ChangedURLs, func(i, j int) bool {
		return result.ChangedURLs[i].URL < result.ChangedURLs[j].URL
	})

	// Enrich lifecycle data
	result.Lifecycle = buildLifecycleSummary(oldMap, newMap, result.RemovedURLs, result.AddedURLs)

	return result
}

// buildLifecycleSummary enriches disappeared and new URLs with traffic data
// and classifies disappeared URLs by reason.
func buildLifecycleSummary(
	oldMap, newMap map[string]*types.PageData,
	removedURLs, addedURLs []string,
) *LifecycleSummary {
	ls := &LifecycleSummary{
		DisappearedWithTraffic: []URLTrafficInfo{},
		NewWithTraffic:         []URLTrafficInfo{},
		DisappearedReasons:     make(map[string]int),
	}

	// Enrich disappeared URLs
	for _, url := range removedURLs {
		oldPage := oldMap[url]
		if oldPage == nil {
			continue
		}

		// Classify based on the page's last-known state before disappearing
		reason := classifyDisappearedState(oldPage)
		ls.DisappearedReasons[reason]++

		// Check if disappeared URL had traffic
		info := extractTrafficInfo(oldPage)
		if info != nil {
			info.Reason = reason
			ls.DisappearedWithTraffic = append(ls.DisappearedWithTraffic, *info)
		}
	}

	// Enrich new URLs with traffic (unusual but possible if GSC/GA4 data exists)
	for _, url := range addedURLs {
		newPage := newMap[url]
		if newPage == nil {
			continue
		}
		info := extractTrafficInfo(newPage)
		if info != nil {
			ls.NewWithTraffic = append(ls.NewWithTraffic, *info)
		}
	}

	// Sort by traffic (clicks desc)
	sort.Slice(ls.DisappearedWithTraffic, func(i, j int) bool {
		return ls.DisappearedWithTraffic[i].Clicks > ls.DisappearedWithTraffic[j].Clicks
	})
	sort.Slice(ls.NewWithTraffic, func(i, j int) bool {
		return ls.NewWithTraffic[i].Clicks > ls.NewWithTraffic[j].Clicks
	})

	return ls
}

// classifyDisappearedState classifies a disappeared URL based on its
// last-known state in the old crawl (not the cause of disappearance).
func classifyDisappearedState(oldPage *types.PageData) string {
	if len(oldPage.RedirectChain) > 0 {
		return "redirected"
	}
	if oldPage.StatusCode >= 500 {
		return "server-error"
	}
	if oldPage.StatusCode >= 400 {
		return "client-error"
	}
	if !oldPage.Indexability.Indexable {
		return "non-indexable"
	}
	return "removed"
}

// extractTrafficInfo returns traffic data for a page, or nil if no traffic.
func extractTrafficInfo(p *types.PageData) *URLTrafficInfo {
	var clicks, impressions, sessions, visitors int
	hasTraffic := false

	if p.GscData != nil {
		clicks = p.GscData.Clicks
		impressions = p.GscData.Impressions
		if clicks > 0 || impressions > 0 {
			hasTraffic = true
		}
	}
	if p.Ga4Data != nil && p.Ga4Data.Sessions > 0 {
		sessions = p.Ga4Data.Sessions
		hasTraffic = true
	}
	if p.PlausibleData != nil && p.PlausibleData.Visitors > 0 {
		visitors = p.PlausibleData.Visitors
		hasTraffic = true
	}

	if !hasTraffic {
		return nil
	}
	return &URLTrafficInfo{
		URL:         p.URL,
		Clicks:      clicks,
		Impressions: impressions,
		Sessions:    sessions,
		Visitors:    visitors,
		StatusCode:  p.StatusCode,
	}
}

func diffPage(old, new *types.PageData) []FieldDiff {
	var diffs []FieldDiff

	if old.StatusCode != new.StatusCode {
		diffs = append(diffs, FieldDiff{Field: "statusCode", OldValue: old.StatusCode, NewValue: new.StatusCode})
	}
	if titleText(old) != titleText(new) {
		diffs = append(diffs, FieldDiff{Field: "title", OldValue: titleText(old), NewValue: titleText(new)})
	}
	if metaDesc(old) != metaDesc(new) {
		diffs = append(diffs, FieldDiff{Field: "metaDescription", OldValue: metaDesc(old), NewValue: metaDesc(new)})
	}
	if h1Text(old) != h1Text(new) {
		diffs = append(diffs, FieldDiff{Field: "h1", OldValue: h1Text(old), NewValue: h1Text(new)})
	}
	if old.WordCount != new.WordCount {
		diffs = append(diffs, FieldDiff{Field: "wordCount", OldValue: old.WordCount, NewValue: new.WordCount})
	}
	if canonicalHref(old) != canonicalHref(new) {
		diffs = append(diffs, FieldDiff{Field: "canonical", OldValue: canonicalHref(old), NewValue: canonicalHref(new)})
	}
	if old.Indexability.Indexable != new.Indexability.Indexable {
		diffs = append(diffs, FieldDiff{Field: "indexable", OldValue: old.Indexability.Indexable, NewValue: new.Indexability.Indexable})
	}
	if len(old.InternalLinks) != len(new.InternalLinks) {
		diffs = append(diffs, FieldDiff{Field: "internalLinks", OldValue: len(old.InternalLinks), NewValue: len(new.InternalLinks)})
	}
	if len(old.ExternalLinks) != len(new.ExternalLinks) {
		diffs = append(diffs, FieldDiff{Field: "externalLinks", OldValue: len(old.ExternalLinks), NewValue: len(new.ExternalLinks)})
	}
	if old.Depth != new.Depth {
		diffs = append(diffs, FieldDiff{Field: "depth", OldValue: old.Depth, NewValue: new.Depth})
	}

	return diffs
}

func titleText(p *types.PageData) string {
	if p.Title != nil {
		return p.Title.Text
	}
	return ""
}

func metaDesc(p *types.PageData) string {
	if p.MetaDescription != nil {
		return p.MetaDescription.Text
	}
	return ""
}

func h1Text(p *types.PageData) string {
	if len(p.Headings.H1) > 0 {
		return p.Headings.H1[0]
	}
	return ""
}

func canonicalHref(p *types.PageData) string {
	if p.Canonical != nil {
		return *p.Canonical
	}
	return ""
}

// PrintDiffSummary writes a text summary of the diff to w.
func PrintDiffSummary(w io.Writer, diff *DiffResult) {
	fmt.Fprintf(w, "Crawl Diff: %d → %d pages\n", diff.OldCount, diff.NewCount)
	fmt.Fprintf(w, "  Added:     %d\n", len(diff.AddedURLs))
	fmt.Fprintf(w, "  Removed:   %d\n", len(diff.RemovedURLs))
	fmt.Fprintf(w, "  Changed:   %d\n", len(diff.ChangedURLs))
	fmt.Fprintf(w, "  Unchanged: %d\n", diff.UnchangedCount)

	if len(diff.FieldSummary) > 0 {
		fmt.Fprintf(w, "\nField changes:\n")
		// Sort by count descending
		type kv struct {
			k string
			v int
		}
		var sorted []kv
		for k, v := range diff.FieldSummary {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
		for _, s := range sorted {
			fmt.Fprintf(w, "  %-20s %d\n", s.k, s.v)
		}
	}

	// Lifecycle summary
	if diff.Lifecycle != nil {
		if len(diff.Lifecycle.DisappearedReasons) > 0 {
			fmt.Fprintf(w, "\nDisappeared URL reasons:\n")
			type rkv struct {
				k string
				v int
			}
			var rsorted []rkv
			for k, v := range diff.Lifecycle.DisappearedReasons {
				rsorted = append(rsorted, rkv{k, v})
			}
			sort.Slice(rsorted, func(i, j int) bool { return rsorted[i].v > rsorted[j].v })
			for _, s := range rsorted {
				fmt.Fprintf(w, "  %-20s %d\n", s.k, s.v)
			}
		}
		if len(diff.Lifecycle.DisappearedWithTraffic) > 0 {
			fmt.Fprintf(w, "\n⚠ Disappeared URLs with traffic (%d):\n", len(diff.Lifecycle.DisappearedWithTraffic))
			for _, u := range diff.Lifecycle.DisappearedWithTraffic {
				fmt.Fprintf(w, "  %s (clicks=%d, impressions=%d, reason=%s)\n", u.URL, u.Clicks, u.Impressions, u.Reason)
			}
		}
		if len(diff.Lifecycle.NewWithTraffic) > 0 {
			fmt.Fprintf(w, "\nNew URLs with traffic (%d):\n", len(diff.Lifecycle.NewWithTraffic))
			for _, u := range diff.Lifecycle.NewWithTraffic {
				fmt.Fprintf(w, "  %s (clicks=%d, impressions=%d)\n", u.URL, u.Clicks, u.Impressions)
			}
		}
	}
}

// GenerateDiffHTML generates an HTML diff report and writes it to w.
func GenerateDiffHTML(w io.Writer, diff *DiffResult) error {
	diffJSON, _ := json.Marshal(diff)
	data := struct {
		Diff     *DiffResult
		DiffJSON template.JS
	}{
		Diff:     diff,
		DiffJSON: template.JS(diffJSON),
	}
	return diffHTMLTemplate.Execute(w, data)
}

// GenerateDiffHTMLString returns the HTML diff report as a string.
func GenerateDiffHTMLString(diff *DiffResult) (string, error) {
	var buf bytes.Buffer
	if err := GenerateDiffHTML(&buf, diff); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var diffHTMLTemplate = template.Must(template.New("diff").Parse(diffReportHTML))

const diffReportHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Micelio Crawl Diff Report</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0d1117;--surface:#161b22;--border:#30363d;--text:#c9d1d9;--text-muted:#8b949e;--accent:#58a6ff;--green:#3fb950;--red:#f85149;--yellow:#d29922}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;background:var(--bg);color:var(--text);line-height:1.6}
.container{max-width:1400px;margin:0 auto;padding:24px}
header{border-bottom:1px solid var(--border);padding-bottom:16px;margin-bottom:24px}
header h1{font-size:24px;font-weight:600}
.summary{display:grid;grid-template-columns:repeat(4,1fr);gap:16px;margin-bottom:24px}
.stat{background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:16px;text-align:center}
.stat .n{font-size:32px;font-weight:600}
.stat .l{font-size:12px;color:var(--text-muted);text-transform:uppercase}
.added{color:var(--green)}
.removed{color:var(--red)}
.changed{color:var(--yellow)}
.tabs{display:flex;gap:0;border-bottom:1px solid var(--border);margin-bottom:16px}
.tab{padding:8px 16px;cursor:pointer;color:var(--text-muted);border-bottom:2px solid transparent;font-size:14px}
.tab.active{color:var(--accent);border-bottom-color:var(--accent)}
.panel{display:none}
.panel.active{display:block}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:8px 12px;text-align:left;border-bottom:1px solid var(--border)}
th{background:var(--surface);color:var(--text-muted);font-weight:600}
.old{background:rgba(248,81,73,0.1);color:var(--red);text-decoration:line-through}
.new{background:rgba(63,185,80,0.1);color:var(--green)}
footer{margin-top:32px;padding-top:16px;border-top:1px solid var(--border);color:var(--text-muted);font-size:12px;text-align:center}
</style>
</head>
<body>
<div class="container">
<header><h1>Crawl Diff Report</h1></header>

<div class="summary">
<div class="stat"><div class="n">{{.Diff.OldCount}} → {{.Diff.NewCount}}</div><div class="l">Pages</div></div>
<div class="stat"><div class="n added">+{{len .Diff.AddedURLs}}</div><div class="l">Added</div></div>
<div class="stat"><div class="n removed">-{{len .Diff.RemovedURLs}}</div><div class="l">Removed</div></div>
<div class="stat"><div class="n changed">~{{len .Diff.ChangedURLs}}</div><div class="l">Changed</div></div>
</div>

<div class="tabs">
<div class="tab active" data-panel="changed-panel">Changed</div>
<div class="tab" data-panel="added-panel">Added</div>
<div class="tab" data-panel="removed-panel">Removed</div>
</div>

<div id="changed-panel" class="panel active">
<table><thead><tr><th>URL</th><th>Field</th><th>Old</th><th>New</th></tr></thead>
<tbody id="changed-body"></tbody></table>
</div>

<div id="added-panel" class="panel">
<table><thead><tr><th>URL</th></tr></thead>
<tbody id="added-body"></tbody></table>
</div>

<div id="removed-panel" class="panel">
<table><thead><tr><th>URL</th></tr></thead>
<tbody id="removed-body"></tbody></table>
</div>

<footer>Generated by Micelio SEO Crawler</footer>
</div>

<script>
const diff = {{.DiffJSON}};

function esc(s) { const d=document.createElement('div'); d.textContent=String(s||''); return d.innerHTML; }

// Changed
const changedRows = (diff.changedUrls||[]).slice(0,500).flatMap(u =>
  u.changes.map((c,i) =>
    '<tr><td>'+(i===0?esc(u.url):'')+'</td><td>'+esc(c.field)+'</td><td class="old">'+esc(c.oldValue)+'</td><td class="new">'+esc(c.newValue)+'</td></tr>'
  )
).join('');
document.getElementById('changed-body').innerHTML = changedRows;

// Added
document.getElementById('added-body').innerHTML = (diff.addedUrls||[]).slice(0,500).map(u => '<tr><td class="added">'+esc(u)+'</td></tr>').join('');

// Removed
document.getElementById('removed-body').innerHTML = (diff.removedUrls||[]).slice(0,500).map(u => '<tr><td class="removed">'+esc(u)+'</td></tr>').join('');

// Tabs
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
    tab.classList.add('active');
    document.getElementById(tab.dataset.panel).classList.add('active');
  });
});
</script>
</body>
</html>`
