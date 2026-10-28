package server

import (
	"encoding/binary"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"weak"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// Universe view: a 3D overview of every crawled page, grouped into clusters.
//
// The layout is computed once per crawl on the server and streamed to the
// dashboard as compact binary chunks (10 bytes per page), ordered by PageRank
// so the first chunk always holds the most important pages. The browser keeps
// the bytes in typed arrays and hands them straight to the GPU, so it never
// builds a JS object per page. Page details are fetched only on demand.

const (
	universeMaxClusters  = 60  // clusters beyond this are merged into "Other"
	universeCardsPerClus = 8   // top pages per cluster listed in the cluster summary
	universeMaxLinks     = 240 // strongest cluster-to-cluster connections sent to the client
	universeMaxChunk     = 200_000
	universeCacheSize    = 2 // layouts kept in memory (10 bytes/page each)
	universeOtherKey     = "__other__"
	universeHeaderBytes  = 16
	universeBytesPerNode = 10
)

// universeDims are the grouping dimensions the view supports.
var universeDims = []string{"section", "dir1", "dir2", "template"}

type universeCard struct {
	Index  int     `json:"i"`
	URL    string  `json:"url"`
	Title  string  `json:"title"`
	PR     float64 `json:"pr"`
	In     int     `json:"in"`
	Out    int     `json:"out"`
	Status int     `json:"status"`
	Depth  int     `json:"depth"`
}

type universeCluster struct {
	Key    string         `json:"key"`
	Label  string         `json:"label"`
	Count  int            `json:"count"`
	PRSum  float64        `json:"prSum"`
	InAvg  float64        `json:"inAvg"`
	OutAvg float64        `json:"outAvg"`
	Errors int            `json:"errors"`
	Center [3]float64     `json:"center"`
	Spread float64        `json:"spread"`
	Top    []universeCard `json:"top"`
	// members holds global order positions, already sorted by PageRank.
	members []int32
}

// universeLink is the number of real links between two clusters.
type universeLink struct {
	A     int `json:"a"`
	B     int `json:"b"`
	Count int `json:"count"`
}

type universeLayout struct {
	key      string
	dim      string
	layout   string                     // "links" (link tree) or "folders"
	owner    weak.Pointer[CrawlResults] // weak, so the cache never keeps evicted results alive
	n        int
	order    []int32 // order position -> page index (PageRank desc)
	inDeg    []int32 // page index -> in-degree
	clusters []*universeCluster
	bound    float64 // world units mapped to int16 max
	center   [3]float64
	extent   float64
	errors   int
	// Structure-of-arrays payload, pre-encoded little endian.
	pos   []byte // int16 x,y,z
	clus  []byte // uint16 cluster index
	rank  []byte // uint8 PageRank bucket
	flags []byte // bits 0-1 status class, bit 2 blocked by robots.txt
	// Link graph lookups. posOf maps a page index to its order position.
	posOf []int32
	links []universeLink
	// sitewide marks pages linked from most of the site (menus, footers).
	// Links to them are template links, so they are left out of the
	// architecture view unless asked for.
	sitewide []bool
	// Link tree (layout "links"): parent page index and click level per page,
	// -1 for pages not reached from the root.
	// view holds the pages (and graph) this layout was built from; hidden
	// counts the pages it leaves out (see universeView).
	view   *CrawlResults
	hidden universeHidden
	all    bool

	parent          []int32
	inMainTree      []bool // reached from the start page (not from an extra root)
	level           []int32
	root            int32
	unreached       int
	maxLevel        int
	sitewideTargets int
	edgesContextual int
}

var universeCache = struct {
	sync.Mutex
	items []*universeLayout
}{}

func universeCacheKey(crawlID, dim, layout string, n int, all bool) string {
	return crawlID + "|" + dim + "|" + layout + "|" + strconv.Itoa(n) + "|" + strconv.FormatBool(all)
}

// universeArchitecture reports whether a page is part of the site's
// architecture: fetched, answered 2xx and did not redirect off the site.
// Errors, URLs blocked by robots.txt and redirects to other hosts are left
// out of Universe unless asked for. A URL that redirects is stored with the
// final status (often 200) and its RedirectChain; on the same host it holds
// the destination page (the link graph merges both URLs), so it stays.
func universeArchitecture(p *types.PageData) bool {
	return !p.RobotsBlocked && !universeLeavesSite(p) && p.StatusCode >= 200 && p.StatusCode < 300
}

// universeLeavesSite reports a redirect whose destination is on another host.
func universeLeavesSite(p *types.PageData) bool {
	if len(p.RedirectChain) == 0 || p.FinalURL == "" {
		return false
	}
	a, errA := url.Parse(p.URL)
	b, errB := url.Parse(p.FinalURL)
	return errA == nil && errB == nil && !strings.EqualFold(a.Host, b.Host)
}

// universeStatus is the status class Universe draws with "Redirects, errors
// & blocked" on: 0 page, 1 redirect, 2 error (pageStatusClass, with a
// followed redirect counted as redirect).
func universeStatus(p *types.PageData) byte {
	c := pageStatusClass(p)
	if c == 0 && len(p.RedirectChain) > 0 {
		return 1
	}
	return c
}

// universeHidden counts the pages that are not architecture, by kind (left
// out of the view unless all is set).
type universeHidden struct {
	Redirects int `json:"redirects"`
	Errors    int `json:"errors"`
	Blocked   int `json:"blocked"`
}

// universeStatusCounts counts the drawn pages by status kind (status pills).
func universeStatusCounts(pages []*types.PageData) universeHidden {
	var c universeHidden
	for _, p := range pages {
		switch {
		case p.RobotsBlocked:
			c.Blocked++
		case universeStatus(p) == 1:
			c.Redirects++
		case universeStatus(p) == 2:
			c.Errors++
		}
	}
	return c
}

// universeView returns the pages (and the link graph between them) that
// Universe draws: every page when all is set, else only architecture pages.
// The view shares page pointers with results; handlers given a layout must
// use l.view, whose indices match the layout's.
func universeView(results *CrawlResults, all bool) (*CrawlResults, universeHidden) {
	var hidden universeHidden
	keep := make([]int32, len(results.Pages))
	kept := 0
	for i, p := range results.Pages {
		arch := universeArchitecture(p)
		if all || arch {
			keep[i] = int32(kept)
			kept++
		} else {
			keep[i] = -1
		}
		if arch {
			continue
		}
		switch {
		case p.RobotsBlocked:
			hidden.Blocked++
		case universeStatus(p) == 1:
			hidden.Redirects++
		default:
			hidden.Errors++
		}
	}
	if kept == len(results.Pages) {
		return results, hidden
	}
	view := &CrawlResults{Pages: make([]*types.PageData, 0, kept), PagesReady: true}
	for i, p := range results.Pages {
		if keep[i] >= 0 {
			view.Pages = append(view.Pages, p)
		}
	}
	if g := universeGraph(results); g != nil {
		sub := &analysis.AdjacencyGraph{
			N: kept, Offsets: make([]int32, 1, kept+1),
			URLs: make([]string, 0, kept), StatusCodes: make([]int16, 0, kept),
		}
		for i := 0; i < g.N; i++ {
			if keep[i] < 0 {
				continue
			}
			for _, t := range g.Edges[g.Offsets[i]:g.Offsets[i+1]] {
				if nt := keep[t]; nt >= 0 {
					sub.Edges = append(sub.Edges, nt)
				}
			}
			sub.Offsets = append(sub.Offsets, int32(len(sub.Edges)))
			sub.URLs = append(sub.URLs, g.URLs[i])
			sub.StatusCodes = append(sub.StatusCodes, g.StatusCodes[i])
		}
		view.Graph = sub
	}
	return view, hidden
}

// getUniverseLayout returns a cached layout or builds one. Callers must hold
// results.mu.RLock.
func getUniverseLayout(crawlID string, results *CrawlResults, dim, layout string, all bool) *universeLayout {
	// Resolve "auto" first so it shares the cache entry of the grouping it
	// picks (the dashboard asks for that grouping by name right after).
	dim = resolveUniverseDim(results.Pages, dim)
	layout = resolveUniverseLayout(results, layout)
	key := universeCacheKey(crawlID, dim, layout, len(results.Pages), all)
	universeCache.Lock()
	for _, l := range universeCache.items {
		if l.key == key && l.owner.Value() == results {
			universeCache.Unlock()
			return l
		}
	}
	universeCache.Unlock()

	view, hidden := universeView(results, all)
	l := buildUniverseLayout(view, dim, layout)
	l.view, l.hidden, l.all = view, hidden, all
	l.key = key
	l.owner = weak.Make(results)
	universeCache.Lock()
	kept := universeCache.items[:0]
	for _, old := range universeCache.items {
		if old.key != key && old.owner.Value() != nil {
			kept = append(kept, old)
		}
	}
	universeCache.items = append([]*universeLayout{l}, kept...)
	if len(universeCache.items) > universeCacheSize {
		universeCache.items = universeCache.items[:universeCacheSize]
	}
	universeCache.Unlock()
	return l
}

// universeSegmenter returns the grouping function for dim. "section" finds
// the site's trunk (the path most pages share, e.g. /en-US/docs/Web/) by
// walking down while one folder holds more than 60% of the pages below it,
// then groups pages by the folder right under it (/en-US/docs/Web/CSS/).
// Pages outside the trunk are grouped where they leave it (/en-US/blog/).
func universeSegmenter(pages []*types.PageData, dim string) func(*types.PageData) string {
	if dim != "section" {
		return func(p *types.PageData) string { return universeSegment(p, dim) }
	}
	paths := make([][]string, len(pages))
	for i, p := range pages {
		if u, err := url.Parse(p.URL); err == nil {
			paths[i] = splitPathSegments(u.Path)
			if strings.HasSuffix(u.Path, "/") { // folder index page: belongs to that folder
				paths[i] = append(paths[i], "")
			}
		}
	}
	var trunk []string
	for depth := 0; depth < 6; depth++ {
		counts := map[string]int{}
		under := 0
		for _, s := range paths {
			if len(s) > depth+1 && hasPrefix(s, trunk) { // folder, not the page itself
				counts[s[depth]]++
				under++
			}
		}
		best, bc := "", 0
		for k, c := range counts {
			if c > bc || (c == bc && k < best) {
				best, bc = k, c
			}
		}
		if under == 0 || float64(bc) <= 0.6*float64(under) {
			break
		}
		trunk = append(trunk, best)
	}
	index := make(map[*types.PageData]int, len(pages))
	for i, p := range pages {
		index[p] = i
	}
	folderKey := func(segs []string) string {
		if len(segs) == 0 {
			return "/"
		}
		return dirPath(segs) + "/"
	}
	return func(p *types.PageData) string {
		s := paths[index[p]]
		if len(s) <= 1 && (len(s) == 0 || s[0] == "") {
			return "/"
		}
		folder := s[:len(s)-1]
		d := 0
		for d < len(trunk) && d < len(folder) && folder[d] == trunk[d] {
			d++
		}
		if d == len(folder) { // the page sits in the trunk or above it
			return folderKey(folder)
		}
		return folderKey(folder[:d+1])
	}
}

func hasPrefix(s, prefix []string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func universeSegment(p *types.PageData, dim string) string {
	var v string
	if p.AutoSegments != nil {
		switch dim {
		case "dir1":
			v = p.AutoSegments.Dir1
		case "dir2":
			v = p.AutoSegments.Dir2
		case "template":
			v = p.AutoSegments.Template
		}
	}
	if v == "" && dim == "template" {
		v = p.TemplateType
	}
	if v == "" {
		if dim == "template" {
			return "other"
		}
		return "/"
	}
	return v
}

// resolveUniverseDim picks a grouping for "auto". A grouping is usable when
// its groups average 10+ pages (so it is not one group per page, like
// /catalogue/<book>/). Among usable ones, the first in dir1, dir2, template
// order (section first) whose largest group holds at most 60% of the site wins; otherwise
// the usable one with the smallest largest group.
func resolveUniverseDim(pages []*types.PageData, dim string) string {
	for _, d := range universeDims {
		if d == dim {
			return dim
		}
	}
	n := len(pages)
	if n == 0 {
		return "dir1"
	}
	best, bestShare := "dir1", 2.0
	for _, d := range universeDims {
		counts := map[string]int{}
		largest := 0
		seg := universeSegmenter(pages, d)
		for _, p := range pages {
			counts[seg(p)]++
		}
		for _, c := range counts {
			largest = max(largest, c)
		}
		if n >= 50 && float64(n)/float64(len(counts)) < 10 {
			continue
		}
		share := float64(largest) / float64(n)
		if share <= 0.6 {
			return d
		}
		if share < bestShare {
			best, bestShare = d, share
		}
	}
	return best
}

// splitmix64 gives a deterministic pseudo-random stream per page.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func unitFloat(x uint64) float64 { return float64(x>>11) / (1 << 53) }

func gaussPair(seed uint64) (float64, float64) {
	u := max(unitFloat(splitmix64(seed)), 1e-12)
	v := unitFloat(splitmix64(seed ^ 0xabcdef))
	r := math.Sqrt(-2 * math.Log(u))
	return r * math.Cos(2*math.Pi*v), r * math.Sin(2*math.Pi*v)
}

func pageTitle(p *types.PageData) string {
	if p.Title != nil && p.Title.Text != "" {
		return p.Title.Text
	}
	return p.URL
}

// universeInDegrees returns each page's inlink count. With a graph it is the
// graph's cached in-degree (shared, read-only; self-links do not count, same
// as the inlinks API and the pages table).
func universeInDegrees(results *CrawlResults) []int32 {
	pages := results.Pages
	if g := results.Graph; g != nil && g.N == len(pages) {
		return g.InDegree()
	}
	in := make([]int32, len(pages))
	for i, p := range pages {
		switch {
		case p.Inlinks > 0:
			in[i] = int32(p.Inlinks)
		case p.LinkIntelligence != nil:
			in[i] = int32(p.LinkIntelligence.InDegree)
		}
	}
	return in
}

func pageOutDegree(p *types.PageData) int {
	if n := len(p.InternalLinks); n > 0 {
		return n
	}
	if p.InternalLinkCount > 0 {
		return p.InternalLinkCount
	}
	if p.LinkIntelligence != nil {
		return p.LinkIntelligence.OutDegree
	}
	return 0
}

// pageStatusClass is statusClass for a page. URLs blocked by robots.txt were
// never fetched, so they have no status code but are not errors.
func pageStatusClass(p *types.PageData) byte {
	if p.RobotsBlocked {
		return 0
	}
	return statusClass(p.StatusCode)
}

func statusClass(code int) byte {
	switch {
	case code >= 200 && code < 300:
		return 0
	case code >= 300 && code < 400:
		return 1
	default:
		return 2
	}
}

func buildUniverseLayout(results *CrawlResults, requested, layout string) *universeLayout {
	pages := results.Pages
	n := len(pages)
	dim := resolveUniverseDim(pages, requested)
	l := &universeLayout{dim: dim, layout: resolveUniverseLayout(results, layout), n: n, inDeg: universeInDegrees(results)}

	// Global order: PageRank desc, in-degree as tiebreaker.
	l.order = make([]int32, n)
	for i := range l.order {
		l.order[i] = int32(i)
	}
	sort.SliceStable(l.order, func(a, b int) bool {
		pa, pb := pages[l.order[a]], pages[l.order[b]]
		if pa.PageRank != pb.PageRank {
			return pa.PageRank > pb.PageRank
		}
		return l.inDeg[l.order[a]] > l.inDeg[l.order[b]]
	})

	// Clusters by segment, biggest first; the long tail becomes "Other".
	segOf := universeSegmenter(pages, dim)
	counts := map[string]int{}
	for _, p := range pages {
		counts[segOf(p)]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if counts[keys[a]] != counts[keys[b]] {
			return counts[keys[a]] > counts[keys[b]]
		}
		return keys[a] < keys[b]
	})
	clusterOf := make(map[string]int, len(keys))
	for i, k := range keys {
		if i < universeMaxClusters-1 || len(keys) <= universeMaxClusters {
			clusterOf[k] = len(l.clusters)
			l.clusters = append(l.clusters, &universeCluster{Key: k, Label: k})
			continue
		}
		if _, ok := clusterOf[universeOtherKey]; !ok {
			clusterOf[universeOtherKey] = len(l.clusters)
			l.clusters = append(l.clusters, &universeCluster{Key: universeOtherKey, Label: "Other"})
		}
		clusterOf[k] = clusterOf[universeOtherKey]
	}

	pageCluster := make([]uint16, n)
	maxPR := 0.0
	for pos, pi := range l.order {
		p := pages[pi]
		ci := clusterOf[segOf(p)]
		pageCluster[pi] = uint16(ci)
		c := l.clusters[ci]
		c.Count++
		c.PRSum += p.PageRank
		c.InAvg += float64(l.inDeg[pi])
		c.OutAvg += float64(pageOutDegree(p))
		if pageStatusClass(p) == 2 {
			c.Errors++
			l.errors++
		}
		c.members = append(c.members, int32(pos))
		maxPR = max(maxPR, p.PageRank)
		if len(c.Top) < universeCardsPerClus {
			c.Top = append(c.Top, universeCardFor(l, pages, pos))
		}
	}
	for _, c := range l.clusters {
		if c.Count > 0 {
			c.InAvg /= float64(c.Count)
			c.OutAvg /= float64(c.Count)
		}
	}

	l.posOf = make([]int32, n)
	for pos, pi := range l.order {
		l.posOf[pi] = int32(pos)
	}
	l.sitewide, l.sitewideTargets = universeSitewide(results, l.inDeg, pageCluster, l.clusters)
	l.links, l.edgesContextual = universeClusterLinks(results, pageCluster, l.sitewide)

	var xyz []float64
	if l.layout == "links" {
		xyz = universeLinkPositions(results, l, pageCluster)
	} else {
		xyz = universeFolderPositions(l, n)
	}
	bound := 1.0
	for _, v := range xyz {
		bound = math.Max(bound, math.Abs(v))
	}
	// Frame for the camera: centre of all pages and the radius that holds 98%
	// of them (a few far-off pages should not shrink the whole picture).
	if n > 0 {
		for a := 0; a < 3; a++ {
			s := 0.0
			for i := 0; i < n; i++ {
				s += xyz[i*3+a]
			}
			l.center[a] = s / float64(n)
		}
		dist := make([]float64, n)
		for i := 0; i < n; i++ {
			dx, dy, dz := xyz[i*3]-l.center[0], xyz[i*3+1]-l.center[1], xyz[i*3+2]-l.center[2]
			dist[i] = math.Sqrt(dx*dx + dy*dy + dz*dz)
		}
		sort.Float64s(dist)
		l.extent = math.Max(500, dist[int(float64(n-1)*0.98)])
	}
	l.bound = bound * 1.01

	l.pos = make([]byte, 6*n)
	l.clus = make([]byte, 2*n)
	l.rank = make([]byte, n)
	l.flags = make([]byte, n)
	for pos, pi := range l.order {
		p := pages[pi]
		for a := 0; a < 3; a++ {
			v := int16(math.Round(xyz[int(pi)*3+a] / l.bound * 32767))
			binary.LittleEndian.PutUint16(l.pos[pos*6+a*2:], uint16(v))
		}
		binary.LittleEndian.PutUint16(l.clus[pos*2:], pageCluster[pi])
		if maxPR > 0 {
			l.rank[pos] = byte(math.Round(255 * math.Sqrt(math.Max(0, p.PageRank)/maxPR)))
		}
		l.flags[pos] = universeStatus(p)
		if p.RobotsBlocked {
			l.flags[pos] |= 4 // bit 2: blocked by robots.txt (status class 0)
		}
	}
	return l
}

// universeFolderPositions places each cluster around its own centre. The
// biggest clusters sit near the middle and the long tail further out, along a
// golden-ratio sequence so clusters next to each other in size never line up.
// Inside a cluster the highest PageRank page is at the centre and pages move
// outwards as PageRank drops.
func universeFolderPositions(l *universeLayout, n int) []float64 {
	k := len(l.clusters)
	radius := math.Max(1400, 470*math.Sqrt(float64(k)))
	if k > 0 { // room for the biggest cluster's spread on both sides
		radius = math.Max(radius, 3.2*(110+26*math.Sqrt(float64(l.clusters[0].Count))))
	}
	for i, c := range l.clusters {
		f := (float64(i) + 0.5) / float64(k)
		_, frac := math.Modf((float64(i) + 0.5) * 0.6180339887)
		y := 1 - 2*frac
		r := math.Sqrt(math.Max(0, 1-y*y))
		th := float64(i) * math.Pi * (3 - math.Sqrt(5))
		rr := radius * (0.4 + 0.6*math.Sqrt(f))
		c.Center = [3]float64{math.Cos(th) * r * rr, y * rr * 0.62, math.Sin(th) * r * rr}
		c.Spread = math.Min(110+26*math.Sqrt(float64(c.Count)), radius*0.32)
	}
	xyz := make([]float64, 3*n)
	for _, c := range l.clusters {
		for j, pos := range c.members {
			pi := l.order[pos]
			var x, y, z float64
			if j > 0 {
				seed := uint64(pi)*0x2545f4914f6cdd1d + 1
				gx, gy := gaussPair(seed)
				gz, jitter := gaussPair(seed + 7)
				d := c.Spread * (0.12 + 0.88*math.Sqrt(float64(j)/float64(len(c.members)))) * (0.9 + 0.08*jitter)
				norm := math.Max(1e-9, math.Sqrt(gx*gx+gy*gy+gz*gz))
				x, y, z = gx/norm*d, gy/norm*d*0.8, gz/norm*d
			}
			xyz[pi*3], xyz[pi*3+1], xyz[pi*3+2] = x+c.Center[0], y+c.Center[1], z+c.Center[2]
		}
	}
	return xyz
}

func gaussOne(seed uint64) float64 {
	g, _ := gaussPair(seed)
	return g
}

func universeCardFor(l *universeLayout, pages []*types.PageData, pos int) universeCard {
	pi := l.order[pos]
	p := pages[pi]
	return universeCard{
		Index: pos, URL: p.URL, Title: pageTitle(p), PR: p.PageRank,
		In: int(l.inDeg[pi]), Out: pageOutDegree(p), Status: p.StatusCode, Depth: p.Depth,
	}
}

// writeUniverseChunk writes nodes [offset, offset+count) as:
// "UNV1" | offset u32 | count u32 | total u32 | int16 xyz*count |
// uint16 cluster*count | uint8 rank*count | uint8 flags*count.
func writeUniverseChunk(w http.ResponseWriter, l *universeLayout, offset, count int) {
	total := len(l.order)
	offset = max(0, min(offset, total))
	count = max(0, min(count, total-offset))
	buf := make([]byte, universeHeaderBytes, universeHeaderBytes+count*universeBytesPerNode)
	copy(buf, "UNV1")
	binary.LittleEndian.PutUint32(buf[4:], uint32(offset))
	binary.LittleEndian.PutUint32(buf[8:], uint32(count))
	binary.LittleEndian.PutUint32(buf[12:], uint32(total))
	buf = append(buf, l.pos[offset*6:(offset+count)*6]...)
	buf = append(buf, l.clus[offset*2:(offset+count)*2]...)
	buf = append(buf, l.rank[offset:offset+count]...)
	buf = append(buf, l.flags[offset:offset+count]...)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(buf)
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return max(lo, min(v, hi))
}

// withUniverse resolves the crawl results and layout, holding the read lock
// for the duration of fn.
func withUniverse(manager *CrawlManager, w http.ResponseWriter, r *http.Request, fn func(results *CrawlResults, l *universeLayout)) {
	crawlID := r.PathValue("id")
	results := manager.GetOrLoadResults(crawlID)
	if results == nil {
		jsonError(w, "results not available", http.StatusNotFound)
		return
	}
	results.mu.RLock()
	defer results.mu.RUnlock()
	if !results.PagesReady {
		jsonError(w, "pages still loading", http.StatusServiceUnavailable)
		return
	}
	if len(results.Pages) == 0 {
		jsonError(w, "no pages", http.StatusNotFound)
		return
	}
	dim := r.URL.Query().Get("dim")
	if dim == "" {
		dim = "auto"
	}
	// Handlers get the layout's view so page indices match it.
	l := getUniverseLayout(crawlID, results, dim, r.URL.Query().Get("layout"), r.URL.Query().Get("all") == "1")
	if len(l.view.Pages) == 0 && !l.all {
		// No 2xx page at all (e.g. everything redirects): show what there is.
		l = getUniverseLayout(crawlID, results, dim, r.URL.Query().Get("layout"), true)
	}
	if len(l.view.Pages) == 0 {
		jsonError(w, "no pages", http.StatusNotFound)
		return
	}
	fn(l.view, l)
}

func registerUniverseRoutes(mux *http.ServeMux, store *UiStore, manager *CrawlManager) {
	mux.HandleFunc("GET /api/crawl/{id}/universe", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			jsonGzip(w, r, map[string]interface{}{
				"dim":             l.dim,
				"dims":            universeDims,
				"total":           len(l.order),
				"errors":          l.errors,
				"bound":           l.bound,
				"center":          l.center,
				"extent":          l.extent,
				"clusters":        l.clusters,
				"bytesPerNode":    universeBytesPerNode,
				"maxChunk":        universeMaxChunk,
				"cardsPerGroup":   universeCardsPerClus,
				"layout":          l.layout,
				"unreached":       l.unreached,
				"maxLevel":        l.maxLevel,
				"links":           l.links,
				"hasGraph":        universeGraph(results) != nil,
				"edgesTotal":      universeEdgeCount(results),
				"edgesContextual": l.edgesContextual,
				"sitewideTargets": l.sitewideTargets,
				"hidden":          l.hidden,
				"statusCounts":    universeStatusCounts(l.view.Pages),
				"all":             l.all,
			})
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/universe/nodes", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(_ *CrawlResults, l *universeLayout) {
			offset := intParam(r, "offset", 0, 0, math.MaxInt32)
			limit := intParam(r, "limit", 10_000, 1, universeMaxChunk)
			writeUniverseChunk(w, l, offset, limit)
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/universe/node", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			i := intParam(r, "i", -1, -1, len(l.order)-1)
			if i < 0 {
				jsonError(w, "invalid index", http.StatusBadRequest)
				return
			}
			card := universeCardFor(l, results.Pages, i)
			cl := binary.LittleEndian.Uint16(l.clus[i*2:])
			jsonOK(w, map[string]interface{}{"page": card, "cluster": l.clusters[cl].Key})
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/universe/edges", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			n := intParam(r, "n", len(l.order), 0, len(l.order))
			offset := intParam(r, "offset", 0, 0, math.MaxInt32)
			limit := intParam(r, "limit", 100_000, 1, 500_000)
			per := intParam(r, "per", 8, 1, 4096)
			if r.URL.Query().Get("tree") == "1" {
				writeUniverseTreeEdges(w, l, n, offset, limit)
				return
			}
			cluster := intParam(r, "c", -1, -1, len(l.clusters)-1)
			writeUniverseEdges(w, results, l, n, offset, limit, per, cluster, r.URL.Query().Get("sitewide") == "1")
		})
	})

	// Page search by title or URL, best PageRank first.
	mux.HandleFunc("GET /api/crawl/{id}/universe/search", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
			limit := intParam(r, "limit", 20, 1, 100)
			rows := []universeCard{}
			if q != "" {
				for pos := 0; pos < len(l.order) && len(rows) < limit; pos++ {
					p := results.Pages[l.order[pos]]
					if containsFold(p.URL, q) || (p.Title != nil && containsFold(p.Title.Text, q)) {
						rows = append(rows, universeCardFor(l, results.Pages, pos))
					}
				}
			}
			jsonOK(w, map[string]interface{}{"rows": rows})
		})
	})

	// Per-page colour metric for the first n pages, one byte each in order:
	// "UNM1" | count u32 | 8 bytes reserved | uint8*count. depth is the crawl
	// depth (capped at 255); authority and hub are percentiles 1-255 among
	// pages with a score, 0 for none.
	mux.HandleFunc("GET /api/crawl/{id}/universe/metric", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			n := intParam(r, "n", len(l.order), 0, len(l.order))
			name := r.URL.Query().Get("name")
			val := func(p *types.PageData) float64 {
				li := p.LinkIntelligence
				switch name {
				case "authority":
					if li != nil {
						return li.AuthorityScore
					}
				case "hub":
					if li != nil {
						return li.HubScore
					}
				default:
					return float64(p.Depth)
				}
				return 0
			}
			if name != "depth" && name != "authority" && name != "hub" {
				jsonError(w, "unknown metric", http.StatusBadRequest)
				return
			}
			out := make([]byte, universeHeaderBytes+n)
			copy(out, "UNM1")
			binary.LittleEndian.PutUint32(out[4:], uint32(n))
			if name == "depth" {
				for k, pi := range l.order[:n] {
					out[universeHeaderBytes+k] = byte(min(255, max(0, int(val(results.Pages[pi])))))
				}
			} else {
				// HITS scores are very skewed (a few hubs, a long tail near
				// zero), so colour by percentile among pages with a score.
				vals := make([]float64, n)
				ranked := make([]int, 0, n)
				for k, pi := range l.order[:n] {
					vals[k] = val(results.Pages[pi])
					if vals[k] > 0 {
						ranked = append(ranked, k)
					}
				}
				sort.Slice(ranked, func(a, b int) bool { return vals[ranked[a]] < vals[ranked[b]] })
				for r, k := range ranked {
					out[universeHeaderBytes+k] = byte(1 + math.Round(254*float64(r)/math.Max(1, float64(len(ranked)-1))))
				}
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(out)))
			_, _ = w.Write(out)
		})
	})

	mux.HandleFunc("GET /api/crawl/{id}/universe/node-links", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			i := intParam(r, "i", -1, -1, len(l.order)-1)
			if i < 0 {
				jsonError(w, "invalid index", http.StatusBadRequest)
				return
			}
			res := universeNodeLinks(results, l, i, intParam(r, "max", 3000, 1, 20_000))
			card := universeCardFor(l, results.Pages, i)
			res["page"] = card
			res["cluster"] = int(binary.LittleEndian.Uint16(l.clus[i*2:]))
			if l.parent != nil { // link layout: how the page is reached from the start page
				pi := l.order[i]
				if l.inMainTree[pi] {
					res["level"] = l.level[pi]
				} else {
					res["orphan"] = true // its level counts from an extra root, so leave it out
				}
				if p := l.parent[pi]; p >= 0 {
					res["parent"] = universeCardFor(l, results.Pages, int(l.posOf[p]))
				}
			}
			jsonOK(w, res)
		})
	})

	// Paged page list for one cluster, sortable by pr, in, out or title.
	mux.HandleFunc("GET /api/crawl/{id}/universe/cluster", func(w http.ResponseWriter, r *http.Request) {
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			ci := intParam(r, "c", -1, -1, len(l.clusters)-1)
			if ci < 0 {
				jsonError(w, "invalid cluster", http.StatusBadRequest)
				return
			}
			c := l.clusters[ci]
			offset := intParam(r, "offset", 0, 0, c.Count)
			limit := intParam(r, "limit", 50, 1, 500)
			sortBy := r.URL.Query().Get("sort")
			desc := r.URL.Query().Get("dir") != "asc"
			members := c.members
			if sortBy != "" && sortBy != "pr" || !desc {
				members = append([]int32(nil), c.members...)
				less := universeLess(l, results.Pages, sortBy, members)
				sort.SliceStable(members, func(a, b int) bool {
					if desc {
						return less(members[b], members[a])
					}
					return less(members[a], members[b])
				})
			}
			end := min(offset+limit, len(members))
			rows := make([]universeCard, 0, end-offset)
			for _, pos := range members[offset:end] {
				rows = append(rows, universeCardFor(l, results.Pages, int(pos)))
			}
			jsonOK(w, map[string]interface{}{"rows": rows, "total": c.Count, "offset": offset})
		})
	})

	registerUniverseThumbRoute(mux, store, manager)
}

