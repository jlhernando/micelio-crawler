package analysis

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Automatic segmentation assigns every page a value per dimension (directory,
// extension, template, text amount, locale, ...) without user-written rules.
// Values are short, stable labels so pages can be grouped and filtered.

const (
	// templateClusterMaxDistance is the largest Hamming distance between two
	// DOM fingerprints that still counts as the same template.
	templateClusterMaxDistance = 8
	// templateClusterMaxReps caps cluster representatives per directory so
	// clustering stays O(pages × reps) on very large sections.
	templateClusterMaxReps = 64
	// minUnnamedGroupSize is the smallest cluster of unclassified pages that
	// gets its own "group N" template label.
	minUnnamedGroupSize = 5
	// maxSegmentValues caps the values reported per dimension in stats.
	maxSegmentValues = 100
)

var (
	blogPathRe     = regexp.MustCompile(`(?i)/(blog|blogs|news|noticias|articulos|artikel|magazine|journal|stories)(/|$)`)
	categoryPathRe = regexp.MustCompile(`(?i)/(categor[a-z]*|collections?|c|departments?|dept|shop|tienda)/`)
	paginationRe   = regexp.MustCompile(`(?i)/(page|pagina|seite|strona)[-/]?\d+/?$`)
	localePathRe   = regexp.MustCompile(`^/([a-z]{2}(?:[-_][a-z]{2})?)(?:/|$)`)
	extensionRe    = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)
)

var (
	paginationParams = map[string]bool{"page": true, "p": true, "pg": true, "paged": true, "offset": true, "start": true, "pagina": true, "seite": true}
	searchParams     = map[string]bool{"q": true, "query": true, "s": true, "search": true, "keyword": true, "keywords": true, "k": true}
	trackingParams   = map[string]bool{"gclid": true, "fbclid": true, "msclkid": true, "ref": true, "_ga": true, "mc_cid": true, "mc_eid": true, "srsltid": true}
	facetParams      = map[string]bool{"sort": true, "order": true, "orderby": true, "filter": true, "price": true, "brand": true, "color": true, "colour": true, "size": true, "category": true, "cat": true, "make": true, "model": true, "min_price": true, "max_price": true, "view": true}
)

// genericSchemaTypes are present on most pages and say little about page purpose.
var genericSchemaTypes = map[string]bool{
	"breadcrumblist": true, "website": true, "webpage": true, "organization": true, "searchaction": true,
	"imageobject": true, "person": true, "listitem": true, "sitenavigationelement": true, "wpheader": true,
	"wpfooter": true, "wpsidebar": true, "corporation": true, "logo": true, "thing": true,
}

// AutoSegmentOptions configures automatic segmentation.
type AutoSegmentOptions struct {
	Rules    []Segment          // user regex rules; matches fill PageData.Segments
	PageRank map[string]float64 // optional scores keyed by URL; falls back to PageData.PageRank/Inlinks
}

