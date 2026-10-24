package server

import (
	"log"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// analysisSnapshot is the per-page analysis output stored in the crawl DB
// (see storage/crawlstore_analysis.go): node i is pages[i].
type analysisSnapshot struct {
	urlsHash  uint64
	pageRank  []float64
	authority []float64
	hub       []float64
	hasHits   bool
	graph     *analysis.AdjacencyGraph // nil: keep the stored graph
}

// snapshotAnalysis copies scores off the pages so the DB write can run in
// the background without reading pages other goroutines may update. Pass
// withGraph to store g as well (immutable once built).
func snapshotAnalysis(pages []*types.PageData, g *analysis.AdjacencyGraph, withGraph bool) *analysisSnapshot {
	if len(pages) == 0 || (withGraph && (g == nil || g.N != len(pages))) {
		return nil
	}
	snap := &analysisSnapshot{
		urlsHash:  storage.PageOrderHash(pages),
		pageRank:  make([]float64, len(pages)),
		authority: make([]float64, len(pages)),
		hub:       make([]float64, len(pages)),
	}
	for i, p := range pages {
		snap.pageRank[i] = p.PageRank
		if li := p.LinkIntelligence; li != nil {
			snap.authority[i], snap.hub[i] = li.AuthorityScore, li.HubScore
			if li.AuthorityScore != 0 || li.HubScore != 0 {
				snap.hasHits = true
			}
		}
	}
	if withGraph {
		snap.graph = g
	}
	return snap
}

// save writes the snapshot to the crawl DB. Returns false on any failure.
func (snap *analysisSnapshot) save(dbPath string) bool {
	if snap == nil || dbPath == "" {
		return false
	}
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		log.Printf("analysis store: open %s: %v", dbPath, err)
		return false
	}
	defer cs.Close()
	if g := snap.graph; g != nil {
		if err := cs.SaveLinkGraph(snap.urlsHash, g.Offsets, g.Edges); err != nil {
			log.Printf("analysis store: save link graph: %v", err)
			return false
		}
	}
	if err := cs.SavePageScores(snap.urlsHash, snap.pageRank, snap.authority, snap.hub, snap.hasHits); err != nil {
		log.Printf("analysis store: save page scores: %v", err)
		return false
	}
	return true
}

// loadStoredAnalysis reads the stored link graph and page scores for pages
// reloaded from a crawl DB. graph is nil when none is stored (older crawls);
// scoresOK reports whether PageRank (and HITS, when stored) was assigned.
func loadStoredAnalysis(dbPath string, pages []*types.PageData) (graph *analysis.AdjacencyGraph, scoresOK bool) {
	if dbPath == "" || len(pages) == 0 {
		return nil, false
	}
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil, false
	}
	defer cs.Close()
	hash := storage.PageOrderHash(pages)
	if offsets, edges, ok, err := cs.LoadLinkGraph(hash, len(pages)); err != nil {
		log.Printf("analysis store: load link graph: %v", err)
	} else if ok {
		graph = analysis.NewAdjacencyGraphFromCSR(pages, offsets, edges)
	}
	pr, auth, hub, ok, err := cs.LoadPageScores(hash, len(pages))
	if err != nil {
		log.Printf("analysis store: load page scores: %v", err)
	}
	if !ok {
		return graph, false
	}
	for i, p := range pages {
		p.PageRank = pr[i]
		if auth != nil && (auth[i] != 0 || hub[i] != 0) {
			if p.LinkIntelligence == nil {
				p.LinkIntelligence = &types.LinkIntelligenceData{}
			}
			p.LinkIntelligence.AuthorityScore = auth[i]
			p.LinkIntelligence.HubScore = hub[i]
		}
	}
	return graph, true
}
