package analysis

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// PostProcessConfig holds options for post-crawl analysis.
type PostProcessConfig struct {
	NgramAnalyzer       *NgramAnalyzer
	OnLIPhase           func(phase string) // Called before each LI phase (e.g. "click_depth", "hits", "centrality", "suggestions")
	OnPersist           func()             // Called after each LI phase to persist intermediate results to DB
	// InternalLinksIter, if non-nil, provides internal link edges from disk.
	// Used by BuildAdjacencyList to avoid loading InternalLinks from pages (saves ~5GB at 1M pages).
	InternalLinksIter func(fn func(source string, targets []string)) error
	SeedURL             string
	Language            string
	EmbeddingModel      string
	EmbeddingProvider   string
	EmbeddingKey        string
	SegmentRules        []types.SegmentRule
	LIMaxSuggestions    int
	LIMaxPages          int // Skip heavy LI phases if page count exceeds this (0 = no limit)
	SimilarityThreshold float64
	SitemapEntries      []string
	CrawlDurationMs     int64
	LinkIntelligence    bool
	LINoCentrality      bool
	Embeddings          bool
	Ngrams              bool
}

// RunCoreAnalysis runs the fast synchronous analysis (reporter + n-grams + field stripping).
// Returns CrawlStats without link intelligence or embeddings.
func RunCoreAnalysis(ctx context.Context, pages []*types.PageData, cfg PostProcessConfig) types.CrawlStats {
	// Parse segment rules
	var segments []Segment
	for _, rule := range cfg.SegmentRules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] invalid segment pattern %q: %v\n", rule.Pattern, err)
			continue
		}
		segments = append(segments, Segment{Name: rule.Name, Pattern: re})
	}

	// Run core reporter (always)
	reportCfg := ReportConfig{
		SeedURL:           cfg.SeedURL,
		SegmentRules:      segments,
		SitemapEntries:    cfg.SitemapEntries,
		TotalSitemapURLs:  len(cfg.SitemapEntries),
		InternalLinksIter: cfg.InternalLinksIter,
	}
	stats := GenerateReport(pages, cfg.CrawlDurationMs, reportCfg)

	// N-grams (optional)
	if cfg.Ngrams {
		lang := cfg.Language
		if lang == "" {
			lang = "en"
		}
		// Use pre-computed analyzer from crawl if available (BodyText already released)
		analyzer := cfg.NgramAnalyzer
		if analyzer == nil {
			analyzer = NewNgramAnalyzer(lang)
			for _, p := range pages {
				if p.StatusCode == 200 && p.BodyText != "" {
					analyzer.AddPage(p.BodyText)
				}
			}
		}
		ngramResults := analyzer.GetResults(100)
		stats.NgramStats = &types.NgramStats{
			Unigrams:    convertNgrams(ngramResults.Unigrams),
			Bigrams:     convertNgrams(ngramResults.Bigrams),
			Trigrams:    convertNgrams(ngramResults.Trigrams),
			TotalPages:  ngramResults.TotalPages,
			TotalTokens: ngramResults.TotalTokens,
		}
		fmt.Fprintf(os.Stderr, "\n  [ngrams] Analyzed %d pages (%s)\n", ngramResults.TotalPages, lang)
	}

	// Topicality stats — topicality computed per-page during extraction (needs BodyText)
	stats.TopicalityStats = buildTopicalityStats(pages)

	// Strip heavy PageData fields no longer needed for link intelligence or embeddings.
	// BodyText was already consumed by n-grams/reporter; the rest are not used by graph algorithms.
	for _, p := range pages {
		p.Anchors = nil
		p.Images = nil
		p.StructuredData = nil
		p.SchemaValidation = nil
		p.RenderDiffs = nil
		p.JSErrors = nil
		p.SnippetResults = nil
		p.BodyText = ""
		// Strip InternalLinks early if disk iterator is available for BuildAdjacencyList.
		// This saves ~3-10 GB for large crawls. ExternalLinks already stripped at load.
		if cfg.InternalLinksIter != nil {
			p.InternalLinkCount = len(p.InternalLinks)
			p.InternalLinks = nil
		}
		if p.ExternalLinkCount == 0 && len(p.ExternalLinks) > 0 {
			p.ExternalLinkCount = len(p.ExternalLinks)
		}
		p.ExternalLinks = nil
	}

	// GC checkpoint: release report temporaries + stripped fields before next phase
	debug.FreeOSMemory()

	return stats
}

