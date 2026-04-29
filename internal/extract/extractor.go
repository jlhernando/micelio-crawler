// Package extract implements HTML parsing and SEO data extraction.
package extract

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// ExtractionOptions configures what data to extract.
type ExtractionOptions struct {
	Headers           map[string]string
	PageURL           string
	FinalURL          string
	CustomExtractions []types.CustomExtractionRule
	CustomSearches    []types.CustomSearchRule
	StatusCode        int
	Depth             int
}

// ExtractPageData parses HTML and produces a PageData struct.
func ExtractPageData(html string, opts ExtractionOptions) types.PageData {
	baseURL := opts.FinalURL
	if baseURL == "" {
		baseURL = opts.PageURL
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return types.PageData{
			URL:        opts.PageURL,
			FinalURL:   opts.FinalURL,
			StatusCode: opts.StatusCode,
			Depth:      opts.Depth,
			Error:      fmt.Sprintf("HTML parse error: %v", err),
		}
	}

	page := types.PageData{
		URL:        opts.PageURL,
		FinalURL:   opts.FinalURL,
		StatusCode: opts.StatusCode,
		Depth:      opts.Depth,
	}

	// Title
	if titleEl := doc.Find("title").First(); titleEl.Length() > 0 {
		text := strings.TrimSpace(titleEl.Text())
		page.Title = &types.TextLength{Text: text, Length: utf8.RuneCountInString(text)}
	}

	// Meta description
	if desc, exists := doc.Find(`meta[name="description"]`).Attr("content"); exists {
		text := strings.TrimSpace(desc)
		page.MetaDescription = &types.TextLength{Text: text, Length: utf8.RuneCountInString(text)}
	}

	// Canonical
	page.CanonicalCount = doc.Find(`link[rel="canonical"]`).Length()
	if href, exists := doc.Find(`link[rel="canonical"]`).First().Attr("href"); exists {
		raw := strings.TrimSpace(href)
		page.CanonicalRaw = &raw
		resolved := resolveRef(baseURL, raw)
		page.Canonical = &resolved
	}

	// Meta robots
	if content, exists := doc.Find(`meta[name="robots"]`).Attr("content"); exists {
		page.MetaRobots = &content
	}

	// X-Robots-Tag from headers
	if xRobots, ok := opts.Headers["x-robots-tag"]; ok && xRobots != "" {
		page.XRobotsTag = &xRobots
	}

	// Headings
	page.Headings = extractHeadings(doc)

	// Links
	page.InternalLinks, page.ExternalLinks, page.Anchors = extractLinks(doc, baseURL)

	// Images
	page.Images = extractImages(doc, baseURL)

	// Hreflang
	page.Hreflang = extractHreflang(doc)

	// Structured data
	page.StructuredData = extractStructuredData(doc)

	// Open Graph
	page.OpenGraph = extractMetaProperties(doc, "og:")

	// Twitter Card
	page.TwitterCard = extractMetaProperties(doc, "twitter:")

	// Body text + word count
	bodyText := extractBodyText(doc)
	page.WordCount = countWords(bodyText)
	page.BodyText = bodyText

	// Content hash (MD5 of body text)
	page.ContentHash = fmt.Sprintf("%x", md5.Sum([]byte(bodyText)))

	// Text-to-code ratio
	htmlLen := len(html)
	textLen := len(bodyText)
	if htmlLen > 0 {
		page.TextToCodeRatio = math.Round(float64(textLen)/float64(htmlLen)*10000) / 100
	}

	// Security
	page.Security = extractSecurity(baseURL, doc, opts.Headers)

	// Indexability
	page.Indexability = determineIndexability(opts.StatusCode, page.MetaRobots, page.XRobotsTag, page.Canonical, opts.FinalURL)

	// URL issues
	page.URLIssues = detectURLIssues(opts.FinalURL)

	// Soft 404
	if opts.StatusCode == 200 {
		page.IsSoft404 = detectSoft404(doc, bodyText)
	}

	// Readability (Flesch-Kincaid)
	if page.WordCount >= 30 {
		page.Readability = computeReadability(bodyText)
	}

	// Freshness signals (Patent US8924379, DOJ)
	page.Freshness = extractFreshness(doc, page.OpenGraph, page.StructuredData, opts.Headers, time.Now())

	// Content richness (DOJ contentEffort, Patent US8682892)
	page.ContentRichness = extractContentRichness(doc, page.Headings, page.WordCount)

	// Listing signals (HTML structure patterns for listing/grid/results pages)
	page.ListingSignals = extractListingSignals(doc)

	// Ad detail signals (classifieds/marketplace ad detail vs e-commerce product)
	page.AdDetailSignals = extractAdDetailSignals(doc)

	// E-E-A-T signals (DOJ, Patent US9697259)
	page.EEAT = extractEEAT(doc, page.StructuredData, page.Anchors, page.ExternalLinks, baseURL)

	// Passage readiness (Patent US20160078102)
	page.PassageReadiness = extractPassageReadiness(doc, page.StructuredData, page.Headings, bodyText, page.WordCount)

	// AI Overview readiness (DOJ FastSearch, Leak SnippetBrain)
	page.AIReadiness = extractAIReadiness(doc, page.StructuredData, page.Headings, bodyText, page.WordCount)

	// Consensus readiness (Candour exploit: passages aligned with general consensus)
	page.ConsensusReadiness = extractConsensusReadiness(doc, bodyText, page.WordCount)

	// Topicality alignment (DOJ T* body, titlematchScore, Leak Ascorer)
	if page.Title != nil && page.Title.Text != "" {
		allHeadings := make([]string, 0, len(page.Headings.H2)+len(page.Headings.H3))
		allHeadings = append(append(allHeadings, page.Headings.H2...), page.Headings.H3...)
		page.Topicality = analysis.ComputeTopicality(page.Title.Text, page.Headings.H1, allHeadings, bodyText, page.WordCount)
	}

	// Googlebot truncation risk (only runs for pages > 2MB, zero overhead otherwise)
	if truncated := GooglebotTruncationCheck(html); len(truncated) > 0 {
		page.TruncatedElements = truncated
	}

	// Custom extractions
	if len(opts.CustomExtractions) > 0 {
		page.CustomExtractions = runCustomExtractions(doc, opts.CustomExtractions)
	}

	// Custom searches
	if len(opts.CustomSearches) > 0 {
		page.CustomSearches = runCustomSearches(html, opts.CustomSearches)
	}

	return page
}

