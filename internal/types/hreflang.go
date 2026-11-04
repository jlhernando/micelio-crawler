package types

import (
	"hash/fnv"
	"strings"
)

// Hreflang sources. HreflangEntry.Source "" means HTML (<link> in <head>),
// so pages stored before sources existed keep their meaning.
const (
	HreflangSourceHTML    = "html"
	HreflangSourceHeader  = "header"  // HTTP Link: header
	HreflangSourceSitemap = "sitemap" // <xhtml:link> inside a sitemap <url>
)

// SourceOrHTML returns the entry's source, "html" when unset.
func (h HreflangEntry) SourceOrHTML() string {
	if h.Source == "" {
		return HreflangSourceHTML
	}
	return h.Source
}

// MaxSitemapHreflangURLs bounds how many sitemap <url> entries keep their
// alternates (a 150K-URL sitemap uses a few MB thanks to set sharing).
const MaxSitemapHreflangURLs = 2_000_000

// SitemapHreflang holds the hreflang alternates declared in XML sitemaps.
// Pages of one language cluster usually list the same alternates, so each
// distinct set is stored once (Sets) and URLs point at it (Set[i] is the
// set of URLs[i]). Not safe for concurrent use.
type SitemapHreflang struct {
	Sets      [][]HreflangEntry `json:"sets"`
	URLs      []string          `json:"urls"`
	Set       []int32           `json:"set"`
	Truncated bool              `json:"truncated,omitempty"` // MaxSitemapHreflangURLs reached

	setIndex map[uint64][]int32 // hash of a set -> set ids (collisions compared)
	urlIndex map[string]int32   // URL -> position in URLs
}

// Add records the alternates of one sitemap <url>. A URL already present
// keeps its first set (sitemaps that list a URL twice).
func (s *SitemapHreflang) Add(loc string, alts []HreflangEntry) {
	if loc == "" || len(alts) == 0 {
		return
	}
	s.ensureIndex()
	if _, ok := s.urlIndex[loc]; ok {
		return
	}
	if len(s.URLs) >= MaxSitemapHreflangURLs {
		s.Truncated = true
		return
	}
	h := hashHreflangSet(alts)
	id := int32(-1)
	for _, cand := range s.setIndex[h] {
		if equalHreflangSets(s.Sets[cand], alts) {
			id = cand
			break
		}
	}
	if id < 0 {
		id = int32(len(s.Sets))
		cp := make([]HreflangEntry, len(alts))
		for i, a := range alts {
			cp[i] = HreflangEntry{Lang: a.Lang, Href: a.Href, Source: HreflangSourceSitemap}
		}
		s.Sets = append(s.Sets, cp)
		s.setIndex[h] = append(s.setIndex[h], id)
	}
	s.urlIndex[loc] = int32(len(s.URLs))
	s.URLs = append(s.URLs, loc)
	s.Set = append(s.Set, id)
}

// Len is the number of URLs with alternates.
func (s *SitemapHreflang) Len() int {
	if s == nil {
		return 0
	}
	return len(s.URLs)
}

// Alternates returns the alternates declared for loc (shared, do not modify).
func (s *SitemapHreflang) Alternates(loc string) []HreflangEntry {
	if s == nil {
		return nil
	}
	s.ensureIndex()
	if i, ok := s.urlIndex[loc]; ok {
		return s.Sets[s.Set[i]]
	}
	return nil
}

func (s *SitemapHreflang) ensureIndex() {
	if s.urlIndex != nil {
		return
	}
	s.urlIndex = make(map[string]int32, len(s.URLs))
	s.setIndex = make(map[uint64][]int32, len(s.Sets))
	for i, u := range s.URLs {
		if i < len(s.Set) {
			s.urlIndex[u] = int32(i)
		}
	}
	for i, set := range s.Sets {
		h := hashHreflangSet(set)
		s.setIndex[h] = append(s.setIndex[h], int32(i))
	}
}

