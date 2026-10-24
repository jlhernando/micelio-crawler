package server

import (
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// dirPageSource gives the directory index read access to pages. In-memory
// crawls hand out their cached *PageData; crawls answered from the page_index
// table (pages not loaded yet) build a PageData in scratch, so a returned
// pointer is only valid until scratch is reused.
type dirPageSource interface {
	Len() int
	Page(i int, scratch *types.PageData) *types.PageData
}

type memPages []*types.PageData

func (m memPages) Len() int                                      { return len(m) }
func (m memPages) Page(i int, _ *types.PageData) *types.PageData { return m[i] }

// indexPages adapts page_index rows. Only the fields the tree aggregates are
// set (status, inlinks, indexability); the rest stay zero.
type indexPages []storage.PageIndexEntry

func (x indexPages) Len() int { return len(x) }
func (x indexPages) Page(i int, scratch *types.PageData) *types.PageData {
	e := &x[i]
	*scratch = types.PageData{URL: e.URL, StatusCode: e.StatusCode, Inlinks: e.Inlinks}
	scratch.Indexability.Indexable = e.Indexable
	return scratch
}

// Lazy directory tree: the dashboard loads one directory at a time, like map
// tiles. The server keeps an index of directories only (pages stay as indices
// into results.Pages), so even a 1M-page crawl costs a few MB, and each
// response holds just the children the user opened.

const (
	dirTreePageSize = 100 // children per request; the rest come with "Load more"
	dirTreeL2Size   = 20  // children preloaded for each of the first dirTreeL2Dirs directories
	dirTreeL2Dirs   = 20
)

type dirIndexEntry struct {
	node    *dirNode // aggregated metrics; Children stays nil
	subdirs []*dirIndexEntry
	pages   []int32 // pages directly in this directory, PageRank desc
	// trafficMissing is set when GSC/GA4/Plausible numbers are not known
	// for this node (see applyTreeTraffic).
	trafficMissing bool
}

type dirIndex struct {
	owner weak.Pointer[CrawlResults]
	n     int
	dirs  map[string]*dirIndexEntry
	// fromTree holds traffic numbers taken from the cached full tree when
	// the in-memory pages no longer carry them (nil otherwise).
	fromTree map[string]*dirNode
	// variants marks paths crawled with more than one ?query; the full tree
	// merges those into one leaf, so its numbers cannot be split back.
	variants map[string]bool
	// Cache key parts beyond the results object (see getDirIndex).
	pageTraffic bool
	treeRoot    *dirNode
}

var dirIndexCache = struct {
	sync.Mutex
	items map[string]*dirIndex
}{items: map[string]*dirIndex{}}

// lazyDirNode is a tree node in a lazy response. childCount tells the client
// how many children exist so it can offer "Load more".
type lazyDirNode struct {
	*dirNode
	HasChildren    bool          `json:"hasChildren"`
	ChildCount     int           `json:"childCount"`
	TrafficMissing bool          `json:"trafficMissing,omitempty"`
	Children       []lazyDirNode `json:"children,omitempty"`
}

func dirPath(segs []string) string {
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// pageSegments splits a page URL into decoded path segments the same way
// buildDirectoryTree does, so paths match between the two trees.
func pageSegments(p *types.PageData) ([]string, string, bool) {
	if p.URL == "" {
		return nil, "", false
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, "", false
	}
	segs := splitPathSegments(u.Path)
	if len(segs) > maxTreeDepth {
		segs = segs[:maxTreeDepth]
	}
	return segs, u.RawQuery, true
}

// getDirIndex returns a cached index or builds one. Callers hold results.mu.RLock.
func getDirIndex(crawlID string, results *CrawlResults) *dirIndex {
	dirIndexCache.Lock()
	defer dirIndexCache.Unlock()
	root, _ := results.DirTree["tree"].(*dirNode)
	traffic := pagesHaveTraffic(results.Pages)
	// Stripping pages after analysis removes traffic in place, and the full
	// tree can be replaced, without changing the results object or page
	// count, so both are part of the cache key.
	if ix := dirIndexCache.items[crawlID]; ix != nil && ix.owner.Value() == results && ix.n == len(results.Pages) &&
		ix.pageTraffic == traffic && ix.treeRoot == root {
		return ix
	}
	for id, ix := range dirIndexCache.items {
		if ix.owner.Value() == nil {
			delete(dirIndexCache.items, id)
		}
	}
	ix := buildDirIndex(memPages(results.Pages))
	ix.pageTraffic, ix.treeRoot = traffic, root
	if !traffic {
		ix.applyTreeTraffic(root)
	}
	ix.owner = weak.Make(results)
	dirIndexCache.items[crawlID] = ix
	return ix
}

func buildDirIndex(src dirPageSource) *dirIndex {
	n := src.Len()
	ix := &dirIndex{n: n, dirs: map[string]*dirIndexEntry{}, variants: map[string]bool{}}
	var scratch, scratchB types.PageData
	entry := func(segs []string) *dirIndexEntry {
		key := dirPath(segs)
		e := ix.dirs[key]
		if e == nil {
			name := "/"
			if len(segs) > 0 {
				name = segs[len(segs)-1]
			}
			e = &dirIndexEntry{node: &dirNode{Name: name, Path: key, Depth: len(segs), StatusCodes: map[int]int{}}}
			ix.dirs[key] = e
		}
		return e
	}

	// Pass 1: every proper prefix of a page path is a directory.
	entry(nil)
	type parsed struct {
		segs  []string
		query bool
		ok    bool
	}
	parsedPages := make([]parsed, n) // parse each URL once for both passes
	for k := range n {
		segs, query, ok := pageSegments(src.Page(k, &scratch))
		parsedPages[k] = parsed{segs, query != "", ok}
		if !ok {
			continue
		}
		for i := 1; i < len(segs); i++ {
			if _, seen := ix.dirs[dirPath(segs[:i])]; !seen {
				child := entry(segs[:i])
				parent := entry(segs[:i-1])
				parent.subdirs = append(parent.subdirs, child)
			}
		}
	}

	// Pass 2: aggregate metrics up the tree. A page whose path is also a
	// directory is shown as that directory (isPage), not as a separate leaf.
	for i := range n {
		segs, ok := parsedPages[i].segs, parsedPages[i].ok
		if !ok {
			continue
		}
		p := src.Page(i, &scratch)
		for d := 0; d < len(segs); d++ {
			bubbleUpMetrics(ix.dirs[dirPath(segs[:d])].node, p)
		}
		if self, isDir := ix.dirs[dirPath(segs)]; isDir {
			bubbleUpMetrics(self.node, p)
			self.node.IsPage = true
			continue
		}
		parent := ix.dirs[dirPath(segs[:len(segs)-1])]
		parent.pages = append(parent.pages, int32(i))
		if parsedPages[i].query {
			ix.variants[dirPath(segs)] = true
		}
	}
	for _, e := range ix.dirs {
		finalizeDirNode(e.node)
		sort.Slice(e.subdirs, func(a, b int) bool {
			if e.subdirs[a].node.Pages != e.subdirs[b].node.Pages {
				return e.subdirs[a].node.Pages > e.subdirs[b].node.Pages
			}
			return e.subdirs[a].node.Name < e.subdirs[b].node.Name
		})
		sort.SliceStable(e.pages, func(a, b int) bool {
			return src.Page(int(e.pages[a]), &scratch).PageRank > src.Page(int(e.pages[b]), &scratchB).PageRank
		})
	}
	return ix
}

func (e *dirIndexEntry) childCount() int { return len(e.subdirs) + len(e.pages) }

// children returns children [offset, offset+limit): subdirectories first
// (most pages first), then the directory's own pages (PageRank first).
func (ix *dirIndex) children(e *dirIndexEntry, pages dirPageSource, offset, limit, depth int) []lazyDirNode {
	total := e.childCount()
	end := min(total, offset+limit)
	out := make([]lazyDirNode, 0, max(0, end-offset))
	var scratch types.PageData
	for k := offset; k < end; k++ {
		if k < len(e.subdirs) {
			sub := e.subdirs[k]
			n := lazyDirNode{dirNode: sub.node, HasChildren: sub.childCount() > 0, ChildCount: sub.childCount(), TrafficMissing: sub.trafficMissing}
			if depth > 1 && k < dirTreeL2Dirs {
				n.Children = ix.children(sub, pages, 0, dirTreeL2Size, depth-1)
			}
			out = append(out, n)
			continue
		}
		p := pages.Page(int(e.pages[k-len(e.subdirs)]), &scratch)
		segs, query, _ := pageSegments(p)
		name, path := segs[len(segs)-1], dirPath(segs)
		plain := path
		if query != "" { // keep ?query so variants of one path stay apart
			name += "?" + query
			path += "?" + query
		}
		leaf := &dirNode{Name: name, Path: path, Depth: len(segs), IsPage: true, StatusCodes: map[int]int{}}
		bubbleUpMetrics(leaf, p)
		finalizeDirNode(leaf)
		missing := false
		if ix.fromTree != nil {
			from := ix.fromTree[plain]
			missing = from == nil || ix.variants[plain]
			if !missing {
				copyTraffic(leaf, from)
			}
		}
		out = append(out, lazyDirNode{dirNode: leaf, TrafficMissing: missing})
	}
	return out
}

// serveLazyDirTree answers GET /directory-tree?lazy=1&path=/a/b&offset=&limit=
// from in-memory pages. It returns false when pages are not loaded, so the
// caller can answer from the crawl DB instead (serveLazyDirTreeFromDB).
func serveLazyDirTree(w http.ResponseWriter, r *http.Request, crawlID string, results *CrawlResults) bool {
	if results == nil {
		return false
	}
	results.mu.RLock()
	defer results.mu.RUnlock()
	if !results.PagesReady || len(results.Pages) == 0 {
		return false
	}
	writeLazyDirTree(w, r, getDirIndex(crawlID, results), memPages(results.Pages))
	return true
}

// dbDirIndexCache keeps the last index built from a crawl DB's page_index so
// expanding folders does not re-read the table. One entry is enough: it only
// serves the window before a crawl's pages are loaded into memory.
var dbDirIndexCache struct {
	sync.Mutex
	crawlID, dbPath string
	modTime         time.Time
	size            int64
	ix              *dirIndex
	pages           indexPages
}

// serveLazyDirTreeFromDB answers a lazy request from the page_index table.
// It returns false when the DB has no page index.
func serveLazyDirTreeFromDB(w http.ResponseWriter, r *http.Request, crawlID, dbPath string) bool {
	ix, pages := dbDirIndex(crawlID, dbPath)
	if ix == nil {
		return false
	}
	writeLazyDirTree(w, r, ix, pages)
	return true
}

func dbDirIndex(crawlID, dbPath string) (*dirIndex, indexPages) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		return nil, nil
	}
	c := &dbDirIndexCache
	c.Lock()
	defer c.Unlock()
	if c.ix != nil && c.crawlID == crawlID && c.dbPath == dbPath && c.modTime.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c.ix, c.pages
	}
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil, nil
	}
	entries, err := cs.GetPageIndex()
	cs.Close()
	if err != nil || len(entries) == 0 {
		return nil, nil
	}
	pages := indexPages(entries)
	c.crawlID, c.dbPath, c.modTime, c.size = crawlID, dbPath, fi.ModTime(), fi.Size()
	c.ix, c.pages = buildDirIndex(pages), pages
	return c.ix, c.pages
}

