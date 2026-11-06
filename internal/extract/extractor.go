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
	page.LinkZones = countInternalLinkZones(page.Anchors)
	page.NofollowIntLinks = countNofollowInternal(page.Anchors)

	// Page language and template skeleton (used by automatic segmentation)
	if lang, ok := doc.Find("html").First().Attr("lang"); ok {
		page.HTMLLang = strings.TrimSpace(lang)
	}
	page.DOMFingerprint = domFingerprint(doc)

	// Images
	page.Images = extractImages(doc, baseURL)

	// Hreflang
	page.Hreflang = extractHreflang(doc)
	page.Hreflang = append(page.Hreflang, HreflangFromLinkHeader(opts.Headers["link"])...)

	// Structured data
	page.StructuredData = extractStructuredData(doc)
	page.SchemaValidation = validatePageSchema(doc, page.StructuredData)

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

	// Soft 404 wording (analysis combines it with other signals)
	if opts.StatusCode == 200 {
		page.Soft404Text = detectSoft404Text(doc, bodyText)
		page.IsSoft404 = page.Soft404Text != ""
	}

	// Readability (Flesch-Kincaid)
	if page.WordCount >= 30 {
		page.Readability = computeReadability(bodyText)
	}

	// Freshness signals (Patent US8924379)
	page.Freshness = extractFreshness(doc, page.OpenGraph, page.StructuredData, opts.Headers, time.Now())

	// Content richness (Patent US8682892)
	page.ContentRichness = extractContentRichness(doc, page.Headings, page.WordCount)

	// Listing signals (HTML structure patterns for listing/grid/results pages)
	page.ListingSignals = extractListingSignals(doc)

	// Ad detail signals (classifieds/marketplace ad detail vs e-commerce product)
	page.AdDetailSignals = extractAdDetailSignals(doc)

	// Commerce signals: a single buy button marks a product page; a grid of
	// product cards or many buy buttons marks a product listing.
	// Prices and buy buttons are only counted outside product cards, so a
	// product page with a "related products" strip still reads as a product.
	buy, items, prices := extractCommerceSignals(doc)
	page.ProductCards = items
	page.ItemLinks = countItemLinks(page.InternalLinks)
	switch {
	case buy >= 3 || (items >= 3 && buy == 0 && prices == 0):
		page.ListingSignals = min(page.ListingSignals+2, 5)
	case (buy > 0 && buy <= 2) || (prices > 0 && prices <= 3):
		// A mini-cart total or sticky buy bar adds one more of each.
		page.ProductSignals = buy + prices
	}

	// E-E-A-T signals (Patent US9697259)
	page.EEAT = extractEEAT(doc, page.StructuredData, page.Anchors, page.ExternalLinks, baseURL)

	// Passage readiness (Patent US20160078102)
	page.PassageReadiness = extractPassageReadiness(doc, page.StructuredData, page.Headings, bodyText, page.WordCount)

	// AI answer readiness (heuristic)
	page.AIReadiness = extractAIReadiness(doc, page.StructuredData, page.Headings, bodyText, page.WordCount)

	// Consensus readiness (claims backed by evidence; heuristic)
	page.ConsensusReadiness = extractConsensusReadiness(doc, bodyText, page.WordCount)

	// Topicality alignment (heuristic)
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

// countNofollowInternal counts distinct internal hrefs that have at least one
// rel="nofollow" anchor. Allocation-free when the page has no nofollow links.
func countNofollowInternal(anchors []types.AnchorData) int {
	var seen map[string]struct{}
	for i := range anchors {
		a := &anchors[i]
		if !a.IsInternal || a.Rel == nil || !containsFoldASCII(*a.Rel, "nofollow") {
			continue
		}
		if seen == nil {
			seen = make(map[string]struct{}, 4)
		}
		seen[a.Href] = struct{}{}
	}
	return len(seen)
}