// universeLess returns a comparator over order positions. For "title" the
// lower-cased titles of members are computed once up front instead of twice
// per comparison.
func universeLess(l *universeLayout, pages []*types.PageData, by string, members []int32) func(a, b int32) bool {
	page := func(pos int32) *types.PageData { return pages[l.order[pos]] }
	switch by {
	case "in":
		return func(a, b int32) bool { return l.inDeg[l.order[a]] < l.inDeg[l.order[b]] }
	case "out":
		return func(a, b int32) bool { return pageOutDegree(page(a)) < pageOutDegree(page(b)) }
	case "title":
		keys := make(map[int32]string, len(members))
		for _, pos := range members {
			keys[pos] = strings.ToLower(pageTitle(page(pos)))
		}
		return func(a, b int32) bool { return keys[a] < keys[b] }
	default: // pr: order position is already PageRank desc
		return func(a, b int32) bool { return a > b }
	}
}

// universeGraph returns the crawl's link graph when its node indices match
// results.Pages.
func universeGraph(results *CrawlResults) *analysis.AdjacencyGraph {
	if g := results.Graph; g != nil && g.N == len(results.Pages) && len(g.Offsets) == g.N+1 {
		return g
	}
	return nil
}

// universeClusterLinks counts real links between clusters (both directions
// merged) and keeps the strongest pairs.
func universeClusterLinks(results *CrawlResults, pageCluster []uint16, sitewide []bool) ([]universeLink, int) {
	g := universeGraph(results)
	if g == nil {
		return nil, 0
	}
	contextual := 0
	counts := map[[2]uint16]int{}
	for src := 0; src < g.N; src++ {
		a := pageCluster[src]
		for _, dst := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
			if sitewide[dst] {
				continue
			}
			contextual++
			b := pageCluster[dst]
			if a == b {
				continue
			}
			key := [2]uint16{min(a, b), max(a, b)}
			counts[key]++
		}
	}
	links := make([]universeLink, 0, len(counts))
	for k, c := range counts {
		links = append(links, universeLink{A: int(k[0]), B: int(k[1]), Count: c})
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Count != links[j].Count {
			return links[i].Count > links[j].Count
		}
		return links[i].A*65536+links[i].B < links[j].A*65536+links[j].B
	})
	if len(links) > universeMaxLinks {
		links = links[:universeMaxLinks]
	}
	return links, contextual
}

