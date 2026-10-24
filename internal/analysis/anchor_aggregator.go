package analysis

import (
	"hash/maphash"
	"math"
	"math/bits"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/micelio/micelio/internal/types"
)

// Anchor aggregator limits. They keep memory bounded on very large crawls
// (700K+ pages) while staying exact for typical sites.
const (
	// maxAnchorTexts caps distinct anchor texts kept for the Anchor Cloud and
	// for topicality anchor text lookups. When reached, rare texts are pruned.
	maxAnchorTexts = 200_000
	// maxAnchorTextRunes is the longest anchor text kept in the text table.
	maxAnchorTextRunes = 80
	// maxAnchorTargets caps distinct link targets tracked for anchor health.
	maxAnchorTargets = 1_000_000
	// maxTextsPerTarget caps distinct inbound texts tracked per target. Extra
	// distinct texts are still counted (overflow) for diversity.
	maxTextsPerTarget = 16
	// maxOverflowHashes caps the distinct untracked texts remembered per target
	// so each is counted once. Only hub pages (many distinct texts) use it.
	maxOverflowHashes = 256
	// maxNonDescriptiveAnchors mirrors the reporter cap.
	maxNonDescriptiveAnchors = 50
	// anchorCloudLimit mirrors the server anchor-stats response cap.
	anchorCloudLimit = 300

	hllPrecision = 12
	hllRegisters = 1 << hllPrecision
)

type anchorTextEntry struct {
	text        string
	count       uint32
	internal    bool
	nonDescript bool
}

type targetText struct {
	hash       uint64
	count      uint32
	meaningful bool // not empty, not generic, not a naked URL
	topical    bool // usable for topicality anchor (extractor non-descriptive flag false)
}

type anchorTarget struct {
	texts    []targetText
	total    uint32
	generic  uint32
	naked    uint32
	// overflowHashes holds distinct texts beyond maxTextsPerTarget (counted once
	// each); overflowBeyond counts anchors past both caps, assumed new texts.
	overflowHashes []uint64
	overflowBeyond uint32
}

// AnchorAggregator collects anchor statistics during a crawl, page by page,
// so anchor-based reports work after page Anchors are dropped before storage.
// It is safe for concurrent use.
type AnchorAggregator struct {
	seed        maphash.Seed
	texts       map[uint64]*anchorTextEntry
	targets     map[uint64]*anchorTarget
	nonDescSeen map[string]struct{}
	nonDesc     []types.NonDescriptiveAnchor
	hll         [hllRegisters]uint8
	pages       int
	pruneFloor  uint32
	pruned      bool
	dropTargets int
	mu          sync.Mutex
}

// NewAnchorAggregator returns an empty aggregator.
func NewAnchorAggregator() *AnchorAggregator {
	return &AnchorAggregator{
		seed:        maphash.MakeSeed(),
		texts:       make(map[uint64]*anchorTextEntry, 1024),
		targets:     make(map[uint64]*anchorTarget, 1024),
		nonDescSeen: make(map[string]struct{}, maxNonDescriptiveAnchors),
	}
}

type preparedAnchor struct {
	lower       string
	textHash    uint64
	targetHash  uint64
	href        string
	internal    bool
	nonDescExtr bool // extractor flag (topicality anchor filter)
	generic     bool // reporter non-descriptive or empty
	naked       bool
	cloud       bool // eligible for the text table
	target      bool // internal anchor from a 200 page: feeds anchor health
}

