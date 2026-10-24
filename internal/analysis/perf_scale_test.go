package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// SyntheticCrawl builds n pages in a site-like link structure: every page
// links to the home page, its folder hub and outPerPage pseudo-random pages
// (half of them skewed towards low indices so in-degree is uneven).
// Deterministic so before/after measurements compare like for like.
func syntheticCrawl(n, outPerPage int) []*types.PageData {
	pages := make([]*types.PageData, n)
	url := func(i int) string {
		if i == 0 {
			return "https://ex.com/"
		}
		return "https://ex.com/d" + strconv.Itoa(i%200) + "/p" + strconv.Itoa(i)
	}
	x := uint64(88172645463325252)
	next := func() int {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return int(x % uint64(n))
	}
	for i := 0; i < n; i++ {
		links := make([]string, 0, outPerPage+2)
		links = append(links, url(0), url(i%200+1+(i%200+1)/19))
		for k := 0; k < outPerPage; k++ {
			t := next()
			if k%2 == 0 {
				t = t % (n/50 + 1)
			}
			if t%20 == 19 {
				t-- // every 20th page is never linked: orphan + unreachable
			}
			links = append(links, url(t))
		}
		title := "Page " + strconv.Itoa(i)
		pages[i] = &types.PageData{
			URL:               url(i),
			StatusCode:        200,
			Depth:             i % 7,
			Title:             &types.TextLength{Text: title, Length: len(title)},
			InternalLinks:     links,
			InternalLinkCount: len(links),
			WordCount:         300 + i%900,
		}
		pages[i].Indexability.Indexable = true
		pages[i].Security.IsHTTPS = true
	}
	return pages
}

// TestPerfStatsSize reports the stats JSON size for a large synthetic crawl.
// Run with: MICELIO_PERF=200000 go test ./internal/analysis -run TestPerfStatsSize -v
func TestPerfStatsSize(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("MICELIO_PERF"))
	if n == 0 {
		t.Skip("set MICELIO_PERF=<pages> to run")
	}
	pages := syntheticCrawl(n, 60)
	cfg := PostProcessConfig{SeedURL: "https://ex.com/", LinkIntelligence: true, LINoCentrality: true}
	start := time.Now()
	stats, _ := RunPostCrawlAnalysis(context.Background(), pages, cfg)
	elapsed := time.Since(start)
	encStart := time.Now()
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	encElapsed := time.Since(encStart)
	fmt.Printf("PERF pages=%d analysis=%s statsJSON=%.2fMB encode=%s\n", n, elapsed.Round(time.Millisecond), float64(len(data))/1e6, encElapsed.Round(time.Millisecond))
	var top map[string]json.RawMessage
	_ = json.Unmarshal(data, &top)
	for k, v := range top {
		if len(v) > 50_000 || k == "pageRankScores" || k == "orphanPages" {
			fmt.Printf("PERF   %s=%.2fMB\n", k, float64(len(v))/1e6)
		}
	}
	var li map[string]json.RawMessage
	_ = json.Unmarshal(top["linkIntelligenceStats"], &li)
	for _, k := range []string{"dilutionWarnings", "unreachablePages", "nearOrphans"} {
		fmt.Printf("PERF   linkIntelligenceStats.%s=%.2fMB\n", k, float64(len(li[k]))/1e6)
	}
}
