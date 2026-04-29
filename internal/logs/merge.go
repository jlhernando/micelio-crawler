package logs

import (
	"net/url"
	"strings"
)

// PageSegment classifies a page based on cross-referencing log and crawl data.
type PageSegment string

const (
	SegHealthy          PageSegment = "healthy"            // Crawled by bots + indexable
	SegCrawledNotIndex  PageSegment = "crawled_not_indexed" // In logs but noindex/canonical elsewhere
	SegUncrawled        PageSegment = "uncrawled_indexable" // Indexable but never visited by bots
	SegOrphanCrawled    PageSegment = "orphan_crawled"      // In logs but not in crawl data
	SegCrawlWaste       PageSegment = "crawl_waste"         // Non-indexable but heavily crawled by bots
	SegRedirectWaste    PageSegment = "redirect_waste"      // Redirects consuming crawl budget
	SegErrorPages       PageSegment = "error_pages"         // Consistent 4xx/5xx served to bots
)

// MergedPage combines log and crawl data for a single URL.
type MergedPage struct {
	URL         string      `json:"url"`
	Segment     PageSegment `json:"segment"`
	// Reason is a human-readable one-liner explaining why this page received
	// its segment classification (e.g. "noindex meta", "canonical → /news/page/1/").
	Reason string `json:"reason,omitempty"`
	// Log data
	LogHits      int64  `json:"logHits"`
	LogBotHits   int64  `json:"logBotHits"`
	LogHumanHits int64  `json:"logHumanHits"`
	LogTopBot    string `json:"logTopBot,omitempty"`
	LogStatus    int    `json:"logStatus,omitempty"`
	LogBytes     int64  `json:"logBytes,omitempty"`
	// Crawl data (populated if page found in crawl)
	CrawlStatus   int    `json:"crawlStatus,omitempty"`
	CrawlIndexable bool  `json:"crawlIndexable,omitempty"`
	CrawlCanonical string `json:"crawlCanonical,omitempty"`
	CrawlDepth    int    `json:"crawlDepth,omitempty"`
	CrawlInlinks  int    `json:"crawlInlinks,omitempty"`
	CrawlWordCount int   `json:"crawlWordCount,omitempty"`
	CrawlTitle    string `json:"crawlTitle,omitempty"`
	HasNoindex    bool   `json:"hasNoindex,omitempty"`
	InCrawl       bool   `json:"inCrawl"`
	InLogs        bool   `json:"inLogs"`
}

// MergeResult holds the output of log-to-crawl merge.
type MergeResult struct {
	TotalPages      int                        `json:"totalPages"`
	Segments        map[PageSegment]int         `json:"segments"`
	OrphanPages     []MergedPage               `json:"orphanPages"`     // In logs but not in crawl
	GhostPages      []MergedPage               `json:"ghostPages"`      // In crawl but never in logs
	MismatchPages   []MergedPage               `json:"mismatchPages"`   // Status differs between log and crawl
	TopMergedPages  []MergedPage               `json:"topMergedPages"`  // Top 200 by log bot hits
	SegmentDetails  map[PageSegment][]MergedPage `json:"segmentDetails"` // Top 50 per segment
}

// CrawlPageInfo holds the minimal crawl page data needed for merging.
type CrawlPageInfo struct {
	URL         string
	StatusCode  int
	Indexable   bool
	Canonical   string
	Depth       int
	Inlinks     int
	WordCount   int
	Title       string
	HasNoindex  bool
	IsRedirect  bool
}

// MergeSummary holds the aggregate result of a streaming merge — counts only,
// no per-page lists. Use StreamMergeEmit + a persistence callback to capture
// every merged page.
type MergeSummary struct {
	TotalPages    int                 `json:"totalPages"`
	Segments      map[PageSegment]int `json:"segments"`
	OrphanCount   int                 `json:"orphanCount"`
	GhostCount    int                 `json:"ghostCount"`
	MismatchCount int                 `json:"mismatchCount"`
	HealthyCount  int                 `json:"healthyCount"`
	LogURLsTotal  int                 `json:"logUrlsTotal"`  // distinct URLs from logs
	CrawlPages    int                 `json:"crawlPages"`    // crawl pages considered
	CappedURLs    bool                `json:"cappedUrls,omitempty"` // true when merge used the capped TopURLs fallback
}

