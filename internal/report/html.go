// Package report generates HTML crawl reports and diff comparisons.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// ReportData contains all data for the HTML report template.
type ReportData struct {
	Stats       *types.CrawlStats `json:"stats"`
	GeneratedAt string            `json:"generatedAt"`
	SeedURL     string            `json:"seedUrl"`
	SiteDomain  string            `json:"siteDomain"`
	PagesJSON   template.JS       `json:"-"`
	StatsJSON   template.JS       `json:"-"`
	Pages       []*LightPage      `json:"pages"`
}

// LightPage is a stripped-down page representation for the HTML report.
type LightPage struct {
	Ga4Sessions        *int     `json:"ga4Sessions"`
	InSitemap          *bool    `json:"inSitemap"`
	ReadabilityScore   *float64 `json:"readabilityScore"`
	Ga4Pageviews       *int     `json:"ga4Pageviews"`
	GscClicks          *int     `json:"gscClicks"`
	GscImpressions     *int     `json:"gscImpressions"`
	PSIScore           *float64 `json:"psiScore"`
	TopicalConsistency *float64 `json:"topicalConsistency"`
	RichnessScore      *float64 `json:"richnessScore"`
	PassageScore       *float64 `json:"passageScore"`
	AIReadinessScore   *float64 `json:"aiReadinessScore"`
	ContentAgeDays     *int     `json:"contentAgeDays"`
	Title              string   `json:"title"`
	MetaDescription    string   `json:"metaDescription"`
	H1                 string   `json:"h1"`
	URL                string   `json:"url"`
	Canonical          string   `json:"canonical"`
	Issues             []string `json:"issues"`
	Depth              int      `json:"depth"`
	PageRank           float64  `json:"pageRank"`
	Inlinks            int      `json:"inlinks"`
	ExternalLinks      int      `json:"externalLinks"`
	InternalLinks      int      `json:"internalLinks"`
	ResponseTimeMs     int64    `json:"responseTimeMs"`
	WordCount          int      `json:"wordCount"`
	StatusCode         int      `json:"statusCode"`
	OriginalityScore   int      `json:"originalityScore"`
	Indexable          bool     `json:"indexable"`
	HasAuthor          bool     `json:"hasAuthor"`
}

// LightenPages strips heavy fields from PageData for embedding in HTML.
func LightenPages(pages []*types.PageData) []*LightPage {
	light := make([]*LightPage, 0, len(pages))
	for _, p := range pages {
		lp := &LightPage{
			URL:            p.URL,
			StatusCode:     p.StatusCode,
			Depth:          p.Depth,
			WordCount:      p.WordCount,
			ResponseTimeMs: p.ResponseTimeMs,
			InternalLinks:  max(len(p.InternalLinks), p.InternalLinkCount),
			ExternalLinks:  max(len(p.ExternalLinks), p.ExternalLinkCount),
			Inlinks:        p.Inlinks,
			PageRank:       p.PageRank,
			Indexable:      p.Indexability.Indexable,
			Issues:         p.URLIssues,
		}
		if p.Title != nil {
			lp.Title = p.Title.Text
		}
		if p.MetaDescription != nil {
			lp.MetaDescription = p.MetaDescription.Text
		}
		if len(p.Headings.H1) > 0 {
			lp.H1 = p.Headings.H1[0]
		}
		if p.Canonical != nil {
			lp.Canonical = *p.Canonical
		}
		if p.Pagespeed != nil && p.Pagespeed.Error == "" {
			lp.PSIScore = &p.Pagespeed.PerformanceScore
		}
		if p.GscData != nil {
			lp.GscImpressions = &p.GscData.Impressions
			lp.GscClicks = &p.GscData.Clicks
		}
		if p.Ga4Data != nil {
			lp.Ga4Sessions = &p.Ga4Data.Sessions
			lp.Ga4Pageviews = &p.Ga4Data.Pageviews
		}
		if p.Readability != nil {
			lp.ReadabilityScore = &p.Readability.FleschKincaid
		}
		if p.SitemapData != nil {
			v := p.SitemapData.InSitemap
			lp.InSitemap = &v
		}
		if p.Topicality != nil {
			lp.TopicalConsistency = &p.Topicality.TopicalConsistency
		}
		if p.ContentRichness != nil {
			lp.RichnessScore = &p.ContentRichness.RichnessScore
		}
		if p.PassageReadiness != nil {
			lp.PassageScore = &p.PassageReadiness.PassageScore
		}
		if p.AIReadiness != nil {
			lp.AIReadinessScore = &p.AIReadiness.AIReadinessScore
		}
		if p.Freshness != nil && p.Freshness.ContentAgeDays > 0 {
			lp.ContentAgeDays = &p.Freshness.ContentAgeDays
		}
		if p.EEAT != nil {
			lp.HasAuthor = p.EEAT.HasAuthor
		}
		lp.OriginalityScore = p.OriginalityScore
		light = append(light, lp)
	}
	return light
}

// GenerateHTML generates a self-contained HTML report and writes it to w.
func GenerateHTML(w io.Writer, seedURL string, stats *types.CrawlStats, pages []*types.PageData) error {
	lightPages := LightenPages(pages)

	pagesJSON, _ := json.Marshal(lightPages)
	statsJSON, _ := json.Marshal(stats)

	// Extract domain + path prefix for display (e.g. "motortrend.com/cars")
	siteDomain := seedURL
	if parsed, err := url.Parse(seedURL); err == nil {
		d := strings.TrimPrefix(parsed.Hostname(), "www.")
		p := strings.TrimRight(parsed.Path, "/")
		if p != "" {
			siteDomain = d + p
		} else {
			siteDomain = d
		}
	}

	data := ReportData{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		SeedURL:     seedURL,
		SiteDomain:  siteDomain,
		Stats:       stats,
		Pages:       lightPages,
		PagesJSON:   template.JS(pagesJSON),
		StatsJSON:   template.JS(statsJSON),
	}

	return htmlTemplate.Execute(w, data)
}

// GenerateHTMLString returns the HTML report as a string.
func GenerateHTMLString(seedURL string, stats *types.CrawlStats, pages []*types.PageData) (string, error) {
	var buf bytes.Buffer
	if err := GenerateHTML(&buf, seedURL, stats, pages); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var htmlTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(n, total int) string {
		if total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
	},
	"comma": func(n int) string {
		if n < 0 {
			return "-" + commaFormat(-n)
		}
		return commaFormat(n)
	},
}).Parse(reportHTML))

