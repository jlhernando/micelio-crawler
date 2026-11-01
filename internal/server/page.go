package server

import (
	"net/http"

	"github.com/micelio/micelio/internal/analysis"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// registerPageRoute adds GET /api/crawl/{id}/page?url=: one page of a
// crawl with its internal outlinks. The URL matches exactly or with the
// trailing slash added or removed, like /page-inlinks.
func registerPageRoute(mux *http.ServeMux, manager *CrawlManager) {
	mux.HandleFunc("GET /api/crawl/{id}/page", func(w http.ResponseWriter, r *http.Request) {
		pageURL := r.URL.Query().Get("url")
		if pageURL == "" {
			jsonError(w, "url parameter required", http.StatusBadRequest)
			return
		}
		results := manager.GetOrLoadResults(r.PathValue("id"))
		if results == nil {
			jsonError(w, "results not available", http.StatusNotFound)
			return
		}
		results.mu.RLock()
		if !results.PagesReady {
			results.mu.RUnlock()
			jsonError(w, "pages still loading", http.StatusServiceUnavailable)
			return
		}
		page := findPage(results.Pages, pageURL)
		if page == nil {
			results.mu.RUnlock()
			jsonError(w, "page not found in this crawl", http.StatusNotFound)
			return
		}
		out := *page // a copy: the cached page is shared
		var outlinks []string
		if len(page.InternalLinks) > 0 {
			outlinks = append(outlinks, page.InternalLinks...)
		}
		inlinks := page.Inlinks
		if results.InlinkIndex != nil {
			inlinks = results.InlinkIndex.Count(page.URL)
		}
		dbPath, graph := results.DBPath, results.Graph
		results.mu.RUnlock()

		// The crawl DB keeps every internal link; the graph only links
		// between crawled pages, so it is the last resort.
		if outlinks == nil && dbPath != "" {
			outlinks = dbOutlinks(dbPath, page.URL)
		}
		if outlinks == nil {
			outlinks = graphOutlinks(graph, page.URL)
		}
		if outlinks == nil {
			outlinks = []string{}
		}
		out.InternalLinks = nil
		jsonOK(w, map[string]interface{}{"page": &out, "outlinks": outlinks, "inlinkCount": inlinks})
	})
}

// findPage returns the page whose URL matches u or one of its variants.
func findPage(pages []*types.PageData, u string) *types.PageData {
	for _, v := range inlinkVariants(u) {
		for _, p := range pages {
			if p.URL == v {
				return p
			}
		}
	}
	return nil
}

// graphOutlinks returns the pages u links to in the link graph.
func graphOutlinks(g *analysis.AdjacencyGraph, u string) []string {
	if g == nil {
		return nil
	}
	i, ok := g.URLIndex[u]
	if !ok || i >= g.N {
		return nil
	}
	out := make([]string, 0, g.OutDegree(i))
	for _, t := range g.Neighbors(i) {
		out = append(out, g.URLs[t])
	}
	return out
}

// dbOutlinks reads the page's internal links from the crawl DB.
func dbOutlinks(dbPath, pageURL string) []string {
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()
	links, err := cs.GetPageInternalLinks(pageURL)
	if err != nil {
		return nil
	}
	return links
}
