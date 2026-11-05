package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/report"
	"github.com/micelio/micelio/internal/types"
)

// PDF export of a crawl: GET /api/crawl/{id}/export/pdf. Each export starts
// its own headless Chrome; one runs at a time so a few clicks cannot start
// several Chromes.

var pdfSlot = make(chan struct{}, 1)

// printPDF is swapped in tests so they do not need Chrome.
var printPDF = browser.PrintPDF

func registerPDFExportRoute(mux *http.ServeMux, store *UiStore, manager *CrawlManager) {
	// The backlog as CSV for JIRA's import (one ticket per row).
	mux.HandleFunc("GET /api/crawl/{id}/export/backlog.csv", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		in, ok := reportInputs(w, r, id, store, manager)
		if !ok {
			return
		}
		var buf strings.Builder
		in.results.mu.RLock()
		err := report.BacklogCSV(&buf, in.seedURL, in.stats, in.pages, in.opts)
		in.results.mu.RUnlock()
		if err != nil {
			jsonError(w, "backlog export failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", strings.TrimSuffix(pdfFileName(in.seedURL, id), ".pdf")+"-backlog.csv"))
		w.Write([]byte(buf.String()))
	})

	mux.HandleFunc("GET /api/crawl/{id}/export/pdf", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		in, ok := reportInputs(w, r, id, store, manager)
		if !ok {
			return
		}
		results, seedURL, stats, pages := in.results, in.seedURL, in.stats, in.pages

		select {
		case pdfSlot <- struct{}{}:
			defer func() { <-pdfSlot }()
		case <-r.Context().Done():
			return
		case <-time.After(30 * time.Second):
			jsonError(w, "another PDF is being generated, try again", http.StatusServiceUnavailable)
			return
		}

		// Universe screenshots load the crawl through the API, so they are
		// taken before the results lock is held.
		opts := in.opts
		if r.URL.Query().Get("universe") != "0" {
			results.mu.RLock()
			keys := report.BacklogViews(seedURL, stats, pages, opts)
			results.mu.RUnlock()
			opts.Universe = universeShots(r.Context(), id, keys)
		}

		// Pages are read only while the HTML is built; Chrome prints
		// without holding the results lock.
		var html strings.Builder
		results.mu.RLock()
		err := report.GeneratePrintHTML(&html, seedURL, stats, pages, opts)
		results.mu.RUnlock()
		var pdf []byte
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			pdf, err = printPDF(ctx, html.String())
		}
		if err != nil {
			log.Printf("pdf export %s: %v", id, err)
			msg := "PDF export failed"
			if isChromeMissing(err) {
				msg = "PDF export needs Google Chrome or Chromium installed"
			}
			jsonError(w, msg, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", pdfFileName(seedURL, id)))
		w.Write(pdf)
	})
}

type reportInput struct {
	results *CrawlResults
	seedURL string
	stats   *types.CrawlStats
	pages   []*types.PageData
	opts    report.PDFOptions
}

// reportInputs loads what the PDF and backlog exports need; it writes the
// error response itself when the crawl cannot be read.
func reportInputs(w http.ResponseWriter, r *http.Request, id string, store *UiStore, manager *CrawlManager) (reportInput, bool) {
	results := manager.GetOrLoadResults(id)
	if results == nil {
		jsonError(w, "results not available", http.StatusNotFound)
		return reportInput{}, false
	}
	in := reportInput{results: results}
	if job, err := store.GetCrawlJob(id); err == nil && job != nil {
		in.seedURL = job.SeedURL
	}
	results.mu.RLock()
	raw, err := json.Marshal(results.Stats)
	in.pages = results.Pages
	in.opts = report.PDFOptions{Totals: statsTotals(results.Stats)}
	results.mu.RUnlock()
	var stats types.CrawlStats
	if err == nil {
		err = json.Unmarshal(raw, &stats)
	}
	if err != nil {
		jsonError(w, "stats not readable", http.StatusInternalServerError)
		return reportInput{}, false
	}
	in.stats = &stats
	if r.URL.Query().Get("web") != "0" && in.seedURL != "" {
		in.opts.CWV, in.opts.CDN = webChecks(r.Context(), id, in.seedURL, store, manager)
	}
	return in, true
}

// statsTotals reads the total* counters stats keep next to capped lists.
func statsTotals(stats map[string]interface{}) map[string]int {
	out := map[string]int{}
	for k, v := range stats {
		if !strings.HasPrefix(k, "total") {
			continue
		}
		switch n := v.(type) {
		case int:
			out[k] = n
		case float64:
			out[k] = int(n)
		}
	}
	return out
}

func pdfFileName(seedURL, id string) string {
	host := seedURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimPrefix(strings.SplitN(host, "/", 2)[0], "www.")
	var b strings.Builder
	for _, c := range host {
		if c == '.' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	name := b.String()
	if name == "" {
		name = "crawl-" + id
	}
	return "micelio-" + name + "-" + time.Now().Format("2006-01-02") + ".pdf"
}