// PropagateTemplateClusters groups pages with near-identical DOM skeletons
// within the same first-level directory. Unclassified ("other") pages inherit
// the majority template of their cluster when classified members agree.
// Returns a label ("group N · /dir/") for each unclassified page that sits in
// a large cluster without a known template, keyed by page URL.
func PropagateTemplateClusters(pages []*types.PageData) map[string]string {
	type cluster struct {
		rep     uint64
		members []*types.PageData
	}
	byDir := make(map[string][]*cluster)
	for _, p := range pages {
		if p.DOMFingerprint == 0 || p.StatusCode != 200 || p.TemplateType == "" || p.TemplateType == TemplateHomepage {
			continue
		}
		dir := dirLevels(pageURL(p))[0]
		clusters := byDir[dir]
		placed := false
		for _, c := range clusters {
			if HammingDistance(c.rep, p.DOMFingerprint) <= templateClusterMaxDistance {
				c.members = append(c.members, p)
				placed = true
				break
			}
		}
		if !placed && len(clusters) < templateClusterMaxReps {
			byDir[dir] = append(clusters, &cluster{rep: p.DOMFingerprint, members: []*types.PageData{p}})
		}
	}

	type unnamed struct {
		dir     string
		members []*types.PageData
	}
	var groups []unnamed
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		for _, c := range byDir[dir] {
			known := make(map[string]int)
			knownTotal := 0
			var others []*types.PageData
			for _, m := range c.members {
				if m.TemplateType == TemplateOther {
					others = append(others, m)
				} else {
					known[m.TemplateType]++
					knownTotal++
				}
			}
			if len(others) == 0 {
				continue
			}
			best, bestCount := "", 0
			for t, n := range known {
				if n > bestCount || (n == bestCount && t < best) {
					best, bestCount = t, n
				}
			}
			if bestCount >= 2 && bestCount*10 >= knownTotal*6 {
				for _, m := range others {
					m.TemplateType = best
				}
				continue
			}
			if len(others) >= minUnnamedGroupSize {
				groups = append(groups, unnamed{dir: dir, members: others})
			}
		}
	}

	sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].members) > len(groups[j].members) })
	labels := make(map[string]string)
	for i, g := range groups {
		label := fmt.Sprintf("group %d · %s", i+1, g.dir)
		for _, m := range g.members {
			labels[m.URL] = label
		}
	}
	return labels
}

// needsAutoSegments reports whether any page still lacks automatic segments.
func needsAutoSegments(pages []*types.PageData) bool {
	for _, p := range pages {
		if p.AutoSegments == nil {
			return true
		}
	}
	return false
}

// AssignAutoSegments fills PageData.AutoSegments (and PageData.Segments from
// user rules) for every page. groupLabels comes from PropagateTemplateClusters.
func AssignAutoSegments(pages []*types.PageData, groupLabels map[string]string, opts AutoSegmentOptions) {
	low, high := wordCountTerciles(pages)
	commerce := isCommerceSite(pages)
	// Intern values so pages share one copy of each label (directory values
	// repeat across many pages on large crawls).
	pool := make(map[string]string, 1024)
	intern := func(v string) string {
		if c, ok := pool[v]; ok {
			return c
		}
		pool[v] = v
		return v
	}
	for _, p := range pages {
		u := pageURL(p)
		parsed, err := url.Parse(u)
		if err != nil {
			parsed = &url.URL{}
		}
		seg := &types.PageSegments{}
		dirs := dirLevelsOf(parsed)
		seg.Dir1, seg.Dir2, seg.Dir3 = intern(dirs[0]), intern(dirs[1]), intern(dirs[2])
		seg.Extension = intern(extensionOf(parsed))
		seg.Template = intern(templateSegment(p, groupLabels[p.URL], commerce))
		seg.TextAmount = textAmount(p, low, high)
		seg.Locale = intern(pageLocale(p, u, parsed))
		seg.URLParams = paramTypeOf(parsed)
		seg.DepthBand = depthBand(p)
		seg.IndexState = indexState(p)
		seg.SchemaType = intern(primarySchemaType(p))
		seg.Subdomain = intern(strings.ToLower(parsed.Hostname()))
		seg.Freshness = freshnessBand(p)
		seg.LinkProfile = linkProfile(p)
		p.AutoSegments = seg

		if len(opts.Rules) > 0 {
			p.Segments = AssignSegments(u, opts.Rules)
		}
	}
	AssignAuthoritySegments(pages, opts.PageRank)
}

