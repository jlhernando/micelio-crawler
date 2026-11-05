package analysis

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Hreflang audit. Alternates come from three places: <link rel=alternate
// hreflang> in the HTML, the HTTP Link header, and <xhtml:link> in the XML
// sitemaps (sites like petsitters.example declare them only there). Every page
// that declares alternates, crawled or only listed in a sitemap, is a node;
// its declarations are checked on their own (codes, self reference,
// x-default, duplicates, HTML vs sitemap) and against the crawl (target
// status, redirect, noindex, canonical, robots) and against the target's
// own declarations (return links).

var hreflangIssueMeta = map[string]struct {
	label    string
	severity string
	order    int
}{
	types.HreflangInvalidCode:      {"Invalid language or region code", "error", 0},
	types.HreflangMissingHref:      {"Alternate without a URL", "error", 1},
	types.HreflangNoReturn:         {"Missing return link (not reciprocal)", "error", 2},
	types.HreflangTargetError:      {"Alternate URL returns an error", "error", 3},
	types.HreflangTargetNoindex:    {"Alternate URL is noindex", "error", 4},
	types.HreflangTargetRedirect:   {"Alternate URL redirects", "warning", 5},
	types.HreflangTargetCanonical:  {"Alternate URL is canonicalised elsewhere", "warning", 6},
	types.HreflangTargetBlocked:    {"Alternate URL blocked by robots.txt", "warning", 7},
	types.HreflangMissingSelf:      {"No self-referencing hreflang", "warning", 8},
	types.HreflangDuplicateCode:    {"Same code points to different URLs", "warning", 9},
	types.HreflangSourceConflict:   {"HTML and sitemap declare different alternates", "warning", 10},
	types.HreflangLangMismatch:     {"html lang differs from the page's hreflang", "warning", 11},
	types.HreflangRelativeURL:      {"Relative alternate URL", "warning", 12},
	types.HreflangMissingXDefault:  {"No x-default", "info", 13},
	types.HreflangTargetNotCrawled: {"Alternate URL not crawled", "info", 14},
}

type hreflangNode struct {
	url  string
	page *types.PageData // nil: only listed in a sitemap
	own  []types.HreflangEntry
	set  int32 // sitemap set id, -1 none
}

type hreflangIssueAcc struct {
	group    types.HreflangIssueGroup
	lastNode int
}

type hreflangAudit struct {
	issues map[string]*hreflangIssueAcc
	node   int
}

func (a *hreflangAudit) add(id string, s types.HreflangIssueSample) {
	acc := a.issues[id]
	if acc == nil {
		m := hreflangIssueMeta[id]
		acc = &hreflangIssueAcc{group: types.HreflangIssueGroup{ID: id, Label: m.label, Severity: m.severity}, lastNode: -1}
		a.issues[id] = acc
	}
	acc.group.Count++
	if acc.lastNode != a.node {
		acc.lastNode = a.node
		acc.group.Pages++
	}
	if len(acc.group.Samples) < types.MaxHreflangSamples {
		acc.group.Samples = append(acc.group.Samples, s)
	}
}

