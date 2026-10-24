package analysis

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// clusteredVectors builds n random vectors where every 10 consecutive pages
// share a base direction (near-duplicates), so some pairs exceed 0.9.
func clusteredVectors(n, dim int, seed int64) ([]string, map[string][]float64) {
	r := rand.New(rand.NewSource(seed))
	urls := make([]string, n)
	vecs := make(map[string][]float64, n)
	var base []float64
	for i := 0; i < n; i++ {
		if i%10 == 0 {
			base = make([]float64, dim)
			for k := range base {
				base[k] = r.NormFloat64()
			}
		}
		v := make([]float64, dim)
		for k := range v {
			v[k] = base[k] + 0.3*r.NormFloat64()
		}
		urls[i] = fmt.Sprintf("https://ex.com/f%d/p%05d", i%7, i)
		vecs[urls[i]] = v
	}
	sort.Strings(urls)
	return urls, vecs
}

func normalizeAll(urls []string, vecs map[string][]float64) map[string][]float32 {
	out := make(map[string][]float32, len(vecs))
	for _, u := range urls {
		out[u] = normalize32(vecs[u])
	}
	return out
}

func TestCompareEmbeddingsMatchesNaive(t *testing.T) {
	urls, vecs := clusteredVectors(600, 64, 1)
	const thr = 0.9
	type pair struct{ a, b string }
	want := map[pair]float64{}
	for i := range urls {
		for j := i + 1; j < len(urls); j++ {
			if s := CosineSimilarity(vecs[urls[i]], vecs[urls[j]]); s >= thr {
				want[pair{urls[i], urls[j]}] = s
			}
		}
	}
	if len(want) == 0 {
		t.Fatal("test data has no similar pairs")
	}
	got := compareEmbeddings(urls, normalizeAll(urls, vecs), thr, embeddingCompareLimits{})
	if got.scope != "all" || got.compared != len(urls) {
		t.Errorf("scope=%q compared=%d", got.scope, got.compared)
	}
	// Float32 rounding can flip pairs sitting exactly on the threshold.
	if d := got.pairsTotal - len(want); d < -2 || d > 2 {
		t.Errorf("pairsTotal = %d, naive %d", got.pairsTotal, len(want))
	}
	for _, p := range got.pairs {
		w, ok := want[pair{p.URL1, p.URL2}]
		if !ok || math.Abs(w-p.Similarity) > 1e-4 {
			t.Errorf("pair %s %s sim %v, naive %v (%v)", p.URL1, p.URL2, p.Similarity, w, ok)
		}
	}
	for i := 1; i < len(got.pairs); i++ {
		if got.pairs[i].Similarity > got.pairs[i-1].Similarity {
			t.Fatal("pairs not sorted by similarity")
		}
	}
	if len(got.pairs) != min(maxSimilarPairs, got.pairsTotal) {
		t.Errorf("kept %d pairs", len(got.pairs))
	}
	if len(got.groups) == 0 {
		t.Error("no cannibalization groups")
	}
}

func TestCompareEmbeddingsFolderScopeAboveLimit(t *testing.T) {
	urls, vecs := clusteredVectors(300, 32, 2)
	got := compareEmbeddings(urls, normalizeAll(urls, vecs), 0.9, embeddingCompareLimits{allPairsMax: 100, groupMax: 20, maxEdges: 5})
	if got.scope != "folder" {
		t.Fatalf("scope = %q, want folder", got.scope)
	}
	if got.compared != 7*20 { // 7 folders, each capped at 20 pages
		t.Errorf("compared = %d, want 140", got.compared)
	}
	for _, p := range got.pairs {
		if firstURLFolder(p.URL1) != firstURLFolder(p.URL2) {
			t.Errorf("cross-folder pair %s %s", p.URL1, p.URL2)
		}
	}
}

func TestFirstURLFolder(t *testing.T) {
	for in, want := range map[string]string{
		"https://ex.com/":           "",
		"https://ex.com":            "",
		"https://ex.com/page":       "",
		"https://ex.com/blog/post":  "blog",
		"https://ex.com/blog/a/b?x": "blog",
		"https://ex.com/shop?q=1/2": "",
		"https://ex.com/docs/#frag": "docs",
	} {
		if got := firstURLFolder(in); got != want {
			t.Errorf("firstURLFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

// BenchmarkEmbeddingCompare: old single-threaded map + float64 cosine loop
// vs normalised float32 parallel dot products (2000 pages x 1536 dims).
func BenchmarkEmbeddingCompare(b *testing.B) {
	urls, vecs := clusteredVectors(2000, 1536, 3)
	b.Run("old", func(b *testing.B) {
		for it := 0; it < b.N; it++ {
			n := 0
			for i := range urls {
				for j := i + 1; j < len(urls); j++ {
					if CosineSimilarity(vecs[urls[i]], vecs[urls[j]]) >= 0.9 {
						n++
					}
				}
			}
			_ = n
		}
	})
	norm := normalizeAll(urls, vecs)
	b.Run("new", func(b *testing.B) {
		for it := 0; it < b.N; it++ {
			compareEmbeddings(urls, norm, 0.9, embeddingCompareLimits{})
		}
	})
}
