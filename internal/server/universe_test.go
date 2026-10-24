package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

func universePages(n int) []*types.PageData {
	pages := make([]*types.PageData, n)
	for i := range pages {
		dir := fmt.Sprintf("/d%d/", i%3)
		pages[i] = &types.PageData{
			URL:          fmt.Sprintf("https://ex.com%sp%d", dir, i),
			StatusCode:   200,
			Title:        &types.TextLength{Text: fmt.Sprintf("Page %d", i)},
			PageRank:     float64(n - i),
			AutoSegments: &types.PageSegments{Dir1: dir, Dir2: dir, Template: "article"},
		}
	}
	pages[5].StatusCode = 404
	return pages
}

func TestUniverseLayoutOrderAndClusters(t *testing.T) {
	pages := universePages(30)
	l := buildUniverseLayout(&CrawlResults{Pages: pages}, "auto", "")
	if l.dim != "section" || len(l.clusters) != 3 {
		t.Fatalf("dim=%s clusters=%d, want section and 3", l.dim, len(l.clusters))
	}
	for k := 1; k < len(l.order); k++ {
		if pages[l.order[k-1]].PageRank < pages[l.order[k]].PageRank {
			t.Fatalf("order not PageRank desc at %d", k)
		}
	}
	for _, c := range l.clusters {
		if c.Count != 10 || len(c.Top) != universeCardsPerClus {
			t.Errorf("cluster %s count=%d top=%d", c.Key, c.Count, len(c.Top))
		}
		for j := 1; j < len(c.Top); j++ {
			if c.Top[j-1].PR < c.Top[j].PR {
				t.Errorf("cluster %s top not sorted", c.Key)
			}
		}
	}
	if l.errors != 1 {
		t.Errorf("errors = %d, want 1", l.errors)
	}
}

func TestUniverseOtherCluster(t *testing.T) {
	n := universeMaxClusters + 15
	pages := make([]*types.PageData, n)
	for i := range pages {
		d := fmt.Sprintf("/d%d/", i)
		pages[i] = &types.PageData{URL: "https://ex.com" + d, StatusCode: 200, AutoSegments: &types.PageSegments{Dir1: d}}
	}
	l := buildUniverseLayout(&CrawlResults{Pages: pages}, "dir1", "")
	if len(l.clusters) != universeMaxClusters {
		t.Fatalf("clusters = %d, want %d", len(l.clusters), universeMaxClusters)
	}
	last := l.clusters[len(l.clusters)-1]
	if last.Key != universeOtherKey || last.Count != n-(universeMaxClusters-1) {
		t.Errorf("other = %s/%d", last.Key, last.Count)
	}
}

func TestUniverseEndpoints(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	for i, p := range pages {
		p.PageRank = float64(10 - i)
		p.AutoSegments = &types.PageSegments{Dir1: "/cat/"}
	}
	pages[0].AutoSegments.Dir1 = "/"
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)
	m.mu.Lock()
	m.results["uv"] = &CrawlResults{Pages: pages, Graph: g, PagesReady: true}
	m.mu.Unlock()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/uv/universe?dim=dir1", nil))
	var meta struct {
		Total    int               `json:"total"`
		Clusters []universeCluster `json:"clusters"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Total != 5 || len(meta.Clusters) != 2 || meta.Clusters[0].Key != "/cat/" {
		t.Fatalf("meta = %+v", meta)
	}
	// p2 is linked from /, /cat/ and /cat/p1.
	for _, c := range meta.Clusters[0].Top {
		if c.URL == "https://ex.com/cat/p2" && c.In != 3 {
			t.Errorf("p2 in = %d, want 3", c.In)
		}
	}

	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/uv/universe/nodes?dim=dir1&offset=1&limit=3", nil))
	b := rec.Body.Bytes()
	if string(b[:4]) != "UNV1" || len(b) != universeHeaderBytes+3*universeBytesPerNode {
		t.Fatalf("chunk header %q len %d", b[:4], len(b))
	}
	if off, cnt, tot := binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint32(b[8:]), binary.LittleEndian.Uint32(b[12:]); off != 1 || cnt != 3 || tot != 5 {
		t.Errorf("offset/count/total = %d/%d/%d", off, cnt, tot)
	}

	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/uv/universe/cluster?dim=dir1&c=0&sort=title&dir=asc&limit=2", nil))
	var rows struct {
		Rows  []universeCard `json:"rows"`
		Total int            `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if rows.Total != 4 || len(rows.Rows) != 2 || rows.Rows[0].Title > rows.Rows[1].Title {
		t.Errorf("cluster rows = %+v", rows)
	}
}