// RunDeferredAnalysis runs link intelligence and embeddings on already-analyzed pages.
// Call after RunCoreAnalysis. Returns the AdjacencyGraph and updates stats in place.
func RunDeferredAnalysis(ctx context.Context, pages []*types.PageData, cfg PostProcessConfig, stats *types.CrawlStats) *AdjacencyGraph {
	// Link Intelligence (optional)
	var graph *AdjacencyGraph
	if cfg.LinkIntelligence {
		var liStats *types.LinkIntelligenceStats
		liStats, graph = runLinkIntelligence(pages, cfg, stats)
		stats.LinkIntelligenceStats = liStats

		// T*-Anchors enrichment: compute anchor-title relevance now that graph is available.
		// This adds the "A" (Anchors) sub-signal to the T* topicality score computed at extraction.
		// Source: DOJ ABC framework (Nayak PXR0357), Patent US7260573.
		EnrichTopicalityAnchors(pages, graph)

		// GC checkpoint: release link intel temporaries
		debug.FreeOSMemory()
	}

	// Embeddings (optional)
	if cfg.Embeddings && cfg.EmbeddingKey != "" {
		provider := EmbeddingOpenAI
		if strings.ToLower(cfg.EmbeddingProvider) == "ollama" {
			provider = EmbeddingOllama
		}
		threshold := cfg.SimilarityThreshold
		if threshold == 0 {
			threshold = 0.9
		}
		embCfg := EmbeddingConfig{
			Provider:  provider,
			APIKey:    cfg.EmbeddingKey,
			Model:     cfg.EmbeddingModel,
			Threshold: threshold,
		}
		embStats, err := ComputeEmbeddings(ctx, pages, embCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] embedding analysis failed: %v\n", err)
		} else if embStats != nil {
			stats.EmbeddingStats = embStats
			fmt.Fprintf(os.Stderr, "\n  [embeddings] %d pages embedded, %d similarity pairs\n",
				embStats.PagesEmbedded, len(embStats.SimilarPairs))
		}
	}

	return graph
}

// RunPostCrawlAnalysis runs all analysis modules on crawled pages (synchronous).
// Used by CLI commands that need everything computed before returning.
func RunPostCrawlAnalysis(ctx context.Context, pages []*types.PageData, cfg PostProcessConfig) (types.CrawlStats, *AdjacencyGraph) {
	stats := RunCoreAnalysis(ctx, pages, cfg)
	graph := RunDeferredAnalysis(ctx, pages, cfg, &stats)
	return stats, graph
}