// StreamMergeEmit cross-references log URLs with crawl pages and calls onPage
// for every merged record (orphan, ghost, healthy, waste, etc). The caller is
// responsible for persistence — typically batched SQLite inserts.
//
// Memory footprint: crawl-page index (~50 B/page) + matched-set (≤ #crawl pages).
// No top-N buffering, no per-segment lists. The returned MergeSummary holds
// just the counts needed for KPI cards and the donut chart.
func StreamMergeEmit(
	iterateLogURLs func(fn func(URLStat) error) error,
	crawlPages []CrawlPageInfo,
	onPage func(MergedPage) error,
) (*MergeSummary, error) {
	summary := &MergeSummary{
		Segments:   make(map[PageSegment]int),
		CrawlPages: len(crawlPages),
	}

	// Pre-expand the crawl index with all URL variants (trailing slash,
	// decoded, encoded) so lookup is a single map hit instead of up to 8
	// attempts. Uses ~3× the memory of a basic index but eliminates all
	// per-URL decoding/encoding work during the 5M+ iteration.
	crawlIndex := buildExpandedCrawlIndex(crawlPages)
	matchedCrawlPaths := make(map[string]struct{}, len(crawlPages)/4)

	if err := iterateLogURLs(func(lu URLStat) error {
		summary.LogURLsTotal++
		mp := MergedPage{
			URL:          lu.Path,
			LogHits:      lu.Hits,
			LogBotHits:   lu.BotHits,
			LogHumanHits: lu.HumanHits,
			LogTopBot:    lu.TopBot,
			LogStatus:    lu.Status,
			LogBytes:     lu.Bytes,
			InLogs:       true,
		}
		cp := fastFindCrawlPage(crawlIndex, lu.Path)
		if cp != nil {
			mp.InCrawl = true
			mp.CrawlStatus = cp.StatusCode
			mp.CrawlIndexable = cp.Indexable
			mp.CrawlCanonical = cp.Canonical
			mp.CrawlDepth = cp.Depth
			mp.CrawlInlinks = cp.Inlinks
			mp.CrawlWordCount = cp.WordCount
			mp.CrawlTitle = cp.Title
			mp.HasNoindex = cp.HasNoindex
			mp.Segment, mp.Reason = classifySegment(mp, cp)
			matchedCrawlPaths[extractPath(cp.URL)] = struct{}{}

			if mp.LogStatus > 0 && cp.StatusCode > 0 && statusGroup(mp.LogStatus) != statusGroup(cp.StatusCode) {
				summary.MismatchCount++
			}
		} else {
			mp.Segment = SegOrphanCrawled
			mp.Reason = "Bots are visiting this URL but it's not in the crawl — no internal links discovered, page may be missing or outside crawl scope"
			summary.OrphanCount++
		}
		summary.Segments[mp.Segment]++
		if mp.Segment == SegHealthy {
			summary.HealthyCount++
		}
		return onPage(mp)
	}); err != nil {
		return summary, err
	}

	// Pass 2: ghosts (in crawl, no log traffic).
	// Track emitted ghost paths to avoid duplicates — multiple crawl pages
	// can share the same extracted path (http vs https, www vs non-www).
	emittedGhosts := make(map[string]struct{}, len(crawlPages)/4)
	for i := range crawlPages {
		cp := &crawlPages[i]
		ghostPath := extractPath(cp.URL)
		if _, matched := matchedCrawlPaths[ghostPath]; matched {
			continue
		}
		if _, dup := emittedGhosts[ghostPath]; dup {
			continue
		}
		emittedGhosts[ghostPath] = struct{}{}
		summary.GhostCount++
		summary.Segments[SegUncrawled]++
		ghost := MergedPage{
			URL:            extractPath(cp.URL),
			InCrawl:        true,
			CrawlStatus:    cp.StatusCode,
			CrawlIndexable: cp.Indexable,
			CrawlCanonical: cp.Canonical,
			CrawlDepth:     cp.Depth,
			CrawlInlinks:   cp.Inlinks,
			CrawlWordCount: cp.WordCount,
			CrawlTitle:     cp.Title,
			HasNoindex:     cp.HasNoindex,
			Segment:        SegUncrawled,
			Reason:         "In crawl but no bot traffic in the log timeframe — page may be too deep, blocked by robots, or genuinely unimportant",
		}
		if err := onPage(ghost); err != nil {
			return summary, err
		}
	}

	summary.TotalPages = summary.LogURLsTotal + summary.GhostCount
	return summary, nil
}