// hreflangKey makes URLs comparable: lower-case scheme and host, no default
// port, no fragment. Paths and trailing slashes stay as they are, because
// search engines treat /de and /de/ as different URLs.
func hreflangKey(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	host = strings.TrimSuffix(strings.TrimSuffix(host, ":443"), ":80")
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func resolveHreflangHref(base, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	b, err := url.Parse(base)
	if err != nil {
		return href
	}
	r, err := url.Parse(href)
	if err != nil {
		return href
	}
	return b.ResolveReference(r).String()
}

// redirectsOffsite reports whether the page redirected to another host. Its
// hreflang then belongs to that site (an app store, a partner), not this one.
func redirectsOffsite(p *types.PageData) bool {
	if p.FinalURL == "" || p.FinalURL == p.URL {
		return false
	}
	from, err1 := url.Parse(p.URL)
	to, err2 := url.Parse(p.FinalURL)
	return err1 == nil && err2 == nil && !strings.EqualFold(from.Hostname(), to.Hostname())
}

// AnalyzeHreflang audits hreflang across the crawl and the sitemap
// alternates. Returns nil when nothing declares hreflang.
func AnalyzeHreflang(pages []*types.PageData, sm *types.SitemapHreflang) *types.HreflangStats {
	crawled := make(map[string]*types.PageData, len(pages))
	var nodes []hreflangNode
	nodeIdx := make(map[string]int32)
	for _, p := range pages {
		k := hreflangKey(p.URL)
		if _, ok := crawled[k]; !ok {
			crawled[k] = p
		}
		if len(p.Hreflang) == 0 || redirectsOffsite(p) {
			continue
		}
		if _, ok := nodeIdx[k]; ok {
			continue
		}
		nodeIdx[k] = int32(len(nodes))
		nodes = append(nodes, hreflangNode{url: p.URL, page: p, own: p.Hreflang, set: -1})
	}
	if sm != nil {
		for i, loc := range sm.URLs {
			k := hreflangKey(loc)
			if j, ok := nodeIdx[k]; ok {
				if nodes[j].set < 0 {
					nodes[j].set = sm.Set[i]
				}
				continue
			}
			nodeIdx[k] = int32(len(nodes))
			nodes = append(nodes, hreflangNode{url: loc, page: crawled[k], set: sm.Set[i]})
		}
	}
	if len(nodes) == 0 {
		return nil
	}

	// Target keys of each sitemap set, for the return-link check.
	var setKeys []map[string]bool
	var setKeyList [][]string
	if sm != nil {
		setKeys = make([]map[string]bool, len(sm.Sets))
		setKeyList = make([][]string, len(sm.Sets))
		for i, set := range sm.Sets {
			m := make(map[string]bool, len(set))
			list := make([]string, len(set))
			for j, a := range set {
				list[j] = hreflangKey(a.Href)
				m[list[j]] = true
			}
			setKeys[i] = m
			setKeyList[i] = list
		}
	}
	declares := func(n *hreflangNode, key string) bool {
		if n.set >= 0 && setKeys[n.set][key] {
			return true
		}
		base := n.url
		if n.page != nil && n.page.FinalURL != "" {
			base = n.page.FinalURL
		}
		for _, a := range n.own {
			if a.Href != "" && hreflangKey(resolveHreflangHref(base, a.Href)) == key {
				return true
			}
		}
		return false
	}

	stats := &types.HreflangStats{BySource: map[string]int{}, SitemapTruncated: sm != nil && sm.Truncated}
	audit := &hreflangAudit{issues: map[string]*hreflangIssueAcc{}}
	codePages := map[string]int{}
	codeShown := map[string]string{} // first spelling seen, for display
	codeSeen := map[string]bool{}
	targets := make(map[string]struct{})
	type codeCheck struct {
		ok   bool
		hint string
	}
	checked := map[string]codeCheck{}
	validCode := func(c string) (bool, string) {
		r, ok := checked[c]
		if !ok {
			r.ok, r.hint = validateHreflangCode(c)
			checked[c] = r
		}
		return r.ok, r.hint
	}

	for ni := range nodes {
		n := &nodes[ni]
		audit.node = ni
		stats.PagesWithHreflang++
		if n.page != nil {
			stats.CrawledPages++
		} else {
			stats.SitemapOnlyPages++
		}
		selfKey := hreflangKey(n.url)
		finalKey := selfKey
		base := n.url
		if n.page != nil && n.page.FinalURL != "" {
			base = n.page.FinalURL
			finalKey = hreflangKey(n.page.FinalURL)
		}
		// alts and their comparable target URLs (keys[i]).
		alts := n.own
		var keys []string
		for _, a := range n.own {
			keys = append(keys, hreflangKey(resolveHreflangHref(base, a.Href)))
		}
		if n.set >= 0 {
			if len(n.own) == 0 {
				alts, keys = sm.Sets[n.set], setKeyList[n.set]
			} else {
				alts = append(append([]types.HreflangEntry{}, n.own...), sm.Sets[n.set]...)
				keys = append(keys, setKeyList[n.set]...)
			}
		}

		// Sources, codes, x-default.
		srcSeen := map[string]bool{}
		for k := range codeSeen {
			delete(codeSeen, k)
		}
		hasX := false
		for _, a := range alts {
			src := a.SourceOrHTML()
			if !srcSeen[src] {
				srcSeen[src] = true
				stats.BySource[src]++
			}
			code := types.NormalizeHreflangCode(a.Lang)
			if code == "x-default" {
				hasX = true
			}
			if codeSeen[code] {
				continue
			}
			codeSeen[code] = true
			if codePages[code] == 0 {
				codeShown[code] = strings.TrimSpace(a.Lang)
			}
			codePages[code]++
			if ok, hint := validCode(a.Lang); !ok {
				audit.add(types.HreflangInvalidCode, types.HreflangIssueSample{URL: n.url, Lang: a.Lang, Target: a.Href, Source: src, Detail: hint})
			}
		}
		if hasX {
			stats.XDefaultPages++
		} else if len(codeSeen) >= 2 {
			audit.add(types.HreflangMissingXDefault, types.HreflangIssueSample{URL: n.url, Source: hreflangSources(srcSeen)})
		}

		// Per source: hrefs, duplicates, self reference.
		bySource := map[string]map[string]string{} // source -> code -> target key
		hasSelf := false
		var selfCodes []string
		for i, a := range alts {
			src := a.SourceOrHTML()
			if strings.TrimSpace(a.Href) == "" {
				audit.add(types.HreflangMissingHref, types.HreflangIssueSample{URL: n.url, Lang: a.Lang, Source: src})
				continue
			}
			if src != types.HreflangSourceSitemap && !strings.HasPrefix(a.Href, "http://") && !strings.HasPrefix(a.Href, "https://") {
				audit.add(types.HreflangRelativeURL, types.HreflangIssueSample{URL: n.url, Lang: a.Lang, Target: a.Href, Source: src, Detail: "use a fully qualified URL"})
			}
			key := keys[i]
			code := types.NormalizeHreflangCode(a.Lang)
			m := bySource[src]
			if m == nil {
				m = map[string]string{}
				bySource[src] = m
			}
			if prev, ok := m[code]; ok {
				if prev != key {
					audit.add(types.HreflangDuplicateCode, types.HreflangIssueSample{URL: n.url, Lang: a.Lang, Target: a.Href, Source: src, Detail: "also " + prev})
				}
				continue
			}
			m[code] = key
			if key == selfKey || key == finalKey {
				hasSelf = true
				if code != "x-default" {
					selfCodes = append(selfCodes, a.Lang)
				}
			}
		}
		if !hasSelf && len(alts) > 0 {
			audit.add(types.HreflangMissingSelf, types.HreflangIssueSample{URL: n.url, Source: hreflangSources(srcSeen)})
		}

		// HTML (or header) vs sitemap.
		if sitemapSet, ok := bySource[types.HreflangSourceSitemap]; ok {
			for _, src := range []string{types.HreflangSourceHTML, types.HreflangSourceHeader} {
				if onPage, ok := bySource[src]; ok {
					if d := hreflangSetDiff(onPage, sitemapSet); d != "" {
						audit.add(types.HreflangSourceConflict, types.HreflangIssueSample{URL: n.url, Source: src + "+sitemap", Detail: d})
					}
				}
			}
		}

		// html lang vs the code this page claims for itself.
		if n.page != nil && n.page.HTMLLang != "" && len(selfCodes) > 0 {
			pageLang := hreflangLanguage(n.page.HTMLLang)
			match := false
			for _, c := range selfCodes {
				if hreflangLanguage(c) == pageLang {
					match = true
					break
				}
			}
			if !match {
				audit.add(types.HreflangLangMismatch, types.HreflangIssueSample{URL: n.url, Lang: selfCodes[0], Detail: "html lang=\"" + n.page.HTMLLang + "\""})
			}
		}

		// Targets: crawl status and return links. A page that itself
		// redirects or fails is not indexed, so what it declares does not
		// count (the sitemap listing it is the problem).
		if n.page != nil && (n.page.StatusCode != 200 || len(n.page.RedirectChain) > 0 || n.page.RobotsBlocked) {
			continue
		}
		done := map[string]bool{}
		for _, src := range []string{types.HreflangSourceHTML, types.HreflangSourceHeader, types.HreflangSourceSitemap} {
			m := bySource[src]
			codes := make([]string, 0, len(m))
			for c := range m {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			for _, code := range codes {
				key := m[code]
				if key == selfKey || key == finalKey || done[key] {
					continue
				}
				done[key] = true
				stats.Annotations++
				targets[key] = struct{}{}
				sample := types.HreflangIssueSample{URL: n.url, Lang: code, Target: key, Source: src}
				checkReturn := true
				tp := crawled[key]
				switch {
				case tp == nil:
					if _, listed := nodeIdx[key]; listed {
						sample.Detail = "listed in the sitemap"
					}
					audit.add(types.HreflangTargetNotCrawled, sample)
				case tp.RobotsBlocked:
					audit.add(types.HreflangTargetBlocked, sample)
					checkReturn = false
				case tp.StatusCode == 0 || tp.StatusCode >= 400:
					s := sample
					if tp.StatusCode == 0 {
						s.Detail = "no response"
						if tp.Error != "" {
							s.Detail = tp.Error
						}
					} else {
						s.Detail = fmt.Sprintf("HTTP %d", tp.StatusCode)
					}
					audit.add(types.HreflangTargetError, s)
					checkReturn = false
				case len(tp.RedirectChain) > 0 || (tp.FinalURL != "" && hreflangKey(tp.FinalURL) != key):
					s := sample
					s.Detail = "→ " + tp.FinalURL
					audit.add(types.HreflangTargetRedirect, s)
					checkReturn = false
				case strings.Contains(strings.ToLower(tp.Indexability.Reason), "noindex"):
					s := sample
					s.Detail = tp.Indexability.Reason
					audit.add(types.HreflangTargetNoindex, s)
					checkReturn = false
				case tp.Indexability.Reason == "canonicalized":
					s := sample
					if tp.Canonical != nil {
						s.Detail = "canonical → " + *tp.Canonical
					}
					audit.add(types.HreflangTargetCanonical, s)
					checkReturn = false
				}
				if !checkReturn {
					continue
				}
				if j, ok := nodeIdx[key]; ok {
					if !declares(&nodes[j], selfKey) && (finalKey == selfKey || !declares(&nodes[j], finalKey)) {
						audit.add(types.HreflangNoReturn, sample)
					}
				} else if tp != nil {
					s := sample
					s.Detail = "target declares no hreflang"
					audit.add(types.HreflangNoReturn, s)
				}
			}
		}
	}

	stats.Targets = len(targets)
	for code, n := range codePages {
		ok, hint := validateHreflangCode(code)
		stats.Codes = append(stats.Codes, types.HreflangCodeCount{Code: codeShown[code], Pages: n, Valid: ok, Hint: hint})
	}
	sort.Slice(stats.Codes, func(i, j int) bool {
		if stats.Codes[i].Pages != stats.Codes[j].Pages {
			return stats.Codes[i].Pages > stats.Codes[j].Pages
		}
		return strings.ToLower(stats.Codes[i].Code) < strings.ToLower(stats.Codes[j].Code)
	})
	for _, acc := range audit.issues {
		stats.Issues = append(stats.Issues, acc.group)
	}
	sort.Slice(stats.Issues, func(i, j int) bool {
		return hreflangIssueMeta[stats.Issues[i].ID].order < hreflangIssueMeta[stats.Issues[j].ID].order
	})
	if stats.Codes == nil {
		stats.Codes = []types.HreflangCodeCount{}
	}
	if stats.Issues == nil {
		stats.Issues = []types.HreflangIssueGroup{}
	}
	return stats
}

func hreflangSources(seen map[string]bool) string {
	var out []string
	for _, s := range []string{types.HreflangSourceHTML, types.HreflangSourceHeader, types.HreflangSourceSitemap} {
		if seen[s] {
			out = append(out, s)
		}
	}
	return strings.Join(out, "+")
}

// hreflangSetDiff describes how two code→URL maps differ, "" if equal.
func hreflangSetDiff(a, b map[string]string) string {
	var onlyA, onlyB, differ []string
	for c, u := range a {
		if v, ok := b[c]; !ok {
			onlyA = append(onlyA, c)
		} else if v != u {
			differ = append(differ, c)
		}
	}
	for c := range b {
		if _, ok := a[c]; !ok {
			onlyB = append(onlyB, c)
		}
	}
	if len(onlyA)+len(onlyB)+len(differ) == 0 {
		return ""
	}
	var parts []string
	if len(onlyA) > 0 {
		parts = append(parts, "only on page: "+joinCodes(onlyA))
	}
	if len(onlyB) > 0 {
		parts = append(parts, "only in sitemap: "+joinCodes(onlyB))
	}
	if len(differ) > 0 {
		parts = append(parts, "different URL: "+joinCodes(differ))
	}
	return strings.Join(parts, "; ")
}

func joinCodes(c []string) string {
	sort.Strings(c)
	if len(c) > 6 {
		return strings.Join(c[:6], ", ") + fmt.Sprintf(" +%d", len(c)-6)
	}
	return strings.Join(c, ", ")
}

// HreflangLegacyIssues flattens the audit into CrawlStats.HreflangIssues
// (url + text), capped, for older consumers (Issues cards, alerts).
func HreflangLegacyIssues(h *types.HreflangStats) []types.HreflangIssue {
	if h == nil {
		return nil
	}
	var out []types.HreflangIssue
	for _, g := range h.Issues {
		if g.Severity == "info" {
			continue
		}
		for _, s := range g.Samples {
			text := g.Label
			if s.Lang != "" {
				text += " [" + s.Lang + "]"
			}
			if s.Target != "" {
				text += " → " + s.Target
			}
			if s.Detail != "" {
				text += " (" + s.Detail + ")"
			}
			out = append(out, types.HreflangIssue{URL: s.URL, Issue: text, Type: g.ID})
			if len(out) >= types.StatsListCap {
				return out
			}
		}
	}
	return out
}
