package server

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/logs"
	"github.com/micelio/micelio/internal/report"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"github.com/micelio/micelio/internal/webhook"
)

const (
	maxRequestBody        = 1 << 20 // 1 MB
	edgeSamplingThreshold = 5000
	edgesPerNodeSample    = 15
	maxGraphNodes         = 50_000
)

var validSortFields = map[string]bool{
	"url": true, "statusCode": true, "responseTimeMs": true, "title": true,
	"wordCount": true, "depth": true, "inlinks": true, "pageRank": true,
	"htmlBytes": true, "totalBytes": true,
}

// ValidSettings defines the allowed setting keys and their types.
var ValidSettings = map[string]string{
	"defaultDepth": "number", "defaultLimit": "number", "defaultConcurrency": "number",
	"defaultDelay": "number", "defaultUserAgent": "string",
	"psiKey": "string", "aiProvider": "string", "aiModel": "string", "aiKey": "string",
	"gscKeyFile": "string", "gscProperty": "string", "gscDays": "number",
	"ga4KeyFile": "string", "ga4Property": "string", "ga4Days": "number",
	"cruxKey":         "string",
	"plausibleApiKey": "string", "plausibleSiteId": "string",
	"plausibleHost": "string", "plausibleDays": "number",
	"defaultEmbeddings": "boolean", "embeddingModel": "string", "similarityThreshold": "number",
	"defaultNgrams": "boolean", "defaultLinkIntelligence": "boolean",
	"liMaxSuggestions": "number", "liNoCentrality": "boolean",
	"defaultSitemapOut": "boolean", "defaultOutputFormat": "string",
	"defaultHtmlReport": "boolean", "defaultCheckExternal": "boolean",
	"defaultJsRendering": "boolean", "defaultRespectRobots": "boolean",
	"defaultShowBlockedInternal": "boolean",
	"alertWebhookUrl": "string", "alertSlackUrl": "string",
}

// envFallbacks maps setting keys to environment variable names.
var envFallbacks = map[string]string{
	"psiKey":          "MICELIO_PSI_KEY",
	"aiKey":           "MICELIO_AI_KEY",
	"cruxKey":         "MICELIO_CRUX_KEY",
	"plausibleApiKey": "MICELIO_PLAUSIBLE_KEY",
}