// MergeLogWithCrawl cross-references log stats with crawl page data.
// Convenience wrapper for callers that have all log URLs in memory; for
// large datasets prefer MergeLogWithCrawlStream which avoids materializing
// the full URL slice.
func MergeLogWithCrawl(logStats *LogStats, crawlPages []CrawlPageInfo) *MergeResult {
	urls := logStats.TopURLs
	result, _ := MergeLogWithCrawlStream(func(fn func(URLStat) error) error {
		for _, u := range urls {
			if err := fn(u); err != nil {
				return err
			}
		}
		return nil
	}, crawlPages)
	return result
}

// MergeLogWithCrawlStream cross-references log URL stats with crawl page data
// using a streaming iterator. Memory cost is bounded by:
//   - the crawl-page index (~50 bytes/page × #crawl pages)
//   - a "matched" set of crawl paths that actually had log traffic (≤ #crawl pages)
//   - the bounded result lists (orphans, ghosts, top-by-bot-hits)
//
// In particular, it does NOT load all log URLs into memory at once.
//
// To preserve "top N merged pages by bot hits" semantics without buffering
// every merged record, we keep a min-heap-like top-N tracker (size 200).
func MergeLogWithCrawlStream(
	iterateLogURLs func(fn func(URLStat) error) error,
	crawlPages []CrawlPageInfo,
) (*MergeResult, error) {
	const (
		topMergedCap   = 200
		orphanListCap  = 200
		ghostListCap   = 200
		mismatchCap    = 100
		segmentDetailN = 50
	)

	result := &MergeResult{
		Segments:       make(map[PageSegment]int),
		SegmentDetails: make(map[PageSegment][]MergedPage),
	}

	crawlIndex := buildExpandedCrawlIndex(crawlPages)

	// Set of crawl paths matched by some log URL — bounded by #crawl pages.
	matchedCrawlPaths := make(map[string]struct{}, len(crawlPages)/4)

	// Top-N merged pages by bot hits, kept as a sorted slice (cheap when
	// topMergedCap is small). For >>200 we'd want a heap; 200 is fine.
	topMerged := make([]MergedPage, 0, topMergedCap+1)
	insertTop := func(mp MergedPage) {
		if len(topMerged) < topMergedCap {
			topMerged = append(topMerged, mp)
			// Insertion sort: bubble it into position by LogBotHits desc.
			for i := len(topMerged) - 1; i > 0 && topMerged[i].LogBotHits > topMerged[i-1].LogBotHits; i-- {
				topMerged[i], topMerged[i-1] = topMerged[i-1], topMerged[i]
			}
			return
		}
		if mp.LogBotHits <= topMerged[len(topMerged)-1].LogBotHits {
			return
		}
		topMerged[len(topMerged)-1] = mp
		for i := len(topMerged) - 1; i > 0 && topMerged[i].LogBotHits > topMerged[i-1].LogBotHits; i-- {
			topMerged[i], topMerged[i-1] = topMerged[i-1], topMerged[i]
		}
	}

	totalLogURLs := 0
	if err := iterateLogURLs(func(lu URLStat) error {
		totalLogURLs++
		mp := MergedPage{
			URL:          lu.Path,
			LogHits:      lu.Hits,
			LogBotHits:   lu.BotHits,
			LogHumanHits: lu.HumanHits,
			LogTopBot:    lu.TopBot,
			LogStatus:    lu.Status,
			LogBytes:     lu.Bytes,
			InLogs:       true,
		}

		cp := fastFindCrawlPage(crawlIndex, lu.Path)
		if cp != nil {
			mp.InCrawl = true
			mp.CrawlStatus = cp.StatusCode
			mp.CrawlIndexable = cp.Indexable
			mp.CrawlCanonical = cp.Canonical
			mp.CrawlDepth = cp.Depth
			mp.CrawlInlinks = cp.Inlinks
			mp.CrawlWordCount = cp.WordCount
			mp.CrawlTitle = cp.Title
			mp.HasNoindex = cp.HasNoindex
			mp.Segment, mp.Reason = classifySegment(mp, cp)
			matchedCrawlPaths[extractPath(cp.URL)] = struct{}{}

			if mp.LogStatus > 0 && cp.StatusCode > 0 && statusGroup(mp.LogStatus) != statusGroup(cp.StatusCode) {
				if len(result.MismatchPages) < mismatchCap {
					result.MismatchPages = append(result.MismatchPages, mp)
				}
			}
		} else {
			mp.Segment = SegOrphanCrawled
			mp.Reason = "Bots are visiting this URL but it's not in the crawl — no internal links discovered, page may be missing or outside crawl scope"
			// Keep top-N orphans by bot hits using the same trick.
			if len(result.OrphanPages) < orphanListCap {
				result.OrphanPages = append(result.OrphanPages, mp)
				for i := len(result.OrphanPages) - 1; i > 0 && result.OrphanPages[i].LogBotHits > result.OrphanPages[i-1].LogBotHits; i-- {
					result.OrphanPages[i], result.OrphanPages[i-1] = result.OrphanPages[i-1], result.OrphanPages[i]
				}
			} else if mp.LogBotHits > result.OrphanPages[len(result.OrphanPages)-1].LogBotHits {
				result.OrphanPages[len(result.OrphanPages)-1] = mp
				for i := len(result.OrphanPages) - 1; i > 0 && result.OrphanPages[i].LogBotHits > result.OrphanPages[i-1].LogBotHits; i-- {
					result.OrphanPages[i], result.OrphanPages[i-1] = result.OrphanPages[i-1], result.OrphanPages[i]
				}
			}
		}

		result.Segments[mp.Segment]++
		addToSegmentDetails(result.SegmentDetails, mp)
		insertTop(mp)
		return nil
	}); err != nil {
		return nil, err
	}

	// Pass 2: walk crawl pages once to find ghosts (crawled, no log traffic).
	// O(#crawl pages); no extra memory beyond the matched set.
	emittedGhosts := make(map[string]struct{}, len(crawlPages)/4)
	for i := range crawlPages {
		cp := &crawlPages[i]
		ghostPath := extractPath(cp.URL)
		if _, matched := matchedCrawlPaths[ghostPath]; matched {
			continue
		}
		if _, dup := emittedGhosts[ghostPath]; dup {
			continue
		}
		emittedGhosts[ghostPath] = struct{}{}
		result.Segments[SegUncrawled]++
		if len(result.GhostPages) < ghostListCap {
			result.GhostPages = append(result.GhostPages, MergedPage{
				URL:            extractPath(cp.URL),
				InCrawl:        true,
				CrawlStatus:    cp.StatusCode,
				CrawlIndexable: cp.Indexable,
				CrawlCanonical: cp.Canonical,
				CrawlDepth:     cp.Depth,
				CrawlInlinks:   cp.Inlinks,
				CrawlWordCount: cp.WordCount,
				CrawlTitle:     cp.Title,
				HasNoindex:     cp.HasNoindex,
				Segment:        SegUncrawled,
				Reason:         "In crawl but no bot traffic in the log timeframe — page may be too deep, blocked by robots, or genuinely unimportant",
			})
		}
	}

	result.TotalPages = totalLogURLs + result.Segments[SegUncrawled]
	result.TopMergedPages = topMerged
	return result, nil
}

