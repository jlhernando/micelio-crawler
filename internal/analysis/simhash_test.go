package analysis

import (
	"testing"
)

func TestSimhashIdentical(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog near the river"
	a := Simhash(text)
	b := Simhash(text)
	if a != b {
		t.Errorf("Identical texts should produce same hash: %d != %d", a, b)
	}
	if HammingDistance(a, b) != 0 {
		t.Error("Hamming distance of identical hashes should be 0")
	}
	if SimhashSimilarity(a, b) != 100 {
		t.Error("Similarity of identical hashes should be 100")
	}
}

func TestSimhashSimilar(t *testing.T) {
	a := Simhash("the quick brown fox jumps over the lazy dog near the river bank")
	b := Simhash("the quick brown fox jumps over the lazy cat near the river bank")
	dist := HammingDistance(a, b)
	sim := SimhashSimilarity(a, b)
	if sim < 70 {
		t.Errorf("Similar texts should have high similarity, got %d%% (dist=%d)", sim, dist)
	}
}

func TestSimhashDifferent(t *testing.T) {
	a := Simhash("machine learning algorithms for natural language processing tasks")
	b := Simhash("cooking recipes for traditional italian pasta dishes and sauces")
	sim := SimhashSimilarity(a, b)
	if sim > 80 {
		t.Errorf("Different texts should have lower similarity, got %d%%", sim)
	}
}

func TestSimhashEmpty(t *testing.T) {
	if Simhash("") != 0 {
		t.Error("Empty text should produce 0")
	}
}

func TestFindNearDuplicates(t *testing.T) {
	base := "the quick brown fox jumps over the lazy dog near the river"
	items := []SimhashItem{
		{URL: "https://a.com/1", Fingerprint: Simhash(base)},
		{URL: "https://a.com/2", Fingerprint: Simhash(base + " bank")},
		{URL: "https://a.com/3", Fingerprint: Simhash("completely different text about cooking recipes and food preparation")},
	}
	groups := FindNearDuplicates(items, 80)
	// Items 1 and 2 should be grouped (very similar)
	found := false
	for _, g := range groups {
		if len(g.URLs) >= 2 {
			found = true
		}
	}
	if !found {
		t.Error("Expected to find a duplicate group with similar texts")
	}
}

func TestHammingDistance(t *testing.T) {
	if HammingDistance(0, 0) != 0 {
		t.Error("Hamming(0,0) should be 0")
	}
	if HammingDistance(0xFF, 0x00) != 8 {
		t.Error("Hamming(0xFF,0x00) should be 8")
	}
}