// AssignAuthoritySegments buckets pages into internal-authority terciles using
// PageRank when it varies across pages, else inlink counts (or the link-graph
// in-degree). Pages without AutoSegments are skipped.
func AssignAuthoritySegments(pages []*types.PageData, scores map[string]float64) {
	pageRank := func(p *types.PageData) float64 {
		if s, ok := scores[p.URL]; ok {
			return s
		}
		return p.PageRank
	}
	inlinks := func(p *types.PageData) float64 { return float64(pageInlinks(p)) }

	value := pageRank
	vals := authorityValues(pages, value)
	// Uniform PageRank (e.g. computed without link data) carries no signal.
	if len(vals) == 0 || vals[0] == vals[len(vals)-1] {
		value = inlinks
		vals = authorityValues(pages, value)
	}
	if len(vals) == 0 {
		for _, p := range pages {
			if p.AutoSegments != nil {
				p.AutoSegments.Authority = ""
			}
		}
		return
	}
	lowCut := vals[len(vals)/3]
	highCut := vals[len(vals)*2/3]
	for _, p := range pages {
		if p.AutoSegments == nil {
			continue
		}
		if p.StatusCode != 200 {
			p.AutoSegments.Authority = ""
			continue
		}
		v := value(p)
		// Strict ">" for high keeps a large block of tied pages (common on
		// templated sites) in "medium" instead of inflating "high".
		switch {
		case v <= 0:
			p.AutoSegments.Authority = "none"
		case v > highCut:
			p.AutoSegments.Authority = "high"
		case v >= lowCut:
			p.AutoSegments.Authority = "medium"
		default:
			p.AutoSegments.Authority = "low"
		}
	}
}

// authorityValues returns the sorted positive values for HTML pages.
func authorityValues(pages []*types.PageData, value func(*types.PageData) float64) []float64 {
	var vals []float64
	for _, p := range pages {
		if p.AutoSegments != nil && p.StatusCode == 200 {
			if v := value(p); v > 0 {
				vals = append(vals, v)
			}
		}
	}
	sort.Float64s(vals)
	return vals
}

// BuildAutoSegmentStats aggregates pages per value for every dimension.
func BuildAutoSegmentStats(pages []*types.PageData) []types.SegmentDimensionStats {
	type acc struct {
		pages, indexable, errors, words, inlinks int
		respTotal                                int64
		sample                                   string
	}
	out := make([]types.SegmentDimensionStats, 0, len(types.SegmentDimensions))
	for _, dim := range types.SegmentDimensions {
		values := make(map[string]*acc)
		unassigned := 0
		for _, p := range pages {
			v := p.AutoSegments.Value(dim)
			if v == "" {
				unassigned++
				continue
			}
			a := values[v]
			if a == nil {
				a = &acc{sample: p.URL}
				values[v] = a
			}
			a.pages++
			if p.Indexability.Indexable {
				a.indexable++
			}
			if p.StatusCode >= 400 || p.StatusCode == 0 {
				a.errors++
			}
			a.words += p.WordCount
			a.inlinks += pageInlinks(p)
			a.respTotal += p.ResponseTimeMs
		}
		if len(values) == 0 {
			continue
		}
		stats := make([]types.SegmentValueStats, 0, len(values))
		for v, a := range values {
			n := float64(a.pages)
			stats = append(stats, types.SegmentValueStats{
				Value:         v,
				Pages:         a.pages,
				IndexablePct:  round1(float64(a.indexable) * 100 / n),
				ErrorPct:      round1(float64(a.errors) * 100 / n),
				AvgWords:      round1(float64(a.words) / n),
				AvgInlinks:    round1(float64(a.inlinks) / n),
				AvgResponseMs: a.respTotal / int64(a.pages),
				SampleURL:     a.sample,
			})
		}
		sort.Slice(stats, func(i, j int) bool {
			if stats[i].Pages != stats[j].Pages {
				return stats[i].Pages > stats[j].Pages
			}
			return stats[i].Value < stats[j].Value
		})
		total := len(stats)
		if len(stats) > maxSegmentValues {
			stats = stats[:maxSegmentValues]
		}
		out = append(out, types.SegmentDimensionStats{Dimension: dim, Values: stats, Unassigned: unassigned, TotalValues: total})
	}
	return out
}

// pageInlinks returns the page's internal inlink count, falling back to the
// link-graph in-degree when inlinks were not computed (pages reloaded from disk).
func pageInlinks(p *types.PageData) int {
	if p.Inlinks == 0 && p.LinkIntelligence != nil {
		return p.LinkIntelligence.InDegree
	}
	return p.Inlinks
}