// writeEmptyLazyDirTree answers a lazy request for a crawl with no pages.
func writeEmptyLazyDirTree(w http.ResponseWriter, r *http.Request) {
	writeLazyDirTree(w, r, buildDirIndex(memPages(nil)), memPages(nil))
}

func writeLazyDirTree(w http.ResponseWriter, r *http.Request, ix *dirIndex, pages dirPageSource) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	e := ix.dirs[path]
	if e == nil {
		jsonError(w, "directory not found", http.StatusNotFound)
		return
	}
	offset := intParam(r, "offset", 0, 0, e.childCount())
	limit := intParam(r, "limit", dirTreePageSize, 1, 1000)
	depth := intParam(r, "depth", 1, 1, 2)
	node := lazyDirNode{dirNode: e.node, HasChildren: e.childCount() > 0, ChildCount: e.childCount(), TrafficMissing: e.trafficMissing}
	node.Children = ix.children(e, pages, offset, limit, depth)
	jsonOK(w, map[string]interface{}{
		"lazy":       true,
		"tree":       node,
		"offset":     offset,
		"totalPages": pages.Len(),
	})
}

// applyTreeTraffic restores GSC, GA4 and Plausible numbers. Cached pages lose
// that data when they are stripped after analysis, but the full directory
// tree (results.DirTree) was built before, so its nodes still have it. Nodes
// the capped tree does not cover are flagged so the dashboard shows "—"
// instead of a misleading 0.
func (ix *dirIndex) applyTreeTraffic(root *dirNode) {
	if root == nil || !hasTraffic(root) {
		return
	}
	ix.fromTree = map[string]*dirNode{}
	var walk func(n *dirNode)
	walk = func(n *dirNode) {
		ix.fromTree[n.Path] = n
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	for path, e := range ix.dirs {
		if src := ix.fromTree[path]; src != nil {
			copyTraffic(e.node, src)
		} else {
			e.trafficMissing = true
		}
	}
}

// pagesHaveTraffic reports whether any page still carries GSC/GA4/Plausible data.
func pagesHaveTraffic(pages []*types.PageData) bool {
	for _, p := range pages {
		if p.GscData != nil || p.Ga4Data != nil || p.PlausibleData != nil {
			return true
		}
	}
	return false
}

func hasTraffic(n *dirNode) bool {
	return n.GscClicks+n.GscImpressions+n.Ga4Sessions+n.Ga4Pageviews+n.Ga4Conversions+n.PlausibleVisitors+n.PlausibleConversions > 0
}

func copyTraffic(dst, src *dirNode) {
	dst.GscClicks, dst.GscImpressions = src.GscClicks, src.GscImpressions
	dst.Ga4Sessions, dst.Ga4Pageviews, dst.Ga4Conversions = src.Ga4Sessions, src.Ga4Pageviews, src.Ga4Conversions
	dst.PlausibleVisitors, dst.PlausibleConversions = src.PlausibleVisitors, src.PlausibleConversions
}