// classifySegment returns the segment plus a one-line human reason explaining
// which rule fired. The reason is purely descriptive — it never affects the
// segment value, so callers can show it as-is to the user.
func classifySegment(mp MergedPage, cp *CrawlPageInfo) (PageSegment, string) {
	// Redirects consuming budget
	if cp.IsRedirect || (cp.StatusCode >= 300 && cp.StatusCode < 400) {
		reason := "Redirect (HTTP " + itoa(cp.StatusCode) + ")"
		if cp.Canonical != "" {
			reason += " — bots being routed via " + truncate(cp.Canonical, 80)
		}
		return SegRedirectWaste, reason
	}
	// Error pages
	if cp.StatusCode >= 400 {
		return SegErrorPages, "Crawler received HTTP " + itoa(cp.StatusCode)
	}
	// Non-indexable but crawled (waste / waiting to be wasted)
	if cp.HasNoindex {
		seg := SegCrawledNotIndex
		reason := `<meta name="robots" content="noindex">`
		if mp.LogBotHits > 0 {
			seg = SegCrawlWaste
			reason += " — but bots are still hitting it"
		}
		return seg, reason
	}
	if !cp.Indexable {
		seg := SegCrawledNotIndex
		reason := "Marked non-indexable by crawl analysis (robots.txt, noindex, blocked, or non-canonical)"
		if mp.LogBotHits > 0 {
			seg = SegCrawlWaste
			reason = "Non-indexable page — but bots are spending budget on it"
		}
		return seg, reason
	}
	// Canonical pointing elsewhere
	if cp.Canonical != "" && extractPath(cp.Canonical) != extractPath(cp.URL) {
		canonicalPath := truncate(extractPath(cp.Canonical), 80)
		if mp.LogBotHits > 0 {
			return SegCrawlWaste, "Canonical → " + canonicalPath + " (bots wasting budget on the duplicate)"
		}
		return SegHealthy, "Canonical → " + canonicalPath
	}
	if mp.LogBotHits > 0 {
		return SegHealthy, "Indexable, bot-visited"
	}
	return SegHealthy, "Indexable, in crawl"
}