func isNakedURLText(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// AddPage records the anchors found on one crawled page. Call it before the
// page's Anchors are dropped. Text normalisation and hashing run outside the lock.
func (a *AnchorAggregator) AddPage(pageURL string, statusCode int, anchors []types.AnchorData) {
	if a == nil || len(anchors) == 0 {
		return
	}
	prepared := make([]preparedAnchor, len(anchors))
	for i := range anchors {
		an := &anchors[i]
		trimmed := strings.TrimSpace(an.Text)
		lower := strings.ToLower(trimmed)
		runes := utf8.RuneCountInString(lower)
		p := preparedAnchor{
			lower:       lower,
			href:        an.Href,
			internal:    an.IsInternal,
			nonDescExtr: an.IsNonDescriptive,
			generic:     lower == "" || isNonDescriptive(lower),
			naked:       isNakedURLText(trimmed),
			cloud:       runes >= 1 && runes <= maxAnchorTextRunes,
		}
		if lower != "" {
			p.textHash = maphash.String(a.seed, lower)
		}
		if an.IsInternal && statusCode == 200 {
			p.target = true
			p.targetHash = maphash.String(a.seed, normalizeTrailingSlash(an.Href))
		}
		prepared[i] = p
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pages++
	for i := range prepared {
		p := &prepared[i]
		if p.lower != "" {
			a.addHLL(p.textHash)
			if p.cloud {
				a.addText(p)
			}
		}
		if isNonDescriptive(p.lower) && len(a.nonDesc) < maxNonDescriptiveAnchors {
			key := p.href + "|" + p.lower + "|" + pageURL
			if _, ok := a.nonDescSeen[key]; !ok {
				a.nonDescSeen[key] = struct{}{}
				a.nonDesc = append(a.nonDesc, types.NonDescriptiveAnchor{
					URL:     strings.Clone(p.href),
					Text:    strings.Clone(p.lower),
					FoundOn: strings.Clone(pageURL),
				})
			}
		}
		if p.target {
			a.addTarget(p)
		}
	}
}

func (a *AnchorAggregator) addText(p *preparedAnchor) {
	e, ok := a.texts[p.textHash]
	if !ok {
		if len(a.texts) >= maxAnchorTexts {
			a.pruneTexts()
		}
		e = &anchorTextEntry{text: strings.Clone(p.lower)}
		a.texts[p.textHash] = e
	}
	e.count++
	if p.internal {
		e.internal = true
	}
	if p.nonDescExtr {
		e.nonDescript = true
	}
}

// pruneTexts drops the rarest texts until the table is at most half full.
func (a *AnchorAggregator) pruneTexts() {
	a.pruned = true
	for len(a.texts) > maxAnchorTexts/2 {
		a.pruneFloor++
		for h, e := range a.texts {
			if e.count <= a.pruneFloor {
				delete(a.texts, h)
			}
		}
	}
}

func (a *AnchorAggregator) addTarget(p *preparedAnchor) {
	t, ok := a.targets[p.targetHash]
	if !ok {
		if len(a.targets) >= maxAnchorTargets {
			a.dropTargets++
			return
		}
		t = &anchorTarget{}
		a.targets[p.targetHash] = t
	}
	t.total++
	if p.generic {
		t.generic++
	}
	if p.naked {
		t.naked++
	}
	if p.lower == "" {
		return
	}
	for i := range t.texts {
		if t.texts[i].hash == p.textHash {
			t.texts[i].count++
			return
		}
	}
	if len(t.texts) >= maxTextsPerTarget {
		for _, h := range t.overflowHashes {
			if h == p.textHash {
				return
			}
		}
		if len(t.overflowHashes) < maxOverflowHashes {
			t.overflowHashes = append(t.overflowHashes, p.textHash)
		} else {
			t.overflowBeyond++
		}
		return
	}
	t.texts = append(t.texts, targetText{
		hash:       p.textHash,
		count:      1,
		meaningful: !p.generic && !p.naked,
		topical:    !p.nonDescExtr,
	})
}

func (a *AnchorAggregator) addHLL(h uint64) {
	idx := h >> (64 - hllPrecision)
	rank := uint8(bits.LeadingZeros64(h<<hllPrecision|1<<(hllPrecision-1)) + 1)
	if rank > a.hll[idx] {
		a.hll[idx] = rank
	}
}

func (a *AnchorAggregator) estimateUnique() int {
	m := float64(hllRegisters)
	sum := 0.0
	zeros := 0
	for _, r := range a.hll {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	alpha := 0.7213 / (1 + 1.079/m)
	est := alpha * m * m / sum
	if est <= 2.5*m && zeros > 0 {
		est = m * math.Log(m/float64(zeros))
	}
	return int(est + 0.5)
}

// Pages returns how many pages were fed into the aggregator.
func (a *AnchorAggregator) Pages() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pages
}

// AnchorCloud returns the most used anchor texts (same shape as the
// /anchor-stats API). Returns nil when nothing was collected.
func (a *AnchorAggregator) AnchorCloud() *types.AnchorCloudStats {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pages == 0 {
		return nil
	}
	entries := make([]types.AnchorCloudEntry, 0, len(a.texts))
	for _, e := range a.texts {
		if utf8.RuneCountInString(e.text) < 2 {
			continue
		}
		entries = append(entries, types.AnchorCloudEntry{
			Text:             e.text,
			Count:            int(e.count),
			IsInternal:       e.internal,
			IsNonDescriptive: e.nonDescript,
		})
	}
	total := len(entries)
	if a.pruned {
		// Text table was pruned; fall back to the HyperLogLog estimate.
		if est := a.estimateUnique(); est > total {
			total = est
		}
	}
	return finishAnchorCloud(entries, total)
}

func finishAnchorCloud(entries []types.AnchorCloudEntry, totalUnique int) *types.AnchorCloudStats {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Text < entries[j].Text
	})
	if len(entries) > anchorCloudLimit {
		entries = entries[:anchorCloudLimit]
	}
	if entries == nil {
		entries = []types.AnchorCloudEntry{}
	}
	return &types.AnchorCloudStats{Anchors: entries, TotalUnique: totalUnique}
}