func BenchmarkUniverseLayout1M(b *testing.B) {
	n := 1_000_000
	pages := make([]*types.PageData, n)
	for i := range pages {
		d := fmt.Sprintf("/d%d/", i%400)
		pages[i] = &types.PageData{URL: fmt.Sprintf("https://ex.com%s%d", d, i), StatusCode: 200, PageRank: float64(i % 9973),
			Title: &types.TextLength{Text: "t"}, AutoSegments: &types.PageSegments{Dir1: d}}
	}
	res := &CrawlResults{Pages: pages}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buildUniverseLayout(res, "dir1", "")
	}
}

func TestUniverseEdgesAndNodeLinks(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	for i, p := range pages {
		p.PageRank = float64(10 - i) // order position == page index
		p.AutoSegments = &types.PageSegments{Dir1: "/cat/"}
	}
	pages[0].AutoSegments.Dir1 = "/"
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)
	m.mu.Lock()
	m.results["uv"] = &CrawlResults{Pages: pages, Graph: g, PagesReady: true}
	m.mu.Unlock()

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	var meta struct {
		Links      []universeLink `json:"links"`
		EdgesTotal int            `json:"edgesTotal"`
	}
	if err := json.NewDecoder(get("/api/crawl/uv/universe").Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	// "/" links to 3 /cat/ pages; /cat/ and /cat/p2 link back to "/".
	if meta.EdgesTotal != len(g.Edges) || len(meta.Links) != 1 || meta.Links[0].Count != 5 {
		t.Fatalf("meta links=%+v edges=%d", meta.Links, meta.EdgesTotal)
	}

	b := get("/api/crawl/uv/universe/edges?n=5&per=100").Body.Bytes()
	count := int(binary.LittleEndian.Uint32(b[8:]))
	if string(b[:4]) != "UNE1" || count != len(g.Edges) || binary.LittleEndian.Uint32(b[12:]) != 1 {
		t.Fatalf("edges header %q count=%d done=%d", b[:4], count, binary.LittleEndian.Uint32(b[12:]))
	}
	// Only links among the first 2 loaded pages: / <-> /cat/.
	b = get("/api/crawl/uv/universe/edges?n=2&per=100").Body.Bytes()
	if c := binary.LittleEndian.Uint32(b[8:]); c != 2 {
		t.Errorf("edges among first 2 = %d, want 2", c)
	}

	var nl struct {
		In       []int32 `json:"in"`
		Out      []int32 `json:"out"`
		InTotal  int     `json:"inTotal"`
		OutTotal int     `json:"outTotal"`
	}
	if err := json.NewDecoder(get("/api/crawl/uv/universe/node-links?i=3").Body).Decode(&nl); err != nil {
		t.Fatal(err)
	}
	// /cat/p2 (index 3) is linked from /, /cat/ and /cat/p1 and links to /.
	if nl.InTotal != 3 || nl.OutTotal != 1 || len(nl.Out) != 1 || nl.Out[0] != 0 {
		t.Errorf("node links = %+v", nl)
	}
}