func commaFormat(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert commas from right to left
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

const reportHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.SiteDomain}} — Micelio Crawl Report</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0d1117;--surface:#161b22;--surface-2:#1c2129;--border:#30363d;--text:#c9d1d9;--text-muted:#8b949e;--accent:#58a6ff;--green:#3fb950;--red:#f85149;--yellow:#d29922;--orange:#db6d28;--purple:#bc8cff;--teal:#39d353}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;background:var(--bg);color:var(--text);line-height:1.6}
.container{max-width:1400px;margin:0 auto;padding:24px}
header{border-bottom:1px solid var(--border);padding-bottom:16px;margin-bottom:24px}
header .header-row{display:flex;align-items:center;gap:12px}
header .site-logo{width:32px;height:32px;border-radius:4px;flex-shrink:0}
header h1{font-size:24px;font-weight:600;color:var(--text)}
header h1 .site-name{color:var(--accent)}
header .meta{color:var(--text-muted);font-size:14px;margin-top:4px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px;margin-bottom:24px}
.card{background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:16px}
.card .label{font-size:12px;color:var(--text-muted);text-transform:uppercase;letter-spacing:0.5px}
.card .value{font-size:28px;font-weight:600;margin-top:4px}
.card .sub{font-size:11px;color:var(--text-muted);margin-top:2px}
.good{color:var(--green)}.warn{color:var(--yellow)}.bad{color:var(--red)}
.tabs{display:flex;gap:0;border-bottom:1px solid var(--border);margin-bottom:16px;flex-wrap:wrap}
.tab{padding:8px 16px;cursor:pointer;color:var(--text-muted);border-bottom:2px solid transparent;font-size:14px}
.tab.active{color:var(--accent);border-bottom-color:var(--accent)}
.panel{display:none}
.panel.active{display:block}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:8px 12px;text-align:left;border-bottom:1px solid var(--border)}
th{background:var(--surface);color:var(--text-muted);font-weight:600;position:sticky;top:0;z-index:1}
tr:hover{background:rgba(88,166,255,0.04)}
.status-2xx{color:var(--green)}.status-3xx{color:var(--yellow)}.status-4xx{color:var(--red)}.status-5xx{color:var(--red);font-weight:bold}
.search{width:100%;padding:8px 12px;background:var(--surface);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:14px;margin-bottom:16px}
.search:focus{outline:none;border-color:var(--accent)}
.issue-tag{display:inline-block;padding:2px 6px;margin:1px;border-radius:3px;font-size:11px;background:rgba(248,81,73,0.15);color:var(--red)}
footer{margin-top:32px;padding-top:16px;border-top:1px solid var(--border);color:var(--text-muted);font-size:12px;text-align:center}
/* Health Score */
.health-ring{position:relative;width:120px;height:120px;margin:0 auto}
.health-ring svg{transform:rotate(-90deg)}
.health-ring .score-text{position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);text-align:center}
.health-ring .score-num{font-size:32px;font-weight:700;line-height:1}
.health-ring .score-label{font-size:11px;color:var(--text-muted)}
/* Funnel */
.funnel-row{display:flex;align-items:center;gap:12px;margin-bottom:8px}
.funnel-label{width:90px;font-size:12px;font-weight:600;text-align:right;flex-shrink:0}
.funnel-bar-wrap{flex:1;height:28px;background:var(--border);border-radius:4px;overflow:hidden;position:relative}
.funnel-bar{height:100%;border-radius:4px;display:flex;align-items:center;padding-left:8px;font-size:12px;font-weight:600;color:#fff;min-width:30px;transition:width .3s}
.funnel-pct{width:60px;font-size:13px;font-weight:600;text-align:left;flex-shrink:0}
.funnel-drop{margin-left:102px;font-size:11px;color:var(--text-muted);margin-bottom:6px}
/* Tags Health */
.tags-row{display:flex;align-items:center;gap:12px;margin-bottom:6px}
.tags-label{width:80px;font-size:12px;font-weight:600;text-align:right;flex-shrink:0}
.tags-bar{flex:1;height:20px;background:var(--border);border-radius:3px;overflow:hidden;display:flex}
.tags-seg{height:100%;transition:width .3s}
.tags-pct{width:60px;font-size:12px;font-weight:600;text-align:left;flex-shrink:0}
.tags-legend{display:flex;gap:16px;margin-top:6px;margin-left:92px;font-size:11px;color:var(--text-muted)}
.tags-legend span::before{content:'';display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:4px;vertical-align:middle}
.leg-good::before{background:var(--green)!important}.leg-len::before{background:var(--orange)!important}.leg-dup::before{background:var(--yellow)!important}.leg-miss::before{background:var(--red)!important}
/* Charts */
.chart-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:16px;margin-bottom:16px}
.chart-card{background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:16px}
.chart-card h4{font-size:13px;font-weight:600;margin-bottom:12px;color:var(--text-muted);text-transform:uppercase;letter-spacing:0.5px}
.hbar{display:flex;align-items:center;gap:8px;margin-bottom:4px}
.hbar-label{width:60px;font-size:11px;text-align:right;flex-shrink:0;color:var(--text-muted)}
.hbar-track{flex:1;height:16px;background:var(--border);border-radius:3px;overflow:hidden}
.hbar-fill{height:100%;border-radius:3px;min-width:2px}
.hbar-val{width:50px;font-size:11px;flex-shrink:0}
/* Donut (CSS) */
.donut-wrap{display:flex;align-items:center;gap:20px;justify-content:center}
.donut{width:100px;height:100px;border-radius:50%;position:relative}
.donut-center{position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);font-size:20px;font-weight:700}
.donut-legend{font-size:12px;line-height:2}
.donut-legend span{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;vertical-align:middle}
/* Directory tree */
.dir-tree{font-size:12px;font-family:monospace}
.dir-tree details{margin-left:16px}
.dir-tree summary{cursor:pointer;padding:2px 0;color:var(--text)}
.dir-tree summary:hover{color:var(--accent)}
.dir-tree .dir-stats{color:var(--text-muted);font-size:11px;margin-left:8px}
/* Signals enhanced */
.signal-section{background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:20px;margin-bottom:16px}
.signal-section h3{font-size:16px;margin-bottom:4px;color:var(--accent)}
.signal-section .source{font-size:11px;color:var(--text-muted);margin-bottom:4px}
.signal-section .cite{font-size:10px;color:var(--text-muted);opacity:0.6;margin-bottom:12px}
.signal-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}
.signal-metric{padding:10px 14px;background:var(--bg);border-radius:6px;border:1px solid var(--border)}
.signal-metric .label{font-size:10px;color:var(--text-muted);text-transform:uppercase;letter-spacing:0.5px}
.signal-metric .val{font-size:22px;font-weight:600;margin-top:2px}
.signal-metric .sub{font-size:11px;color:var(--text-muted);margin-top:2px;line-height:1.4}
.sig-crawl{border-left:3px solid var(--green)}.sig-relevance{border-left:3px solid var(--accent)}.sig-quality{border-left:3px solid var(--purple)}.sig-authority{border-left:3px solid var(--orange)}.sig-engagement{border-left:3px solid var(--teal)}.sig-funnel{border-left:3px solid var(--yellow)}
.impact-tag{display:inline-block;padding:2px 6px;margin:1px;border-radius:3px;font-size:10px;font-weight:600}
.impact-high{background:rgba(248,81,73,0.2);color:var(--red)}
.impact-medium{background:rgba(210,153,34,0.2);color:var(--yellow)}
.impact-low{background:rgba(63,185,80,0.15);color:var(--green)}
.bar-bg{height:10px;background:var(--border);border-radius:5px;margin-top:6px}
.bar-fill{height:100%;border-radius:5px}
/* Page type tags */
.type-tag{display:inline-block;padding:2px 8px;margin:2px;border-radius:12px;font-size:11px;background:rgba(88,166,255,0.1);color:var(--accent);border:1px solid rgba(88,166,255,0.2)}
@media print{body{background:#fff;color:#222}.container{max-width:100%}:root{--bg:#fff;--surface:#f6f8fa;--surface-2:#f0f2f5;--border:#d0d7de;--text:#1f2328;--text-muted:#656d76;--accent:#0969da;--green:#1a7f37;--red:#cf222e;--yellow:#9a6700;--orange:#bc4c00}}
</style>
</head>
<body>
<div class="container">
<header>
<div class="header-row">
<img class="site-logo" src="https://www.google.com/s2/favicons?domain={{.SiteDomain}}&sz=64" alt="" onerror="this.style.display='none'">
<h1><span class="site-name">{{.SiteDomain}}</span> — Micelio Crawl Report</h1>
</div>
<div class="meta">{{.SeedURL}} · {{comma .Stats.TotalPages}} pages · Generated {{.GeneratedAt}}</div>
</header>

<div class="tabs">
<div class="tab active" data-panel="overview">Overview</div>
<div class="tab" data-panel="pages">Pages</div>
<div class="tab" data-panel="content">Content</div>
<div class="tab" data-panel="links">Links</div>
<div class="tab" data-panel="signals">Signals</div>
<div class="tab" data-panel="issues">Issues</div>
</div>

<input class="search" type="text" placeholder="Search URLs..." id="search-input">

<!-- Overview Tab -->
<div id="overview" class="panel active">
<div id="overview-content"></div>
</div>

<!-- Pages Tab -->
<div id="pages" class="panel">
<table>
<thead><tr>
<th>URL</th><th>Status</th><th>Title</th><th>Depth</th><th>Words</th><th>Response</th><th>Inlinks</th><th>T*</th><th>Issues</th>
</tr></thead>
<tbody id="pages-body"></tbody>
</table>
</div>

<!-- Content Tab -->
<div id="content" class="panel">
<div id="content-tab"></div>
</div>

<!-- Links Tab -->
<div id="links" class="panel">
<table>
<thead><tr><th>URL</th><th>Internal Out</th><th>External Out</th><th>Inlinks</th><th>PageRank</th></tr></thead>
<tbody id="links-body"></tbody>
</table>
</div>

<!-- Signals Tab -->
<div id="signals" class="panel">
<div id="signals-content"></div>
</div>

<!-- Issues Tab -->
<div id="issues" class="panel">
<table>
<thead><tr><th>URL</th><th>Issues</th><th>Ranking Impact</th></tr></thead>
<tbody id="issues-body"></tbody>
</table>
</div>

<footer>Generated by Micelio SEO Crawler</footer>
</div>

<script>
const pages = {{.PagesJSON}};
const stats = {{.StatsJSON}};

function statusClass(c){return c>=200&&c<300?'status-2xx':c>=300&&c<400?'status-3xx':c>=400&&c<500?'status-4xx':'status-5xx'}
function esc(s){const d=document.createElement('div');d.textContent=s||'';return d.innerHTML}
function fmt(v,d){return v!=null?v.toFixed(d!=null?d:2):'-'}
function pct(n,t){return t?(n/t*100).toFixed(1)+'%':'0%'}
function pctN(n,t){return t?parseFloat((n/t*100).toFixed(1)):0}
function comma(n){return n.toLocaleString()}
function bar(val,max,color){const w=max>0?Math.min(val/max*100,100):0;return '<div class="bar-bg"><div class="bar-fill" style="width:'+w+'%;background:'+color+'"></div></div>'}

const impactMap={
  'missing title':{l:'high',t:'T* title signal absent'},'missing h1':{l:'high',t:'T* heading alignment broken'},
  'missing meta':{l:'medium',t:'SnippetBrain input missing'},'duplicate title':{l:'medium',t:'T* titlematchScore diluted'},
  'duplicate description':{l:'low',t:'Snippet dedup risk'},'thin content':{l:'high',t:'Q* quality below threshold'},
  'broken':{l:'high',t:'Trawler crawl budget waste'},'orphan':{l:'high',t:'PageRank_NS unreachable'},
  'redirect chain':{l:'medium',t:'NavBoost signal dilution'},'redirect loop':{l:'high',t:'Trawler infinite loop'},
  'noindex':{l:'medium',t:'Excluded from Alexandria'},'canonical':{l:'medium',t:'Index consolidation signal'},
  'mixed content':{l:'low',t:'Security trust signal (NSR)'},'hreflang':{l:'medium',t:'International targeting mismatch'},
  'multiple h1':{l:'low',t:'T* heading weight split'},'title too long':{l:'low',t:'SERP snippet truncation'},
  'title too short':{l:'low',t:'T* signal weak'},'slow':{l:'medium',t:'NavBoost abandonment risk'},
  'soft 404':{l:'high',t:'Trawler false-positive waste'},'non-descriptive':{l:'low',t:'Anchor mismatch (US7533092)'},
  'robots blocked':{l:'medium',t:'Trawler access denied'},'near-duplicate':{l:'medium',t:'OriginalContentScore penalized'},
  'no internal links':{l:'medium',t:'PageRank dead end'},'schema':{l:'low',t:'Rich result eligibility'}
};
function getImpact(issue){const l=issue.toLowerCase();for(const[k,v]of Object.entries(impactMap)){if(l.includes(k))return v}return null}

// --- Health Score ---
function computeHealth(){
  const s=stats,total=s.totalPages||1;
  let score=0;
  const idx=s.indexabilityStats?s.indexabilityStats.indexable:0;
  score+=25*(idx/total);
  score+=15*((total-s.pagesWithoutTitle)/total);
  score+=15*((total-s.pagesWithoutDescription)/total);
  score+=10*((total-s.pagesWithoutH1)/total);
  if(s.responseTimeBuckets){const fast=s.responseTimeBuckets.fast||0;const med=s.responseTimeBuckets.medium||0;score+=10*((fast+med)/total)}else{score+=10}
  const broken=(s.brokenLinks?s.brokenLinks.length:0)+(s.brokenExternalLinks?s.brokenExternalLinks.length:0);
  score+=Math.max(0,10-broken*0.5);
  if(s.canonicalStats){const cs=s.canonicalStats;score+=10*((cs.selfReferencing+cs.canonicalized)/Math.max(total,1))}else{score+=10}
  const orphans=s.orphanPages?s.orphanPages.length:0;
  score+=Math.max(0,5-orphans*0.25);
  return Math.round(Math.min(100,Math.max(0,score)));
}

function healthRing(score){
  const r=52,circ=2*Math.PI*r,offset=circ-(score/100)*circ;
  const color=score>=80?'var(--green)':score>=60?'var(--yellow)':'var(--red)';
  const label=score>=80?'Good':score>=60?'Needs Work':'Poor';
  return '<div class="health-ring"><svg width="120" height="120"><circle cx="60" cy="60" r="'+r+'" fill="none" stroke="var(--border)" stroke-width="8"/><circle cx="60" cy="60" r="'+r+'" fill="none" stroke="'+color+'" stroke-width="8" stroke-dasharray="'+circ+'" stroke-dashoffset="'+offset+'" stroke-linecap="round"/></svg><div class="score-text"><div class="score-num" style="color:'+color+'">'+score+'</div><div class="score-label">'+label+'</div></div></div>';
}

// --- Overview Tab ---
function renderOverview(){
  const s=stats,total=s.totalPages||0;
  let h='';

  // Top row: Health + Key Metrics
  h+='<div style="display:grid;grid-template-columns:140px 1fr;gap:16px;margin-bottom:20px;align-items:start">';
  h+=healthRing(computeHealth());
  h+='<div class="cards" style="margin-bottom:0">';
  h+='<div class="card"><div class="label">Total Pages</div><div class="value">'+comma(total)+'</div>';
  if(s.crawlDurationMs){const dur=s.crawlDurationMs;const sec=Math.floor(dur/1000)%60,min=Math.floor(dur/60000)%60,hr=Math.floor(dur/3600000);h+='<div class="sub">'+(hr?hr+'h ':'')+(min?min+'m ':'')+(sec+'s')+' · '+fmt(total/(dur/1000),1)+' p/s</div>'}
  h+='</div>';
  h+='<div class="card"><div class="label">Indexable</div><div class="value good">'+comma(s.indexabilityStats?s.indexabilityStats.indexable:0)+'</div><div class="sub">'+pct(s.indexabilityStats?s.indexabilityStats.indexable:0,total)+' of total</div></div>';
  h+='<div class="card"><div class="label">Broken Links</div><div class="value '+(s.brokenLinks&&s.brokenLinks.length?'bad':'good')+'">'+(s.brokenLinks?s.brokenLinks.length:0)+'</div></div>';
  if(s.responseTimePercentiles){const p=s.responseTimePercentiles;const c=p.p50<500?'good':p.p50<1000?'warn':'bad';h+='<div class="card"><div class="label">Response Time</div><div class="value '+c+'">'+Math.round(p.p50)+'ms</div><div class="sub">p90: '+Math.round(p.p90)+'ms · p99: '+Math.round(p.p99)+'ms</div></div>'}
  h+='<div class="card"><div class="label">Orphans</div><div class="value '+(s.orphanPages&&s.orphanPages.length?'warn':'good')+'">'+(s.orphanPages?s.orphanPages.length:0)+'</div></div>';
  h+='</div></div>';

  // Page Types
  if(s.templateTypeDistribution){const types=s.templateTypeDistribution;const keys=Object.keys(types).sort((a,b)=>types[b]-types[a]);if(keys.length){h+='<div style="margin-bottom:16px">';keys.forEach(k=>{h+='<span class="type-tag">'+esc(k)+' <b>'+comma(types[k])+'</b></span>'});h+='</div>'}}

  // SEO Funnel
  if(s.seoFunnelStats){
    const sf=s.seoFunnelStats;
    h+='<div class="chart-card" style="margin-bottom:16px"><h4>SEO Funnel</h4>';
    const stages=[{l:'Crawled',v:sf.crawled,c:'#8b949e'},{l:'Renderable',v:sf.renderable||sf.crawled,c:'var(--accent)'},{l:'Indexable',v:sf.indexable,c:'var(--green)'},{l:'Visible',v:sf.visible,c:'var(--yellow)'},{l:'Active',v:sf.active,c:'var(--teal)'}];
    const maxV=sf.crawled||1;
    let prev=sf.crawled;
    stages.forEach((st,i)=>{
      if(i>2&&st.v===0)return;
      h+='<div class="funnel-row"><div class="funnel-label">'+st.l+'</div><div class="funnel-bar-wrap"><div class="funnel-bar" style="width:'+pctN(st.v,maxV)+'%;background:'+st.c+'">'+comma(st.v)+'</div></div><div class="funnel-pct">'+pct(st.v,maxV)+'</div></div>';
      if(i>0&&prev>0&&st.v<prev){h+='<div class="funnel-drop">&#8595; -'+pct(prev-st.v,prev)+' drop</div>'}
      prev=st.v;
    });
    h+='</div>';
  }

  // Tags Health
  h+='<div class="chart-card" style="margin-bottom:16px"><h4>HTML Tags Health</h4>';
  const ttl=total||1;
  // Title bar
  const tMiss=s.pagesWithoutTitle||0,tLong=(s.titleTooLongCount||0)+(s.titleTooShortCount||0),tDup=s.duplicateTitleCount||0,tGood=Math.max(0,total-tMiss-tLong-tDup);
  const tGoodPct=pctN(tGood,ttl);
  h+='<div class="tags-row"><div class="tags-label">Title</div><div class="tags-bar">';
  h+='<div class="tags-seg" style="width:'+pctN(tGood,ttl)+'%;background:var(--green)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(tLong,ttl)+'%;background:var(--orange)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(tDup,ttl)+'%;background:var(--yellow)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(tMiss,ttl)+'%;background:var(--red)"></div>';
  h+='</div><div class="tags-pct" style="color:'+(tGoodPct>=80?'var(--green)':tGoodPct>=60?'var(--yellow)':'var(--red)')+'">'+tGoodPct.toFixed(1)+'%</div></div>';
  // H1 bar
  const h1Miss=s.pagesWithoutH1||0,h1Multi=s.multipleH1Count||0,h1Good=Math.max(0,total-h1Miss-h1Multi);
  const h1GoodPct=pctN(h1Good,ttl);
  h+='<div class="tags-row"><div class="tags-label">H1</div><div class="tags-bar">';
  h+='<div class="tags-seg" style="width:'+pctN(h1Good,ttl)+'%;background:var(--green)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(h1Multi,ttl)+'%;background:var(--orange)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(h1Miss,ttl)+'%;background:var(--red)"></div>';
  h+='</div><div class="tags-pct" style="color:'+(h1GoodPct>=80?'var(--green)':h1GoodPct>=60?'var(--yellow)':'var(--red)')+'">'+h1GoodPct.toFixed(1)+'%</div></div>';
  // Description bar
  const dMiss=s.pagesWithoutDescription||0,dLong=s.descriptionTooLongCount||0,dDup=s.duplicateDescriptionCount||0,dGood=Math.max(0,total-dMiss-dLong-dDup);
  const dGoodPct=pctN(dGood,ttl);
  h+='<div class="tags-row"><div class="tags-label">Description</div><div class="tags-bar">';
  h+='<div class="tags-seg" style="width:'+pctN(dGood,ttl)+'%;background:var(--green)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(dLong,ttl)+'%;background:var(--orange)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(dDup,ttl)+'%;background:var(--yellow)"></div>';
  h+='<div class="tags-seg" style="width:'+pctN(dMiss,ttl)+'%;background:var(--red)"></div>';
  h+='</div><div class="tags-pct" style="color:'+(dGoodPct>=80?'var(--green)':dGoodPct>=60?'var(--yellow)':'var(--red)')+'">'+dGoodPct.toFixed(1)+'%</div></div>';
  h+='<div class="tags-legend"><span class="leg-good">Good</span><span class="leg-len">Length</span><span class="leg-dup">Duplicate</span><span class="leg-miss">Missing</span></div>';
  h+='</div>';

  // Charts row: Status Codes + Indexability + Response Time
  h+='<div class="chart-grid">';

  // Status Code distribution
  if(s.statusCodes){
    const sc=s.statusCodes;
    const groups={'2xx':0,'3xx':0,'4xx':0,'5xx':0};
    for(const[code,count]of Object.entries(sc)){const c=parseInt(code);if(c<300)groups['2xx']+=count;else if(c<400)groups['3xx']+=count;else if(c<500)groups['4xx']+=count;else groups['5xx']+=count}
    const colors={'2xx':'var(--green)','3xx':'var(--accent)','4xx':'var(--yellow)','5xx':'var(--red)'};
    h+='<div class="chart-card"><h4>Status Codes</h4>';
    for(const g of['2xx','3xx','4xx','5xx']){if(groups[g]>0){h+='<div class="hbar"><div class="hbar-label">'+g+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(groups[g],total)+'%;background:'+colors[g]+'"></div></div><div class="hbar-val">'+comma(groups[g])+'</div></div>'}}
    h+='</div>';
  }

  // Indexability donut
  if(s.indexabilityStats){
    const ix=s.indexabilityStats;
    const idxPct=pctN(ix.indexable,total);
    h+='<div class="chart-card"><h4>Indexability</h4>';
    h+='<div class="donut-wrap"><div class="donut" style="background:conic-gradient(var(--green) 0 '+idxPct+'%,var(--red) '+idxPct+'% 100%)"><div class="donut-center" style="background:var(--surface);width:60px;height:60px;border-radius:50%;display:flex;align-items:center;justify-content:center">'+idxPct.toFixed(0)+'%</div></div>';
    h+='<div class="donut-legend"><div><span style="background:var(--green)"></span>Indexable: '+comma(ix.indexable)+'</div><div><span style="background:var(--red)"></span>Non-indexable: '+comma(ix.nonIndexable)+'</div></div></div>';
    h+='</div>';
  }

  // Response Time percentiles
  if(s.responseTimePercentiles){
    const rp=s.responseTimePercentiles;
    h+='<div class="chart-card"><h4>Response Time</h4>';
    const percs=[{l:'p50',v:rp.p50},{l:'p90',v:rp.p90},{l:'p99',v:rp.p99}];
    const mx=rp.p99||1;
    percs.forEach(p=>{const c=p.v<500?'var(--green)':p.v<1000?'var(--yellow)':'var(--red)';h+='<div class="hbar"><div class="hbar-label">'+p.l+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(p.v,mx)+'%;background:'+c+'"></div></div><div class="hbar-val">'+Math.round(p.v)+'ms</div></div>'});
    if(s.responseTimeBuckets){const b=s.responseTimeBuckets;h+='<div style="font-size:11px;color:var(--text-muted);margin-top:8px">Fast(&lt;500ms): '+comma(b.fast)+' · Med: '+comma(b.medium)+' · Slow: '+comma(b.slow)+' · Slowest: '+comma(b.slowest)+'</div>'}
    h+='</div>';
  }
  h+='</div>';

  // Depth Distribution
  if(s.depthDistribution){
    const dd=s.depthDistribution;
    const depths=Object.keys(dd).map(Number).sort((a,b)=>a-b);
    if(depths.length){
      const mx=Math.max(...Object.values(dd));
      h+='<div class="chart-card" style="margin-bottom:16px"><h4>Depth Distribution</h4>';
      depths.forEach(d=>{const c=d<=2?'var(--green)':d<=4?'var(--accent)':'var(--yellow)';h+='<div class="hbar"><div class="hbar-label">Depth '+d+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(dd[d],mx)+'%;background:'+c+'"></div></div><div class="hbar-val">'+comma(dd[d])+'</div></div>'});
      h+='</div>';
    }
  }

  // Directory Tree
  h+=renderDirTree();

  document.getElementById('overview-content').innerHTML=h;
}

// --- Directory Tree ---
function renderDirTree(){
  const tree={};
  pages.forEach(p=>{
    try{const u=new URL(p.url);const parts=u.pathname.split('/').filter(Boolean);let node=tree;parts.forEach(part=>{if(!node[part])node[part]={_pages:[],_children:{}};node=node[part]._children?node[part]:node[part]={_pages:[],_children:{}};if(!node._children)node._children={};if(!node._pages)node._pages=[];node=node._children;if(!node[part])node[part]={_pages:[],_children:{}}})}catch(e){}
  });
  // Simplified: group by first 2 path segments
  const dirs={};
  pages.forEach(p=>{
    try{const u=new URL(p.url);const segs=u.pathname.split('/').filter(Boolean);const key='/'+segs.slice(0,2).join('/');if(!dirs[key])dirs[key]={count:0,statuses:{},depths:[]};dirs[key].count++;const sc=Math.floor(p.statusCode/100)+'xx';dirs[key].statuses[sc]=(dirs[key].statuses[sc]||0)+1;dirs[key].depths.push(p.depth)}catch(e){}
  });
  const sorted=Object.entries(dirs).sort((a,b)=>b[1].count-a[1].count).slice(0,30);
  if(!sorted.length)return '';
  let h='<div class="chart-card" style="margin-bottom:16px"><h4>Directory Structure (Top 30)</h4><div class="dir-tree">';
  sorted.forEach(([path,d])=>{
    const avgD=(d.depths.reduce((a,b)=>a+b,0)/d.depths.length).toFixed(1);
    const st=Object.entries(d.statuses).map(([k,v])=>k+':'+v).join(' ');
    h+='<div style="padding:2px 0"><span style="color:var(--accent)">'+esc(path||'/')+'</span><span class="dir-stats">'+comma(d.count)+' pages · avg depth '+avgD+' · '+st+'</span></div>';
  });
  h+='</div></div>';
  return h;
}

// --- Content Tab ---
function renderContent(){
  let h='';
  h+='<div class="chart-grid">';

  // PageRank Distribution
  const prVals=pages.map(p=>p.pageRank||0).filter(v=>v>0);
  if(prVals.length){
    prVals.sort((a,b)=>a-b);
    const mn=prVals[0],mx=prVals[prVals.length-1];
    const buckets=new Array(10).fill(0);
    prVals.forEach(v=>{const i=mx>mn?Math.min(Math.floor((v-mn)/(mx-mn)*10),9):0;buckets[i]++});
    const bMax=Math.max(...buckets);
    h+='<div class="chart-card"><h4>PageRank Distribution</h4>';
    buckets.forEach((c,i)=>{const lo=(mn+(mx-mn)*i/10).toFixed(4);h+='<div class="hbar"><div class="hbar-label" style="width:70px">'+lo+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(c,bMax)+'%;background:var(--accent)"></div></div><div class="hbar-val">'+c+'</div></div>'});
    h+='</div>';
  }

  // Inlink Distribution (Top 20)
  const byInlinks=[...pages].sort((a,b)=>b.inlinks-a.inlinks).slice(0,20);
  if(byInlinks.length&&byInlinks[0].inlinks>0){
    const mx=byInlinks[0].inlinks;
    h+='<div class="chart-card"><h4>Top Inlinked Pages</h4>';
    byInlinks.forEach(p=>{const path=p.url.replace(/^https?:\/\/[^/]+/,'');h+='<div class="hbar"><div class="hbar-label" style="width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="'+esc(p.url)+'">'+esc(path)+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(p.inlinks,mx)+'%;background:var(--green)"></div></div><div class="hbar-val">'+p.inlinks+'</div></div>'});
    h+='</div>';
  }
  h+='</div>';

  // Image Audit
  if(stats.imageAuditStats){
    const ia=stats.imageAuditStats;
    h+='<div class="cards" style="margin-bottom:16px">';
    h+='<div class="card"><div class="label">Total Images</div><div class="value">'+comma(ia.totalImages)+'</div></div>';
    h+='<div class="card"><div class="label">Missing Alt</div><div class="value '+(ia.missingAltAttribute>0?'warn':'good')+'">'+comma(ia.missingAltAttribute)+'</div></div>';
    h+='<div class="card"><div class="label">Empty Alt</div><div class="value '+(ia.emptyAlt>0?'warn':'good')+'">'+comma(ia.emptyAlt)+'</div></div>';
    h+='<div class="card"><div class="label">Missing Dimensions</div><div class="value '+(ia.missingDimensions>0?'warn':'good')+'">'+comma(ia.missingDimensions)+'</div></div>';
    h+='<div class="card"><div class="label">Oversized</div><div class="value '+(ia.oversizedImages&&ia.oversizedImages.length>0?'bad':'good')+'">'+(ia.oversizedImages?ia.oversizedImages.length:0)+'</div></div>';
    h+='</div>';
  }

  // Inlink Buckets
  if(stats.linkIntelligenceStats&&stats.linkIntelligenceStats.inlinkBuckets){
    const ib=stats.linkIntelligenceStats.inlinkBuckets;
    const all=ib.zero+ib.one+ib.twoToFive+ib.sixToTwenty+ib.twentyPlus;
    if(all>0){
      h+='<div class="chart-card" style="margin-bottom:16px"><h4>Inlink Buckets</h4>';
      const bkts=[{l:'0',v:ib.zero,c:'var(--red)'},{l:'1',v:ib.one,c:'var(--yellow)'},{l:'2-5',v:ib.twoToFive,c:'var(--accent)'},{l:'6-20',v:ib.sixToTwenty,c:'var(--green)'},{l:'20+',v:ib.twentyPlus,c:'var(--teal)'}];
      const mx=Math.max(...bkts.map(b=>b.v));
      bkts.forEach(b=>{h+='<div class="hbar"><div class="hbar-label">'+b.l+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(b.v,mx)+'%;background:'+b.c+'"></div></div><div class="hbar-val">'+comma(b.v)+'</div></div>'});
      h+='</div>';
    }
  }

  // Page Type breakdown
  if(stats.templateTypeDistribution){
    const types=stats.templateTypeDistribution;
    const entries=Object.entries(types).sort((a,b)=>b[1]-a[1]);
    if(entries.length){
      const mx=entries[0][1];
      h+='<div class="chart-card" style="margin-bottom:16px"><h4>Page Types</h4>';
      entries.forEach(([k,v])=>{h+='<div class="hbar"><div class="hbar-label" style="width:100px">'+esc(k)+'</div><div class="hbar-track"><div class="hbar-fill" style="width:'+pctN(v,mx)+'%;background:var(--purple)"></div></div><div class="hbar-val">'+comma(v)+'</div></div>'});
      h+='</div>';
    }
  }

  document.getElementById('content-tab').innerHTML=h;
}

// --- Signals Tab (Enhanced) ---
function renderSignals(){
  const s=stats;
  let h='';
  const total=s.totalPages||0;
  const idx=s.indexabilityStats?s.indexabilityStats.indexable:0;
  const nonIdx=s.indexabilityStats?s.indexabilityStats.nonIndexable:0;
  const broken=s.brokenLinks?s.brokenLinks.length:0;
  const orphans=s.orphanPages?s.orphanPages.length:0;

  // 1. Crawl & Index
  h+='<div class="signal-section" style="border-left:3px solid var(--green)"><h3>Crawl &amp; Index (Trawler / Alexandria)</h3>';
  h+='<div class="source">Google\'s Trawler crawls and Alexandria indexes pages. Only indexed pages can rank. Crawl budget wasted on non-indexable URLs reduces the rate at which Googlebot discovers and refreshes your content.</div>';
  h+='<div class="cite">Rankpedia: doj:trawler, doj:caffeine-indexing</div>';
  h+='<div class="signal-grid">';
  h+='<div class="signal-metric sig-crawl"><div class="label">Index Rate</div><div class="val good">'+pct(idx,total)+'</div><div class="sub">'+comma(idx)+' of '+comma(total)+' pages indexable (no noindex, canonical self-referencing, 200 status)</div>'+bar(idx,total,'var(--green)')+'</div>';
  h+='<div class="signal-metric sig-crawl"><div class="label">Crawl Waste</div><div class="val '+(nonIdx+broken>0?'warn':'good')+'">'+pct(nonIdx+broken,total)+'</div><div class="sub">'+comma(nonIdx+broken)+' URLs consuming crawl budget without entering the index (redirects, 4xx/5xx, noindex, duplicate)</div></div>';
  h+='<div class="signal-metric sig-crawl"><div class="label">Orphan Pages</div><div class="val '+(orphans>0?'warn':'good')+'">'+comma(orphans)+'</div><div class="sub">Pages with no internal link path from seed. Googlebot relies on links for discovery; orphans depend entirely on sitemaps or external links</div></div>';
  if(s.redirectStats){const nb=s.redirectStats.navBoostAtRisk||0;h+='<div class="signal-metric sig-crawl"><div class="label">NavBoost at Risk</div><div class="val '+(nb>0?'warn':'good')+'">'+nb+'</div><div class="sub">Pages behind 302/307 redirects. NavBoost click signals accumulate on the redirect URL instead of the destination, diluting ranking power. <em style="opacity:0.6">Rankpedia: doj:navboost</em></div></div>'}
  h+='</div></div>';

  // 2. Relevance (T*)
  if(s.topicalityStats){
    const t=s.topicalityStats;
    h+='<div class="signal-section" style="border-left:3px solid var(--accent)"><h3>Relevance (T* Topicality)</h3>';
    h+='<div class="source">T* is Google\'s query-dependent topicality score combining three sub-signals: Anchors (A) what the web says about you, Body (B) what your page says about itself, and Clicks (C) what users say about you. Our proxy measures the B component (on-page alignment). Confirmed by Pandu Nayak (sworn testimony).</div>';
    h+='<div class="cite">Rankpedia: doj:t-star, doj:abc-body, leak:Ascorer</div>';
    h+='<div class="signal-grid">';
    h+='<div class="signal-metric sig-relevance"><div class="label">Title-Body Overlap</div><div class="val">'+fmt(t.avgTitleBodyOverlap)+'</div><div class="sub">Fraction of title terms found in body text. Proxy for titlematchScore: the higher, the more Google trusts the title reflects the content</div>'+bar(t.avgTitleBodyOverlap,1,'var(--accent)')+'</div>';
    h+='<div class="signal-metric sig-relevance"><div class="label">Title-H1 Alignment</div><div class="val">'+fmt(t.avgTitleH1Alignment)+'</div><div class="sub">Jaccard similarity between title tag and H1. Misalignment signals inconsistent topic targeting, weakening the Body (B) sub-signal</div>'+bar(t.avgTitleH1Alignment,1,'var(--accent)')+'</div>';
    h+='<div class="signal-metric sig-relevance"><div class="label">Topical Consistency</div><div class="val">'+fmt(t.avgTopicalConsistency)+'</div><div class="sub">Weighted composite of title-body, title-H1, and heading-body coverage. Proxy for the overall T* Body sub-signal strength</div>'+bar(t.avgTopicalConsistency,1,'var(--accent)')+'</div>';
    h+='<div class="signal-metric sig-relevance"><div class="label">Low Alignment</div><div class="val '+(t.lowAlignmentPages>0?'warn':'good')+'">'+t.lowAlignmentPages+'</div><div class="sub">Pages with T* proxy &lt; 0.3, where title, headings, and body discuss different topics. Likely demoted in initial retrieval</div></div>';
    h+='</div></div>';
  }

  // 3. Quality (Q*)
  if(s.readabilityStats||s.contentRichnessStats||s.eeatStats){
    h+='<div class="signal-section" style="border-left:3px solid var(--purple)"><h3>Quality (Q* Score)</h3>';
    h+='<div class="source">Q* is Google\'s site-wide, query-independent quality score on a 0-1 scale. Pages below 0.4 are ineligible for featured snippets and rich results. Hand-crafted (not ML), operates at subdomain level, tied to E-E-A-T. Confirmed by Pandu Nayak. Candour Agency independently verified the 0.4 threshold across 800K domains.</div>';
    h+='<div class="cite">Rankpedia: doj:q-star, leak:contentEffort, patent:US8682892</div>';
    h+='<div class="signal-grid">';
    if(s.readabilityStats){
      const r=s.readabilityStats;
      h+='<div class="signal-metric sig-quality"><div class="label">Q* High</div><div class="val good">'+(r.qualityTier1||0)+' <span style="font-size:12px;font-weight:normal;color:var(--text-muted)">pages</span></div><div class="sub">Q* proxy &gt; 0.7. Eligible for all SERP features including AI Overviews and featured snippets</div></div>';
      h+='<div class="signal-metric sig-quality"><div class="label">Q* Acceptable</div><div class="val">'+(r.qualityTier2||0)+' <span style="font-size:12px;font-weight:normal;color:var(--text-muted)">pages</span></div><div class="sub">Q* proxy 0.4-0.7. Above the rich result threshold but may lose out on premium placements</div></div>';
      h+='<div class="signal-metric sig-quality"><div class="label">Q* Below Threshold</div><div class="val '+(r.qualityTier3>0?'bad':'good')+'">'+(r.qualityTier3||0)+' <span style="font-size:12px;font-weight:normal;color:var(--text-muted)">pages</span></div><div class="sub">Q* proxy &lt; 0.4. Below the gate for rich results. These pages are limited to standard blue links</div></div>';
      h+='<div class="signal-metric sig-quality"><div class="label">Avg Readability</div><div class="val">'+fmt(r.avgScore,1)+'</div><div class="sub">Flesch-Kincaid Grade Level. Lower is more accessible. Google\'s quality rater guidelines emphasize content should match audience reading level</div></div>';
    }
    if(s.contentRichnessStats){
      const cr=s.contentRichnessStats;
      h+='<div class="signal-metric sig-quality"><div class="label">Content Richness</div><div class="val">'+fmt(cr.avgRichnessScore)+'</div><div class="sub">Proxy for contentEffort (leaked LLM signal). Measures structural diversity: headings, lists, tables, images, code blocks. <em style="opacity:0.6">Rankpedia: leak:contentEffort</em></div>'+bar(cr.avgRichnessScore,1,'var(--green)')+'</div>';
      h+='<div class="signal-metric sig-quality"><div class="label">Low Richness</div><div class="val '+(cr.lowRichnessPages>0?'warn':'good')+'">'+cr.lowRichnessPages+' <span style="font-size:12px;font-weight:normal;color:var(--text-muted)">pages</span></div><div class="sub">Score &lt; 0.2. Thin content with minimal structural elements, likely scored low on contentEffort</div></div>';
    }
    if(s.eeatStats){
      const e=s.eeatStats;
      h+='<div class="signal-metric sig-quality"><div class="label">Author Coverage</div><div class="val">'+fmt(e.authorCoverage,0)+'%</div><div class="sub">'+e.pagesWithAuthor+' pages with author markup. Google\'s authorshipMarkup patent (US9697259) uses structured author data for expertise signals. <em style="opacity:0.6">Rankpedia: patent:US9697259</em></div>'+bar(e.authorCoverage,100,'var(--accent)')+'</div>';
      const trust=[e.hasAboutPage?'About':'',e.hasContactPage?'Contact':'',e.hasEditorialPolicy?'Editorial':''].filter(Boolean).join(', ');
      h+='<div class="signal-metric sig-quality"><div class="label">Trust Pages</div><div class="val">'+(trust||'None')+'</div><div class="sub">E-E-A-T trust signals. About, Contact, and Editorial Policy pages strengthen domain trustworthiness, a direct input to Q*</div></div>';
    }
    h+='</div></div>';
  }

  // 4. Authority (NSR / PageRank_NS)
  if(s.linkIntelligenceStats||s.anchorHealthStats){
    h+='<div class="signal-section" style="border-left:3px solid var(--orange)"><h3>Authority (NSR / PageRank<sub>NS</sub>)</h3>';
    h+='<div class="source">NSR (Normalized Site Rank) is a 46-signal composite measuring site-wide authority. PageRank<sub>NS</sub> (Nearest Seed) computes link authority based on proximity to trusted seed pages, confirmed still active in the leaked API. Internal link structure directly controls how authority flows across your site.</div>';
    h+='<div class="cite">Rankpedia: leak:pagerank-ns, leak:site-pr, doj:nsr, patent:US9165040</div>';
    h+='<div class="signal-grid">';
    if(s.linkIntelligenceStats){
      const li=s.linkIntelligenceStats;
      h+='<div class="signal-metric sig-authority"><div class="label">Avg Click Depth</div><div class="val">'+fmt(li.avgClickDepth,1)+'</div><div class="sub">Average hops from seed URL. Pages deeper than 3-4 clicks receive exponentially less crawl frequency and PageRank flow</div></div>';
      h+='<div class="signal-metric sig-authority"><div class="label">Near Orphans</div><div class="val '+(li.nearOrphansCount>0?'warn':'good')+'">'+li.nearOrphansCount+'</div><div class="sub">Pages with only 1-2 inlinks. Minimal internal linking starves these pages of PageRank and makes them fragile to link loss</div></div>';
      h+='<div class="signal-metric sig-authority"><div class="label">Link Dilution</div><div class="val '+(li.dilutionWarningsCount>0?'warn':'good')+'">'+li.dilutionWarningsCount+'</div><div class="sub">Pages with excess outlinks. PageRank is divided equally among outlinks; too many dilutes the authority passed to each target</div></div>';
    }
    if(s.anchorHealthStats){
      const ah=s.anchorHealthStats;
      h+='<div class="signal-metric sig-authority"><div class="label">Anchor Diversity</div><div class="val">'+fmt(ah.avgInboundDiversity)+'</div><div class="sub">Anchor text variation in inbound links. Low diversity triggers anchorMismatch spam signals. Natural profiles mix brand, keyword, and generic anchors. <em style="opacity:0.6">Rankpedia: patent:US7533092, doj:BayesSpam</em></div>'+bar(ah.avgInboundDiversity,1,'var(--green)')+'</div>';
      h+='<div class="signal-metric sig-authority"><div class="label">Over-Optimized</div><div class="val '+(ah.pagesOverOptimized>0?'warn':'good')+'">'+ah.pagesOverOptimized+'</div><div class="sub">Pages with suspicious anchor text patterns. BayesSpam classifier flags over-optimized anchor profiles as manipulation signals</div></div>';
    }
    h+='</div></div>';
  }

  // 5. Engagement Readiness (P* / NavBoost)
  if(s.passageReadinessStats||s.aiReadinessStats||s.freshnessStats){
    h+='<div class="signal-section" style="border-left:3px solid var(--teal)"><h3>Engagement Readiness (P* / NavBoost Proxy)</h3>';
    h+='<div class="source">P* is Google\'s dynamic engagement metric combining NavBoost click data with anchor signals. NavBoost is "just a big table" of 13 months of click statistics (not ML), achieving 91% accuracy. It tracks goodClicks, badClicks, and lastLongestClicks, segmented by location and device. This section measures how well your content is structured to earn those positive signals.</div>';
    h+='<div class="cite">Rankpedia: doj:p-star, doj:navboost, doj:good-clicks, doj:last-longest-clicks, patent:US20160078102</div>';
    h+='<div class="signal-grid">';
    if(s.passageReadinessStats){
      const pr=s.passageReadinessStats;
      h+='<div class="signal-metric sig-engagement"><div class="label">Passage Score</div><div class="val">'+fmt(pr.avgPassageScore)+'</div><div class="sub">Passage ranking readiness (0-1). Google can rank individual passages within a page (patent US9940367). Well-sectioned content with clear headings is more likely to be selected</div>'+bar(pr.avgPassageScore,1,'var(--accent)')+'</div>';
      h+='<div class="signal-metric sig-engagement"><div class="label">FAQ Pages</div><div class="val">'+pr.pagesWithFaq+'</div><div class="sub">Pages with FAQ structure (question-answer pairs). Eligible for FAQ rich results and passage extraction by AI Overviews</div></div>';
      h+='<div class="signal-metric sig-engagement"><div class="label">HowTo Pages</div><div class="val">'+pr.pagesWithHowTo+'</div><div class="sub">Pages with step-by-step structure. Structured procedural content earns HowTo rich results and increased SERP real estate</div></div>';
    }
    if(s.aiReadinessStats){
      const ai=s.aiReadinessStats;
      h+='<div class="signal-metric sig-engagement"><div class="label">AI Readiness</div><div class="val">'+fmt(ai.avgAiReadinessScore)+'</div><div class="sub">AI Overview citation readiness (0-1). Measures concise definitions, structured answers, and schema markup that FastSearch/SnippetBrain can extract. <em style="opacity:0.6">Rankpedia: doj:fastsearch, doj:snippetbrain</em></div>'+bar(ai.avgAiReadinessScore,1,'var(--accent)')+'</div>';
      h+='<div class="signal-metric sig-engagement"><div class="label">Structured Answers</div><div class="val">'+ai.pagesWithStructuredAnswer+'</div><div class="sub">Pages with a concise definition or answer near the top. Prime candidates for featured snippets and AI Overview citations</div></div>';
    }
    if(s.freshnessStats){
      const fr=s.freshnessStats;
      h+='<div class="signal-metric sig-engagement"><div class="label">Dated Content</div><div class="val">'+pct(fr.pagesWithDate,fr.pagesWithDate+fr.pagesWithoutDate)+'</div><div class="sub">'+fr.pagesWithDate+' pages with date signals (byline, schema, meta). Google uses semanticDateInfo and bylineDate for freshness scoring. <em style="opacity:0.6">Rankpedia: patent:US8924379, patent:US8583617</em></div></div>';
      h+='<div class="signal-metric sig-engagement"><div class="label">Inconsistent Dates</div><div class="val '+(fr.inconsistentDates>0?'warn':'good')+'">'+fr.inconsistentDates+'</div><div class="sub">Schema datePublished vs meta/byline date mismatch. Inconsistent dates confuse freshness signals and can trigger wrong QDF treatment</div></div>';
    }
    h+='</div></div>';
  }

  // 6. Visibility Funnel
  if(s.seoFunnelStats){
    const sf=s.seoFunnelStats;
    h+='<div class="signal-section" style="border-left:3px solid var(--yellow)"><h3>Visibility Funnel (SuperRoot / SERP)</h3>';
    h+='<div class="source">End-to-end pipeline: Trawler crawls, Alexandria indexes, Ascorer scores T*, Mustang retrieves candidates, SuperRoot assembles the SERP. Each stage filters pages. This funnel shows how many of your pages survive each gate. GSC/GA4 integration adds Visible (impressions) and Active (clicks) stages.</div>';
    h+='<div class="cite">Rankpedia: doj:superroot, doj:mustang, doj:ascorer</div>';
    h+='<div class="signal-grid">';
    h+='<div class="signal-metric sig-funnel"><div class="label">Crawled</div><div class="val">'+comma(sf.crawled)+'</div><div class="sub">Total URLs successfully fetched by the crawler</div></div>';
    h+='<div class="signal-metric sig-funnel"><div class="label">Indexable</div><div class="val">'+comma(sf.indexable)+'</div><div class="sub">'+pct(sf.indexable,sf.crawled)+' pass rate. Pages eligible for Alexandria\'s index (200, no noindex, self-canonical)</div>'+bar(sf.indexable,sf.crawled,'var(--green)')+'</div>';
    if(sf.visible>0){h+='<div class="signal-metric sig-funnel"><div class="label">Visible</div><div class="val">'+comma(sf.visible)+'</div><div class="sub">'+pct(sf.visible,sf.crawled)+' of crawled. Pages with GSC impressions (appeared in SERPs)</div>'+bar(sf.visible,sf.crawled,'var(--yellow)')+'</div>'}
    if(sf.active>0){h+='<div class="signal-metric sig-funnel"><div class="label">Active</div><div class="val">'+comma(sf.active)+'</div><div class="sub">'+pct(sf.active,sf.crawled)+' of crawled. Pages with organic clicks (contributing to NavBoost signals)</div>'+bar(sf.active,sf.crawled,'var(--accent)')+'</div>'}
    h+='</div></div>';
  }

  document.getElementById('signals-content').innerHTML=h;
}

// --- Pages Tab ---
function renderPages(filter){
  const f=(filter||'').toLowerCase();
  const filtered=f?pages.filter(p=>p.url.toLowerCase().includes(f)||(p.title||'').toLowerCase().includes(f)):pages;
  const rows=filtered.slice(0,500).map(p=>{
    const issues=(p.issues||[]).map(i=>'<span class="issue-tag">'+esc(i)+'</span>').join('');
    return '<tr><td>'+esc(p.url)+'</td><td class="'+statusClass(p.statusCode)+'">'+p.statusCode+'</td><td>'+esc(p.title)+'</td><td>'+p.depth+'</td><td>'+p.wordCount+'</td><td>'+p.responseTimeMs+'ms</td><td>'+p.inlinks+'</td><td>'+fmt(p.topicalConsistency)+'</td><td>'+issues+'</td></tr>';
  }).join('');
  document.getElementById('pages-body').innerHTML=rows;
}

// --- Issues Tab ---
function renderIssues(filter){
  const f=(filter||'').toLowerCase();
  const withIssues=pages.filter(p=>p.issues&&p.issues.length>0);
  const filtered=f?withIssues.filter(p=>p.url.toLowerCase().includes(f)):withIssues;
  const rows=filtered.slice(0,500).map(p=>{
    const issues=p.issues.map(i=>'<span class="issue-tag">'+esc(i)+'</span>').join('');
    const impacts=p.issues.map(i=>{const imp=getImpact(i);return imp?'<span class="impact-tag impact-'+imp.l+'">'+esc(imp.t)+'</span>':''}).filter(Boolean).join('');
    return '<tr><td>'+esc(p.url)+'</td><td>'+issues+'</td><td>'+impacts+'</td></tr>';
  }).join('');
  document.getElementById('issues-body').innerHTML=rows;
}

// --- Links Tab ---
function renderLinks(filter){
  const f=(filter||'').toLowerCase();
  const filtered=f?pages.filter(p=>p.url.toLowerCase().includes(f)):pages;
  const sorted=[...filtered].sort((a,b)=>b.inlinks-a.inlinks);
  const rows=sorted.slice(0,500).map(p=>
    '<tr><td>'+esc(p.url)+'</td><td>'+p.internalLinks+'</td><td>'+p.externalLinks+'</td><td>'+p.inlinks+'</td><td>'+fmt(p.pageRank,4)+'</td></tr>'
  ).join('');
  document.getElementById('links-body').innerHTML=rows;
}

// Tab switching
document.querySelectorAll('.tab').forEach(tab=>{
  tab.addEventListener('click',()=>{
    document.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'));
    document.querySelectorAll('.panel').forEach(p=>p.classList.remove('active'));
    tab.classList.add('active');
    document.getElementById(tab.dataset.panel).classList.add('active');
    render(document.getElementById('search-input').value);
  });
});

function render(filter){
  const active=document.querySelector('.tab.active').dataset.panel;
  if(active==='overview')renderOverview();
  else if(active==='pages')renderPages(filter);
  else if(active==='content')renderContent();
  else if(active==='signals')renderSignals();
  else if(active==='issues')renderIssues(filter);
  else if(active==='links')renderLinks(filter);
}

document.getElementById('search-input').addEventListener('input',(e)=>render(e.target.value));
render('');
</script>
</body>
</html>`