func itoa(n int) string {
	// Cheap inline conversion to avoid pulling strconv just for this.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// buildExpandedCrawlIndex creates a map with all URL variants for each crawl
// page — with/without trailing slash, percent-decoded, percent-encoded.
// For 464K crawl pages this uses ~50 MB extra but eliminates all per-URL
// normalization work during the 5M+ log URL iteration.
func buildExpandedCrawlIndex(crawlPages []CrawlPageInfo) map[string]*CrawlPageInfo {
	// Pre-allocate for ~4 variants per page on average.
	index := make(map[string]*CrawlPageInfo, len(crawlPages)*4)
	for i := range crawlPages {
		cp := &crawlPages[i]
		base := extractPath(cp.URL)

		// Register the base path and its trailing-slash variant.
		addVariants := func(p string) {
			if _, exists := index[p]; !exists {
				index[p] = cp
			}
			if strings.HasSuffix(p, "/") {
				trimmed := strings.TrimSuffix(p, "/")
				if _, exists := index[trimmed]; !exists {
					index[trimmed] = cp
				}
			} else {
				slashed := p + "/"
				if _, exists := index[slashed]; !exists {
					index[slashed] = cp
				}
			}
		}

		addVariants(base)

		// Decoded variant (crawl URLs from url.Parse are already decoded, but
		// add the encoded form too so log paths with %XX match directly).
		if enc := encodePathSafe(base); enc != base {
			addVariants(enc)
		}
		// Also add the decoded form in case extractPath returned encoded.
		if dec := decodePathSafe(base); dec != base {
			addVariants(dec)
		}
	}
	return index
}

// fastFindCrawlPage looks up a log path in the pre-expanded crawl index.
// Tries: exact path → query-stripped path. No decoding/encoding needed
// because all variants are already in the index.
func fastFindCrawlPage(index map[string]*CrawlPageInfo, path string) *CrawlPageInfo {
	if cp := index[path]; cp != nil {
		return cp
	}
	// Strip query string — bots hit URLs with utm_*, fbclid, etc.
	if i := strings.IndexByte(path, '?'); i > 0 {
		if cp := index[path[:i]]; cp != nil {
			return cp
		}
	}
	return nil
}

func findCrawlPage(index map[string]*CrawlPageInfo, path string) *CrawlPageInfo {
	// Try the path as-is first; cheapest path for already-matching URLs.
	if cp := lookupPath(index, path); cp != nil {
		return cp
	}
	// Percent-decoded variant: log paths are often encoded
	// (e.g. /osobowe/uzywane/%C5%82%C3%B3d%C5%BA) while the crawler stores
	// decoded paths via url.Parse (e.g. /osobowe/uzywane/łódź). For sites
	// with non-ASCII URLs (Polish, Czech, German…) this is the dominant
	// reason a real match looks like a "ghost".
	if dec := decodePathSafe(path); dec != path {
		if cp := lookupPath(index, dec); cp != nil {
			return cp
		}
	}
	// Reverse case: crawl somehow has the encoded form, log has the decoded form.
	if enc := encodePathSafe(path); enc != path {
		if cp := lookupPath(index, enc); cp != nil {
			return cp
		}
	}
	return nil
}

// lookupPath tries the exact path, with/without trailing slash, and with the
// query string stripped (bots constantly hit pages with ?utm_*, ?fbclid=,
// faceted-nav params, etc., while the crawler stores the canonical URL).
func lookupPath(index map[string]*CrawlPageInfo, path string) *CrawlPageInfo {
	if cp := index[path]; cp != nil {
		return cp
	}
	if cp := tryTrailingSlash(index, path); cp != nil {
		return cp
	}
	if i := strings.IndexByte(path, '?'); i > 0 {
		bare := path[:i]
		if cp := index[bare]; cp != nil {
			return cp
		}
		if cp := tryTrailingSlash(index, bare); cp != nil {
			return cp
		}
	}
	return nil
}

func tryTrailingSlash(index map[string]*CrawlPageInfo, path string) *CrawlPageInfo {
	if strings.HasSuffix(path, "/") {
		if cp := index[strings.TrimSuffix(path, "/")]; cp != nil {
			return cp
		}
	} else {
		if cp := index[path+"/"]; cp != nil {
			return cp
		}
	}
	return nil
}

// decodePathSafe URL-decodes the path portion (before any '?'). Falls back to
// the original on error. Cheap no-op when there's no '%' to decode.
func decodePathSafe(path string) string {
	q := strings.IndexByte(path, '?')
	pathPart := path
	queryPart := ""
	if q >= 0 {
		pathPart = path[:q]
		queryPart = path[q:]
	}
	if !strings.ContainsRune(pathPart, '%') {
		return path
	}
	dec, err := url.PathUnescape(pathPart)
	if err != nil {
		return path
	}
	return dec + queryPart
}

// encodePathSafe escapes path-unsafe runes into percent-encoding. Cheap no-op
// when the path is already pure ASCII (no chars to encode).
func encodePathSafe(path string) string {
	q := strings.IndexByte(path, '?')
	pathPart := path
	queryPart := ""
	if q >= 0 {
		pathPart = path[:q]
		queryPart = path[q:]
	}
	asciiOnly := true
	for i := 0; i < len(pathPart); i++ {
		if pathPart[i] >= 0x80 {
			asciiOnly = false
			break
		}
	}
	if asciiOnly {
		return path
	}
	// Encode each segment between '/' so the slashes stay literal.
	parts := strings.Split(pathPart, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/") + queryPart
}

func extractPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		return path + "?" + u.RawQuery
	}
	return path
}

func addToSegmentDetails(details map[PageSegment][]MergedPage, mp MergedPage) {
	if len(details[mp.Segment]) < 50 {
		details[mp.Segment] = append(details[mp.Segment], mp)
	}
}