func TestUniverseLinkTreeLayout(t *testing.T) {
	pages := linkedPages() // / -> /cat/, p1, p2 ; /cat/ -> p1, p2, / ; p1 -> p2, /cat ; p2 -> / ; /lonely
	for i, p := range pages {
		p.PageRank = float64(10 - i)
		p.AutoSegments = &types.PageSegments{Dir1: "/cat/"}
	}
	pages[0].AutoSegments.Dir1 = "/"
	iter := diskIter(pages)
	stripLinks(pages)
	res := &CrawlResults{Pages: pages, Graph: analysis.BuildAdjacencyListFromDisk(pages, iter)}
	l := buildUniverseLayout(res, "dir1", "")
	if l.layout != "links" || l.root != 0 {
		t.Fatalf("layout=%s root=%d", l.layout, l.root)
	}
	// p1 and p2 are linked from "/" directly (depth 1), so "/" is their parent.
	want := []int32{-1, 0, 0, 0, -1}
	for i, w := range want {
		if l.parent[i] != w {
			t.Errorf("parent[%s] = %d, want %d", pages[i].URL, l.parent[i], w)
		}
	}
	if l.unreached != 1 { // /lonely
		t.Errorf("unreached = %d, want 1", l.unreached)
	}
	if got := buildUniverseLayout(res, "dir1", "folders").layout; got != "folders" {
		t.Errorf("folders layout = %s", got)
	}
}

func BenchmarkUniverseLinkLayout1M(b *testing.B) {
	n := 1_000_000
	pages := make([]*types.PageData, n)
	for i := range pages {
		d := fmt.Sprintf("/d%d/", i%400)
		pages[i] = &types.PageData{URL: fmt.Sprintf("https://ex.com%s%d", d, i), StatusCode: 200, PageRank: float64(i % 9973),
			Title: &types.TextLength{Text: "t"}, AutoSegments: &types.PageSegments{Dir1: d}}
	}
	g := &analysis.AdjacencyGraph{N: n, Offsets: make([]int32, n+1)}
	x := uint64(1)
	for i := 0; i < n; i++ {
		for j := 0; j < 20; j++ {
			x = splitmix64(x)
			g.Edges = append(g.Edges, int32(x%uint64(n)))
		}
		g.Offsets[i+1] = int32(len(g.Edges))
	}
	res := &CrawlResults{Pages: pages, Graph: g}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buildUniverseLayout(res, "dir1", "links")
	}
}

func TestUniverseSectionGrouping(t *testing.T) {
	var pages []*types.PageData
	add := func(u string) { pages = append(pages, &types.PageData{URL: "https://ex.com" + u}) }
	add("/")
	for i := 0; i < 20; i++ {
		add(fmt.Sprintf("/en-US/docs/Web/CSS/p%d", i))
		add(fmt.Sprintf("/en-US/docs/Web/API/p%d", i))
	}
	add("/en-US/docs/Web/index")
	add("/en-US/blog/post")
	seg := universeSegmenter(pages, "section")
	want := map[string]string{
		"/": "/", "/en-US/docs/Web/CSS/p3": "/en-US/docs/Web/CSS/", "/en-US/docs/Web/API/p1": "/en-US/docs/Web/API/",
		"/en-US/docs/Web/index": "/en-US/docs/Web/", "/en-US/blog/post": "/en-US/blog/",
	}
	for _, p := range pages {
		if w, ok := want[strings.TrimPrefix(p.URL, "https://ex.com")]; ok && seg(p) != w {
			t.Errorf("%s -> %s, want %s", p.URL, seg(p), w)
		}
	}
}

func TestUniverseNodeLinksOrphan(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	for i, p := range pages {
		p.PageRank = float64(10 - i)
	}
	iter := diskIter(pages)
	stripLinks(pages)
	m.mu.Lock()
	m.results["or"] = &CrawlResults{Pages: pages, Graph: analysis.BuildAdjacencyListFromDisk(pages, iter), PagesReady: true}
	m.mu.Unlock()
	get := func(i int) map[string]interface{} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/crawl/or/universe/node-links?i=%d", i), nil))
		var body map[string]interface{}
		json.NewDecoder(rec.Body).Decode(&body)
		return body
	}
	if b := get(0); b["level"] != float64(0) || b["orphan"] != nil {
		t.Errorf("start page = level %v orphan %v", b["level"], b["orphan"])
	}
	if b := get(4); b["orphan"] != true || b["level"] != nil { // /lonely
		t.Errorf("/lonely = level %v orphan %v", b["level"], b["orphan"])
	}
}