// --- Headings ---

func extractHeadings(doc *goquery.Document) types.HeadingData {
	h := types.HeadingData{}
	for level := 1; level <= 6; level++ {
		sel := fmt.Sprintf("h%d", level)
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text == "" {
				return
			}
			switch level {
			case 1:
				h.H1 = append(h.H1, text)
			case 2:
				h.H2 = append(h.H2, text)
			case 3:
				h.H3 = append(h.H3, text)
			case 4:
				h.H4 = append(h.H4, text)
			case 5:
				h.H5 = append(h.H5, text)
			case 6:
				h.H6 = append(h.H6, text)
			}
		})
	}
	return h
}

// --- Links ---

func extractLinks(doc *goquery.Document, baseURL string) (internal []string, external []string, anchors []types.AnchorData) {
	baseP, _ := url.Parse(baseURL)
	seedHost := ""
	if baseP != nil {
		seedHost = baseP.Hostname()
	}

	seen := make(map[string]bool)

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
			return
		}

		resolved := resolveRef(baseURL, href)
		resolvedP, err := url.Parse(resolved)
		if err != nil {
			return
		}

		text := strings.TrimSpace(s.Text())
		relAttr, _ := s.Attr("rel")
		isInternal := resolvedP.Hostname() == seedHost
		position := detectLinkPosition(s)

		anchor := types.AnchorData{
			Href:             resolved,
			Text:             text,
			IsInternal:       isInternal,
			IsNonDescriptive: isNonDescriptiveAnchor(text),
			Position:         position,
		}
		if relAttr != "" {
			anchor.Rel = &relAttr
		}
		anchors = append(anchors, anchor)

		// Deduplicated link lists
		if !seen[resolved] {
			seen[resolved] = true
			if isInternal {
				internal = append(internal, resolved)
			} else {
				external = append(external, resolved)
			}
		}
	})

	return
}