// ── Per-dimension helpers ──

func pageURL(p *types.PageData) string {
	if p.FinalURL != "" {
		return p.FinalURL
	}
	return p.URL
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

// dirLevels returns the first three directory levels of a URL. A missing level
// inherits its parent so each level partitions all pages. The last path
// segment counts as a directory only when the URL ends with a slash.
func dirLevels(raw string) [3]string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return [3]string{"/", "/", "/"}
	}
	return dirLevelsOf(parsed)
}

func dirLevelsOf(parsed *url.URL) [3]string {
	var out [3]string
	p := "/"
	if parsed.Path != "" {
		p = parsed.Path
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if !strings.HasSuffix(p, "/") && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	prefix := "/"
	for i := 0; i < 3; i++ {
		if i < len(parts) && parts[i] != "" {
			prefix += parts[i] + "/"
		}
		out[i] = prefix
	}
	return out
}

func urlExtension(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return extensionOf(parsed)
}

func extensionOf(parsed *url.URL) string {
	p := parsed.Path
	if p == "" || strings.HasSuffix(p, "/") {
		return "/"
	}
	ext := strings.ToLower(path.Ext(p))
	if ext == "" || !extensionRe.MatchString(ext) || strings.Trim(ext[1:], "0123456789") == "" {
		return "none"
	}
	return ext
}

// isCommerceSite reports whether enough pages are product pages for listings
// to be product listing pages (PLPs) rather than generic listings.
func isCommerceSite(pages []*types.PageData) bool {
	products, total := 0, 0
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		total++
		if p.TemplateType == TemplateProduct {
			products++
		}
	}
	return products >= 3 && products*50 >= total // at least 2% of HTML pages
}

// templateSegment refines the detected template into the labels SEOs use for
// segmentation (PLP/PDP, category, blog index/post) and applies cluster labels.
// Listings become "plp" only on commerce sites.
func templateSegment(p *types.PageData, groupLabel string, commerce bool) string {
	u := pageURL(p)
	switch p.TemplateType {
	case "":
		return ""
	case TemplateOther:
		if groupLabel != "" {
			return groupLabel
		}
		return TemplateOther
	case TemplateArticle:
		if blogPathRe.MatchString(u) || hasSchemaType(p, "blogposting") {
			return "blog_post"
		}
		return TemplateArticle
	case TemplateListing:
		if blogPathRe.MatchString(u) {
			return "blog_index"
		}
		if categoryPathRe.MatchString(u) && p.ListingSignals == 0 && !strings.Contains(u, "?") && !paginationRe.MatchString(u) {
			return "category"
		}
		if commerce {
			return "plp"
		}
		return TemplateListing
	case TemplateProduct:
		return "pdp"
	}
	return p.TemplateType
}

func hasSchemaType(p *types.PageData, t string) bool {
	for _, sd := range p.StructuredData {
		if strings.EqualFold(sd.Type, t) {
			return true
		}
	}
	return false
}

// wordCountTerciles returns the 33rd and 66th percentile word counts across
// HTML pages with text, so text amount is relative to the site itself.
func wordCountTerciles(pages []*types.PageData) (int, int) {
	var counts []int
	for _, p := range pages {
		if p.StatusCode == 200 && p.WordCount > 0 {
			counts = append(counts, p.WordCount)
		}
	}
	if len(counts) < 3 {
		return 300, 1000
	}
	sort.Ints(counts)
	return counts[len(counts)/3], counts[len(counts)*2/3]
}

func textAmount(p *types.PageData, low, high int) string {
	if p.StatusCode != 200 {
		return ""
	}
	switch {
	case p.WordCount == 0:
		return "empty"
	case p.WordCount < low:
		return "low"
	case p.WordCount < high:
		return "medium"
	default:
		return "high"
	}
}

