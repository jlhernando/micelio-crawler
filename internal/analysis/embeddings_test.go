package analysis

import (
	"testing"
)

func TestBuildCannibalizationGroupsSingleGroup(t *testing.T) {
	urls := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}
	// a-b and b-c are similar → all in one group
	simMap := map[indexPair]float64{
		{0, 1}: 0.95,
		{1, 2}: 0.92,
	}
	groups := buildCannibalizationGroups(urls, simMap)
	if len(groups) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(groups))
	}
	if len(groups[0].URLs) != 3 {
		t.Errorf("Expected 3 URLs in group, got %d", len(groups[0].URLs))
	}
}

func TestBuildCannibalizationGroupsTwoGroups(t *testing.T) {
	urls := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
		"https://example.com/d",
	}
	// a-b form one group, c-d form another
	simMap := map[indexPair]float64{
		{0, 1}: 0.95,
		{2, 3}: 0.93,
	}
	groups := buildCannibalizationGroups(urls, simMap)
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}
}

func TestBuildCannibalizationGroupsNoSimilar(t *testing.T) {
	urls := []string{
		"https://example.com/a",
		"https://example.com/b",
	}
	simMap := map[indexPair]float64{}
	groups := buildCannibalizationGroups(urls, simMap)
	if len(groups) != 0 {
		t.Errorf("Expected 0 groups with no similarities, got %d", len(groups))
	}
}

func TestBuildCannibalizationGroupsAvgSimilarity(t *testing.T) {
	urls := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}
	// All three are similar
	simMap := map[indexPair]float64{
		{0, 1}: 0.90,
		{0, 2}: 0.80,
		{1, 2}: 0.85,
	}
	groups := buildCannibalizationGroups(urls, simMap)
	if len(groups) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(groups))
	}
	// Average = (0.90 + 0.80 + 0.85) / 3 = 0.85
	expectedAvg := 0.85
	if diff := groups[0].Similarity - expectedAvg; diff > 0.001 || diff < -0.001 {
		t.Errorf("Average similarity = %f, want %f", groups[0].Similarity, expectedAvg)
	}
}