var nonDescriptivePatterns = regexp.MustCompile(`(?i)^(click here|here|read more|more|learn more|link|this|go|see more|details|continue|next|previous|download|visit|open|view|source|full article|full story)$`)

func isNonDescriptiveAnchor(text string) bool {
	if text == "" {
		return true
	}
	return nonDescriptivePatterns.MatchString(strings.TrimSpace(text))
}

func detectLinkPosition(s *goquery.Selection) types.LinkPosition {
	for p := s.Parent(); p.Length() > 0; p = p.Parent() {
		tag := goquery.NodeName(p)
		if tag == "nav" {
			return types.LinkPosNavigation
		}
		if tag == "footer" {
			return types.LinkPosFooter
		}
		if tag == "header" {
			return types.LinkPosHeader
		}
		if tag == "aside" {
			return types.LinkPosSidebar
		}
		if tag == "main" || tag == "article" {
			return types.LinkPosContent
		}
		// Check role attributes
		if role, exists := p.Attr("role"); exists {
			switch role {
			case "navigation":
				return types.LinkPosNavigation
			case "contentinfo":
				return types.LinkPosFooter
			case "banner":
				return types.LinkPosHeader
			case "complementary":
				return types.LinkPosSidebar
			case "main":
				return types.LinkPosContent
			}
		}
	}
	return types.LinkPosOther
}

// --- Images ---

func extractImages(doc *goquery.Document, baseURL string) []types.ImageData {
	var images []types.ImageData

	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src == "" {
			// Try data-src for lazy-loaded images
			src, _ = s.Attr("data-src")
		}
		if src != "" {
			src = resolveRef(baseURL, src)
		}

		_, hasAlt := s.Attr("alt")
		altVal, _ := s.Attr("alt")
		altLen := len(altVal)

		width, hasWidth := s.Attr("width")
		height, hasHeight := s.Attr("height")

		img := types.ImageData{
			Src:             src,
			HasAltAttribute: hasAlt,
			MissingAlt:      !hasAlt,
			AltLength:       altLen,
			AltTooLong:      altLen > 125,
			MissingWidth:    !hasWidth,
			MissingHeight:   !hasHeight,
		}
		if hasAlt {
			img.Alt = &altVal
		}
		if hasWidth {
			img.Width = &width
		}
		if hasHeight {
			img.Height = &height
		}

		images = append(images, img)
	})

	return images
}

// --- Hreflang ---

func extractHreflang(doc *goquery.Document) []types.HreflangEntry {
	var entries []types.HreflangEntry
	doc.Find(`link[rel="alternate"][hreflang]`).Each(func(_ int, s *goquery.Selection) {
		lang, _ := s.Attr("hreflang")
		href, _ := s.Attr("href")
		if lang != "" && href != "" {
			entries = append(entries, types.HreflangEntry{Lang: lang, Href: href})
		}
	})
	return entries
}

// --- Structured Data ---

const jsonldMaxRaw = 5120 // 5KB — sufficient for schema type identification and key properties

func extractStructuredData(doc *goquery.Document) []types.StructuredDataEntry {
	var entries []types.StructuredDataEntry

	// JSON-LD
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return
		}
		// Expand @graph arrays into individual entries so downstream
		// consumers (AIReadiness, EEAT, schema stats) see each type
		// with its own item-level Raw JSON.
		if items := extractJSONLDGraphItems(raw); len(items) > 0 {
			entries = append(entries, items...)
			return
		}
		sdType := extractJSONLDType(raw)
		if len(raw) > jsonldMaxRaw {
			raw = raw[:jsonldMaxRaw]
		}
		entries = append(entries, types.StructuredDataEntry{
			Type:   sdType,
			Format: types.FormatJSONLD,
			Raw:    raw,
		})
	})

	// Microdata — only top-level itemscope elements (skip nested ones)
	doc.Find("[itemscope]").Each(func(_ int, s *goquery.Selection) {
		// Skip nested itemscope elements
		if s.ParentsFiltered("[itemscope]").Length() > 0 {
			return
		}
		itemtype, _ := s.Attr("itemtype")
		if itemtype == "" {
			return
		}
		// Extract schema.org type from URL
		sdType := itemtype
		if idx := strings.LastIndex(itemtype, "/"); idx >= 0 {
			sdType = itemtype[idx+1:]
		}
		// Store only the itemtype URL, not full HTML (matches TS behavior)
		entries = append(entries, types.StructuredDataEntry{
			Type:   sdType,
			Format: types.FormatMicrodata,
			Raw:    itemtype,
		})
	})

	return entries
}

