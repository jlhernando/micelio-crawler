// Package analysis: topicality.go computes T* (Topicality) alignment signals.
//
// T* is one of three Ascorer pillars (T*, Q*, P*). It measures query-dependent
// relevance via three sub-signals known as the ABC framework:
//   - A (Anchors): what the web says about the page via inbound link text
//   - B (Body): what the page says about itself (title, headings, content)
//   - C (Clicks): what users say via NavBoost (not available at crawl time)
//
// Source: DOJ (HJ Kim PXR0356, Nayak PXR0357), Leak (Ascorer T* pillar).
// Confidence: CONFIRMED for structure; STRONG_PROXY for composite weights.
package analysis

import (
	"math"
	"strings"
	"unicode"

	"github.com/micelio/micelio/internal/types"
)

// ComputeTopicality calculates T*-Body proxy scores for a page at extraction time.
// This covers the "B" (Body) sub-signal of T*. The "A" (Anchors) sub-signal
// requires graph-level data and is computed post-crawl via EnrichTopicalityAnchors.
// Source: DOJ ABC framework (Body sub-signal), Leak (titlematchScore).
func ComputeTopicality(title string, h1 []string, headings []string, bodyText string, wordCount int) *types.TopicalityData {
	return computeTopicality(title, h1, headings, bodyText, wordCount, nil)
}

// computeTopicality is the internal implementation supporting optional anchor texts.
func computeTopicality(title string, h1 []string, headings []string, bodyText string, wordCount int, inboundAnchorTexts []string) *types.TopicalityData {
	if wordCount < 30 || title == "" {
		return nil
	}
	t := &types.TopicalityData{}
	titleTerms := tokenizeTerms(title)
	bodyTerms := tokenizeTermsToSet(bodyText)

	// T*-Body: Title-body overlap — fraction of title terms found in body.
	// Source: Leak (titlematchScore). Confidence: CONFIRMED.
	if len(titleTerms) > 0 {
		matches := 0
		for _, term := range titleTerms {
			if bodyTerms[term] {
				matches++
			}
		}
		t.TitleBodyOverlap = math.Round(float64(matches)/float64(len(titleTerms))*1000) / 1000
	}

	// T*-Body: Title-H1 alignment — Jaccard similarity.
	// Source: SEO best practice + Leak (titlematchScore). Confidence: STRONG_PROXY.
	if len(h1) > 0 {
		h1Terms := tokenizeTerms(h1[0])
		t.TitleH1Alignment = jaccard(titleTerms, h1Terms)
	}

	// T*-Body: Heading-body coverage — fraction of heading terms in body.
	// Source: General content analysis. Confidence: HEURISTIC.
	if len(headings) > 0 {
		allHeadingTerms := make(map[string]bool)
		for _, h := range headings {
			for _, t := range tokenizeTerms(h) {
				allHeadingTerms[t] = true
			}
		}
		if len(allHeadingTerms) > 0 {
			matches := 0
			for term := range allHeadingTerms {
				if bodyTerms[term] {
					matches++
				}
			}
			t.HeadingBodyCoverage = math.Round(float64(matches)/float64(len(allHeadingTerms))*1000) / 1000
		}
	}

	// T*-Anchors: How well inbound anchor text aligns with the page title.
	// This is the "A" in ABC — what the web says about the page.
	// Source: DOJ (T* Anchors sub-signal), Patent US7260573 (personalized anchor text).
	// Confidence: CONFIRMED (DOJ testimony), STRONG_PROXY (implementation).
	if len(inboundAnchorTexts) > 0 && len(titleTerms) > 0 {
		titleSet := make(map[string]bool, len(titleTerms))
		for _, term := range titleTerms {
			titleSet[term] = true
		}
		totalRelevance := 0.0
		counted := 0
		for _, anchorText := range inboundAnchorTexts {
			anchorTerms := tokenizeTerms(anchorText)
			if len(anchorTerms) == 0 {
				continue
			}
			matches := 0
			for _, term := range anchorTerms {
				if titleSet[term] {
					matches++
				}
			}
			totalRelevance += float64(matches) / float64(len(anchorTerms))
			counted++
		}
		if counted > 0 {
			t.AnchorTitleRelevance = math.Round((totalRelevance/float64(counted))*1000) / 1000
		}
	}

	// Topical consistency: weighted composite of A + B sub-signals.
	// Weights: Body signals 70% (title-body 30%, title-H1 20%, heading-body 20%),
	// Anchors 30%. Clicks (C) not available at crawl time.
	// Confidence: HEURISTIC (weights are approximations, not Google's actual values).
	bodyScore := 0.30*t.TitleBodyOverlap + 0.20*t.TitleH1Alignment + 0.20*t.HeadingBodyCoverage
	anchorScore := 0.30 * t.AnchorTitleRelevance
	t.TopicalConsistency = math.Round((bodyScore+anchorScore)*1000) / 1000

	return t
}


var topicalityStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true, "has": true,
	"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
	"could": true, "should": true, "may": true, "might": true, "can": true,
	"this": true, "that": true, "these": true, "those": true, "it": true,
	"its": true, "not": true, "no": true, "so": true, "if": true, "as": true,
}

func tokenizeTerms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var result []string
	for _, w := range words {
		if len(w) > 1 && !topicalityStopwords[w] {
			result = append(result, w)
		}
	}
	return result
}

func tokenizeTermsToSet(text string) map[string]bool {
	// For large body text, only process first ~5000 runes for performance
	if len(text) > 5000 {
		r := []rune(text)
		if len(r) > 5000 {
			r = r[:5000]
		}
		text = string(r)
	}
	set := make(map[string]bool)
	for _, w := range tokenizeTerms(text) {
		set[w] = true
	}
	return set
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	setA := make(map[string]bool, len(a))
	for _, w := range a {
		setA[w] = true
	}
	setB := make(map[string]bool, len(b))
	for _, w := range b {
		setB[w] = true
	}
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return math.Round(float64(intersection)/float64(union)*1000) / 1000
}

// EnrichTopicalityAnchors adds the T*-Anchors sub-signal to existing topicality data.
// Called post-crawl when the full link graph is available. For each page, it computes
// how well inbound anchor text from other pages aligns with this page's title.
//
// This is the "A" in ABC (Anchors, Body, Clicks):
//   - "What the web says about the page" (DOJ, Nayak PXR0357)
//   - Patent US7260573: personalized anchor text ranking
//
// Source: DOJ (T* Anchors sub-signal). Confidence: CONFIRMED (structure), STRONG_PROXY (weights).
func EnrichTopicalityAnchors(pages []*types.PageData, graph *AdjacencyGraph) {
	if graph == nil || graph.N == 0 {
		return
	}

	// Build URL -> page index
	urlToIdx := make(map[string]int, len(pages))
	for i, p := range pages {
		urlToIdx[p.URL] = i
	}

	// Build reverse adjacency: for each page, collect anchor texts pointing to it
	inboundAnchors := make(map[string][]string, len(pages))
	for _, p := range pages {
		for _, a := range p.Anchors {
			if a.IsInternal && a.Text != "" && !a.IsNonDescriptive {
				inboundAnchors[a.Href] = append(inboundAnchors[a.Href], a.Text)
			}
		}
	}

	// Compute anchor-title relevance for each page
	for _, p := range pages {
		anchors := inboundAnchors[p.URL]
		if len(anchors) == 0 || p.Topicality == nil || p.Title == nil {
			continue
		}

		titleTerms := tokenizeTerms(p.Title.Text)
		if len(titleTerms) == 0 {
			continue
		}
		titleSet := make(map[string]bool, len(titleTerms))
		for _, term := range titleTerms {
			titleSet[term] = true
		}

		totalRelevance := 0.0
		counted := 0
		for _, anchorText := range anchors {
			anchorTerms := tokenizeTerms(anchorText)
			if len(anchorTerms) == 0 {
				continue
			}
			matches := 0
			for _, term := range anchorTerms {
				if titleSet[term] {
					matches++
				}
			}
			totalRelevance += float64(matches) / float64(len(anchorTerms))
			counted++
		}
		if counted > 0 {
			p.Topicality.AnchorTitleRelevance = math.Round((totalRelevance/float64(counted))*1000) / 1000
			// Recompute composite: Body 70% + Anchors 30%
			bodyScore := 0.30*p.Topicality.TitleBodyOverlap + 0.20*p.Topicality.TitleH1Alignment + 0.20*p.Topicality.HeadingBodyCoverage
			anchorScore := 0.30 * p.Topicality.AnchorTitleRelevance
			p.Topicality.TopicalConsistency = math.Round((bodyScore+anchorScore)*1000) / 1000
		}
	}
}