func pageLocale(p *types.PageData, u string, parsed *url.URL) string {
	if p.HTMLLang != "" {
		return strings.ReplaceAll(strings.ToLower(p.HTMLLang), "_", "-")
	}
	for _, h := range p.Hreflang {
		if h.Href == u && h.Lang != "" && !strings.EqualFold(h.Lang, "x-default") {
			return strings.ToLower(h.Lang)
		}
	}
	if m := localePathRe.FindStringSubmatch(strings.ToLower(parsed.Path)); m != nil {
		return strings.ReplaceAll(m[1], "_", "-")
	}
	if p.StatusCode == 200 {
		return "unknown"
	}
	return ""
}

func urlParamType(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return paramTypeOf(parsed)
}

func paramTypeOf(parsed *url.URL) string {
	if parsed.RawQuery == "" {
		if paginationRe.MatchString(parsed.Path) {
			return "pagination"
		}
		return "clean"
	}
	var hasPage, hasSearch, hasFacet, hasTracking, hasOther bool
	for key := range parsed.Query() {
		k := strings.ToLower(key)
		switch {
		case paginationParams[k]:
			hasPage = true
		case searchParams[k]:
			hasSearch = true
		case facetParams[k] || strings.HasPrefix(k, "filter") || strings.HasPrefix(k, "f["):
			hasFacet = true
		case trackingParams[k] || strings.HasPrefix(k, "utm_"):
			hasTracking = true
		default:
			hasOther = true
		}
	}
	switch {
	case hasSearch:
		return "search"
	case hasFacet:
		return "facets"
	case hasPage:
		return "pagination"
	case hasOther:
		return "other parameters"
	case hasTracking:
		return "tracking"
	}
	return "clean"
}

func depthBand(p *types.PageData) string {
	switch d := p.Depth; {
	case d <= 1:
		return "0-1"
	case d <= 3:
		return "2-3"
	case d <= 5:
		return "4-5"
	default:
		return "6+"
	}
}

func indexState(p *types.PageData) string {
	switch {
	case p.RobotsBlocked:
		return "robots blocked"
	case p.StatusCode == 0:
		return "fetch error"
	case p.StatusCode >= 500:
		return "5xx"
	case p.StatusCode >= 400:
		return "4xx"
	case p.StatusCode >= 300:
		return "redirect"
	case p.Indexability.Indexable:
		return "indexable"
	}
	reason := strings.ToLower(p.Indexability.Reason)
	switch {
	case strings.Contains(reason, "noindex"):
		return "noindex"
	case strings.Contains(reason, "canonical"):
		return "canonicalised"
	}
	return "non-indexable"
}

func primarySchemaType(p *types.PageData) string {
	if p.StatusCode != 200 {
		return ""
	}
	generic := ""
	for _, sd := range p.StructuredData {
		t := strings.TrimSpace(sd.Type)
		if t == "" {
			continue
		}
		if genericSchemaTypes[strings.ToLower(t)] {
			if generic == "" {
				generic = t
			}
			continue
		}
		return t
	}
	if generic != "" {
		return generic
	}
	return "none"
}

func freshnessBand(p *types.PageData) string {
	if p.StatusCode != 200 {
		return ""
	}
	f := p.Freshness
	if f == nil || (f.BylineDate == nil && f.ModifiedDate == nil) {
		return "no date"
	}
	switch d := f.ContentAgeDays; {
	case d < 183:
		return "< 6 months"
	case d < 730:
		return "6-24 months"
	default:
		return "> 2 years"
	}
}

// linkProfile labels how much of a page's internal linking lives in the main
// content versus site-wide chrome (nav, header, footer, sidebar).
func linkProfile(p *types.PageData) string {
	z := p.LinkZones
	if p.StatusCode != 200 || z == nil {
		return ""
	}
	total := z.Total()
	if total == 0 {
		return "no internal links"
	}
	contextual := z.Content + z.Related + z.Comments
	share := float64(contextual) / float64(total)
	switch {
	case share >= 0.5:
		return "content-led"
	case share >= 0.2:
		return "mixed"
	default:
		return "boilerplate-heavy"
	}
}