func hashHreflangSet(alts []HreflangEntry) uint64 {
	h := fnv.New64a()
	for _, a := range alts {
		h.Write([]byte(a.Lang))
		h.Write([]byte{0})
		h.Write([]byte(a.Href))
		h.Write([]byte{1})
	}
	return h.Sum64()
}

func equalHreflangSets(a, b []HreflangEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Lang != b[i].Lang || a[i].Href != b[i].Href {
			return false
		}
	}
	return true
}

// HreflangStats is the hreflang audit of a crawl (stats.hreflang).
type HreflangStats struct {
	// Pages (crawled, or listed in a sitemap with alternates) that declare
	// hreflang, and how many of them by source (a page can have several).
	PagesWithHreflang int            `json:"pagesWithHreflang"`
	BySource          map[string]int `json:"bySource"`
	// Crawled pages and sitemap-only URLs among PagesWithHreflang.
	CrawledPages     int `json:"crawledPages"`
	SitemapOnlyPages int `json:"sitemapOnlyPages,omitempty"`
	// Alternates checked (page → target pairs) and distinct targets.
	Annotations int `json:"annotations"`
	Targets     int `json:"targets"`
	// Codes: every hreflang value seen, how many pages use it, valid or not.
	Codes []HreflangCodeCount `json:"codes"`
	// Pages that declare x-default.
	XDefaultPages int `json:"xDefaultPages"`
	// Issues sorted by severity then count; Samples capped at
	// MaxHreflangSamples each.
	Issues []HreflangIssueGroup `json:"issues"`
	// SitemapTruncated: the sitemaps had more alternates than Micelio keeps.
	SitemapTruncated bool `json:"sitemapTruncated,omitempty"`
}

// HreflangCodeCount is one hreflang value and how many pages declare it.
type HreflangCodeCount struct {
	Code  string `json:"code"`
	Pages int    `json:"pages"`
	Valid bool   `json:"valid"`
	Hint  string `json:"hint,omitempty"` // for invalid codes: what was probably meant
}

// HreflangIssueGroup is one kind of hreflang problem.
type HreflangIssueGroup struct {
	ID       string                `json:"id"`
	Label    string                `json:"label"`
	Severity string                `json:"severity"` // error, warning, info
	Count    int                   `json:"count"`    // occurrences (page or page→target pair)
	Pages    int                   `json:"pages"`    // distinct pages affected
	Samples  []HreflangIssueSample `json:"samples"`
}

// HreflangIssueSample is one occurrence of an issue.
type HreflangIssueSample struct {
	URL    string `json:"url"`
	Lang   string `json:"lang,omitempty"`
	Target string `json:"target,omitempty"`
	Source string `json:"source,omitempty"` // html, header, sitemap
	Detail string `json:"detail,omitempty"`
}

// MaxHreflangSamples caps the samples kept per issue.
const MaxHreflangSamples = 50

// Hreflang issue ids.
const (
	HreflangInvalidCode      = "hreflang-invalid-code"
	HreflangMissingHref      = "hreflang-missing-href"
	HreflangRelativeURL      = "hreflang-relative-url"
	HreflangMissingSelf      = "hreflang-missing-self"
	HreflangNoReturn         = "hreflang-no-return"
	HreflangMissingXDefault  = "hreflang-missing-x-default"
	HreflangDuplicateCode    = "hreflang-duplicate-code"
	HreflangTargetError      = "hreflang-target-error"
	HreflangTargetRedirect   = "hreflang-target-redirect"
	HreflangTargetNoindex    = "hreflang-target-noindex"
	HreflangTargetCanonical  = "hreflang-target-canonicalised"
	HreflangTargetBlocked    = "hreflang-target-blocked"
	HreflangTargetNotCrawled = "hreflang-target-not-crawled"
	HreflangLangMismatch     = "hreflang-lang-mismatch"
	HreflangSourceConflict   = "hreflang-source-conflict"
)

// NormalizeHreflangCode lower-cases a code for counting ("en-GB" and
// "en-gb" are the same to search engines) and trims spaces.
func NormalizeHreflangCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}
