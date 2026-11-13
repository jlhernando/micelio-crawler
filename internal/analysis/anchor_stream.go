package analysis

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/micelio/micelio/internal/types"
)

// AnchorStream computes the reporter's anchor results (non-descriptive
// anchors, Anchor Cloud, anchor health) one page at a time, so a reload can
// drop each page's Anchors as it is read instead of keeping every anchor of
// the crawl in memory. Unlike AnchorAggregator it has no caps: fed the pages
// in report order, its results equal the in-memory Anchors path.
type AnchorStream struct {
	nonDesc     []types.NonDescriptiveAnchor
	nonDescSeen map[string]bool
	cloud       map[string]*types.AnchorCloudEntry
	health      map[string]*inboundAnchorStats
	pages       int
}

// NewAnchorStream returns an empty stream.
func NewAnchorStream() *AnchorStream {
	return &AnchorStream{nonDescSeen: map[string]bool{}, cloud: map[string]*types.AnchorCloudEntry{}, health: map[string]*inboundAnchorStats{}}
}

// AddPage records the anchors of one page. Pages must come in the order the
// report will see them; the caller may drop p.Anchors afterwards.
func (s *AnchorStream) AddPage(p *types.PageData) {
	if len(p.Anchors) == 0 {
		return
	}
	s.pages++
	// Same rule as the reporter's main loop; its list is cut at 50 after.
	if !p.RobotsBlocked {
		for _, anchor := range p.Anchors {
			if len(s.nonDesc) >= 50 {
				break
			}
			text := strings.ToLower(strings.TrimSpace(anchor.Text))
			if isNonDescriptive(text) {
				key := anchor.Href + "|" + text + "|" + p.URL
				if !s.nonDescSeen[key] {
					s.nonDescSeen[key] = true
					s.nonDesc = append(s.nonDesc, types.NonDescriptiveAnchor{URL: anchor.Href, Text: text, FoundOn: p.URL})
				}
			}
		}
	}
	for _, an := range p.Anchors { // as BuildAnchorCloud
		text := strings.TrimSpace(strings.ToLower(an.Text))
		if n := utf8.RuneCountInString(text); n < 2 || n > maxAnchorTextRunes {
			continue
		}
		e := s.cloud[text]
		if e == nil {
			e = &types.AnchorCloudEntry{Text: text}
			s.cloud[text] = e
		}
		e.Count++
		e.IsInternal = e.IsInternal || an.IsInternal
		e.IsNonDescriptive = e.IsNonDescriptive || an.IsNonDescriptive
	}
	if p.StatusCode == 200 {
		s.addInbound(p.Anchors)
	}
}

var nakedURLPattern = regexp.MustCompile(`^https?://`)

// inboundAnchorStats is what buildAnchorHealth reads of a target's inbound
// internal anchors: texts maps each non-empty lowercased text to the number
// of its anchors that are meaningful (its textCounts; the naked-URL test
// there runs on the untrimmed text).
type inboundAnchorStats struct {
	total, generic, naked int
	texts                 map[string]int
}

func (s *AnchorStream) addInbound(anchors []types.AnchorData) {
	for _, a := range anchors {
		if !a.IsInternal {
			continue
		}
		target := normalizeTrailingSlash(a.Href)
		t := s.health[target]
		if t == nil {
			t = &inboundAnchorStats{}
			s.health[target] = t
		}
		t.total++
		text := strings.TrimSpace(a.Text)
		lower := strings.ToLower(text)
		if isNonDescriptive(lower) || text == "" {
			t.generic++
		}
		if nakedURLPattern.MatchString(text) {
			t.naked++
		}
		if text == "" {
			continue
		}
		if t.texts == nil {
			t.texts = make(map[string]int, 1)
		}
		n := t.texts[lower]
		if !isNonDescriptive(lower) && !nakedURLPattern.MatchString(a.Text) {
			n++
		}
		t.texts[lower] = n
	}
}

// applyAnchorHealth sets AnchorHealth as buildAnchorHealth does.
func (s *AnchorStream) applyAnchorHealth(pages []*types.PageData) {
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		t := s.health[normalizeTrailingSlash(p.URL)]
		if t == nil || t.total == 0 {
			continue
		}
		ah := &types.AnchorHealthData{GenericAnchors: t.generic, NakedURLAnchors: t.naked}
		ah.InboundAnchorDiversity = float64(len(t.texts)) / float64(t.total)
		meaningful := t.total - t.generic - t.naked
		for _, count := range t.texts {
			if meaningful > 3 && float64(count)/float64(meaningful) > 0.5 {
				ah.OverOptimizedAnchors++
			}
		}
		p.AnchorHealth = ah
	}
}

// anchorCloud is BuildAnchorCloud's result.
func (s *AnchorStream) anchorCloud() *types.AnchorCloudStats {
	entries := make([]types.AnchorCloudEntry, 0, len(s.cloud))
	for _, e := range s.cloud {
		entries = append(entries, *e)
	}
	return finishAnchorCloud(entries, len(s.cloud))
}

// Pages returns how many pages with anchors were added.
func (s *AnchorStream) Pages() int {
	if s == nil {
		return 0
	}
	return s.pages
}