// extractJSONLDType extracts @type from JSON-LD using proper JSON parsing.
func extractJSONLDType(raw string) string {
	var parsed interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "Unknown"
	}
	var obj map[string]interface{}
	switch v := parsed.(type) {
	case map[string]interface{}:
		obj = v
	case []interface{}:
		if len(v) > 0 {
			if m, ok := v[0].(map[string]interface{}); ok {
				obj = m
			}
		}
	}
	if obj == nil {
		return "Unknown"
	}
	t, ok := obj["@type"]
	if !ok {
		return "Unknown"
	}
	switch v := t.(type) {
	case string:
		return v
	case []interface{}:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return "Unknown"
}

// extractJSONLDGraphItems expands a @graph JSON-LD into individual StructuredDataEntry
// items, each with its own type and item-level Raw JSON. For entries that use @id
// author references, the referenced Person/Organization is inlined so downstream
// consumers (EEAT) can find the author name.
// Returns nil if the JSON-LD does not contain a @graph key.
func extractJSONLDGraphItems(raw string) []types.StructuredDataEntry {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	graphRaw, ok := obj["@graph"]
	if !ok {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(graphRaw, &items); err != nil || len(items) == 0 {
		return nil
	}
	// Build @id index for reference resolution.
	idIndex := make(map[string]map[string]json.RawMessage)
	for _, item := range items {
		if idRaw, ok := item["@id"]; ok {
			var id string
			if json.Unmarshal(idRaw, &id) == nil && id != "" {
				idIndex[id] = item
			}
		}
	}
	var entries []types.StructuredDataEntry
	for _, item := range items {
		sdType := jsonldItemType(item)
		if sdType == "" {
			continue
		}
		// Resolve @id author references inline.
		if authorRaw, ok := item["author"]; ok {
			var ref map[string]json.RawMessage
			if json.Unmarshal(authorRaw, &ref) == nil {
				if _, hasName := ref["name"]; !hasName {
					if idRaw, hasID := ref["@id"]; hasID {
						var id string
						if json.Unmarshal(idRaw, &id) == nil {
							if resolved, found := idIndex[id]; found {
								if b, err := json.Marshal(resolved); err == nil {
									item["author"] = json.RawMessage(b)
								}
							}
						}
					}
				}
			}
		}
		itemJSON, err := json.Marshal(item)
		if err != nil {
			continue
		}
		r := string(itemJSON)
		if len(r) > jsonldMaxRaw {
			r = r[:jsonldMaxRaw]
		}
		entries = append(entries, types.StructuredDataEntry{
			Type:   sdType,
			Format: types.FormatJSONLD,
			Raw:    r,
		})
	}
	return entries
}

// jsonldItemType extracts the @type from a parsed JSON-LD item.
func jsonldItemType(item map[string]json.RawMessage) string {
	typeRaw, ok := item["@type"]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(typeRaw, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(typeRaw, &arr) == nil && len(arr) > 0 {
		return arr[0]
	}
	return ""
}

// --- Meta Properties (OG / Twitter) ---

func extractMetaProperties(doc *goquery.Document, prefix string) map[string]string {
	props := make(map[string]string)
	doc.Find("meta[property], meta[name]").Each(func(_ int, s *goquery.Selection) {
		prop, _ := s.Attr("property")
		if prop == "" {
			prop, _ = s.Attr("name")
		}
		if strings.HasPrefix(prop, prefix) {
			content, _ := s.Attr("content")
			props[prop] = content
		}
	})
	return props
}

// --- Helpers ---

func resolveRef(base, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	baseP, err := url.Parse(base)
	if err != nil {
		return ref
	}
	refP, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return baseP.ResolveReference(refP).String()
}