// BuildAnchorCloud computes Anchor Cloud stats from in-memory page Anchors.
func BuildAnchorCloud(pages []*types.PageData) *types.AnchorCloudStats {
	type agg struct {
		count       int
		internal    bool
		nonDescript bool
	}
	m := make(map[string]*agg, 4096)
	for _, p := range pages {
		for _, an := range p.Anchors {
			text := strings.TrimSpace(strings.ToLower(an.Text))
			n := utf8.RuneCountInString(text)
			if n < 2 || n > maxAnchorTextRunes {
				continue
			}
			e := m[text]
			if e == nil {
				e = &agg{}
				m[text] = e
			}
			e.count++
			if an.IsInternal {
				e.internal = true
			}
			if an.IsNonDescriptive {
				e.nonDescript = true
			}
		}
	}
	entries := make([]types.AnchorCloudEntry, 0, len(m))
	for text, e := range m {
		entries = append(entries, types.AnchorCloudEntry{
			Text: text, Count: e.count, IsInternal: e.internal, IsNonDescriptive: e.nonDescript,
		})
	}
	return finishAnchorCloud(entries, len(m))
}

// NonDescriptiveAnchors returns up to 50 unique non-descriptive anchors.
func (a *AnchorAggregator) NonDescriptiveAnchors() []types.NonDescriptiveAnchor {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.nonDesc) == 0 {
		return nil
	}
	out := make([]types.NonDescriptiveAnchor, len(a.nonDesc))
	copy(out, a.nonDesc)
	return out
}

// ApplyAnchorHealth sets per-page AnchorHealth from aggregated inbound anchors.
// Equivalent to buildAnchorHealth for pages without Anchors.
func (a *AnchorAggregator) ApplyAnchorHealth(pages []*types.PageData) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		t := a.targets[maphash.String(a.seed, normalizeTrailingSlash(p.URL))]
		if t == nil || t.total == 0 {
			continue
		}
		ah := &types.AnchorHealthData{
			GenericAnchors:  int(t.generic),
			NakedURLAnchors: int(t.naked),
		}
		unique := len(t.texts) + len(t.overflowHashes) + int(t.overflowBeyond)
		ah.InboundAnchorDiversity = float64(unique) / float64(t.total)
		if unique > 0 {
			meaningful := int(t.total) - ah.GenericAnchors - ah.NakedURLAnchors
			for _, tt := range t.texts {
				if tt.meaningful && meaningful > 3 && float64(tt.count)/float64(meaningful) > 0.5 {
					ah.OverOptimizedAnchors++
				}
			}
		}
		p.AnchorHealth = ah
	}
}

// inboundTopicalTexts returns tracked inbound anchor texts (with counts) usable
// for topicality anchor relevance. Texts pruned from the text table are skipped.
func (a *AnchorAggregator) inboundTopicalTexts(pageURL string, fn func(text string, count int)) {
	t := a.targets[maphash.String(a.seed, normalizeTrailingSlash(pageURL))]
	if t == nil {
		return
	}
	for _, tt := range t.texts {
		if !tt.topical {
			continue
		}
		if e := a.texts[tt.hash]; e != nil {
			fn(e.text, int(tt.count))
		}
	}
}
