package analysis

import (
	"net/url"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Soft-404 candidates: pages that answer 200 (or redirect) but show no real
// content. Google does not publish how it classifies them; its documentation
// describes the cases these signals look for: pages that say the content does
// not exist, empty results/category pages, and URLs redirected to the home
// page. Results are candidates to review, confirmed in Search Console.

const (
	// soft404MaxCandidates caps the list kept in stats.
	soft404MaxCandidates = 1000
	// A crawled page matches the site's own "not found" page when its DOM
	// skeleton is this close to the probe's.
	soft404ProbeMaxDistance = 3
	// Empty listings are judged against peers: a group needs this many
	// listing pages, and this share of them must link to items.
	soft404MinPeers      = 10
	soft404PeerFullShare = 0.6
	soft404FullListing   = 3 // item links that make a listing "full"
	// Thin pages are judged against pages of the same template.
	soft404MinTemplate = 20
	soft404ThinShare   = 0.25
	soft404ThinWords   = 150
	// A redirect target shared by this many URLs is a mass redirect.
	soft404MassRedirect = 10
)

// DetectSoft404 scores every page, sets page.Soft404 and page.IsSoft404 (true
// for high and medium confidence) and returns the summary. probes are the
// crawl-time "URL that cannot exist" requests (may be nil).
func DetectSoft404(pages []*types.PageData, probes []types.Soft404Probe, seedURL string) *types.Soft404Stats {
	signals := make(map[*types.PageData]map[string]string, 16) // page -> reason -> confidence
	add := func(p *types.PageData, reason, conf string) {
		m := signals[p]
		if m == nil {
			m = map[string]string{}
			signals[p] = m
		}
		m[reason] = conf
	}

	matchProbes := fingerprintProbes(probes)
	home := homeKey(seedURL)

	// Per-page signals.
	for _, p := range pages {
		if p.StatusCode == 200 {
			for _, pr := range matchProbes {
				if sameHost(p.URL, pr.Host) && looksLikeProbe(p, pr) {
					add(p, types.Soft404MatchesErrorPage, types.Soft404High)
					break
				}
			}
			switch p.Soft404Text {
			case "title", "h1":
				if p.WordCount < soft404ThinWords {
					add(p, types.Soft404ErrorTextThin, types.Soft404High)
				} else {
					add(p, types.Soft404ErrorText, types.Soft404Medium)
				}
			case "body":
				add(p, types.Soft404ErrorTextThin, types.Soft404Medium)
			}
		}
		if isRedirect(p) && home != "" && pathDepth(p.URL) >= 2 && pageKey(p.FinalURL) == home && !trivialRedirect(p.URL, p.FinalURL) {
			add(p, types.Soft404RedirectHome, types.Soft404Medium)
		}
	}

	// Empty listings, judged against listing pages of the same group.
	groups := map[string][]*types.PageData{}
	for _, p := range pages {
		if p.StatusCode == 200 && isListingPage(p) {
			k := listingGroup(p)
			groups[k] = append(groups[k], p)
		}
	}
	for _, g := range groups {
		if len(g) < soft404MinPeers {
			continue
		}
		var fullLinks []int
		for _, p := range g {
			if p.ItemLinks >= soft404FullListing || p.ProductCards >= soft404FullListing {
				fullLinks = append(fullLinks, linkCount(p))
			}
		}
		if float64(len(fullLinks)) < soft404PeerFullShare*float64(len(g)) {
			continue // the group itself does not list items: no baseline
		}
		sort.Ints(fullLinks)
		medianLinks := fullLinks[len(fullLinks)/2]
		for _, p := range g {
			// A hub page (links to subcategories, not items) has as many
			// links as a full listing or more; an emptied grid has fewer.
			if p.ItemLinks == 0 && p.ProductCards == 0 && linkCount(p) < medianLinks {
				add(p, types.Soft404EmptyListing, types.Soft404Low)
			}
		}
	}

	// Thin pages for their template.
	byTemplate := map[string][]*types.PageData{}
	for _, p := range pages {
		if p.StatusCode == 200 && p.TemplateType != "" && p.TemplateType != TemplateHomepage {
			byTemplate[p.TemplateType] = append(byTemplate[p.TemplateType], p)
		}
	}
	for _, g := range byTemplate {
		if len(g) < soft404MinTemplate {
			continue
		}
		words := make([]int, len(g))
		for i, p := range g {
			words[i] = p.WordCount
		}
		sort.Ints(words)
		median := words[len(words)/2]
		limit := int(float64(median) * soft404ThinShare)
		for _, p := range g {
			if p.WordCount < limit && p.WordCount < soft404ThinWords {
				add(p, types.Soft404ThinForTemplate, types.Soft404Low)
			}
		}
	}

	// Many URLs redirected to the same page (not the home page: that has its
	// own signal).
	byTarget := map[string][]*types.PageData{}
	for _, p := range pages {
		if isRedirect(p) && !trivialRedirect(p.URL, p.FinalURL) {
			k := pageKey(p.FinalURL)
			byTarget[k] = append(byTarget[k], p)
		}
	}
	for target, g := range byTarget {
		if len(g) >= soft404MassRedirect && target != home {
			for _, p := range g {
				add(p, types.Soft404MassRedirect, types.Soft404Low)
			}
		}
	}

	st := &types.Soft404Stats{ByConfidence: map[string]int{}, ByReason: map[string]int{}, Probes: probes}
	for _, p := range pages {
		p.Soft404 = nil
		p.IsSoft404 = false
		m := signals[p]
		if len(m) == 0 {
			continue
		}
		sig := combineSoft404(m)
		p.Soft404 = sig
		p.IsSoft404 = sig.Confidence != types.Soft404Low
		st.Total++
		st.ByConfidence[sig.Confidence]++
		for _, r := range sig.Reasons {
			st.ByReason[r]++
		}
		st.Candidates = append(st.Candidates, types.Soft404Candidate{
			URL: p.URL, StatusCode: p.StatusCode, Confidence: sig.Confidence, Reasons: sig.Reasons,
		})
	}
	rank := map[string]int{types.Soft404High: 0, types.Soft404Medium: 1, types.Soft404Low: 2}
	sort.SliceStable(st.Candidates, func(i, j int) bool {
		a, b := st.Candidates[i], st.Candidates[j]
		if rank[a.Confidence] != rank[b.Confidence] {
			return rank[a.Confidence] < rank[b.Confidence]
		}
		if len(a.Reasons) != len(b.Reasons) {
			return len(a.Reasons) > len(b.Reasons)
		}
		return a.URL < b.URL
	})
	if len(st.Candidates) > soft404MaxCandidates {
		st.Candidates = st.Candidates[:soft404MaxCandidates]
	}
	if st.Total == 0 && len(probes) == 0 {
		return nil
	}
	return st
}

// combineSoft404 takes the strongest confidence; two independent medium
// signals make a high one.
func combineSoft404(m map[string]string) *types.Soft404Signal {
	sig := &types.Soft404Signal{Confidence: types.Soft404Low}
	medium := 0
	for reason, conf := range m {
		sig.Reasons = append(sig.Reasons, reason)
		switch conf {
		case types.Soft404High:
			sig.Confidence = types.Soft404High
		case types.Soft404Medium:
			medium++
			if sig.Confidence == types.Soft404Low {
				sig.Confidence = types.Soft404Medium
			}
		}
	}
	if medium >= 2 {
		sig.Confidence = types.Soft404High
	}
	// An empty listing alone may just render its items with JavaScript; with
	// "no results" wording it is a strong case.
	if _, empty := m[types.Soft404EmptyListing]; empty {
		_, text := m[types.Soft404ErrorText]
		_, thin := m[types.Soft404ErrorTextThin]
		if text || thin {
			sig.Confidence = types.Soft404High
		}
	}
	sort.Strings(sig.Reasons)
	return sig
}

// fingerprintProbes returns the probes worth matching pages against: the
// host answered 200 with its own page (not a redirect to another page, such
// as the home page, which would match the real one).
func fingerprintProbes(probes []types.Soft404Probe) []types.Soft404Probe {
	var out []types.Soft404Probe
	for _, pr := range probes {
		if pr.StatusCode != 200 || pr.DOMFingerprint == 0 {
			continue
		}
		if pr.FinalURL != "" && pageKey(pr.FinalURL) != pageKey(pr.URL) {
			continue
		}
		out = append(out, pr)
	}
	return out
}

func looksLikeProbe(p *types.PageData, pr types.Soft404Probe) bool {
	if p.DOMFingerprint == 0 || HammingDistance(p.DOMFingerprint, pr.DOMFingerprint) > soft404ProbeMaxDistance {
		return false
	}
	if pr.Title != "" && p.Title != nil && strings.EqualFold(strings.TrimSpace(p.Title.Text), strings.TrimSpace(pr.Title)) {
		return true
	}
	// Same skeleton and about as much text as the error page.
	lo, hi := float64(pr.WordCount)*0.9, float64(pr.WordCount)*1.1
	return p.WordCount < 300 && float64(p.WordCount) >= lo && float64(p.WordCount) <= hi
}

func isRedirect(p *types.PageData) bool {
	return p.StatusCode >= 300 && p.StatusCode < 400 && p.FinalURL != "" && p.FinalURL != p.URL
}

func linkCount(p *types.PageData) int {
	if len(p.InternalLinks) > 0 {
		return len(p.InternalLinks)
	}
	return p.InternalLinkCount
}

func isListingPage(p *types.PageData) bool {
	return p.TemplateType == TemplateListing || p.ListingSignals > 0
}

// listingGroup groups listings by segment rules when set, else by their
// parent folder, cut to two levels ("/es/brand", "/osobowe/bmw"), so category pages
// are compared with category pages and brand pages with brand pages. A
// coarser group would let a section that renders its products with
// JavaScript (no item links in the HTML) look empty next to one that does
// not.
func listingGroup(p *types.PageData) string {
	if len(p.Segments) > 0 {
		return "seg:" + strings.Join(p.Segments, ",")
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	parts = parts[:len(parts)-1] // the parent folder, at most two levels
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return u.Host + "/" + strings.Join(parts, "/")
}

func pathDepth(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// pageKey identifies a page across scheme, "www." and trailing-slash
// variants.
func pageKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	path := strings.TrimRight(u.Path, "/")
	k := host + path
	if u.RawQuery != "" {
		k += "?" + u.RawQuery
	}
	return k
}

func homeKey(seedURL string) string {
	u, err := url.Parse(seedURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

// trivialRedirect is a redirect that only changes scheme, "www." or the
// trailing slash.
func trivialRedirect(from, to string) bool {
	return pageKey(from) == pageKey(to)
}

func sameHost(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimPrefix(u.Host, "www."), strings.TrimPrefix(host, "www."))
}