// CreateAPIHandler returns an http.Handler for all /api/* routes.
func CreateAPIHandler(store *UiStore, manager *CrawlManager) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/crawl/start", func(w http.ResponseWriter, r *http.Request) {
		config, err := readJSON(r)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Validate seed URL against SSRF
		if seedURL, ok := config["seedUrl"].(string); ok && crawler.IsPrivateURL(seedURL) {
			jsonError(w, "seed URL targets a private/loopback address", http.StatusBadRequest)
			return
		}
		job, err := store.CreateCrawlJob(config)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := manager.StartCrawl(job.ID, config); err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, map[string]interface{}{"id": job.ID, "status": "running"})
	})

	mux.HandleFunc("GET /api/crawl/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		job, err := store.GetCrawlJob(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if job == nil {
			jsonError(w, "crawl not found", http.StatusNotFound)
			return
		}
		// Include live stats for active crawls
		if codes := manager.GetActiveStatusCodes(id); codes != nil {
			type jobWithLiveStats struct {
				*CrawlJob
				StatusCodes map[string]int `json:"statusCodes"`
				Queued      int            `json:"queued"`
				TotalSeen   int            `json:"totalSeen"`
				Excluded    int            `json:"excluded"`
			}
			active := manager.GetStatus()
			resp := jobWithLiveStats{CrawlJob: job, StatusCodes: codes}
			if active != nil && active.ID == id {
				resp.Queued = active.Queued
				resp.TotalSeen = active.TotalSeen
				resp.Excluded = active.Excluded
			}
			jsonOK(w, resp)
			return
		}
		jsonOK(w, job)
	})

	mux.HandleFunc("POST /api/crawl/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.PauseCrawl(r.PathValue("id")); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "paused"})
	})

	mux.HandleFunc("POST /api/crawl/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.ResumeCrawl(r.PathValue("id")); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "running"})
	})

	mux.HandleFunc("POST /api/crawl/{id}/restart", func(w http.ResponseWriter, r *http.Request) {
		// Optional config override from request body
		var configOverride map[string]interface{}
		if r.Body != nil && r.ContentLength != 0 {
			var err error
			configOverride, err = readJSON(r)
			if err != nil {
				jsonError(w, "invalid request body", http.StatusBadRequest)
				return
			}
		}
		newID, err := manager.RestartCrawl(r.PathValue("id"), configOverride)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]interface{}{"id": newID, "status": "running"})
	})

	mux.HandleFunc("POST /api/crawl/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.StopCrawl(r.PathValue("id")); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "stopping"})
	})

	mux.HandleFunc("POST /api/crawl/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.CancelCrawl(r.PathValue("id")); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "cancelled"})
	})

	mux.HandleFunc("GET /api/crawl/{id}/results", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		jsonGzip(w, r, results)
	})

	mux.HandleFunc("GET /api/crawl/{id}/pages", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		if offset < 0 {
			offset = 0
		}

		results.mu.RLock()
		ready := results.PagesReady
		dbPath := results.DBPath
		results.mu.RUnlock()

		// If pages aren't loaded yet, serve directly from SQLite
		if !ready && dbPath != "" {
			search := strings.ToLower(q.Get("search"))
			cs, err := storage.NewCrawlStore(dbPath)
			if err != nil {
				jsonError(w, "failed to open crawl db", http.StatusInternalServerError)
				return
			}
			defer cs.Close()
			pages, total, err := cs.GetPagesRange(offset, limit, search)
			if err != nil {
				jsonError(w, "failed to read pages", http.StatusInternalServerError)
				return
			}
			jsonGzip(w, r, map[string]interface{}{
				"pages":         pages,
				"total":         total,
				"totalAll":      total,
				"offset":        offset,
				"limit":         limit,
				"templateTypes": []string{},
				"loading":       true,
			})
			return
		}

		results.mu.RLock()
		defer results.mu.RUnlock()

		sortField := q.Get("sort")
		if sortField != "" && !validSortFields[sortField] {
			sortField = "" // ignore unknown sort fields
		}
		sortOrder := q.Get("order")
		if sortOrder != "asc" && sortOrder != "desc" {
			sortOrder = "asc"
		}
		filtered := filterAndSortPages(results.Pages,
			strings.ToLower(q.Get("search")),
			q.Get("status"), q.Get("indexable"), q.Get("template"),
			q.Get("classification"), q.Get("issue"), q.Get("robotsBlocked"),
			sortField, sortOrder,
		)

		total := len(filtered)
		end := offset + limit
		if end > total {
			end = total
		}
		var pageSlice interface{}
		if offset >= total {
			pageSlice = []interface{}{}
		} else {
			pageSlice = filtered[offset:end]
		}
		tmpl := results.TemplateTypes
		if tmpl == nil {
			tmpl = collectTemplateTypes(results.Pages)
		}
		jsonGzip(w, r, map[string]interface{}{
			"pages":         pageSlice,
			"total":         total,
			"totalAll":      len(results.Pages),
			"offset":        offset,
			"limit":         limit,
			"templateTypes": tmpl,
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/page-html", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		pageURL := r.URL.Query().Get("url")
		if pageURL == "" {
			jsonError(w, "url parameter required", http.StatusBadRequest)
			return
		}
		if results.DBPath == "" {
			jsonError(w, "no database for this crawl", http.StatusNotFound)
			return
		}
		cs, err := storage.NewCrawlStore(results.DBPath)
		if err != nil {
			jsonError(w, "failed to open crawl db", http.StatusInternalServerError)
			return
		}
		defer cs.Close()
		sourceHTML, renderedHTML, err := cs.GetPageHTML(pageURL)
		if err != nil {
			jsonError(w, "failed to read html", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"url":          pageURL,
			"sourceHtml":   sourceHTML,
			"renderedHtml": renderedHTML,
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/page-inlinks", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		results.mu.RLock()
		defer results.mu.RUnlock()
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			jsonError(w, "url parameter required", http.StatusBadRequest)
			return
		}
		var sources []string
		if results.InlinkIndex != nil {
			// O(1) lookup from pre-computed inverted index
			seen := make(map[string]bool)
			for _, variant := range inlinkVariants(targetURL) {
				for _, src := range results.InlinkIndex[variant] {
					if !seen[src] {
						seen[src] = true
						sources = append(sources, src)
					}
				}
			}
		} else {
			// Fallback: scan pages (only if index not available)
			variants := make(map[string]bool, 3)
			for _, v := range inlinkVariants(targetURL) {
				variants[v] = true
			}
			for _, p := range results.Pages {
				for _, link := range p.InternalLinks {
					if variants[link] {
						sources = append(sources, p.URL)
						break
					}
				}
			}
		}
		if sources == nil {
			sources = []string{}
		}
		jsonOK(w, map[string]interface{}{"url": targetURL, "sources": sources})
	})

	mux.HandleFunc("GET /api/crawl/{id}/anchor-stats", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		if results.AnchorStats != nil {
			jsonOK(w, results.AnchorStats)
		} else {
			jsonOK(w, buildAnchorStats(results.Pages))
		}
	})

	mux.HandleFunc("GET /api/crawl/{id}/directory-tree", func(w http.ResponseWriter, r *http.Request) {
		crawlID := r.PathValue("id")
		// Fast path: check cached results (non-blocking)
		if results := manager.GetCachedResults(crawlID); results != nil {
			if results.DirTree != nil {
				jsonOK(w, results.DirTree)
				return
			}
			if results.Pages != nil {
				jsonOK(w, buildDirectoryTree(results.Pages))
				return
			}
		}
		// Fallback: build from page_index in DB (instant even for 742K pages)
		dbPath := ""
		if job, err := store.GetCrawlJob(crawlID); err == nil && job != nil {
			dbPath = job.DBPath
		}
		if dbPath != "" {
			if tree := buildDirectoryTreeFromDB(dbPath); tree != nil {
				jsonOK(w, tree)
				return
			}
		}
		// Last resort: block and load
		results := manager.GetOrLoadResults(crawlID)
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		if results.DirTree != nil {
			jsonOK(w, results.DirTree)
		} else {
			jsonOK(w, buildDirectoryTree(results.Pages))
		}
	})

	mux.HandleFunc("GET /api/crawl/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetQuickStats(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		results.mu.RLock()
		stats := capStatsForAPI(results.Stats)
		resp := map[string]interface{}{
			"stats":           stats,
			"pageCount":       results.PageCount,
			"analysisPending": results.AnalysisPending,
			"pagesReady":      results.PagesReady,
			"loadingProgress": int(results.loadingProgress.Load()),
		}
		results.mu.RUnlock()
		jsonOK(w, resp)
	})

	mux.HandleFunc("GET /api/crawl/{id}/stats/filtered", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		results.mu.RLock()
		defer results.mu.RUnlock()
		if !results.PagesReady {
			jsonError(w, "pages still loading", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		template := q.Get("template")
		if template == "" || template == "all" {
			jsonOK(w, map[string]interface{}{
				"stats":     results.Stats,
				"pageCount": results.PageCount,
			})
			return
		}
		var filtered []*types.PageData
		for _, p := range results.Pages {
			if p.TemplateType == template {
				filtered = append(filtered, p)
			}
		}
		stats := analysis.GenerateReport(filtered, 0, analysis.ReportConfig{})
		data, err := json.Marshal(stats)
		if err != nil {
			jsonError(w, "failed to compute filtered stats", http.StatusInternalServerError)
			return
		}
		var statsMap map[string]interface{}
		if err := json.Unmarshal(data, &statsMap); err != nil {
			jsonError(w, "failed to serialize filtered stats", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"stats":     statsMap,
			"pageCount": len(filtered),
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/loading-progress", func(w http.ResponseWriter, r *http.Request) {
		found, ready, progress, count := manager.GetLoadingProgress(r.PathValue("id"))
		if !found {
			jsonError(w, "results not in cache", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]interface{}{
			"pagesReady":      ready,
			"loadingProgress": progress,
			"pageCount":       count,
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/graph", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		if results.AnalysisPending {
			jsonOK(w, map[string]interface{}{
				"nodes": []interface{}{},
				"edges": []interface{}{},
				"meta":  map[string]interface{}{"pending": true},
			})
			return
		}
		graph := buildGraphPayload(results)
		jsonGzip(w, r, graph)
	})

	mux.HandleFunc("GET /api/crawl/{id}/graph/subgraph", func(w http.ResponseWriter, r *http.Request) {
		crawlID := r.PathValue("id")
		rootURL := r.URL.Query().Get("root")
		depth := 1
		if d := r.URL.Query().Get("depth"); d != "" {
			if v, err := strconv.Atoi(d); err == nil && v >= 0 && v <= 5 {
				depth = v
			}
		}
		maxNodes := 500
		if m := r.URL.Query().Get("max"); m != "" {
			if v, err := strconv.Atoi(m); err == nil && v > 0 && v <= 2000 {
				maxNodes = v
			}
		}

		// Fast path: check if in-memory graph is already loaded (non-blocking)
		if results := manager.GetCachedResults(crawlID); results != nil && results.Graph != nil {
			payload := buildSubgraphPayload(results, rootURL, depth, maxNodes)
			jsonGzip(w, r, payload)
			return
		}

		// Fallback: read from DB directly (instant, even for 742K-page crawls)
		dbPath := ""
		if job, err := store.GetCrawlJob(crawlID); err == nil && job != nil {
			dbPath = job.DBPath
		}
		if dbPath != "" {
			payload := buildSubgraphFromDB(dbPath, rootURL, depth, maxNodes)
			if payload != nil {
				jsonGzip(w, r, payload)
				return
			}
		}

		jsonOK(w, map[string]interface{}{
			"nodes": []interface{}{},
			"edges": []interface{}{},
			"meta":  map[string]interface{}{"pending": true},
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/graph/search", func(w http.ResponseWriter, r *http.Request) {
		crawlID := r.PathValue("id")
		q := strings.ToLower(r.URL.Query().Get("q"))
		if q == "" {
			jsonOK(w, map[string]interface{}{"results": []interface{}{}})
			return
		}
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
				limit = v
			}
		}
		// Fast path: in-memory graph available (non-blocking)
		if results := manager.GetCachedResults(crawlID); results != nil && results.Graph != nil {
			payload := buildGraphSearchPayload(results, q, limit)
			jsonOK(w, payload)
			return
		}
		// Fallback: search via page_index in DB
		dbPath := ""
		if job, err := store.GetCrawlJob(crawlID); err == nil && job != nil {
			dbPath = job.DBPath
		}
		if dbPath != "" {
			payload := buildGraphSearchFromDB(dbPath, q, limit)
			if payload != nil {
				jsonOK(w, payload)
				return
			}
		}
		jsonOK(w, map[string]interface{}{"results": []interface{}{}})
	})

	mux.HandleFunc("GET /api/crawl/{id}/export/csv", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=crawl-%s.csv", r.PathValue("id")))
		csvWriter := csv.NewWriter(w)
		csvWriter.Write([]string{"url", "statusCode", "title", "metaDescription", "h1", "wordCount", "depth", "internalLinks", "externalLinks", "inlinks", "canonical", "indexable"})
		for _, p := range results.Pages {
			title := ""
			if p.Title != nil {
				title = p.Title.Text
			}
			desc := ""
			if p.MetaDescription != nil {
				desc = p.MetaDescription.Text
			}
			h1 := ""
			if len(p.Headings.H1) > 0 {
				h1 = p.Headings.H1[0]
			}
			canonical := ""
			if p.Canonical != nil {
				canonical = *p.Canonical
			}
			csvWriter.Write([]string{
				p.URL,
				strconv.Itoa(p.StatusCode),
				title, desc, h1,
				strconv.Itoa(p.WordCount),
				strconv.Itoa(p.Depth),
				strconv.Itoa(max(len(p.InternalLinks), p.InternalLinkCount)),
				strconv.Itoa(max(len(p.ExternalLinks), p.ExternalLinkCount)),
				strconv.Itoa(p.Inlinks),
				canonical,
				strconv.FormatBool(p.Indexability.Indexable),
			})
		}
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			// Can't change status code after body started; log it
			fmt.Fprintf(os.Stderr, "csv export error: %v\n", err)
		}
	})

	mux.HandleFunc("GET /api/crawl/{id}/export/filtered-csv", func(w http.ResponseWriter, r *http.Request) {
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		results.mu.RLock()
		defer results.mu.RUnlock()
		q := r.URL.Query()
		sortField := q.Get("sort")
		if sortField != "" && !validSortFields[sortField] {
			sortField = ""
		}
		sortOrder := q.Get("order")
		if sortOrder != "asc" && sortOrder != "desc" {
			sortOrder = "asc"
		}
		filtered := filterAndSortPages(results.Pages,
			strings.ToLower(q.Get("search")),
			q.Get("status"), q.Get("indexable"), q.Get("template"),
			q.Get("classification"), q.Get("issue"), q.Get("robotsBlocked"),
			sortField, sortOrder,
		)
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=crawl-%s-filtered-%d.csv", r.PathValue("id"), len(filtered)))
		csvWriter := csv.NewWriter(w)
		csvWriter.Write([]string{"url", "statusCode", "title", "metaDescription", "h1", "wordCount", "depth", "internalLinks", "externalLinks", "inlinks", "canonical", "indexable"})
		for _, p := range filtered {
			title := ""
			if p.Title != nil {
				title = p.Title.Text
			}
			desc := ""
			if p.MetaDescription != nil {
				desc = p.MetaDescription.Text
			}
			h1 := ""
			if len(p.Headings.H1) > 0 {
				h1 = p.Headings.H1[0]
			}
			canonical := ""
			if p.Canonical != nil {
				canonical = *p.Canonical
			}
			csvWriter.Write([]string{
				p.URL,
				strconv.Itoa(p.StatusCode),
				title, desc, h1,
				strconv.Itoa(p.WordCount),
				strconv.Itoa(p.Depth),
				strconv.Itoa(max(len(p.InternalLinks), p.InternalLinkCount)),
				strconv.Itoa(max(len(p.ExternalLinks), p.ExternalLinkCount)),
				strconv.Itoa(p.Inlinks),
				canonical,
				strconv.FormatBool(p.Indexability.Indexable),
			})
		}
		csvWriter.Flush()
	})

	mux.HandleFunc("DELETE /api/crawl/{id}", func(w http.ResponseWriter, r *http.Request) {
		deleted, err := store.DeleteCrawlJob(r.PathValue("id"))
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !deleted {
			jsonError(w, "crawl not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]bool{"deleted": true})
	})

	mux.HandleFunc("GET /api/crawls", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := store.ListCrawlJobs()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{"crawls": jobs, "total": len(jobs)})
	})

	mux.HandleFunc("POST /api/crawl/diff", func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSON(r)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		oldID, _ := body["oldId"].(string)
		newID, _ := body["newId"].(string)
		if oldID == "" || newID == "" {
			jsonError(w, "oldId and newId required", http.StatusBadRequest)
			return
		}
		oldResults := manager.GetOrLoadResults(oldID)
		newResults := manager.GetOrLoadResults(newID)
		if oldResults == nil {
			jsonError(w, "old crawl results not available", http.StatusNotFound)
			return
		}
		if newResults == nil {
			jsonError(w, "new crawl results not available", http.StatusNotFound)
			return
		}

		// Check if full comparison is requested
		full, _ := body["full"].(bool)
		if full {
			oldStats := statsMapToCrawlStats(oldResults.Stats)
			newStats := statsMapToCrawlStats(newResults.Stats)
			comparison := report.ComputeComparison(oldResults.Pages, oldStats, newResults.Pages, newStats)
			jsonOK(w, map[string]interface{}{"oldId": oldID, "newId": newID, "comparison": comparison})
			return
		}

		diff := report.ComputeDiff(oldResults.Pages, newResults.Pages)
		jsonOK(w, map[string]interface{}{"oldId": oldID, "newId": newID, "diff": diff})
	})

	// Presets
	mux.HandleFunc("GET /api/presets", func(w http.ResponseWriter, r *http.Request) {
		presets, err := store.ListPresets()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, presets)
	})

	mux.HandleFunc("POST /api/presets", func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSON(r)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		name, _ := body["name"].(string)
		config, _ := body["config"].(map[string]interface{})
		if name == "" || config == nil {
			jsonError(w, "name and config required", http.StatusBadRequest)
			return
		}
		preset, err := store.SavePreset(name, config)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, preset)
	})

	mux.HandleFunc("DELETE /api/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
		deleted, err := store.DeletePreset(r.PathValue("id"))
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !deleted {
			jsonError(w, "preset not found or is built-in", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]bool{"deleted": true})
	})

	// Settings
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.GetSettings()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		applyEnvFallbacks(settings)
		maskSecrets(settings)
		jsonOK(w, settings)
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSON(r)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		valid, rejected := validateSettings(body)
		if len(valid) > 0 {
			if err := store.UpdateSettings(valid); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		jsonOK(w, map[string]interface{}{"valid": valid, "rejected": rejected})
	})

	// ── Schedules ──

	mux.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		schedules, err := scheduler.ListSchedules()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if schedules == nil {
			schedules = []scheduler.State{}
		}
		// Add human-readable description and active status
		type scheduleWithMeta struct {
			scheduler.State
			Description string `json:"description"`
			Active      bool   `json:"active"`
		}
		result := make([]scheduleWithMeta, len(schedules))
		for i, s := range schedules {
			result[i] = scheduleWithMeta{
				State:       s,
				Description: scheduler.DescribeCron(s.Cron),
				Active:      manager.IsSchedulerActive(s.ID),
			}
		}
		jsonOK(w, result)
	})

	mux.HandleFunc("POST /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSON(r)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		siteURL, _ := body["url"].(string)
		cronExpr, _ := body["cron"].(string)
		if siteURL == "" || cronExpr == "" {
			jsonError(w, "url and cron are required", http.StatusBadRequest)
			return
		}
		outputDir, _ := body["outputDir"].(string)
		if outputDir == "" {
			outputDir = "scheduled-crawls"
		}
		// Reject absolute paths to prevent writing outside project
		if filepath.IsAbs(outputDir) {
			jsonError(w, "outputDir must be a relative path", http.StatusBadRequest)
			return
		}
		// Validate seed URL against SSRF
		if crawler.IsPrivateURL(siteURL) {
			jsonError(w, "seed URL targets a private/loopback address", http.StatusBadRequest)
			return
		}

		webhookURL, _ := body["webhook"].(string)
		if webhookURL != "" {
			if err := webhook.ValidateURL(webhookURL); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Build crawl config from body
		crawlCfg := types.CrawlConfig{
			SeedURL: siteURL,
		}
		if v, ok := body["depth"].(float64); ok {
			crawlCfg.MaxDepth = int(v)
		} else {
			crawlCfg.MaxDepth = 10
		}
		if v, ok := body["limit"].(float64); ok {
			crawlCfg.MaxPages = int(v)
		} else {
			crawlCfg.MaxPages = 1000
		}
		if v, ok := body["concurrency"].(float64); ok {
			crawlCfg.Concurrency = int(v)
		} else {
			crawlCfg.Concurrency = 5
		}
		if v, ok := body["delay"].(float64); ok {
			crawlCfg.DelayMs = int(v)
		} else {
			crawlCfg.DelayMs = 200
		}

		cfg := scheduler.Config{
			URL:         siteURL,
			CronExpr:    cronExpr,
			OutputDir:   outputDir,
			CrawlConfig: crawlCfg,
		}
		if webhookURL != "" {
			cfg.Webhook = &webhook.Options{URL: webhookURL}
		}

		s, err := scheduler.New(cfg)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Check for duplicate before starting
		state := s.GetState()
		if manager.IsSchedulerActive(state.ID) {
			jsonError(w, "a schedule with this URL and cron already exists", http.StatusConflict)
			return
		}
		// Start the scheduler in a background goroutine
		go s.Run()
		manager.TrackScheduler(s)
		jsonOK(w, map[string]interface{}{
			"id":          state.ID,
			"url":         state.URL,
			"cron":        state.Cron,
			"description": scheduler.DescribeCron(state.Cron),
			"nextRun":     state.NextRun,
			"outputDir":   state.OutputDir,
		})
	})

	mux.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// Stop if actively running
		manager.StopScheduler(id)
		// Delete state file
		if err := scheduler.DeleteSchedule(id); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]bool{"deleted": true})
	})

	// ── Log Analysis endpoints ──

	mux.HandleFunc("POST /api/logs/upload", func(w http.ResponseWriter, r *http.Request) {
		// Stream the multipart body directly to disk: no 500MB spill to $TMPDIR,
		// no io.Copy duplication, O(1) memory regardless of file size.
		// Supports multiple "file" parts in one request — all files are merged
		// into a single unified analysis job.
		reader, err := r.MultipartReader()
		if err != nil {
			jsonError(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
			return
		}

		uploadStart := time.Now()
		var formatHint string
		var job *LogJob
		var destPaths []string
		var destFilenames []string
		var fileMeta []LogJobFile
		var totalBytesWritten int64
		contentLength := r.ContentLength // -1 if unknown (chunked)

		logsDir := filepath.Join(store.uiDir, "logs")
		if err := os.MkdirAll(logsDir, 0755); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cleanupOnError := func() {
			for _, p := range destPaths {
				os.Remove(p)
			}
		}

		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				cleanupOnError()
				jsonError(w, "multipart read error: "+err.Error(), http.StatusBadRequest)
				return
			}

			switch part.FormName() {
			case "format":
				buf := make([]byte, 64)
				n, _ := io.ReadFull(part, buf)
				formatHint = strings.TrimSpace(string(buf[:n]))
				io.Copy(io.Discard, part)
			case "file", "files", "files[]":
				safeName := filepath.Base(part.FileName())
				if safeName == "." || safeName == ".." || safeName == "" {
					cleanupOnError()
					jsonError(w, "invalid filename", http.StatusBadRequest)
					return
				}
				filename := part.FileName()

				// Lazily create the job on the first file so we have a jobID
				// to tag upload-progress events with.
				if job == nil {
					displayName := safeName
					created, err := store.CreateLogJob(displayName, 0)
					if err != nil {
						jsonError(w, err.Error(), http.StatusInternalServerError)
						return
					}
					job = created
				}

				jobDir := filepath.Join(logsDir, job.ID)
				if err := os.MkdirAll(jobDir, 0755); err != nil {
					cleanupOnError()
					jsonError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				// Disambiguate duplicate filenames across the multipart body.
				destPath := filepath.Join(jobDir, safeName)
				if _, err := os.Stat(destPath); err == nil {
					destPath = filepath.Join(jobDir, fmt.Sprintf("%d-%s", len(destPaths), safeName))
				}
				dst, err := os.Create(destPath)
				if err != nil {
					cleanupOnError()
					jsonError(w, err.Error(), http.StatusInternalServerError)
					return
				}

				const uploadProgressEvery int64 = 10 << 20
				buf := make([]byte, 256*1024)
				var fileBytes, lastEmit int64
				var readErr error
				fileIdx := len(destPaths)
				for {
					n, er := part.Read(buf)
					if n > 0 {
						if _, we := dst.Write(buf[:n]); we != nil {
							readErr = we
							break
						}
						fileBytes += int64(n)
						totalBytesWritten += int64(n)
						if fileBytes-lastEmit >= uploadProgressEvery {
							lastEmit = fileBytes
							manager.emit(ProgressEvent{
								Type: "log_upload_progress",
								Data: map[string]interface{}{
									"jobId":           job.ID,
									"fileIndex":       fileIdx,
									"filename":        filename,
									"bytesReceived":   fileBytes,
									"fileSize":        contentLength,
									"totalBytes":      totalBytesWritten,
									"totalExpected":   contentLength,
								},
							})
						}
					}
					if er != nil {
						if er != io.EOF {
							readErr = er
						}
						break
					}
				}
				if cerr := dst.Close(); cerr != nil && readErr == nil {
					readErr = cerr
				}
				if readErr != nil {
					os.Remove(destPath)
					cleanupOnError()
					store.UpdateLogJob(job.ID, map[string]interface{}{
						"status":   "failed",
						"errorMsg": readErr.Error(),
					})
					jsonError(w, "upload failed: "+readErr.Error(), http.StatusInternalServerError)
					return
				}
				destPaths = append(destPaths, destPath)
				destFilenames = append(destFilenames, filename)
				fileMeta = append(fileMeta, LogJobFile{
					Filename: filename,
					Size:     fileBytes,
					Status:   "uploaded",
				})
			default:
				io.Copy(io.Discard, part)
			}
		}

		if job == nil || len(destPaths) == 0 {
			jsonError(w, "missing file field", http.StatusBadRequest)
			return
		}

		// Final upload-progress event: snap to 100% for the upload phase.
		manager.emit(ProgressEvent{
			Type: "log_upload_progress",
			Data: map[string]interface{}{
				"jobId":         job.ID,
				"bytesReceived": totalBytesWritten,
				"totalExpected": totalBytesWritten,
				"totalBytes":    totalBytesWritten,
				"complete":      true,
				"fileCount":     len(destPaths),
			},
		})

		// Persist file list + aggregate size + upload duration.
		uploadMs := time.Since(uploadStart).Milliseconds()
		displayName := destFilenames[0]
		if len(destFilenames) > 1 {
			displayName = fmt.Sprintf("%s (+%d more)", destFilenames[0], len(destFilenames)-1)
		}
		store.UpdateLogJob(job.ID, map[string]interface{}{
			"fileSize": totalBytesWritten,
			"files":    fileMeta,
			"filename": displayName,
			"uploadMs": uploadMs,
		})

		// Respond first, then spawn analysis (client also polls as fallback).
		jsonOK(w, map[string]interface{}{
			"id":        job.ID,
			"status":    "processing",
			"fileSize":  totalBytesWritten,
			"fileCount": len(destPaths),
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go manager.runLogAnalysisMulti(job.ID, destPaths, destFilenames, logs.Format(formatHint))
	})

	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := store.ListLogJobs()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if jobs == nil {
			jobs = []LogJob{}
		}
		jsonOK(w, map[string]interface{}{"jobs": jobs, "total": len(jobs)})
	})

	mux.HandleFunc("GET /api/logs/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		job, err := store.GetLogJob(r.PathValue("id"))
		if err != nil || job == nil {
			jsonError(w, "log job not found", http.StatusNotFound)
			return
		}
		jsonOK(w, job)
	})

	mux.HandleFunc("GET /api/logs/{id}/overview", func(w http.ResponseWriter, r *http.Request) {
		raw, err := store.LoadLogStats(r.PathValue("id"))
		if err != nil || raw == nil {
			jsonError(w, "stats not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	})

	mux.HandleFunc("GET /api/logs/{id}/bots", func(w http.ResponseWriter, r *http.Request) {
		raw, err := store.LoadLogStats(r.PathValue("id"))
		if err != nil || raw == nil {
			jsonError(w, "stats not found", http.StatusNotFound)
			return
		}
		var stats map[string]interface{}
		if err := json.Unmarshal(raw, &stats); err != nil {
			jsonError(w, "corrupt stats data", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"botHits":      stats["botHits"],
			"botDailyHits": stats["botDailyHits"],
		})
	})

	mux.HandleFunc("GET /api/logs/{id}/urls", func(w http.ResponseWriter, r *http.Request) {
		raw, err := store.LoadLogStats(r.PathValue("id"))
		if err != nil || raw == nil {
			jsonError(w, "stats not found", http.StatusNotFound)
			return
		}
		var stats map[string]interface{}
		if err := json.Unmarshal(raw, &stats); err != nil {
			jsonError(w, "corrupt stats data", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{"urls": stats["topUrls"]})
	})

	mux.HandleFunc("POST /api/logs/{id}/merge", func(w http.ResponseWriter, r *http.Request) {
		logID := r.PathValue("id")
		// Read crawl job ID from body
		var body struct {
			CrawlID string `json:"crawlId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CrawlID == "" {
			jsonError(w, "missing crawlId", http.StatusBadRequest)
			return
		}

		// Helper: emit a merge_progress WS event so the UI can show what's
		// happening during the (sometimes multi-second) crawl-page load step.
		startedAt := time.Now()
		emitPhase := func(phase string, extra map[string]interface{}) {
			data := map[string]interface{}{
				"logId":     logID,
				"crawlId":   body.CrawlID,
				"phase":     phase,
				"elapsedMs": time.Since(startedAt).Milliseconds(),
			}
			for k, v := range extra {
				data[k] = v
			}
			manager.emit(ProgressEvent{Type: "merge_progress", Data: data})
		}

		emitPhase("loading_log_stats", nil)
		raw, err := store.LoadLogStats(logID)
		if err != nil || raw == nil {
			emitPhase("error", map[string]interface{}{"error": "log stats not found"})
			jsonError(w, "log stats not found", http.StatusNotFound)
			return
		}
		var logStats logs.LogStats
		if err := json.Unmarshal(raw, &logStats); err != nil {
			emitPhase("error", map[string]interface{}{"error": "corrupt log stats"})
			jsonError(w, "corrupt log stats", http.StatusInternalServerError)
			return
		}

		// Try fast path: load lightweight page_index directly from crawl DB
		emitPhase("loading_crawl_pages", nil)
		var pages []logs.CrawlPageInfo
		crawlJob, _ := store.GetCrawlJob(body.CrawlID)
		if crawlJob != nil && crawlJob.DBPath != "" {
			cs, csErr := storage.NewCrawlStore(crawlJob.DBPath)
			if csErr == nil {
				if cs.HasPageIndex() {
					entries, idxErr := cs.GetPageIndex()
					if idxErr == nil && len(entries) > 0 {
						pages = make([]logs.CrawlPageInfo, len(entries))
						for i, e := range entries {
							pages[i] = logs.CrawlPageInfo{
								URL:        e.URL,
								StatusCode: e.StatusCode,
								Indexable:  e.Indexable,
								Canonical:  e.Canonical,
								Depth:      e.Depth,
								Inlinks:    e.Inlinks,
								WordCount:  e.WordCount,
								Title:      e.Title,
								HasNoindex: e.HasNoindex,
								IsRedirect: e.IsRedirect,
							}
						}
						fmt.Fprintf(os.Stderr, "  [merge] Loaded %d pages from page_index (fast path)\n", len(pages))
					}
				}
				cs.Close()
			}
		}

		// Slow fallback: load full pages from memory/DB
		if len(pages) == 0 {
			crawlResult := manager.GetQuickStats(body.CrawlID)
			if crawlResult == nil {
				crawlResult = manager.GetOrLoadResults(body.CrawlID)
			}
			if crawlResult == nil {
				emitPhase("error", map[string]interface{}{"error": "crawl not found"})
				jsonError(w, "crawl not found or pages unavailable on disk", http.StatusNotFound)
				return
			}
			for i := 0; i < 600; i++ {
				crawlResult.mu.RLock()
				ready := crawlResult.PagesReady
				crawlResult.mu.RUnlock()
				if ready {
					break
				}
				progress := crawlResult.loadingProgress.Load()
				emitPhase("loading_crawl_pages", map[string]interface{}{"progress": progress})
				time.Sleep(time.Second)
			}
			crawlResult.mu.RLock()
			defer crawlResult.mu.RUnlock()
			if len(crawlResult.Pages) == 0 {
				emitPhase("error", map[string]interface{}{"error": "no pages loaded"})
				jsonError(w, "crawl has no pages loaded — pages may still be loading, try again", http.StatusNotFound)
				return
			}
			pages = make([]logs.CrawlPageInfo, 0, len(crawlResult.Pages))
			for _, p := range crawlResult.Pages {
				if p == nil {
					continue
				}
				hasNoindex := false
				if p.MetaRobots != nil && strings.Contains(strings.ToLower(*p.MetaRobots), "noindex") {
					hasNoindex = true
				}
				canonical := ""
				if p.Canonical != nil {
					canonical = *p.Canonical
				}
				title := ""
				if p.Title != nil {
					title = p.Title.Text
				}
				pages = append(pages, logs.CrawlPageInfo{
					URL:        p.URL,
					StatusCode: p.StatusCode,
					Indexable:  p.Indexability.Indexable,
					Canonical:  canonical,
					Depth:      p.Depth,
					Inlinks:    p.Inlinks,
					WordCount:  p.WordCount,
					Title:      title,
					HasNoindex: hasNoindex,
					IsRedirect: p.StatusCode >= 300 && p.StatusCode < 400,
				})
			}
		}
		if len(pages) == 0 {
			emitPhase("error", map[string]interface{}{"error": "no pages"})
			jsonError(w, "no crawl pages available", http.StatusNotFound)
			return
		}

		emitPhase("preparing", map[string]interface{}{"pageCount": len(pages)})

		// Count URLs for the progress event (cheap COUNT(*) query).
		urlCount, _ := store.CountLogURLStats(logID)
		emitPhase("merging", map[string]interface{}{
			"pageCount": len(pages),
			"logUrls":   urlCount,
		})

		// Load all log URLs into memory so the DB lock is released before the
		// merge begins. ~100 B/row × 5M rows ≈ 500 MB — acceptable.
		usingCappedFallback := urlCount == 0
		var logURLs []logs.URLStat
		if usingCappedFallback {
			logURLs = logStats.TopURLs
		} else {
			emitPhase("loading_urls", map[string]interface{}{"urlCount": urlCount})
			var loadErr error
			logURLs, loadErr = store.LoadAllLogURLStats(logID)
			if loadErr != nil {
				emitPhase("error", map[string]interface{}{"error": loadErr.Error()})
				jsonError(w, "failed to load log URLs: "+loadErr.Error(), http.StatusInternalServerError)
				return
			}
		}

		iterator := func(fn func(logs.URLStat) error) error {
			for i := range logURLs {
				if err := fn(logURLs[i]); err != nil {
					return err
				}
			}
			return nil
		}

		// Pipe merged pages to a writer goroutine that does everything in a
		// single SQLite transaction (one COMMIT instead of 1000+).
		pageCh := make(chan logs.MergedPage, 10_000)
		var writeErr error
		var totalPersisted int64
		writeDone := make(chan struct{})
		go func() {
			defer close(writeDone)
			writeErr = store.WriteMergePagesBulk(logID, body.CrawlID, pageCh, func(written int64) {
				totalPersisted = written
				emitPhase("persisting", map[string]interface{}{
					"persisted": written,
					"pageCount": len(pages),
				})
			})
		}()

		summary, mergeErr := logs.StreamMergeEmit(iterator, pages, func(mp logs.MergedPage) error {
			pageCh <- mp
			return nil
		})
		close(pageCh)
		<-writeDone

		if mergeErr != nil {
			emitPhase("error", map[string]interface{}{"error": mergeErr.Error()})
			jsonError(w, "merge failed: "+mergeErr.Error(), http.StatusInternalServerError)
			return
		}
		if writeErr != nil {
			emitPhase("error", map[string]interface{}{"error": writeErr.Error()})
			jsonError(w, "persist failed: "+writeErr.Error(), http.StatusInternalServerError)
			return
		}

		if usingCappedFallback {
			summary.CappedURLs = true
		}

		emitPhase("done", map[string]interface{}{
			"pageCount": len(pages),
			"persisted": totalPersisted,
		})
		jsonOK(w, summary)
	})

	mux.HandleFunc("GET /api/logs/{id}/merged", func(w http.ResponseWriter, r *http.Request) {
		logID := r.PathValue("id")
		q := r.URL.Query()
		crawlID := q.Get("crawlId")
		if crawlID == "" {
			jsonError(w, "crawlId query parameter required", http.StatusBadRequest)
			return
		}
		page, _ := strconv.Atoi(q.Get("page"))
		pageSize, _ := strconv.Atoi(q.Get("pageSize"))
		opts := MergeQueryOpts{
			Segment:           q.Get("segment"),
			URLSearch:         q.Get("search"),
			ReasonSearch:      q.Get("reason"),
			TopBotSearch:      q.Get("topBot"),
			LogStatusFilter:   q.Get("logStatus"),
			CrawlStatusFilter: q.Get("crawlStatus"),
			IndexableFilter:   q.Get("indexable"),
			Coverage:          q.Get("coverage"),
			SortBy:            q.Get("sort"),
			Order:             q.Get("order"),
			Page:              page,
			PageSize:          pageSize,
		}
		rows, total, err := store.QueryMergePages(logID, crawlID, opts)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if rows == nil {
			rows = []logs.MergedPage{}
		}
		jsonOK(w, map[string]interface{}{
			"pages":    rows,
			"total":    total,
			"page":     opts.Page,
			"pageSize": opts.PageSize,
		})
	})

	mux.HandleFunc("POST /api/logs/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		stats, spoofed, err := manager.runBotVerification(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]interface{}{"verification": stats, "spoofed": spoofed})
	})

	mux.HandleFunc("DELETE /api/logs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// Validate ID format to prevent path traversal.
		if strings.ContainsAny(id, "/\\.") {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		// Check current status — reject if already deleting.
		job, err := store.GetLogJob(id)
		if err != nil || job == nil {
			jsonError(w, "log job not found", http.StatusNotFound)
			return
		}
		if job.Status == "deleting" {
			jsonOK(w, map[string]bool{"deleting": true})
			return
		}
		// Mark as deleting immediately so the UI can show progress.
		store.UpdateLogJob(id, map[string]interface{}{"status": "deleting"})
		jsonOK(w, map[string]bool{"deleting": true})
		// Delete in background so the API response is instant.
		go func() {
			if err := store.DeleteLogJob(id); err != nil {
				log.Printf("[logs] delete failed jobID=%s: %v", id, err)
				store.UpdateLogJob(id, map[string]interface{}{"status": "failed", "errorMsg": "delete failed: " + err.Error()})
				manager.emit(ProgressEvent{Type: "log_delete_done", Data: map[string]interface{}{"jobId": id, "error": err.Error()}})
				return
			}
			os.RemoveAll(filepath.Join(store.uiDir, "logs", id))
			manager.mu.Lock()
			delete(manager.logBotIPs, id)
			manager.mu.Unlock()
			manager.emit(ProgressEvent{Type: "log_delete_done", Data: map[string]interface{}{"jobId": id}})
		}()
	})

	return mux
}

// ── Helpers ──

func readJSON(r *http.Request) (map[string]interface{}, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return data, nil
}

// capStatsForAPI caps large arrays in crawl stats to prevent multi-hundred-MB JSON responses
// that crash browser tabs. Arrays are truncated and a "totalX" count is preserved.
func capStatsForAPI(stats map[string]interface{}) map[string]interface{} {
	if stats == nil {
		return nil
	}
	const maxItems = 200

	out := make(map[string]interface{}, len(stats))
	for k, v := range stats {
		out[k] = v
	}

	// Cap known large arrays
	capArray := func(key, countKey string) {
		arr, ok := out[key].([]interface{})
		if !ok || len(arr) <= maxItems {
			return
		}
		if countKey != "" {
			out[countKey] = len(arr)
		}
		out[key] = arr[:maxItems]
	}

	capArray("redirectChains", "totalRedirectChains")
	capArray("longRedirectChains", "totalLongRedirectChains")
	capArray("orphanPages", "totalOrphanPages")
	capArray("brokenLinks", "totalBrokenLinks")
	capArray("thinContentPages", "totalThinContentPages")
	capArray("nonHttpsPages", "totalNonHttpsPages")

	// Cap pageRankScores to top 500 entries
	if scores, ok := out["pageRankScores"].(map[string]interface{}); ok && len(scores) > 500 {
		// Just remove it — too large for API, displayed via pages table instead
		out["pageRankScores"] = nil
		out["totalPageRankEntries"] = len(scores)
	}

	// Cap nested arrays inside complex stats objects
	capNestedArray := func(parentKey, childKey, countKey string) {
		parent, ok := out[parentKey].(map[string]interface{})
		if !ok {
			return
		}
		arr, ok := parent[childKey].([]interface{})
		if !ok || len(arr) <= maxItems {
			return
		}
		parent[countKey] = len(arr)
		parent[childKey] = arr[:maxItems]
	}

	capNestedArray("redirectStats", "chains", "totalChains")
	capNestedArray("redirectStats", "crossDomain", "totalCrossDomain")
	capNestedArray("readabilityStats", "difficult", "totalDifficult")
	capNestedArray("readabilityStats", "veryEasy", "totalVeryEasy")
	capNestedArray("canonicalStats", "canonicalized", "totalCanonicalized")
	capNestedArray("canonicalStats", "canonicalToNonIndexable", "totalCanonicalToNonIndexable")
	capNestedArray("canonicalStats", "crossDomain", "totalCrossDomainCanonicals")
	capNestedArray("canonicalStats", "canonicalWithQueryString", "totalCanonicalWithQueryString")
	capNestedArray("imageAuditStats", "missingAlt", "totalMissingAlt")
	capNestedArray("imageAuditStats", "oversized", "totalOversized")
	capNestedArray("imageAuditStats", "altTooLong", "totalAltTooLong")
	capNestedArray("linkIntelligenceStats", "nearOrphans", "totalNearOrphans")

	// Cap all arrays inside urlIssueStats (keyed by issue type)
	if issueStats, ok := out["urlIssueStats"].(map[string]interface{}); ok {
		for k, v := range issueStats {
			if arr, ok := v.([]interface{}); ok && len(arr) > maxItems {
				issueStats["total_"+k] = len(arr)
				issueStats[k] = arr[:maxItems]
			}
		}
	}

	capArray("duplicateContentGroups", "totalDuplicateContentGroups")

	return out
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("jsonOK: encode error: %v", err)
	}
}

// jsonGzip writes JSON with gzip compression if the client accepts it.
// Uses streaming json.Encoder → gzip.Writer to avoid buffering the full payload.
func jsonGzip(w http.ResponseWriter, r *http.Request, data interface{}) {
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		if err := json.NewEncoder(gz).Encode(data); err != nil {
			log.Printf("jsonGzip: encode error: %v", err)
		}
		if err := gz.Close(); err != nil {
			log.Printf("jsonGzip: gzip close error: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("jsonGzip: encode error: %v", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("jsonError: encode error: %v", err)
	}
}

func validateSettings(input map[string]interface{}) (valid map[string]interface{}, rejected []string) {
	valid = make(map[string]interface{})
	for key, val := range input {
		expectedType, ok := ValidSettings[key]
		if !ok {
			rejected = append(rejected, key)
			continue
		}
		// Skip masked secret values (sent back from GET /api/settings)
		if secretSettingKeys[key] {
			if s, ok := val.(string); ok && s == "••••••••" {
				continue
			}
		}
		// Validate file path keys
		if key == "gscKeyFile" || key == "ga4KeyFile" {
			s, ok := val.(string)
			if !ok {
				rejected = append(rejected, key)
				continue
			}
			cleaned := filepath.Clean(s)
			if !filepath.IsAbs(cleaned) || strings.Contains(cleaned, "..") || cleaned != s {
				rejected = append(rejected, key)
				continue
			}
		}
		// Basic type check
		switch expectedType {
		case "string":
			if _, ok := val.(string); !ok {
				rejected = append(rejected, key)
				continue
			}
		case "number":
			if _, ok := val.(float64); !ok {
				rejected = append(rejected, key)
				continue
			}
		case "boolean":
			if _, ok := val.(bool); !ok {
				rejected = append(rejected, key)
				continue
			}
		}
		valid[key] = val
	}
	return
}

// secretSettingKeys are settings that contain API keys or credentials.
// These are masked in GET responses but accepted as-is in PUT requests.
var secretSettingKeys = map[string]bool{
	"psiKey": true, "aiKey": true, "cruxKey": true, "plausibleApiKey": true,
}

// maskSecrets replaces secret values with a masked placeholder.
// Returns "••••••••" if the key is set and non-empty, "" if unset.
func maskSecrets(settings map[string]interface{}) {
	for key := range secretSettingKeys {
		val, exists := settings[key]
		if !exists {
			continue
		}
		if s, ok := val.(string); ok && s != "" {
			settings[key] = "••••••••"
		}
	}
}

func applyEnvFallbacks(settings map[string]interface{}) {
	for key, envVar := range envFallbacks {
		if _, exists := settings[key]; !exists {
			if val := os.Getenv(envVar); val != "" {
				settings[key] = val
			}
		}
	}
}

// ── Server-side pagination helpers ──

// filterAndSortPages applies filters and sorting to a page slice, returning a new slice.
func filterAndSortPages(pages []*types.PageData, search, status, indexable, template, classification, issue, robotsBlocked, sortField, sortOrder string) []*types.PageData {
	noFilter := search == "" &&
		(status == "" || status == "all") &&
		(indexable == "" || indexable == "all") &&
		(template == "" || template == "all") &&
		(classification == "" || classification == "all") &&
		(robotsBlocked == "" || robotsBlocked == "all") &&
		issue == ""

	var dupSet map[string]bool
	if issue == "duplicate-titles" || issue == "duplicate-descriptions" {
		dupSet = computeDuplicateSet(pages, issue)
	}

	var result []*types.PageData
	if noFilter {
		result = make([]*types.PageData, len(pages))
		copy(result, pages)
	} else {
		result = make([]*types.PageData, 0, len(pages)/4)
		for _, p := range pages {
			if !pageMatchesFilters(p, search, status, indexable, template, classification, issue, robotsBlocked, dupSet) {
				continue
			}
			result = append(result, p)
		}
	}

	sortPageSlice(result, sortField, sortOrder)
	return result
}

func pageMatchesFilters(p *types.PageData, search, status, indexable, template, classification, issue, robotsBlocked string, dupSet map[string]bool) bool {
	if status != "" && status != "all" {
		switch status {
		case "2xx":
			if p.StatusCode < 200 || p.StatusCode >= 300 {
				return false
			}
		case "3xx":
			if p.StatusCode < 300 || p.StatusCode >= 400 {
				return false
			}
		case "4xx":
			if p.StatusCode < 400 || p.StatusCode >= 500 {
				return false
			}
		case "5xx":
			if p.StatusCode < 500 {
				return false
			}
		default:
			return false // unknown status filter matches nothing
		}
	}
	if indexable != "" && indexable != "all" {
		if indexable == "yes" && !p.Indexability.Indexable {
			return false
		}
		if indexable == "no" && p.Indexability.Indexable {
			return false
		}
	}
	if template != "" && template != "all" && p.TemplateType != template {
		return false
	}
	if classification != "" && classification != "all" {
		cls := p.URLClassification
		switch classification {
		case "crawled":
			// all pass
		case "indexable":
			if cls != "indexable" && cls != "visible" && cls != "active" {
				return false
			}
		case "visible":
			if cls != "visible" && cls != "active" {
				return false
			}
		case "active":
			if cls != "active" {
				return false
			}
		case "non-indexable":
			if cls != "non-indexable" {
				return false
			}
		default:
			if cls != classification {
				return false
			}
		}
	}
	if robotsBlocked != "" && robotsBlocked != "all" {
		if robotsBlocked == "yes" && !p.RobotsBlocked {
			return false
		}
		if robotsBlocked == "no" && p.RobotsBlocked {
			return false
		}
	}
	if issue != "" && !pageMatchesIssue(p, issue, dupSet) {
		return false
	}
	if search != "" && !strings.Contains(strings.ToLower(p.URL), search) {
		return false
	}
	return true
}

func pageMatchesIssue(p *types.PageData, issue string, dupSet map[string]bool) bool {
	switch issue {
	case "5xx":
		return p.StatusCode >= 500
	case "missing-title":
		return p.Title == nil || p.Title.Text == ""
	case "missing-description":
		return p.MetaDescription == nil || p.MetaDescription.Text == ""
	case "missing-h1":
		return len(p.Headings.H1) == 0
	case "multiple-h1":
		return len(p.Headings.H1) > 1
	case "title-too-long":
		return p.Title != nil && p.Title.Length > 60
	case "title-too-short":
		return p.Title != nil && p.Title.Length > 0 && p.Title.Length < 30
	case "desc-too-long":
		return p.MetaDescription != nil && p.MetaDescription.Length > 160
	case "deep":
		return p.Depth > 4
	case "missing-canonical":
		return p.Canonical == nil
	case "duplicate-titles", "duplicate-descriptions":
		return dupSet[p.URL]
	default:
		return false
	}
}

func computeDuplicateSet(pages []*types.PageData, issue string) map[string]bool {
	valMap := make(map[string][]string, len(pages)/2)
	for _, p := range pages {
		var text string
		if issue == "duplicate-titles" && p.Title != nil {
			text = p.Title.Text
		} else if issue == "duplicate-descriptions" && p.MetaDescription != nil {
			text = p.MetaDescription.Text
		}
		if text != "" {
			valMap[text] = append(valMap[text], p.URL)
		}
	}
	set := make(map[string]bool)
	for _, urls := range valMap {
		if len(urls) > 1 {
			for _, url := range urls {
				set[url] = true
			}
		}
	}
	return set
}

func sortPageSlice(pages []*types.PageData, field, order string) {
	if field == "" {
		return
	}
	desc := order == "desc"
	sort.Slice(pages, func(i, j int) bool {
		var cmp int
		switch field {
		case "url":
			cmp = strings.Compare(pages[i].URL, pages[j].URL)
		case "statusCode":
			cmp = pages[i].StatusCode - pages[j].StatusCode
		case "responseTimeMs":
			d := pages[i].ResponseTimeMs - pages[j].ResponseTimeMs
			if d < 0 {
				cmp = -1
			} else if d > 0 {
				cmp = 1
			}
		case "title":
			a, b := "", ""
			if pages[i].Title != nil {
				a = strings.ToLower(pages[i].Title.Text)
			}
			if pages[j].Title != nil {
				b = strings.ToLower(pages[j].Title.Text)
			}
			cmp = strings.Compare(a, b)
		case "wordCount":
			cmp = pages[i].WordCount - pages[j].WordCount
		case "depth":
			cmp = pages[i].Depth - pages[j].Depth
		case "inlinks":
			cmp = pages[i].Inlinks - pages[j].Inlinks
		case "pageRank":
			a, b := pages[i].PageRank, pages[j].PageRank
			if a < b {
				cmp = -1
			} else if a > b {
				cmp = 1
			}
		case "htmlBytes":
			var a, b int64
			if pages[i].PageWeight != nil {
				a = pages[i].PageWeight.HTMLBytes
			}
			if pages[j].PageWeight != nil {
				b = pages[j].PageWeight.HTMLBytes
			}
			if a < b {
				cmp = -1
			} else if a > b {
				cmp = 1
			}
		case "totalBytes":
			var a, b int64
			if pages[i].PageWeight != nil {
				a = pages[i].PageWeight.TotalBytes
			}
			if pages[j].PageWeight != nil {
				b = pages[j].PageWeight.TotalBytes
			}
			if a < b {
				cmp = -1
			} else if a > b {
				cmp = 1
			}
		case "bodySize":
			a, b := pages[i].BodySize, pages[j].BodySize
			if a < b {
				cmp = -1
			} else if a > b {
				cmp = 1
			}
		default:
			return false
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func collectTemplateTypes(pages []*types.PageData) []string {
	set := make(map[string]struct{})
	for _, p := range pages {
		if p.TemplateType != "" {
			set[p.TemplateType] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for t := range set {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// buildGraphPayload constructs the lightweight graph response from crawl results.
func buildGraphPayload(results *CrawlResults) map[string]interface{} {
	if results == nil || results.Pages == nil {
		return map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}, "meta": map[string]interface{}{}}
	}

	type graphNode struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		Depth      int     `json:"depth"`
		Authority  float64 `json:"authority"`
		Hub        float64 `json:"hub"`
		Centrality float64 `json:"centrality"`
		Closeness  float64 `json:"closeness"`
		InDegree   int     `json:"inDegree"`
		OutDegree  int     `json:"outDegree"`
		PageRank   float64 `json:"pageRank"`
	}
	type graphEdge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}

	nodeMap := make(map[string]*graphNode, len(results.Pages))
	// Build edges grouped by source in a single pass (avoids 3-pass algorithm)
	edgesBySource := make(map[string][]graphEdge, len(results.Pages))
	maxDepth, maxAuth, maxHub, maxInDeg := 0, 0.0, 0.0, 0
	totalEdges := 0

	for _, p := range results.Pages {
		title := ""
		if p.Title != nil {
			title = p.Title.Text
		}
		depth := p.Depth
		if depth > maxDepth {
			maxDepth = depth
		}
		// Use count field when available (InternalLinks may have been freed)
		outDeg := len(p.InternalLinks)
		if outDeg == 0 && p.InternalLinkCount > 0 {
			outDeg = p.InternalLinkCount
		}
		n := &graphNode{
			ID: p.URL, Depth: depth, Title: title,
			OutDegree: outDeg,
			PageRank:  p.PageRank,
		}
		if p.LinkIntelligence != nil {
			n.Authority = p.LinkIntelligence.AuthorityScore
			n.Hub = p.LinkIntelligence.HubScore
			n.Centrality = p.LinkIntelligence.BetweennessCentrality
			n.Closeness = p.LinkIntelligence.ClosenessCentrality
		}
		nodeMap[p.URL] = n

		// Build edges from AdjacencyGraph (preferred) or InternalLinks (fallback for lazy-loaded results)
		if results.Graph != nil {
			if idx, ok := results.Graph.URLIndex[p.URL]; ok {
				neighbors := results.Graph.Neighbors(idx)
				if len(neighbors) > 0 {
					sourceEdges := make([]graphEdge, len(neighbors))
					for j, ni := range neighbors {
						sourceEdges[j] = graphEdge{Source: p.URL, Target: results.Pages[ni].URL}
					}
					edgesBySource[p.URL] = sourceEdges
					totalEdges += len(sourceEdges)
				}
			}
		} else if len(p.InternalLinks) > 0 {
			sourceEdges := make([]graphEdge, len(p.InternalLinks))
			for j, link := range p.InternalLinks {
				sourceEdges[j] = graphEdge{Source: p.URL, Target: link}
			}
			edgesBySource[p.URL] = sourceEdges
			totalEdges += len(sourceEdges)
		}
	}

	// Compute in-degree from grouped edges
	for _, sourceEdges := range edgesBySource {
		for _, e := range sourceEdges {
			if n, ok := nodeMap[e.Target]; ok {
				n.InDegree++
				if n.InDegree > maxInDeg {
					maxInDeg = n.InDegree
				}
			}
		}
	}

	// Build final edge slice with optional sampling
	samplingApplied := totalEdges > edgeSamplingThreshold
	edges := make([]graphEdge, 0, min(totalEdges, edgeSamplingThreshold*2))
	for _, sourceEdges := range edgesBySource {
		if samplingApplied && len(sourceEdges) > edgesPerNodeSample {
			edges = append(edges, sourceEdges[:edgesPerNodeSample]...)
		} else {
			edges = append(edges, sourceEdges...)
		}
	}

	// Convert to slices
	nodes := make([]graphNode, 0, len(nodeMap))
	for _, n := range nodeMap {
		if n.Authority > maxAuth {
			maxAuth = n.Authority
		}
		if n.Hub > maxHub {
			maxHub = n.Hub
		}
		nodes = append(nodes, *n)
	}

	// Cap nodes at maxGraphNodes to prevent browser freeze on very large crawls.
	// Keep top nodes by PageRank (+ in-degree as tiebreaker) to preserve the
	// most structurally important parts of the graph.
	nodeCapped := false
	totalNodes := len(nodes)
	if len(nodes) > maxGraphNodes {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].PageRank != nodes[j].PageRank {
				return nodes[i].PageRank > nodes[j].PageRank
			}
			return nodes[i].InDegree > nodes[j].InDegree
		})
		nodes = nodes[:maxGraphNodes]
		nodeCapped = true

		// Rebuild the surviving node set and filter edges
		surviving := make(map[string]struct{}, maxGraphNodes)
		for i := range nodes {
			surviving[nodes[i].ID] = struct{}{}
		}
		filtered := make([]graphEdge, 0, len(edges)/2)
		for _, e := range edges {
			if _, ok := surviving[e.Source]; !ok {
				continue
			}
			if _, ok := surviving[e.Target]; !ok {
				continue
			}
			filtered = append(filtered, e)
		}
		edges = filtered
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	return map[string]interface{}{
		"nodes": nodes,
		"edges": edges,
		"meta": map[string]interface{}{
			"maxDepth":                 maxDepth,
			"maxAuthority":             maxAuth,
			"maxHub":                   maxHub,
			"maxInDegree":              maxInDeg,
			"totalEdgesBeforeSampling": totalEdges,
			"samplingApplied":          samplingApplied,
			"nodeCapped":               nodeCapped,
			"totalNodes":               totalNodes,
		},
	}
}

// buildSubgraphPayload constructs a subgraph response for progressive graph exploration.
func buildSubgraphPayload(results *CrawlResults, rootURL string, depth, maxNodes int) map[string]interface{} {
	graph := results.Graph
	if graph == nil {
		return map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}, "meta": map[string]interface{}{}}
	}

	// Build URL → page index for metadata lookup
	pageIndex := make(map[string]*types.PageData, len(results.Pages))
	for _, p := range results.Pages {
		pageIndex[p.URL] = p
	}

	// Resolve root node
	rootIdx := -1
	if rootURL == "" {
		// Find seed URL (depth 0)
		for i, u := range graph.URLs {
			if p, ok := pageIndex[u]; ok && p.Depth == 0 {
				rootIdx = i
				break
			}
		}
		if rootIdx < 0 && graph.N > 0 {
			rootIdx = 0
		}
	} else {
		if idx, ok := graph.URLIndex[rootURL]; ok {
			rootIdx = idx
		}
	}
	if rootIdx < 0 {
		return map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}, "meta": map[string]interface{}{"error": "root not found"}}
	}

	sub := graph.SubGraph(rootIdx, depth, maxNodes)

	type subNode struct {
		ID            string  `json:"id"`
		Title         string  `json:"title"`
		Depth         int     `json:"depth"`
		Authority     float64 `json:"authority"`
		Hub           float64 `json:"hub"`
		Centrality    float64 `json:"centrality"`
		Closeness     float64 `json:"closeness"`
		InDegree      int     `json:"inDegree"`
		OutDegree     int     `json:"outDegree"`
		PageRank      float64 `json:"pageRank"`
		Expandable    bool    `json:"expandable"`
		TotalOutlinks int     `json:"totalOutlinks"`
	}

	// Compute in-degree within the subgraph
	inDegMap := make(map[int]int, len(sub.Nodes))
	for _, e := range sub.Edges {
		inDegMap[e[1]]++
	}

	nodes := make([]subNode, 0, len(sub.Nodes))
	for _, sn := range sub.Nodes {
		u := graph.URLs[sn.Index]
		n := subNode{
			ID:            u,
			Expandable:    sn.Expandable,
			TotalOutlinks: sn.TotalOutlinks,
			InDegree:      inDegMap[sn.Index],
			OutDegree:     sn.TotalOutlinks,
		}
		if p, ok := pageIndex[u]; ok {
			if p.Title != nil {
				n.Title = p.Title.Text
			}
			n.Depth = p.Depth
			n.PageRank = p.PageRank
			if p.LinkIntelligence != nil {
				n.Authority = p.LinkIntelligence.AuthorityScore
				n.Hub = p.LinkIntelligence.HubScore
				n.Centrality = p.LinkIntelligence.BetweennessCentrality
				n.Closeness = p.LinkIntelligence.ClosenessCentrality
			}
		}
		nodes = append(nodes, n)
	}

	type subEdge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	edges := make([]subEdge, 0, len(sub.Edges))
	for _, e := range sub.Edges {
		edges = append(edges, subEdge{Source: graph.URLs[e[0]], Target: graph.URLs[e[1]]})
	}

	return map[string]interface{}{
		"nodes": nodes,
		"edges": edges,
		"meta": map[string]interface{}{
			"rootURL":         graph.URLs[rootIdx],
			"hops":            depth,
			"totalNodesInGraph": graph.N,
			"nodesInView":     len(nodes),
		},
	}
}

// buildGraphSearchPayload searches graph URLs for a substring match.
func buildGraphSearchPayload(results *CrawlResults, query string, limit int) map[string]interface{} {
	graph := results.Graph
	if graph == nil {
		return map[string]interface{}{"results": []interface{}{}}
	}

	pageIndex := make(map[string]*types.PageData, len(results.Pages))
	for _, p := range results.Pages {
		pageIndex[p.URL] = p
	}

	type searchResult struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Depth    int     `json:"depth"`
		PageRank float64 `json:"pageRank"`
	}

	var matches []searchResult
	for _, u := range graph.URLs {
		if len(matches) >= limit {
			break
		}
		uLower := strings.ToLower(u)
		titleLower := ""
		if p, ok := pageIndex[u]; ok && p.Title != nil {
			titleLower = strings.ToLower(p.Title.Text)
		}
		if strings.Contains(uLower, query) || strings.Contains(titleLower, query) {
			sr := searchResult{ID: u}
			if p, ok := pageIndex[u]; ok {
				if p.Title != nil {
					sr.Title = p.Title.Text
				}
				sr.Depth = p.Depth
				sr.PageRank = p.PageRank
			}
			matches = append(matches, sr)
		}
	}

	return map[string]interface{}{"results": matches}
}

// buildSubgraphFromDB constructs a subgraph response by reading internal links
// directly from the SQLite crawl database. Used as a fallback when the in-memory
// graph hasn't been built yet (e.g. during lazy loading of large crawls).
func buildSubgraphFromDB(dbPath string, rootURL string, depth, maxNodes int) map[string]interface{} {
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()

	// Resolve seed URL from page_index if rootURL is empty
	if rootURL == "" {
		entries, err := cs.GetPageIndex()
		if err != nil || len(entries) == 0 {
			return nil
		}
		// Find depth-0 page
		for _, e := range entries {
			if e.Depth == 0 {
				rootURL = e.URL
				break
			}
		}
		if rootURL == "" {
			rootURL = entries[0].URL
		}
	}

	// BFS from root, reading internal links from DB at each level
	visited := make(map[string]bool, maxNodes)
	visited[rootURL] = true
	frontier := []string{rootURL}
	edgePairs := make(map[string][]string) // source → targets
	capped := false

	for hop := 0; hop < depth && len(frontier) > 0 && !capped; hop++ {
		var nextFrontier []string
		for _, pageURL := range frontier {
			links, err := cs.GetPageInternalLinks(pageURL)
			if err != nil || len(links) == 0 {
				continue
			}
			var targets []string
			for _, link := range links {
				if !visited[link] {
					visited[link] = true
					nextFrontier = append(nextFrontier, link)
					if len(visited) >= maxNodes {
						targets = append(targets, link)
						capped = true
						break
					}
				}
				targets = append(targets, link)
			}
			edgePairs[pageURL] = targets
			if capped {
				break
			}
		}
		frontier = nextFrontier
	}

	// Get metadata for all visited URLs from page_index
	visitedURLs := make([]string, 0, len(visited))
	for u := range visited {
		visitedURLs = append(visitedURLs, u)
	}
	indexEntries, _ := cs.GetPageIndexByURLs(visitedURLs)

	type subNode struct {
		ID            string  `json:"id"`
		Title         string  `json:"title"`
		Depth         int     `json:"depth"`
		Authority     float64 `json:"authority"`
		Hub           float64 `json:"hub"`
		Centrality    float64 `json:"centrality"`
		Closeness     float64 `json:"closeness"`
		InDegree      int     `json:"inDegree"`
		OutDegree     int     `json:"outDegree"`
		PageRank      float64 `json:"pageRank"`
		Expandable    bool    `json:"expandable"`
		TotalOutlinks int     `json:"totalOutlinks"`
	}
	type subEdge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}

	// Build nodes
	nodes := make([]subNode, 0, len(visited))
	for u := range visited {
		n := subNode{ID: u}
		if e, ok := indexEntries[u]; ok {
			n.Title = e.Title
			n.Depth = e.Depth
			n.InDegree = e.Inlinks
		}
		// Check expandability: has outlinks we haven't included
		if targets, ok := edgePairs[u]; ok {
			n.TotalOutlinks = len(targets)
			for _, t := range targets {
				if !visited[t] {
					n.Expandable = true
					break
				}
			}
		} else {
			// We haven't read this node's links — it may have outlinks
			n.Expandable = true
		}
		nodes = append(nodes, n)
	}

	// Build edges (only between visited nodes)
	edges := make([]subEdge, 0, len(visited)*4)
	edgeSet := make(map[string]bool)
	for source, targets := range edgePairs {
		for _, target := range targets {
			if visited[target] {
				key := source + "\x00" + target
				if !edgeSet[key] {
					edgeSet[key] = true
					edges = append(edges, subEdge{Source: source, Target: target})
				}
			}
		}
	}

	// Estimate total nodes in graph
	totalNodes := len(visited)
	if idx, err := cs.GetPageIndex(); err == nil {
		totalNodes = len(idx)
	}

	return map[string]interface{}{
		"nodes": nodes,
		"edges": edges,
		"meta": map[string]interface{}{
			"rootURL":           rootURL,
			"hops":              depth,
			"totalNodesInGraph": totalNodes,
			"nodesInView":       len(nodes),
		},
	}
}

// buildGraphSearchFromDB searches the page_index table in the crawl DB.
func buildGraphSearchFromDB(dbPath string, query string, limit int) map[string]interface{} {
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()

	entries, err := cs.GetPageIndex()
	if err != nil {
		return nil
	}

	type searchResult struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Depth    int     `json:"depth"`
		PageRank float64 `json:"pageRank"`
	}

	var matches []searchResult
	for _, e := range entries {
		if len(matches) >= limit {
			break
		}
		if strings.Contains(strings.ToLower(e.URL), query) || strings.Contains(strings.ToLower(e.Title), query) {
			matches = append(matches, searchResult{
				ID:    e.URL,
				Title: e.Title,
				Depth: e.Depth,
			})
		}
	}

	return map[string]interface{}{"results": matches}
}

// buildDirectoryTreeFromDB constructs a directory tree from the page_index table.
// Used as a fallback when pages haven't been fully loaded into memory.
func buildDirectoryTreeFromDB(dbPath string) map[string]interface{} {
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()

	entries, err := cs.GetPageIndex()
	if err != nil || len(entries) == 0 {
		return nil
	}

	type dirNode struct {
		Name             string             `json:"name"`
		Path             string             `json:"path"`
		Pages            int                `json:"pages"`
		Children         []*dirNode         `json:"children,omitempty"`
		StatusCodes      map[int]int        `json:"statusCodes"`
		Depth            int                `json:"depth"`
		IsPage           bool               `json:"isPage"`
		Indexable        int                `json:"indexable"`
		NonIndexable     int                `json:"nonIndexable"`
		TotalInlinks     int                `json:"totalInlinks"`
		TotalPageRank    float64            `json:"totalPageRank"`
		AvgPageRank      float64            `json:"avgPageRank"`
		AvgInternalLinks float64            `json:"avgInternalLinks"`
		AvgResponseTime  int                `json:"avgResponseTime"`
		childMap         map[string]*dirNode
	}

	root := &dirNode{Name: "/", Path: "/", StatusCodes: map[int]int{}, childMap: map[string]*dirNode{}}

	for _, e := range entries {
		u, parseErr := url.Parse(e.URL)
		if parseErr != nil {
			continue
		}
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segments) == 1 && segments[0] == "" {
			segments = nil
		}

		current := root
		pathSoFar := ""
		for i, seg := range segments {
			if i >= 3 {
				break
			}
			pathSoFar += "/" + seg
			child, ok := current.childMap[seg]
			if !ok {
				child = &dirNode{Name: seg, Path: pathSoFar, StatusCodes: map[int]int{}, Depth: i + 1, childMap: map[string]*dirNode{}}
				current.childMap[seg] = child
			}
			current = child
		}

		current.Pages++
		current.IsPage = true
		current.StatusCodes[e.StatusCode]++
		current.TotalInlinks += e.Inlinks
		if e.Indexable {
			current.Indexable++
		} else {
			current.NonIndexable++
		}
	}

	var finalize func(n *dirNode)
	finalize = func(n *dirNode) {
		n.Children = make([]*dirNode, 0, len(n.childMap))
		for _, child := range n.childMap {
			finalize(child)
			n.Children = append(n.Children, child)
			n.Pages += child.Pages
			n.Indexable += child.Indexable
			n.NonIndexable += child.NonIndexable
			n.TotalInlinks += child.TotalInlinks
			for code, count := range child.StatusCodes {
				n.StatusCodes[code] += count
			}
		}
		if n.Pages > 0 {
			n.AvgPageRank = n.TotalPageRank / float64(n.Pages)
		}
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Pages > n.Children[j].Pages
		})
		if len(n.Children) > 20 {
			other := &dirNode{Name: "other", Path: n.Path + "/other", StatusCodes: map[int]int{}, Depth: n.Depth + 1}
			for _, child := range n.Children[20:] {
				other.Pages += child.Pages
				other.Indexable += child.Indexable
				other.NonIndexable += child.NonIndexable
				other.TotalInlinks += child.TotalInlinks
				for code, count := range child.StatusCodes {
					other.StatusCodes[code] += count
				}
			}
			n.Children = append(n.Children[:20], other)
		}
		n.childMap = nil
	}
	finalize(root)

	return map[string]interface{}{
		"tree":       root,
		"totalPages": root.Pages,
	}
}

// ── Anchor Stats ──

func buildAnchorStats(pages []*types.PageData) map[string]interface{} {
	type anchorKey struct {
		text string
	}
	type anchorAgg struct {
		Count            int  `json:"count"`
		IsInternal       bool `json:"isInternal"`
		IsNonDescriptive bool `json:"isNonDescriptive"`
	}
	agg := make(map[string]*anchorAgg, 4096)

	for _, p := range pages {
		for _, a := range p.Anchors {
			text := strings.TrimSpace(strings.ToLower(a.Text))
			runeLen := utf8.RuneCountInString(text)
			if runeLen < 2 || runeLen > 80 {
				continue
			}
			if existing, ok := agg[text]; ok {
				existing.Count++
				if a.IsInternal {
					existing.IsInternal = true
				}
				if a.IsNonDescriptive {
					existing.IsNonDescriptive = true
				}
			} else {
				agg[text] = &anchorAgg{
					Count:            1,
					IsInternal:       a.IsInternal,
					IsNonDescriptive: a.IsNonDescriptive,
				}
			}
		}
	}

	type anchorEntry struct {
		Text             string `json:"text"`
		Count            int    `json:"count"`
		IsInternal       bool   `json:"isInternal"`
		IsNonDescriptive bool   `json:"isNonDescriptive"`
	}
	entries := make([]anchorEntry, 0, len(agg))
	for text, a := range agg {
		entries = append(entries, anchorEntry{
			Text:             text,
			Count:            a.Count,
			IsInternal:       a.IsInternal,
			IsNonDescriptive: a.IsNonDescriptive,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Count > entries[j].Count })

	// Cap at 300 to keep response small
	if len(entries) > 300 {
		entries = entries[:300]
	}

	totalUnique := len(agg)
	return map[string]interface{}{
		"anchors":     entries,
		"totalUnique": totalUnique,
	}
}

// ── Directory Tree ──

type dirNode struct {
	Name             string             `json:"name"`
	Path             string             `json:"path"`
	Pages            int                `json:"pages"`
	Children         []*dirNode         `json:"children,omitempty"`
	StatusCodes      map[int]int        `json:"statusCodes"`
	AvgResponseTime  int                `json:"avgResponseTime"`
	TotalRespTime    int64              `json:"-"`
	Depth            int                `json:"depth"`
	IsPage           bool               `json:"isPage"`

	// SEO metrics (aggregated from child pages)
	TotalPageRank      float64 `json:"totalPageRank"`
	AvgPageRank        float64 `json:"avgPageRank"`
	TotalInternalLinks int     `json:"totalInternalLinks"`
	AvgInternalLinks   float64 `json:"avgInternalLinks"`
	Indexable          int     `json:"indexable"`
	NonIndexable       int     `json:"nonIndexable"`
	TotalInlinks       int     `json:"totalInlinks"`

	// Traffic & conversions (from GSC/GA4/Plausible, zero when no data)
	GscClicks            int `json:"gscClicks,omitempty"`
	GscImpressions       int `json:"gscImpressions,omitempty"`
	Ga4Sessions          int `json:"ga4Sessions,omitempty"`
	Ga4Pageviews         int `json:"ga4Pageviews,omitempty"`
	Ga4Conversions       int `json:"ga4Conversions,omitempty"`
	PlausibleVisitors    int `json:"plausibleVisitors,omitempty"`
	PlausibleConversions int `json:"plausibleConversions,omitempty"`

	childMap map[string]*dirNode
}

const maxTreeDepth = 15

func buildDirectoryTree(pages []*types.PageData) map[string]interface{} {
	root := &dirNode{
		Name:        "/",
		Path:        "/",
		StatusCodes: make(map[int]int),
		childMap:    make(map[string]*dirNode),
	}

	for _, p := range pages {
		if p.URL == "" {
			continue
		}
		parsed, err := url.Parse(p.URL)
		if err != nil {
			continue
		}
		pathname := parsed.Path
		segments := splitPathSegments(pathname)
		if len(segments) > maxTreeDepth {
			segments = segments[:maxTreeDepth]
		}

		current := root
		currentPath := "/"

		for i, seg := range segments {
			if currentPath == "/" {
				currentPath = "/" + seg
			} else {
				currentPath = currentPath + "/" + seg
			}
			child, ok := current.childMap[seg]
			if !ok {
				child = &dirNode{
					Name:        seg,
					Path:        currentPath,
					Depth:       i + 1,
					StatusCodes: make(map[int]int),
					childMap:    make(map[string]*dirNode),
				}
				current.childMap[seg] = child
			}
			current = child
		}

		// Mark leaf
		current.IsPage = true
		current.Pages++
		sc := p.StatusCode
		current.StatusCodes[sc]++
		current.TotalRespTime += int64(p.ResponseTimeMs)

		// SEO metrics
		current.TotalPageRank += p.PageRank
		current.TotalInternalLinks += p.InternalLinkCount
		current.TotalInlinks += p.Inlinks
		if p.Indexability.Indexable {
			current.Indexable++
		} else {
			current.NonIndexable++
		}

		// Traffic & conversions
		if g := p.GscData; g != nil {
			current.GscClicks += g.Clicks
			current.GscImpressions += g.Impressions
		}
		if g := p.Ga4Data; g != nil {
			current.Ga4Sessions += g.Sessions
			current.Ga4Pageviews += g.Pageviews
			current.Ga4Conversions += g.Conversions
		}
		if pl := p.PlausibleData; pl != nil {
			current.PlausibleVisitors += pl.Visitors
			current.PlausibleConversions += pl.Conversions
		}

		// Bubble up to ancestors
		if len(segments) > 0 {
			ancestor := root
			bubbleUpMetrics(ancestor, p)

			for _, seg := range segments {
				ancestor = ancestor.childMap[seg]
				if ancestor != current {
					bubbleUpMetrics(ancestor, p)
				}
			}
		}
	}

	// Compute averages and flatten children maps to slices
	finalizeDirNode(root)

	return map[string]interface{}{
		"tree":       root,
		"totalPages": len(pages),
	}
}

func splitPathSegments(path string) []string {
	var segments []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

func bubbleUpMetrics(n *dirNode, p *types.PageData) {
	n.Pages++
	n.StatusCodes[p.StatusCode]++
	n.TotalRespTime += int64(p.ResponseTimeMs)
	n.TotalPageRank += p.PageRank
	n.TotalInternalLinks += p.InternalLinkCount
	n.TotalInlinks += p.Inlinks
	if p.Indexability.Indexable {
		n.Indexable++
	} else {
		n.NonIndexable++
	}
	if g := p.GscData; g != nil {
		n.GscClicks += g.Clicks
		n.GscImpressions += g.Impressions
	}
	if g := p.Ga4Data; g != nil {
		n.Ga4Sessions += g.Sessions
		n.Ga4Pageviews += g.Pageviews
		n.Ga4Conversions += g.Conversions
	}
	if pl := p.PlausibleData; pl != nil {
		n.PlausibleVisitors += pl.Visitors
		n.PlausibleConversions += pl.Conversions
	}
}

func finalizeDirNode(node *dirNode) {
	if node.Pages > 0 {
		node.AvgResponseTime = int(node.TotalRespTime / int64(node.Pages))
		node.AvgPageRank = node.TotalPageRank / float64(node.Pages)
		node.AvgInternalLinks = float64(node.TotalInternalLinks) / float64(node.Pages)
	}
	if len(node.childMap) > 0 {
		node.Children = make([]*dirNode, 0, len(node.childMap))
		for _, child := range node.childMap {
			finalizeDirNode(child)
			node.Children = append(node.Children, child)
		}
		// Sort: directories first (by page count desc), then leaves
		sort.Slice(node.Children, func(i, j int) bool {
			iDir := len(node.Children[i].Children) > 0
			jDir := len(node.Children[j].Children) > 0
			if iDir != jDir {
				if iDir {
					return true
				}
				return false
			}
			return node.Children[i].Pages > node.Children[j].Pages
		})
	}
	node.childMap = nil // release for GC
}

// ── Inlink Index ──

// buildInlinkIndex creates an inverted index: targetURL → []sourceURL for O(1) lookups.
func buildInlinkIndex(pages []*types.PageData) map[string][]string {
	idx := make(map[string][]string, len(pages))
	for _, p := range pages {
		seen := make(map[string]bool, len(p.InternalLinks))
		for _, link := range p.InternalLinks {
			if !seen[link] {
				seen[link] = true
				idx[link] = append(idx[link], p.URL)
			}
		}
	}
	return idx
}

// statsMapToCrawlStats converts a map[string]interface{} stats back to typed CrawlStats
// via JSON round-trip.
func statsMapToCrawlStats(m map[string]interface{}) *types.CrawlStats {
	if m == nil {
		return &types.CrawlStats{}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return &types.CrawlStats{}
	}
	var stats types.CrawlStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return &types.CrawlStats{}
	}
	return &stats
}

// inlinkVariants returns URL variants (with/without trailing slash) for inlink matching.
func inlinkVariants(targetURL string) []string {
	variants := []string{targetURL}
	if strings.HasSuffix(targetURL, "/") {
		variants = append(variants, strings.TrimSuffix(targetURL, "/"))
	} else {
		variants = append(variants, targetURL+"/")
	}
	return variants
}
