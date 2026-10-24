package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/types"
)

const (
	maxEmbedTextLen    = 8000
	embeddingBatchSize = 25
	embeddingRateMs    = 200
	maxSimilarPairs    = 100
	maxCanniGroups     = 50
)

// indexPair identifies a pair of URL indices for similarity lookup.
type indexPair struct{ i, j int }

// EmbeddingProvider specifies the embedding API to use.
type EmbeddingProvider string

const (
	EmbeddingOpenAI EmbeddingProvider = "openai"
	EmbeddingOllama EmbeddingProvider = "ollama"
)

// EmbeddingConfig configures embedding computation.
type EmbeddingConfig struct {
	Provider  EmbeddingProvider
	APIKey    string
	Model     string
	Threshold float64 // similarity threshold (e.g. 0.9)
}

// ComputeEmbeddings generates embeddings for all pages and computes similarity.
func ComputeEmbeddings(ctx context.Context, pages []*types.PageData, cfg EmbeddingConfig) (*types.EmbeddingStats, error) {
	if cfg.Model == "" {
		switch cfg.Provider {
		case EmbeddingOpenAI:
			cfg.Model = "text-embedding-3-small"
		case EmbeddingOllama:
			cfg.Model = "nomic-embed-text"
		}
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.9
	}

	// Collect texts and URLs
	type pageText struct {
		url  string
		text string
	}
	var inputs []pageText
	for _, p := range pages {
		if p.StatusCode != 200 || p.WordCount < 50 {
			continue
		}
		title := ""
		if p.Title != nil {
			title = p.Title.Text
		}
		desc := ""
		if p.MetaDescription != nil {
			desc = p.MetaDescription.Text
		}
		text := title + " " + desc
		if len(text) > maxEmbedTextLen {
			text = text[:maxEmbedTextLen]
		}
		inputs = append(inputs, pageText{url: p.URL, text: text})
	}

	if len(inputs) == 0 {
		return &types.EmbeddingStats{Provider: string(cfg.Provider), Model: cfg.Model}, nil
	}

	// Generate embeddings. Stored normalised as float32: half the memory of
	// the API's float64 and cosine similarity becomes a plain dot product.
	vectors := make(map[string][]float32)
	dimensions := 0

	switch cfg.Provider {
	case EmbeddingOpenAI:
		// Process in batches
		for i := 0; i < len(inputs); i += embeddingBatchSize {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			end := i + embeddingBatchSize
			if end > len(inputs) {
				end = len(inputs)
			}
			batch := inputs[i:end]

			texts := make([]string, len(batch))
			for j, pt := range batch {
				texts[j] = pt.text
			}

			vecs, err := callOpenAIEmbeddings(ctx, cfg.APIKey, cfg.Model, texts)
			if err != nil {
				return nil, fmt.Errorf("openai embeddings batch %d: %w", i/embeddingBatchSize, err)
			}

			for j, vec := range vecs {
				vectors[batch[j].url] = normalize32(vec)
				if dimensions == 0 {
					dimensions = len(vec)
				}
			}

			if i+embeddingBatchSize < len(inputs) {
				time.Sleep(time.Duration(embeddingRateMs) * time.Millisecond)
			}
		}

	case EmbeddingOllama:
		for _, pt := range inputs {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			vec, err := callOllamaEmbedding(ctx, cfg.Model, pt.text)
			if err != nil {
				return nil, fmt.Errorf("ollama embedding for %s: %w", pt.url, err)
			}
			vectors[pt.url] = normalize32(vec)
			if dimensions == 0 {
				dimensions = len(vec)
			}
			time.Sleep(time.Duration(embeddingRateMs) * time.Millisecond)
		}
	}

	// Compare pages (parallel dot products over normalised float32 vectors).
	urls := make([]string, 0, len(vectors))
	for u := range vectors {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	sim := compareEmbeddings(urls, vectors, cfg.Threshold, embeddingCompareLimits{})

	pagesEmbedded := len(urls)
	return &types.EmbeddingStats{
		PagesEmbedded:         pagesEmbedded,
		SimilarPairs:          sim.pairs,
		SimilarPairsTotal:     sim.pairsTotal,
		CannibalizationGroups: sim.groups,
		ComparisonScope:       sim.scope,
		PagesCompared:         sim.compared,
		Provider:              string(cfg.Provider),
		Model:                 cfg.Model,
		Dimensions:            dimensions,
	}, nil
}

// embeddingCompareLimits bounds the pairwise comparison. Zero values use the
// defaults below.
type embeddingCompareLimits struct {
	allPairsMax int // compare every pair up to this many pages
	groupMax    int // above it, compare within URL folders of at most this many pages
	maxEdges    int // similar pairs kept for grouping
}

const (
	defaultAllPairsMax = 10_000  // 50M dot products
	defaultGroupMax    = 5_000   // per-folder cap above allPairsMax
	defaultMaxSimEdges = 500_000 // pairs over threshold kept for union-find
)

type embeddingSimilarity struct {
	pairs      []types.SimilarPair
	pairsTotal int
	groups     []types.CannibalizationGroup
	scope      string
	compared   int
}

// compareEmbeddings finds page pairs at or above threshold.
//
// Up to allPairsMax pages every pair is compared. Larger crawls compare
// pages within the same first URL folder only (cannibalisation is mostly a
// within-section problem), each folder capped at groupMax pages, and the
// stats say so (ComparisonScope = "folder", PagesCompared). Vectors are
// normalised once to float32 so each comparison is one dot product, and rows
// are spread over all CPUs. At most maxEdges pairs are kept for grouping;
// SimilarPairsTotal counts all of them.
func compareEmbeddings(urls []string, vectors map[string][]float32, threshold float64, lim embeddingCompareLimits) embeddingSimilarity {
	if lim.allPairsMax == 0 {
		lim.allPairsMax = defaultAllPairsMax
	}
	if lim.groupMax == 0 {
		lim.groupMax = defaultGroupMax
	}
	if lim.maxEdges == 0 {
		lim.maxEdges = defaultMaxSimEdges
	}
	n := len(urls)
	out := embeddingSimilarity{scope: "all"}
	if n < 2 {
		out.compared = n
		return out
	}
	vecs := make([][]float32, n) // by url index
	for i, u := range urls {
		vecs[i] = vectors[u]
	}

	var groups [][]int32
	if n <= lim.allPairsMax {
		all := make([]int32, n)
		for i := range all {
			all[i] = int32(i)
		}
		groups = [][]int32{all}
		out.compared = n
	} else {
		out.scope = "folder"
		byFolder := map[string][]int32{}
		var order []string
		for i, u := range urls {
			f := firstURLFolder(u)
			if _, ok := byFolder[f]; !ok {
				order = append(order, f)
			}
			if len(byFolder[f]) < lim.groupMax {
				byFolder[f] = append(byFolder[f], int32(i))
			}
		}
		for _, f := range order {
			if g := byFolder[f]; len(g) >= 2 {
				groups = append(groups, g)
				out.compared += len(g)
			}
		}
	}

	// Work items: one row (group g, member k) compares member k with every
	// later member of its group.
	type row struct{ g, k int }
	type rowResult struct {
		edges []simEdge
	}
	var rows []row
	for g, members := range groups {
		for k := 0; k < len(members)-1; k++ {
			rows = append(rows, row{g, k})
		}
	}
	thr := float32(threshold)
	results := make(chan rowResult, 256)
	var next atomic.Int64
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r := int(next.Add(1) - 1)
				if r >= len(rows) {
					return
				}
				members := groups[rows[r].g]
				i := members[rows[r].k]
				a := vecs[i]
				var edges []simEdge
				for _, j := range members[rows[r].k+1:] {
					if d := dot32(a, vecs[j]); d >= thr {
						edges = append(edges, simEdge{i, j, float64(d)})
					}
				}
				if len(edges) > 0 {
					results <- rowResult{edges}
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var kept []simEdge
	top := make([]simEdge, 0, maxSimilarPairs+1)
	for res := range results {
		for _, e := range res.edges {
			out.pairsTotal++
			if len(kept) < lim.maxEdges {
				kept = append(kept, e)
			}
			top = pushTopEdge(top, e)
		}
	}
	sort.Slice(top, func(a, b int) bool { return top[a].sim > top[b].sim })
	for _, e := range top {
		out.pairs = append(out.pairs, types.SimilarPair{URL1: urls[e.i], URL2: urls[e.j], Similarity: e.sim})
	}
	out.groups = groupsFromEdges(urls, kept)
	if len(out.groups) > maxCanniGroups {
		out.groups = out.groups[:maxCanniGroups]
	}
	return out
}

// simEdge is a page pair (i < j, indices into urls) over the threshold.
type simEdge struct {
	i, j int32
	sim  float64
}

// pushTopEdge keeps the maxSimilarPairs most similar edges (unordered).
func pushTopEdge(top []simEdge, e simEdge) []simEdge {
	if len(top) < maxSimilarPairs {
		return append(top, e)
	}
	minIdx := 0
	for k := 1; k < len(top); k++ {
		if top[k].sim < top[minIdx].sim {
			minIdx = k
		}
	}
	if e.sim > top[minIdx].sim {
		top[minIdx] = e
	}
	return top
}

// normalize32 returns v scaled to unit length as float32 (zero stays zero).
func normalize32(v []float64) []float32 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	out := make([]float32, len(v))
	if norm == 0 {
		return out
	}
	inv := 1 / math.Sqrt(norm)
	for k, x := range v {
		out[k] = float32(x * inv)
	}
	return out
}

// dot32 is the dot product of two vectors; for unit vectors it is their
// cosine similarity. Vectors of different length compare as 0.
func dot32(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var s0, s1, s2, s3 float32
	k := 0
	for ; k+4 <= len(a); k += 4 {
		s0 += a[k] * b[k]
		s1 += a[k+1] * b[k+1]
		s2 += a[k+2] * b[k+2]
		s3 += a[k+3] * b[k+3]
	}
	for ; k < len(a); k++ {
		s0 += a[k] * b[k]
	}
	return s0 + s1 + s2 + s3
}

// firstURLFolder returns the first path segment of a URL ("" for the root).
func firstURLFolder(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	slash := strings.IndexByte(u, '/')
	if slash < 0 {
		return ""
	}
	rest := u[slash+1:]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 && rest[end] == '/' {
		return rest[:end]
	}
	return "" // a page directly under the root
}

// groupsFromEdges clusters pages linked by similar pairs.
func groupsFromEdges(urls []string, edges []simEdge) []types.CannibalizationGroup {
	simMap := make(map[indexPair]float64, len(edges))
	for _, e := range edges {
		simMap[indexPair{int(e.i), int(e.j)}] = e.sim
	}
	return buildCannibalizationGroups(urls, simMap)
}

// buildCannibalizationGroups uses union-find to cluster similar pages.
// simMap contains precomputed similarities for pairs exceeding the threshold.
func buildCannibalizationGroups(urls []string, simMap map[indexPair]float64) []types.CannibalizationGroup {
	n := len(urls)
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if rank[ra] < rank[rb] {
			parent[ra] = rb
		} else if rank[ra] > rank[rb] {
			parent[rb] = ra
		} else {
			parent[rb] = ra
			rank[ra]++
		}
	}

	// Union all pairs that exceeded the threshold (already computed)
	for pair := range simMap {
		union(pair.i, pair.j)
	}

	// Collect groups
	groupMap := make(map[int][]int)
	for i := 0; i < n; i++ {
		root := find(i)
		groupMap[root] = append(groupMap[root], i)
	}

	var groups []types.CannibalizationGroup
	for _, members := range groupMap {
		if len(members) < 2 {
			continue
		}
		groupURLs := make([]string, len(members))
		for i, idx := range members {
			groupURLs[i] = urls[idx]
		}
		// Compute average pairwise similarity from precomputed map
		var totalSim float64
		pairs := 0
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				a, b := members[i], members[j]
				if a > b {
					a, b = b, a
				}
				if sim, ok := simMap[indexPair{a, b}]; ok {
					totalSim += sim
				}
				pairs++
			}
		}
		avgSim := 0.0
		if pairs > 0 {
			avgSim = totalSim / float64(pairs)
		}
		groups = append(groups, types.CannibalizationGroup{
			URLs:       groupURLs,
			Similarity: avgSim,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].URLs) > len(groups[j].URLs)
	})
	return groups
}

// ── API Clients ──

func callOpenAIEmbeddings(ctx context.Context, apiKey, model string, texts []string) ([][]float64, error) {
	body := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Index < result.Data[j].Index
	})

	vecs := make([][]float64, len(result.Data))
	for i, d := range result.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

func callOllamaEmbedding(ctx context.Context, model, text string) ([]float64, error) {
	body := map[string]interface{}{
		"model":  model,
		"prompt": text,
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("Ollama API error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Embedding, nil
}