// universeSitewide flags navigation targets: pages linked from at least 40%
// of the site (menus, footers), or from at least half of their own cluster
// when it has 20+ pages (section menus, sidebars). Links to them come from
// templates, so the architecture view leaves them out unless asked for.
// Sites under 50 pages are never flagged.
func universeSitewide(results *CrawlResults, inDeg []int32, pageCluster []uint16, clusters []*universeCluster) ([]bool, int) {
	n := len(inDeg)
	flags := make([]bool, n)
	if n < 50 {
		return flags, 0
	}
	threshold := int32(float64(n) * 0.4)
	for i, d := range inDeg {
		flags[i] = d >= threshold
	}
	if g := universeGraph(results); g != nil {
		local := make([]int32, n) // inlinks from the target's own cluster
		for src := 0; src < g.N; src++ {
			for _, dst := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
				if pageCluster[src] == pageCluster[dst] {
					local[dst]++
				}
			}
		}
		for i, c := range local {
			size := clusters[pageCluster[i]].Count
			if size >= 20 && c >= int32(size/2) {
				flags[i] = true
			}
		}
	}
	count := 0
	for _, f := range flags {
		if f {
			count++
		}
	}
	return flags, count
}

// writeUniverseEdges streams real page-to-page links (links to sitewide
// targets only when sitewide is set) as pairs of order
// positions: "UNE1" | next u32 | count u32 | done u32 | (u32 src, u32 dst)*count.
// Only links whose two ends are among the first n pages (the ones the browser
// has loaded) are sent. Sources are walked in PageRank order from offset; each
// contributes at most per links, picked evenly from its outlinks. When cluster
// >= 0 only sources in that cluster are walked.
func writeUniverseEdges(w http.ResponseWriter, results *CrawlResults, l *universeLayout, n, offset, limit, per, cluster int, sitewide bool) {
	g := universeGraph(results)
	n = min(n, len(l.order))
	out := make([]byte, universeHeaderBytes, universeHeaderBytes+min(limit, 1<<16)*8)
	count := 0
	var kept []int32
	emit := func(src int32) {
		pi := l.order[src]
		nb := g.Edges[g.Offsets[pi]:g.Offsets[pi+1]]
		if !sitewide {
			kept = kept[:0]
			for _, t := range nb {
				if !l.sitewide[t] {
					kept = append(kept, t)
				}
			}
			nb = kept
		}
		step := 1.0
		if len(nb) > per {
			step = float64(len(nb)) / float64(per)
		}
		for f := 0.0; int(f) < len(nb); f += step {
			dst := l.posOf[nb[int(f)]]
			if int(dst) >= n || dst == src {
				continue
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(src))
			out = binary.LittleEndian.AppendUint32(out, uint32(dst))
			count++
		}
	}
	// Sources in PageRank order: every page, or the members of one cluster.
	sources := func(k int) (int32, bool) {
		if cluster >= 0 {
			m := l.clusters[cluster].members
			if k >= len(m) || int(m[k]) >= n {
				return 0, false
			}
			return m[k], true
		}
		return int32(k), k < n
	}
	k := offset
	done := g == nil
	for !done && count < limit {
		src, ok := sources(k)
		if !ok {
			done = true
			break
		}
		emit(src)
		k++
	}
	copy(out, "UNE1")
	binary.LittleEndian.PutUint32(out[4:], uint32(k))
	binary.LittleEndian.PutUint32(out[8:], uint32(count))
	if done {
		binary.LittleEndian.PutUint32(out[12:], 1)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	_, _ = w.Write(out)
}

// universeNodeLinks lists the real outlinks and inlinks of one page as order
// positions, capped at max each. Inlinks come from the graph's cached reverse
// index (built once, then O(in-degree) per click instead of a scan of every
// edge).
func universeNodeLinks(results *CrawlResults, l *universeLayout, pos, max int) map[string]interface{} {
	res := map[string]interface{}{"out": []int32{}, "in": []int32{}, "outTotal": 0, "inTotal": 0}
	g := universeGraph(results)
	if g == nil {
		return res
	}
	pi := l.order[pos]
	nb := g.Edges[g.Offsets[pi]:g.Offsets[pi+1]]
	outs := make([]int32, 0, min(len(nb), max))
	for _, t := range nb {
		if len(outs) == max {
			break
		}
		outs = append(outs, l.posOf[t])
	}
	srcs := g.InNeighbors(int(pi))
	ins := make([]int32, 0, min(len(srcs), max))
	for _, src := range srcs[:min(len(srcs), max)] {
		ins = append(ins, l.posOf[src])
	}
	res["out"], res["in"], res["outTotal"], res["inTotal"] = outs, ins, len(nb), len(srcs)
	return res
}

func universeEdgeCount(results *CrawlResults) int {
	if g := universeGraph(results); g != nil {
		return len(g.Edges)
	}
	return 0
}

// resolveUniverseLayout picks "links" (positions follow the link graph)
// unless "folders" is asked for or the crawl has no link graph.
func resolveUniverseLayout(results *CrawlResults, layout string) string {
	if layout == "folders" || universeGraph(results) == nil {
		return "folders"
	}
	return "links"
}

// universeLinkTree turns the link graph into a tree rooted at the start page
// (crawl depth 0, else the highest PageRank page). A breadth-first walk gives
// every page the parent that links to it on its shortest click path; when
// several pages at that depth link to it, one from the same cluster wins,
// then the one with the highest PageRank. Pages the walk never reaches are
// grown into extra trees from their own highest PageRank page.
// It returns the walk order and the roots of every tree, main root first.
func universeLinkTree(results *CrawlResults, l *universeLayout, pageCluster []uint16) (seq []int32, roots []int32) {
	g := universeGraph(results)
	pages := results.Pages
	n := len(pages)
	l.parent = make([]int32, n)
	l.level = make([]int32, n)
	for i := range l.parent {
		l.parent[i], l.level[i] = -1, -1
	}
	l.root = l.order[0]
	for i, p := range pages {
		if p.Depth == 0 && (pages[l.root].Depth != 0 || l.posOf[i] < l.posOf[l.root]) {
			l.root = int32(i)
		}
	}
	seq = make([]int32, 0, n)
	tree := make([]int32, n) // which walk reached each page (1-based)
	walks := int32(0)
	walk := func(root int32) {
		walks++
		l.level[root], tree[root] = 0, walks
		frontier := []int32{root}
		for d := int32(0); len(frontier) > 0; d++ {
			// Higher PageRank parents claim children first.
			sort.Slice(frontier, func(a, b int) bool { return l.posOf[frontier[a]] < l.posOf[frontier[b]] })
			seq = append(seq, frontier...)
			var next []int32
			for _, u := range frontier {
				for _, v := range g.Edges[g.Offsets[u]:g.Offsets[u+1]] {
					switch {
					case l.level[v] == -1:
						l.level[v], l.parent[v], tree[v] = d+1, u, walks
						next = append(next, v)
					case l.level[v] == d+1 && tree[v] == walks:
						if pageCluster[l.parent[v]] != pageCluster[v] && pageCluster[u] == pageCluster[v] {
							l.parent[v] = u
						}
					}
				}
			}
			l.maxLevel = max(l.maxLevel, int(d))
			frontier = next
		}
	}
	walk(l.root)
	roots = []int32{l.root}
	for _, pi := range l.order {
		if l.level[pi] == -1 {
			roots = append(roots, pi)
			walk(pi)
		}
	}
	l.inMainTree = make([]bool, n)
	for i, w := range tree {
		l.inMainTree[i] = w == 1
	}
	return seq, roots
}

// universeLinkPositions lays the link tree out in 3D. The root sits at the
// centre and every page is placed just past the page that links to it, so
// each branch of the picture is a chain of real links. A branch gets a cone
// of space in proportion to the pages below it; children spread over that
// cone on a Fibonacci pattern. Extra trees (pages not reached from the root)
// sit on an outer shell. Cluster centres and spreads are then measured from
// where their pages ended up.
func universeLinkPositions(results *CrawlResults, l *universeLayout, pageCluster []uint16) []float64 {
	n := len(results.Pages)
	seq, roots := universeLinkTree(results, l, pageCluster)

	// Subtree sizes (reverse walk order) and children lists.
	size := make([]int32, n)
	for i := len(seq) - 1; i >= 0; i-- {
		v := seq[i]
		size[v]++
		if p := l.parent[v]; p >= 0 {
			size[p] += size[v]
		}
	}
	offs := make([]int32, n+1)
	for _, v := range seq {
		if p := l.parent[v]; p >= 0 {
			offs[p+1]++
		}
	}
	for i := 0; i < n; i++ {
		offs[i+1] += offs[i]
	}
	kids := make([]int32, offs[n])
	fill := append([]int32(nil), offs[:n]...)
	for _, v := range seq {
		if p := l.parent[v]; p >= 0 {
			kids[fill[p]] = v
			fill[p]++
		}
	}

	const step0 = 900.0
	xyz := make([]float64, 3*n)
	axis := make([]float64, 3*n)
	cone := make([]float64, n)
	placeTree := func(root int32, at, dir [3]float64, a float64) {
		xyz[root*3], xyz[root*3+1], xyz[root*3+2] = at[0], at[1], at[2]
		axis[root*3], axis[root*3+1], axis[root*3+2] = dir[0], dir[1], dir[2]
		cone[root] = a
	}
	place := func(u int32) {
		ch := kids[offs[u]:offs[u+1]]
		m := len(ch)
		if m == 0 {
			return
		}
		sort.Slice(ch, func(a, b int) bool { return size[ch[a]] > size[ch[b]] })
		total := float64(size[u] - 1)
		au := cone[u]
		A := [3]float64{axis[u*3], axis[u*3+1], axis[u*3+2]}
		t1, t2 := orthoBasis(A)
		step := step0 * math.Pow(0.8, float64(l.level[u])) * (1 + 0.45*math.Log10(1+float64(m)))
		cosA := math.Cos(au)
		for k, v := range ch {
			z := 1 - (1-cosA)*(float64(k)+0.5)/float64(m)
			r := math.Sqrt(math.Max(0, 1-z*z))
			th := float64(k) * math.Pi * (3 - math.Sqrt(5))
			lx, ly := r*math.Cos(th), r*math.Sin(th)
			var d [3]float64
			for c := 0; c < 3; c++ {
				d[c] = t1[c]*lx + t2[c]*ly + A[c]*z
			}
			jitter := 0.7 + 0.6*unitFloat(splitmix64(uint64(v)+99))
			share := float64(size[v]) / math.Max(1, total)
			cone[v] = math.Min(math.Max(au*math.Sqrt(share)*1.3, 0.06), math.Min(au, 1.1))
			for c := 0; c < 3; c++ {
				xyz[int(v)*3+c] = xyz[int(u)*3+c] + d[c]*step*jitter
				axis[int(v)*3+c] = d[c]
			}
		}
	}

	// Main tree first, then measure it to put the extra trees outside.
	placeTree(roots[0], [3]float64{}, [3]float64{0, 1, 0}, math.Pi)
	mainEnd := len(seq)
	for i, v := range seq {
		if i > 0 && l.level[v] == 0 {
			mainEnd = i
			break
		}
	}
	for _, v := range seq[:mainEnd] {
		place(v)
	}
	reach := 0.0
	for _, v := range seq[:mainEnd] {
		reach = math.Max(reach, math.Sqrt(xyz[v*3]*xyz[v*3]+xyz[v*3+1]*xyz[v*3+1]+xyz[v*3+2]*xyz[v*3+2]))
	}
	shell := reach*1.3 + step0
	extra := roots[1:]
	for i, r := range extra {
		y := 1 - (float64(i)+0.5)/float64(len(extra))*2
		rr := math.Sqrt(math.Max(0, 1-y*y))
		th := float64(i) * math.Pi * (3 - math.Sqrt(5))
		dir := [3]float64{math.Cos(th) * rr, y, math.Sin(th) * rr}
		placeTree(r, [3]float64{dir[0] * shell, dir[1] * shell, dir[2] * shell}, dir, 0.5)
	}
	for _, v := range seq[mainEnd:] {
		place(v)
	}
	l.unreached = len(seq) - mainEnd

	// Clusters: centre = mean position, spread = RMS distance from it.
	sums := make([][4]float64, len(l.clusters))
	for i := 0; i < n; i++ {
		c := &sums[pageCluster[i]]
		c[0] += xyz[i*3]
		c[1] += xyz[i*3+1]
		c[2] += xyz[i*3+2]
		c[3]++
	}
	for ci, c := range l.clusters {
		if s := sums[ci]; s[3] > 0 {
			c.Center = [3]float64{s[0] / s[3], s[1] / s[3], s[2] / s[3]}
		}
	}
	dev := make([]float64, len(l.clusters))
	for i := 0; i < n; i++ {
		c := l.clusters[pageCluster[i]].Center
		dx, dy, dz := xyz[i*3]-c[0], xyz[i*3+1]-c[1], xyz[i*3+2]-c[2]
		dev[pageCluster[i]] += dx*dx + dy*dy + dz*dz
	}
	for ci, c := range l.clusters {
		if c.Count > 0 {
			c.Spread = math.Max(120, math.Sqrt(dev[ci]/float64(c.Count)))
		}
	}
	return xyz
}

// orthoBasis returns two unit vectors perpendicular to a and each other.
func orthoBasis(a [3]float64) ([3]float64, [3]float64) {
	h := [3]float64{1, 0, 0}
	if math.Abs(a[0]) > 0.9 {
		h = [3]float64{0, 1, 0}
	}
	t1 := cross(h, a)
	nrm := math.Sqrt(t1[0]*t1[0] + t1[1]*t1[1] + t1[2]*t1[2])
	t1 = [3]float64{t1[0] / nrm, t1[1] / nrm, t1[2] / nrm}
	return t1, cross(a, t1)
}

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// writeUniverseTreeEdges streams the link tree's edges (parent -> page) for
// pages [offset, ...) among the first n, in the same "UNE1" format as
// writeUniverseEdges. These are the backbone of the "links" layout.
func writeUniverseTreeEdges(w http.ResponseWriter, l *universeLayout, n, offset, limit int) {
	n = min(n, len(l.order))
	out := make([]byte, universeHeaderBytes, universeHeaderBytes+min(limit, 1<<16)*8)
	count, k := 0, offset
	for ; l.parent != nil && k < n && count < limit; k++ {
		p := l.parent[l.order[k]]
		if p < 0 || int(l.posOf[p]) >= n {
			continue
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(l.posOf[p]))
		out = binary.LittleEndian.AppendUint32(out, uint32(k))
		count++
	}
	copy(out, "UNE1")
	binary.LittleEndian.PutUint32(out[4:], uint32(k))
	binary.LittleEndian.PutUint32(out[8:], uint32(count))
	if l.parent == nil || k >= n {
		binary.LittleEndian.PutUint32(out[12:], 1)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	_, _ = w.Write(out)
}