// containsFoldASCII reports whether s contains the lowercase ASCII needle,
// ignoring ASCII case.
func containsFoldASCII(s, needle string) bool {
	n := len(needle)
	for i := 0; i+n <= len(s); i++ {
		match := true
		for j := 0; j < n; j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// countInternalLinkZones tallies internal links by the page region they sit in.
func countInternalLinkZones(anchors []types.AnchorData) *types.LinkZoneCounts {
	zones := &types.LinkZoneCounts{}
	for i := range anchors {
		if anchors[i].IsInternal {
			zones.Add(anchors[i].Position)
		}
	}
	return zones
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
		// Class/id hints first: themes mark breadcrumbs, comment lists and
		// related-post blocks inside nav/article elements, and often build
		// footers and sidebars without semantic tags.
		if pos, ok := zoneFromClassID(p); ok {
			return pos
		}
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

// zoneHints maps class/id substrings to link zones, most specific first.
var zoneHints = []struct {
	pos   types.LinkPosition
	hints []string
}{
	{types.LinkPosBreadcrumb, []string{"breadcrumb"}},
	{types.LinkPosComments, []string{"comment", "disqus"}},
	{types.LinkPosRelated, []string{"related", "recommend", "similar-", "upsell", "cross-sell", "crosssell", "also-like", "you-may-like"}},
	{types.LinkPosSidebar, []string{"sidebar", "widget-area"}},
	{types.LinkPosFooter, []string{"footer"}},
	{types.LinkPosNavigation, []string{"navbar", "main-menu", "nav-menu", "megamenu", "mega-menu", "main-nav", "site-nav"}},
}

// zoneFromClassID infers a link zone from an element's class, id, or aria-label.
func zoneFromClassID(s *goquery.Selection) (types.LinkPosition, bool) {
	class, _ := s.Attr("class")
	id, _ := s.Attr("id")
	label, _ := s.Attr("aria-label")
	if class == "" && id == "" && label == "" {
		return "", false
	}
	attrs := strings.ToLower(class + " " + id + " " + label)
	for _, z := range zoneHints {
		for _, h := range z.hints {
			if strings.Contains(attrs, h) {
				return z.pos, true
			}
		}
	}
	return "", false
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

// HreflangFromLinkHeader reads hreflang alternates from an HTTP Link header:
// <https://example.com/de/>; rel="alternate"; hreflang="de", <...>; ...
func HreflangFromLinkHeader(header string) []types.HreflangEntry {
	if header == "" || !strings.Contains(strings.ToLower(header), "hreflang") {
		return nil
	}
	var out []types.HreflangEntry
	for _, part := range splitLinkHeader(header) {
		open, close := strings.Index(part, "<"), strings.Index(part, ">")
		if open < 0 || close < open {
			continue
		}
		href := strings.TrimSpace(part[open+1 : close])
		var rel, lang string
		for _, param := range strings.Split(part[close+1:], ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "rel":
				rel = strings.ToLower(v)
			case "hreflang":
				lang = v
			}
		}
		if lang == "" || !strings.Contains(" "+rel+" ", " alternate ") {
			continue
		}
		out = append(out, types.HreflangEntry{Lang: lang, Href: href, Source: types.HreflangSourceHeader})
	}
	return out
}

// splitLinkHeader splits a Link header on the commas between links, not
// those inside <...> or quotes.
func splitLinkHeader(h string) []string {
	var parts []string
	inURL, inQuote, start := false, false, 0
	for i := 0; i < len(h); i++ {
		switch h[i] {
		case '<':
			if !inQuote {
				inURL = true
			}
		case '>':
			inURL = false
		case '"':
			if !inURL {
				inQuote = !inQuote
			}
		case ',':
			if !inURL && !inQuote {
				parts = append(parts, h[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, h[start:])
}

// --- Structured Data ---

const jsonldMaxRaw = 5120 // 5KB — sufficient for schema type identification and key properties

// Limits against hostile JSON-LD. Crawled HTML is attacker-controlled and
// up to 10 MB: parsing a huge @graph into maps, indexing it and re-encoding
// every item could cost hundreds of MB per page across many workers.
const (
	// jsonldMaxGraphBytes: larger scripts are not expanded per @graph
	// item; they still give one entry with their top-level @type.
	jsonldMaxGraphBytes = 1 << 20
	// jsonldMaxGraphPageBytes caps the JSON-LD bytes expanded per page.
	jsonldMaxGraphPageBytes = 2 << 20
	// jsonldMaxGraphItems caps the @graph items read from one script.
	jsonldMaxGraphItems = 500
	// maxStructuredEntries caps the structured-data entries of one page.
	maxStructuredEntries = 1000
)

func extractStructuredData(doc *goquery.Document) []types.StructuredDataEntry {
	var entries []types.StructuredDataEntry
	graphBudget := jsonldMaxGraphPageBytes

	// JSON-LD
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if len(entries) >= maxStructuredEntries {
			return false
		}
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return true
		}
		// Expand @graph arrays into individual entries so downstream
		// consumers (AIReadiness, EEAT, schema stats) see each type
		// with its own item-level Raw JSON.
		if len(raw) <= jsonldMaxGraphBytes && len(raw) <= graphBudget {
			graphBudget -= len(raw)
			if items := extractJSONLDGraphItems(raw); len(items) > 0 {
				entries = append(entries, items[:min(len(items), maxStructuredEntries-len(entries))]...)
				return true
			}
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
		return true
	})

	// Microdata — only top-level itemscope elements (skip nested ones)
	doc.Find("[itemscope]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if len(entries) >= maxStructuredEntries {
			return false
		}
		// Skip nested itemscope elements
		if s.ParentsFiltered("[itemscope]").Length() > 0 {
			return true
		}
		itemtype, _ := s.Attr("itemtype")
		if itemtype == "" {
			return true
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
		return true
	})

	return entries
}

// extractJSONLDType extracts @type from JSON-LD: the top-level object's, or
// the first array element's. It streams the tokens instead of building the
// whole document as maps, so a huge script costs no more than its bytes.
func extractJSONLDType(raw string) string {
	if !json.Valid([]byte(raw)) {
		return "Unknown"
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return "Unknown"
	}
	if d, ok := tok.(json.Delim); ok && d == '[' {
		if !dec.More() {
			return "Unknown"
		}
		if tok, err = dec.Token(); err != nil {
			return "Unknown"
		}
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "Unknown"
	}
	sdType := "Unknown"
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return "Unknown"
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "Unknown"
		}
		if key, _ := kt.(string); key == "@type" {
			// The last @type wins, as with json.Unmarshal.
			sdType = jsonldTypeValue(v)
		}
	}
	return sdType
}

// jsonldTypeValue reads a @type value: a string or the first string of an
// array; "Unknown" otherwise.
func jsonldTypeValue(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var arr []json.RawMessage
	if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
		if json.Unmarshal(arr[0], &s) == nil {
			return s
		}
	}
	return "Unknown"
}

// extractJSONLDGraphItems expands a @graph JSON-LD into individual StructuredDataEntry
// items, each with its own type and item-level Raw JSON. For entries that use @id
// author references, the referenced Person/Organization is inlined so downstream
// consumers (EEAT) can find the author name.
// Returns nil if the JSON-LD does not contain a @graph key. At most
// jsonldMaxGraphItems items are read, and only authors no larger than
// jsonldMaxRaw are inlined (a large one copied into every item would
// multiply memory).
func extractJSONLDGraphItems(raw string) []types.StructuredDataEntry {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	graphRaw, ok := obj["@graph"]
	if !ok {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(graphRaw)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil
	}
	var items []map[string]json.RawMessage
	var itemRaws []json.RawMessage
	for dec.More() && len(items) < jsonldMaxGraphItems {
		var elem json.RawMessage
		if err := dec.Decode(&elem); err != nil {
			return nil
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(elem, &item); err != nil {
			return nil // not an object: not a usable @graph
		}
		if item == nil {
			continue // null
		}
		items = append(items, item)
		itemRaws = append(itemRaws, elem)
	}
	if len(items) == 0 {
		return nil
	}
	// Build @id index for reference resolution.
	idIndex := make(map[string]json.RawMessage)
	for i, item := range items {
		if idRaw, ok := item["@id"]; ok {
			var id string
			if json.Unmarshal(idRaw, &id) == nil && id != "" {
				idIndex[id] = itemRaws[i]
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
							if resolved, found := idIndex[id]; found && len(resolved) <= jsonldMaxRaw {
								item["author"] = resolved
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
