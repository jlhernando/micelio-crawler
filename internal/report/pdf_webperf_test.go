package report

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestCoreWebVitalsAndCDN(t *testing.T) {
	s, pages := backlogFixture()
	s.ResponseTimePercentiles = types.PercentileData{P50: 1200, P90: 3000}
	opts := PDFOptions{
		CWV: &types.CWVReport{Origin: "https://ex.com", Source: "crux", Field: []types.CWVField{
			{FormFactor: "phone", Scope: "origin", Metrics: []types.CWVMetric{
				{ID: "lcp", P75: 4600, Good: .4, NI: .3, Poor: .3},
				{ID: "inp", P75: 150, Good: .85, NI: .1, Poor: .05},
				{ID: "cls", P75: 0.15, Good: .6, NI: .3, Poor: .1},
			}},
			{FormFactor: "desktop", Scope: "origin", Metrics: []types.CWVMetric{{ID: "lcp", P75: 2000, Good: .8, NI: .15, Poor: .05}}},
		}},
		CDN: &types.CDNReport{URL: "https://ex.com/", Provider: "Cloudflare", Evidence: "cf-ray header", Compression: "br", Protocol: "HTTP/2.0",
			HTML:  []types.CDNCache{{Status: "DYNAMIC", CacheControl: "private, no-store"}, {Status: "DYNAMIC"}},
			Asset: &types.CDNCache{URL: "https://ex.com/a.css", Status: "HIT", CacheControl: "public, max-age=3600"}},
	}
	d := BuildPDFData("https://ex.com/", s, pages, opts)

	byTitle := map[string]Ticket{}
	for _, tk := range d.Tickets {
		byTitle[tk.Title] = tk
	}
	if lcp, ok := byTitle["Bring LCP under 2.5 s on phones (now 4.6 s)"]; !ok || lcp.Priority != "P0" || lcp.Unit != "" || !strings.Contains(lcp.Evidence, "30% poor") {
		t.Errorf("LCP ticket %+v", lcp)
	}
	if cls, ok := byTitle["Bring CLS under 0.10 on phones (now 0.15)"]; !ok || cls.Priority != "P1" {
		t.Errorf("CLS ticket %+v", cls)
	}
	for title := range byTitle {
		if strings.Contains(title, "INP") {
			t.Errorf("INP is good, no ticket: %s", title)
		}
	}
	// Slow crawl + HTML not cached at the edge: P1 with Cloudflare steps.
	c, ok := byTitle["Cache HTML at the Cloudflare edge"]
	if !ok || c.Priority != "P1" || !strings.Contains(strings.Join(c.Fix, " "), "Cache Rule") {
		t.Errorf("CDN ticket %+v", c)
	}
	if _, ok := byTitle["Cache static files for a long time"]; !ok {
		t.Error("1 h max-age on assets should raise a ticket")
	}
	// Keys stay in order after the new tickets are merged.
	for i, tk := range d.Tickets {
		if tk.Key != "SEO-"+strconv.Itoa(i+1) {
			t.Errorf("ticket %d key %s", i, tk.Key)
		}
		if i > 0 && tk.Priority < d.Tickets[i-1].Priority {
			t.Errorf("tickets not sorted by priority at %d", i)
		}
	}
	if d.CWV == nil || !d.CWV.Measured || d.CWV.Verdict != "Fails Core Web Vitals" || len(d.CWV.Columns) != 2 {
		t.Errorf("cwv view %+v", d.CWV)
	}
	if d.CDN == nil || d.CDN.Provider != "Cloudflare" || d.CDN.Rows[0].Tone != "bad" || len(d.CDN.Opportunities) == 0 {
		t.Errorf("cdn view %+v", d.CDN)
	}

	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, "https://ex.com/", s, pages, opts); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Core Web Vitals", "Fails Core Web Vitals", "Phones", "Desktop", "CDN &amp; caching", "Crawler Hints", "DYNAMIC → DYNAMIC"} {
		if !strings.Contains(out, want) {
			t.Errorf("PDF misses %q", want)
		}
	}
	if n := strings.Count(out, `<section class="sheet`); n != d.Sheets {
		t.Errorf("rendered %d sheets, data says %d", n, d.Sheets)
	}
}

func TestCoreWebVitalsNotMeasured(t *testing.T) {
	s, pages := backlogFixture()
	s.ResponseTimePercentiles = types.PercentileData{P50: 300, P90: 900}
	d := BuildPDFData("https://ex.com/", s, pages, PDFOptions{CWV: &types.CWVReport{Source: "none", Missing: "no-key"}})
	if d.CWV == nil || d.CWV.Measured || !strings.Contains(d.CWV.Missing, "No Google API key") || len(d.CWV.Proxy) != 2 {
		t.Fatalf("view %+v", d.CWV)
	}
	var buf bytes.Buffer
	GeneratePrintHTML(&buf, "https://ex.com/", s, pages, PDFOptions{CWV: &types.CWVReport{Source: "none", Missing: "no-key"}})
	if !strings.Contains(buf.String(), "Not measured") || !strings.Contains(buf.String(), "MICELIO_CRUX_KEY") {
		t.Error("not-measured state missing")
	}
	if d.CDN != nil {
		t.Error("no CDN probe, no CDN sheet")
	}
}

