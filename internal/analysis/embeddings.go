package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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

	// Generate embeddings
	vectors := make(map[string][]float64)
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
				vectors[batch[j].url] = vec
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
			vectors[pt.url] = vec
			if dimensions == 0 {
				dimensions = len(vec)
			}
			time.Sleep(time.Duration(embeddingRateMs) * time.Millisecond)
		}
	}

	// Compute similar pairs (single O(n²) pass, reused for cannibalization)
	urls := make([]string, 0, len(vectors))
	for u := range vectors {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	// Build similarity map: key is (i,j) where i < j → similarity
	simMap := make(map[indexPair]float64)
	var similarPairs []types.SimilarPair
	for i := 0; i < len(urls); i++ {
		for j := i + 1; j < len(urls); j++ {
			sim := CosineSimilarity(vectors[urls[i]], vectors[urls[j]])
			if sim >= cfg.Threshold {
				simMap[indexPair{i, j}] = sim
				similarPairs = append(similarPairs, types.SimilarPair{
					URL1:       urls[i],
					URL2:       urls[j],
					Similarity: sim,
				})
			}
		}
	}
	sort.Slice(similarPairs, func(i, j int) bool {
		return similarPairs[i].Similarity > similarPairs[j].Similarity
	})
	if len(similarPairs) > maxSimilarPairs {
		similarPairs = similarPairs[:maxSimilarPairs]
	}

	// Union-Find for cannibalization groups (reuses precomputed similarities)
	groups := buildCannibalizationGroups(urls, simMap)
	if len(groups) > maxCanniGroups {
		groups = groups[:maxCanniGroups]
	}

	pagesEmbedded := len(vectors)

	// Release vectors map — similarity/cannibalization already computed above.
	// This frees ~12KB per page (1536-dim float64 vectors) before returning.
	vectors = nil

	return &types.EmbeddingStats{
		PagesEmbedded:         pagesEmbedded,
		SimilarPairs:          similarPairs,
		CannibalizationGroups: groups,
		Provider:              string(cfg.Provider),
		Model:                 cfg.Model,
		Dimensions:            dimensions,
	}, nil
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