func runLinkIntelligence(pages []*types.PageData, cfg PostProcessConfig, stats *types.CrawlStats) (*types.LinkIntelligenceStats, *AdjacencyGraph) {
	const topN = 20 // top-N limit for authorities, hubs, bridges
	emitPhase := func(phase string) {
		if cfg.OnLIPhase != nil {
			cfg.OnLIPhase(phase)
		}
	}
	persist := func() {
		if cfg.OnPersist != nil {
			cfg.OnPersist()
		}
	}
	type urlScore struct {
		url   string
		score float64
	}

	// --- Phase: building_graph ---
	emitPhase("building_graph")
	var graph *AdjacencyGraph
	if cfg.InternalLinksIter != nil {
		graph = BuildAdjacencyListFromDisk(pages, cfg.InternalLinksIter)
	} else {
		graph = BuildAdjacencyList(pages)
	}

	// Capture link counts before releasing slices -- downstream consumers
	// (CSV writer, HTML report, server API) need counts after slices are freed.
	// Release InternalLinks/ExternalLinks now that the CSR graph has captured all edges.
	// This frees ~3-4 GB for large crawls (37.7M URL strings for 93K pages).
	for _, p := range pages {
		p.InternalLinkCount = len(p.InternalLinks)
		p.ExternalLinkCount = len(p.ExternalLinks)
		p.InternalLinks = nil
		p.ExternalLinks = nil
	}

	// Extract metadata needed by GenerateLinkSuggestions before stripping pages.
	wordCounts := make([]int32, graph.N)
	indexable := make([]bool, graph.N)
	for i, p := range pages {
		if i < graph.N {
			wordCounts[i] = int32(p.WordCount)
			indexable[i] = p.Indexability.Indexable
		}
	}

	// Replace full PageData objects with minimal stubs to free memory.
	// Graph algorithms use graph.URLs and graph.StatusCodes instead of pages.
	// Keep Title/MetaDescription/WordCount for embeddings (runs after LI).
	// Keep Depth/ResponseTimeMs/Indexability/Headings/Canonical/Readability/SitemapData
	// and Rankpedia signal fields for LightenPages (HTML report).
	// This reduces per-page from ~20KB to ~500 bytes, saving ~6.5GB for 353K pages.
	for i, p := range pages {
		pages[i] = &types.PageData{
			URL:               p.URL,
			StatusCode:        p.StatusCode,
			Title:             p.Title,
			MetaDescription:   p.MetaDescription,
			WordCount:         p.WordCount,
			InternalLinkCount: p.InternalLinkCount,
			ExternalLinkCount: p.ExternalLinkCount,
			Depth:             p.Depth,
			ResponseTimeMs:    p.ResponseTimeMs,
			Indexability:      p.Indexability,
			URLIssues:         p.URLIssues,
			Headings:          p.Headings,
			Canonical:         p.Canonical,
			Readability:       p.Readability,
			SitemapData:       p.SitemapData,
			OriginalityScore:  p.OriginalityScore,
			Topicality:        p.Topicality,
			ContentRichness:   p.ContentRichness,
			EEAT:              p.EEAT,
			PassageReadiness:  p.PassageReadiness,
			AIReadiness:       p.AIReadiness,
			Freshness:         p.Freshness,
			AnchorHealth:      p.AnchorHealth,
			BodySize:          p.BodySize,
			PageWeight:        p.PageWeight,
			TemplateType:      p.TemplateType,
			RobotsBlocked:     p.RobotsBlocked,
			FinalURL:          p.FinalURL,
			RedirectChain:     p.RedirectChain,
			Inlinks:           p.Inlinks,
			PageRank:          p.PageRank,
			TruncatedElements: p.TruncatedElements,
		}
	}
	debug.FreeOSMemory()

	// Check LI page limit: skip heavy LI phases if page count exceeds threshold
	liMaxPages := cfg.LIMaxPages
	liSkipped := liMaxPages > 0 && graph.N > liMaxPages
	if liSkipped {
		fmt.Fprintf(os.Stderr, "\n  [link-intel] Skipping heavy LI: %d pages > %d limit\n", graph.N, liMaxPages)
	}

	// Initialize liStats early and assign to stats for incremental persistence.
	// Each phase appends results to liStats and calls persist() so that
	// completed phases survive a crash.
	liStats := &types.LinkIntelligenceStats{
		ClickDepthDistribution:   make(map[int]int),
		LinkPositionDistribution: make(map[string]int),
	}
	stats.LinkIntelligenceStats = liStats

	// Compute in-degree from graph edges (replaces nested map[string]map[string]struct{}).
	inDegree := make([]int, graph.N)
	for _, j := range graph.Edges {
		if int(j) >= len(inDegree) {
			continue
		}
		inDegree[j]++
	}

	// --- Phase: click_depth ---
	emitPhase("click_depth")
	clickDepths := ComputeClickDepth(cfg.SeedURL, graph)

	// Per-page: click depth, in/out degree, dilution, near-orphan
	for i := 0; i < graph.N && i < len(pages); i++ {
		if graph.StatusCodes[i] != 200 {
			continue
		}
		url := graph.URLs[i]
		liData := types.LinkIntelligenceData{}
		if cd, ok := clickDepths[url]; ok {
			liData.ClickDepth = &cd
		}
		liData.InDegree = inDegree[i]
		liData.OutDegree = graph.OutDegree(i)
		if liData.OutDegree > 0 {
			liData.LinkDilutionFactor = 1.0 / float64(liData.OutDegree)
		}
		liData.IsNearOrphan = liData.InDegree <= 2 && (liData.ClickDepth == nil || *liData.ClickDepth >= 4)
		pages[i].LinkIntelligence = &liData
	}

	// Aggregate: click depth distribution, unreachable pages
	var depthValues []int
	for i := 0; i < graph.N && i < len(pages); i++ {
		if pages[i].LinkIntelligence == nil {
			continue
		}
		if pages[i].LinkIntelligence.ClickDepth != nil {
			d := *pages[i].LinkIntelligence.ClickDepth
			liStats.ClickDepthDistribution[d]++
			depthValues = append(depthValues, d)
		} else if graph.StatusCodes[i] == 200 {
			liStats.UnreachablePages = append(liStats.UnreachablePages, graph.URLs[i])
		}
	}
	liStats.UnreachablePagesCount = len(liStats.UnreachablePages)
	if len(depthValues) > 0 {
		sum, maxD := 0, 0
		for _, d := range depthValues {
			sum += d
			if d > maxD {
				maxD = d
			}
		}
		liStats.AvgClickDepth = float64(sum) / float64(len(depthValues))
		liStats.MaxClickDepth = maxD
	}

	// Near orphans
	nearOrphans := DetectNearOrphans(clickDepths, graph, inDegree, 2, 4)
	for _, no := range nearOrphans {
		liStats.NearOrphans = append(liStats.NearOrphans, types.NearOrphanEntry{
			URL: no.URL, InDegree: no.InDegree, WorstSourceDepth: no.WorstSourceDepth,
		})
	}
	liStats.NearOrphansCount = len(liStats.NearOrphans)

	// Dilution warnings
	dilutionWarnings := ComputeLinkDilution(graph)
	for _, dw := range dilutionWarnings {
		liStats.DilutionWarnings = append(liStats.DilutionWarnings, types.DilutionWarningEntry{
			URL: dw.URL, OutDegree: dw.OutDegree, Warning: types.DilutionWarning(dw.Warning), EquityPerLink: dw.EquityPerLink,
		})
	}
	liStats.DilutionWarningsCount = len(liStats.DilutionWarnings)

	// Inlink buckets + wasted equity metrics
	for i, deg := range inDegree {
		switch {
		case deg == 0:
			liStats.InlinkBuckets.Zero++
		case deg == 1:
			liStats.InlinkBuckets.One++
		case deg <= 5:
			liStats.InlinkBuckets.TwoToFive++
		case deg <= 20:
			liStats.InlinkBuckets.SixToTwenty++
		default:
			liStats.InlinkBuckets.TwentyPlus++
		}
		if deg > 0 && i < graph.N && graph.StatusCodes[i] >= 300 {
			liStats.Non2xxWithInlinks++
		}
	}

	persist()

	// --- Phase: hits ---
	emitPhase("hits")
	var hitsScores map[string]HitsScores
	const hitsReduceThreshold = 100000
	const hitsSkipThreshold = 200000
	if liSkipped || graph.N > hitsSkipThreshold {
		liStats.HitsSkipped = true
		if liSkipped {
			liStats.HitsSkipReason = fmt.Sprintf("LI page limit exceeded (%d > %d)", graph.N, liMaxPages)
		} else {
			liStats.HitsSkipReason = fmt.Sprintf("auto-skipped: %d pages > %d threshold", graph.N, hitsSkipThreshold)
		}
		fmt.Fprintf(os.Stderr, "\n  [link-intel] Skipping HITS: %s\n", liStats.HitsSkipReason)
		hitsScores = make(map[string]HitsScores)
	} else {
		hitsIter := 100
		if graph.N > hitsReduceThreshold {
			hitsIter = 30
			fmt.Fprintf(os.Stderr, "\n  [link-intel] Reducing HITS iterations to %d (%d pages > %dK threshold)\n", hitsIter, graph.N, hitsReduceThreshold/1000)
		}
		hitsScores = ComputeHits(hitsIter, 1e-6, graph)
	}

	// Per-page: HITS scores
	for i := 0; i < graph.N && i < len(pages); i++ {
		if pages[i].LinkIntelligence == nil {
			continue
		}
		if hits, ok := hitsScores[graph.URLs[i]]; ok {
			pages[i].LinkIntelligence.HubScore = hits.HubScore
			pages[i].LinkIntelligence.AuthorityScore = hits.AuthorityScore
		}
	}

	// Aggregate: top authorities & hubs
	var authorities, hubs []urlScore
	for u, scores := range hitsScores {
		authorities = append(authorities, urlScore{u, scores.AuthorityScore})
		hubs = append(hubs, urlScore{u, scores.HubScore})
	}
	sort.Slice(authorities, func(i, j int) bool { return authorities[i].score > authorities[j].score })
	sort.Slice(hubs, func(i, j int) bool { return hubs[i].score > hubs[j].score })
	if len(authorities) > topN {
		authorities = authorities[:topN]
	}
	if len(hubs) > topN {
		hubs = hubs[:topN]
	}
	for _, a := range authorities {
		liStats.TopAuthorities = append(liStats.TopAuthorities, types.URLScoreEntry{URL: a.url, Score: a.score})
	}
	for _, h := range hubs {
		liStats.TopHubs = append(liStats.TopHubs, types.URLScoreEntry{URL: h.url, Score: h.score})
	}
	hitsScores = nil

	persist()

	// --- Phase: centrality ---
	emitPhase("centrality")
	var betweenness, closeness map[string]float64
	const centralityAutoSkipThreshold = 20000
	if cfg.LINoCentrality {
		liStats.CentralitySkipped = true
		liStats.CentralitySkipReason = "disabled by user"
	} else if liSkipped || graph.N > centralityAutoSkipThreshold {
		liStats.CentralitySkipped = true
		if liSkipped {
			liStats.CentralitySkipReason = fmt.Sprintf("LI page limit exceeded (%d > %d)", graph.N, liMaxPages)
		} else {
			liStats.CentralitySkipReason = fmt.Sprintf("auto-skipped: %d pages > %d threshold", graph.N, centralityAutoSkipThreshold)
		}
		fmt.Fprintf(os.Stderr, "\n  [link-intel] Skipping centrality: %s\n", liStats.CentralitySkipReason)
	} else {
		betweenness = ComputeBetweennessCentrality(5000, graph)
		closeness = ComputeClosenessCentrality(5000, graph)
		debug.FreeOSMemory()
	}

	// Per-page: centrality scores
	for i := 0; i < graph.N && i < len(pages); i++ {
		if pages[i].LinkIntelligence == nil {
			continue
		}
		url := graph.URLs[i]
		if betweenness != nil {
			pages[i].LinkIntelligence.BetweennessCentrality = betweenness[url]
		}
		if closeness != nil {
			pages[i].LinkIntelligence.ClosenessCentrality = closeness[url]
		}
	}

	// Aggregate: top bridges
	if betweenness != nil {
		var bridges []urlScore
		for u, b := range betweenness {
			bridges = append(bridges, urlScore{u, b})
		}
		sort.Slice(bridges, func(i, j int) bool { return bridges[i].score > bridges[j].score })
		if len(bridges) > topN {
			bridges = bridges[:topN]
		}
		for _, b := range bridges {
			liStats.TopBridges = append(liStats.TopBridges, types.URLScoreEntry{URL: b.url, Score: b.score})
		}
	}
	betweenness = nil
	closeness = nil

	persist()

	// --- Phase: suggestions ---
	emitPhase("suggestions")
	maxSuggestions := cfg.LIMaxSuggestions
	if maxSuggestions == 0 {
		maxSuggestions = 20
	}
	suggestions := GenerateLinkSuggestions(
		graph, nil, clickDepths, stats.PageRankScores, inDegree,
		wordCounts, indexable,
		LinkSuggestionsOptions{MaxSuggestions: maxSuggestions},
	)
	liStats.LinkSuggestions = suggestions
	liStats.LinkSuggestionsCount = len(suggestions)
	clickDepths = nil

	persist()

	fmt.Fprintf(os.Stderr, "\n  [link-intel] Graph: %d nodes, %d near-orphans, %d suggestions\n",
		graph.N, liStats.NearOrphansCount, liStats.LinkSuggestionsCount)

	return liStats, graph
}

// PostProcessConfigFromCrawl derives a PostProcessConfig from a CrawlConfig.
func PostProcessConfigFromCrawl(c types.CrawlConfig) PostProcessConfig {
	return PostProcessConfig{
		SeedURL:             c.SeedURL,
		Ngrams:              c.Ngrams,
		Language:            c.Language,
		LinkIntelligence:    c.LinkIntelligence,
		LIMaxSuggestions:    c.LIMaxSuggestions,
		LINoCentrality:      c.LINoCentrality,
		LIMaxPages:          c.LIMaxPages,
		Embeddings:          c.Embeddings,
		EmbeddingModel:      c.EmbeddingModel,
		EmbeddingProvider:   c.EmbeddingProvider,
		EmbeddingKey:        c.EmbeddingKey,
		SimilarityThreshold: c.SimilarityThreshold,
		SegmentRules:        c.SegmentRules,
	}
}

func convertNgrams(entries []NgramEntry) []types.NgramEntry {
	result := make([]types.NgramEntry, len(entries))
	for i, e := range entries {
		result[i] = types.NgramEntry{
			Term:  e.Term,
			Count: e.Count,
			TFIDF: e.TFIDF,
		}
	}
	return result
}
