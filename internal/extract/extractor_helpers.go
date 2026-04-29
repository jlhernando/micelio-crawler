package extract

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/micelio/micelio/internal/types"
)

// --- Body Text ---

func extractBodyText(doc *goquery.Document) string {
	// Clone body selection and remove script/style elements from the clone
	body := doc.Find("body")
	if body.Length() == 0 {
		return ""
	}

	// Work on a clone to avoid mutating the shared document
	bodyClone := body.Clone()
	bodyClone.Find("script, style, noscript, svg").Remove()

	text := bodyClone.Text()
	// Normalize whitespace
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func countWords(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

// --- Security ---

func extractSecurity(baseURL string, doc *goquery.Document, headers map[string]string) types.SecurityData {
	p, _ := url.Parse(baseURL)
	isHTTPS := p != nil && p.Scheme == "https"

	var mixedURLs []string
	if isHTTPS {
		// Check for http:// resources
		doc.Find("script[src], link[href], img[src], iframe[src]").Each(func(_ int, s *goquery.Selection) {
			src, _ := s.Attr("src")
			if src == "" {
				src, _ = s.Attr("href")
			}
			if strings.HasPrefix(src, "http://") {
				mixedURLs = append(mixedURLs, src)
			}
		})
	}

	return types.SecurityData{
		IsHTTPS:          isHTTPS,
		HasMixedContent:  len(mixedURLs) > 0,
		MixedContentURLs: mixedURLs,
		HasHSTS:          headers["strict-transport-security"] != "",
		HasXFrameOptions: headers["x-frame-options"] != "",
		HasCSP:           headers["content-security-policy"] != "",
	}
}

// --- Indexability ---

func determineIndexability(statusCode int, metaRobots, xRobotsTag *string, canonical *string, finalURL string) types.IndexabilityData {
	if statusCode < 200 || statusCode >= 300 {
		return types.IndexabilityData{Indexable: false, Reason: fmt.Sprintf("HTTP %d", statusCode)}
	}

	if metaRobots != nil {
		lower := strings.ToLower(*metaRobots)
		if strings.Contains(lower, "noindex") {
			return types.IndexabilityData{Indexable: false, Reason: "meta robots noindex"}
		}
	}

	if xRobotsTag != nil {
		lower := strings.ToLower(*xRobotsTag)
		if strings.Contains(lower, "noindex") {
			return types.IndexabilityData{Indexable: false, Reason: "X-Robots-Tag noindex"}
		}
	}

	if canonical != nil && *canonical != "" {
		// Normalize both URLs so trailing-slash and www differences don't cause false positives
		normCanonical := normalizeForComparison(*canonical)
		normFinal := normalizeForComparison(finalURL)
		if normCanonical != normFinal {
			return types.IndexabilityData{Indexable: false, Reason: "canonicalized"}
		}
	}

	return types.IndexabilityData{Indexable: true, Reason: ""}
}

// normalizeForComparison normalizes URLs for canonical comparison,
// stripping www prefix, trailing slash, and protocol differences.
func normalizeForComparison(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.TrimRight(rawURL, "/")
	}
	host := strings.TrimPrefix(parsed.Hostname(), "www.")
	path := parsed.Path
	if path == "" {
		path = "/"
	} else if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	if parsed.RawQuery != "" {
		return host + path + "?" + parsed.RawQuery
	}
	return host + path
}

// --- URL Issues ---

var (
	uppercaseRe    = regexp.MustCompile(`[A-Z]`)
	underscoreRe   = regexp.MustCompile(`_`)
	trackingParams = map[string]bool{
		"utm_source": true, "utm_medium": true, "utm_campaign": true,
		"utm_term": true, "utm_content": true, "fbclid": true,
		"gclid": true, "msclkid": true,
	}
)

func detectURLIssues(rawURL string) []string {
	var issues []string
	p, err := url.Parse(rawURL)
	if err != nil {
		return issues
	}

	path := p.Path

	if uppercaseRe.MatchString(path) {
		issues = append(issues, "uppercase-in-path")
	}
	if strings.Contains(path, " ") || strings.Contains(path, "%20") {
		issues = append(issues, "spaces-in-url")
	}
	if underscoreRe.MatchString(path) {
		issues = append(issues, "underscores-in-path")
	}
	if strings.Contains(path, "//") {
		issues = append(issues, "double-slashes")
	}
	if len(rawURL) > 200 {
		issues = append(issues, "url-too-long")
	}

	// Check for tracking parameters
	for param := range p.Query() {
		if trackingParams[param] {
			issues = append(issues, "tracking-parameters")
			break
		}
	}

	// Path depth warning
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) > 5 && segments[0] != "" {
		issues = append(issues, "deep-path")
	}

	return issues
}

// --- Soft 404 ---

var soft404Patterns = regexp.MustCompile(`(?i)(page not found|404 not found|not found|page doesn.t exist|no longer available|nothing found|does not exist)`)

func detectSoft404(doc *goquery.Document, bodyText string) bool {
	// Check title
	title := doc.Find("title").Text()
	if soft404Patterns.MatchString(title) {
		return true
	}
	// Check h1
	h1 := doc.Find("h1").First().Text()
	if soft404Patterns.MatchString(h1) {
		return true
	}
	// Very thin content with 404-like language
	if len(bodyText) < 500 && soft404Patterns.MatchString(bodyText) {
		return true
	}
	return false
}

// --- Readability (Flesch-Kincaid) ---

func computeReadability(text string) *types.ReadabilityData {
	sentences := countSentences(text)
	if sentences == 0 {
		return nil
	}
	words := countWords(text)
	if words == 0 {
		return nil
	}
	syllables := countSyllables(text)

	avgWordsPerSentence := float64(words) / float64(sentences)
	avgSyllablesPerWord := float64(syllables) / float64(words)

	// Flesch-Kincaid Grade Level
	fk := 0.39*avgWordsPerSentence + 11.8*avgSyllablesPerWord - 15.59
	fk = math.Round(fk*100) / 100

	return &types.ReadabilityData{
		FleschKincaid:       fk,
		SentenceCount:       sentences,
		AvgWordsPerSentence: math.Round(avgWordsPerSentence*100) / 100,
		SyllableCount:       syllables,
	}
}

var sentenceEndRe = regexp.MustCompile(`[.!?]+`)

func countSentences(text string) int {
	matches := sentenceEndRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return 1
	}
	return len(matches)
}

func countSyllables(text string) int {
	words := strings.Fields(text)
	total := 0
	for _, w := range words {
		total += syllablesInWord(strings.ToLower(w))
	}
	return total
}

func syllablesInWord(word string) int {
	if len(word) <= 3 {
		return 1
	}

	// Remove trailing 'e'
	w := word
	if strings.HasSuffix(w, "e") && len(w) > 3 {
		w = w[:len(w)-1]
	}

	count := 0
	prevVowel := false
	for _, ch := range w {
		isVowel := strings.ContainsRune("aeiouy", ch)
		if isVowel && !prevVowel {
			count++
		}
		prevVowel = isVowel
	}

	if count == 0 {
		count = 1
	}
	return count
}

// --- Custom Extractions ---

func runCustomExtractions(doc *goquery.Document, rules []types.CustomExtractionRule) map[string][]string {
	results := make(map[string][]string)
	for _, rule := range rules {
		if rule.Type != "css" {
			continue
		}
		var values []string
		doc.Find(rule.Selector).Each(func(_ int, s *goquery.Selection) {
			values = append(values, strings.TrimSpace(s.Text()))
		})
		results[rule.Name] = values
	}
	return results
}

// --- Custom Searches ---

func runCustomSearches(html string, rules []types.CustomSearchRule) map[string]bool {
	results := make(map[string]bool)
	for _, rule := range rules {
		if rule.IsRegex {
			re, err := regexp.Compile(rule.Pattern)
			if err == nil {
				results[rule.Name] = re.MatchString(html)
			} else {
				results[rule.Name] = false
			}
		} else {
			results[rule.Name] = strings.Contains(html, rule.Pattern)
		}
	}
	return results
}

// --- Exported Helpers ---

// ExtractBodyText extracts clean body text from an HTML string.
// Exported for use by n-gram and embedding analyzers.
func ExtractBodyText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	return extractBodyText(doc)
}

// ExtractHTMLLang extracts the lang attribute from the <html> tag.
func ExtractHTMLLang(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	lang, _ := doc.Find("html").Attr("lang")
	return lang
}

// --- Freshness extraction ---

var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"January 2, 2006",
	"Jan 2, 2006",
	"02 Jan 2006",
	"2006/01/02",
}

func tryParseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func extractFreshness(doc *goquery.Document, og map[string]string, sd []types.StructuredDataEntry, headers map[string]string, now time.Time) *types.FreshnessData {
	f := &types.FreshnessData{}
	var sources []string

	// 1. OG article dates
	if pub := og["article:published_time"]; pub != "" {
		if t := tryParseDate(pub); t != nil {
			f.BylineDate = t
			f.DateSource = "opengraph"
			sources = append(sources, t.Format("2006-01-02"))
		}
	}
	if mod := og["article:modified_time"]; mod != "" {
		if t := tryParseDate(mod); t != nil {
			f.ModifiedDate = t
		}
	}

	// 2. Schema datePublished/dateModified
	for _, entry := range sd {
		if entry.Format != types.FormatJSONLD {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(entry.Raw), &obj); err != nil {
			continue
		}
		if f.BylineDate == nil {
			if raw, ok := obj["datePublished"]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					if t := tryParseDate(s); t != nil {
						f.BylineDate = t
						f.DateSource = "schema"
						sources = append(sources, t.Format("2006-01-02"))
					}
				}
			}
		}
		if f.ModifiedDate == nil {
			if raw, ok := obj["dateModified"]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					if t := tryParseDate(s); t != nil {
						f.ModifiedDate = t
					}
				}
			}
		}
	}

	// 3. <meta name="date"> or <meta name="pubdate">
	if f.BylineDate == nil {
		doc.Find(`meta[name="date"], meta[name="pubdate"], meta[name="publish-date"]`).Each(func(_ int, s *goquery.Selection) {
			if f.BylineDate != nil {
				return
			}
			if content, exists := s.Attr("content"); exists {
				if t := tryParseDate(content); t != nil {
					f.BylineDate = t
					f.DateSource = "meta"
					sources = append(sources, t.Format("2006-01-02"))
				}
			}
		})
	}

	// 4. <time datetime=""> near top of content
	if f.BylineDate == nil {
		doc.Find("time[datetime]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			dt, _ := s.Attr("datetime")
			if t := tryParseDate(dt); t != nil {
				f.BylineDate = t
				f.DateSource = "html-time"
				sources = append(sources, t.Format("2006-01-02"))
				return false
			}
			return true
		})
	}

	// 5. Last-Modified header
	if lm := headers["last-modified"]; lm != "" {
		if t, err := time.Parse(time.RFC1123, lm); err == nil {
			if f.ModifiedDate == nil {
				f.ModifiedDate = &t
			}
			sources = append(sources, t.Format("2006-01-02"))
		}
	}

	if f.BylineDate == nil && f.ModifiedDate == nil {
		return nil
	}

	// Content age (days since oldest available date)
	oldest := f.BylineDate
	if oldest == nil || (f.ModifiedDate != nil && f.ModifiedDate.Before(*oldest)) {
		oldest = f.ModifiedDate
	}
	if oldest != nil {
		f.ContentAgeDays = int(now.Sub(*oldest).Hours() / 24)
		if f.ContentAgeDays < 0 {
			f.ContentAgeDays = 0
		}
	}

	// Date consistency: do sources agree within 7 days?
	f.DateConsistency = len(sources) <= 1
	if len(sources) >= 2 {
		first := tryParseDate(sources[0])
		allClose := true
		for _, s := range sources[1:] {
			other := tryParseDate(s)
			if first != nil && other != nil {
				diff := first.Sub(*other)
				if diff < 0 {
					diff = -diff
				}
				if diff > 7*24*time.Hour {
					allClose = false
					break
				}
			}
		}
		f.DateConsistency = allClose
	}

	return f
}

// --- Content richness extraction ---

func extractContentRichness(doc *goquery.Document, headings types.HeadingData, wordCount int) *types.ContentRichnessData {
	if wordCount < 10 {
		return nil
	}
	cr := &types.ContentRichnessData{}

	// Heading depth and count
	for level := 6; level >= 1; level-- {
		var count int
		switch level {
		case 1:
			count = len(headings.H1)
		case 2:
			count = len(headings.H2)
		case 3:
			count = len(headings.H3)
		case 4:
			count = len(headings.H4)
		case 5:
			count = len(headings.H5)
		case 6:
			count = len(headings.H6)
		}
		cr.HeadingCount += count
		if count > 0 && cr.HeadingDepth == 0 {
			cr.HeadingDepth = level
		}
	}

	// Count structural elements (single DOM pass)
	body := doc.Find("body")
	cr.ListCount = body.Find("ul, ol").Length()
	cr.ListItemCount = body.Find("li").Length()
	cr.TableCount = body.Find("table").Length()
	cr.VideoEmbeds = body.Find("video, iframe[src*='youtube'], iframe[src*='vimeo']").Length()
	cr.CodeBlocks = body.Find("pre, code").Length()
	cr.BlockquoteCount = body.Find("blockquote").Length()
	cr.DefinitionLists = body.Find("dl").Length()

	// Images in main content (not nav/footer/header)
	body.Find("main img, article img, [role='main'] img").Each(func(_ int, s *goquery.Selection) {
		cr.ImageInContent++
	})
	// Fallback: count all body images if no main/article container
	if cr.ImageInContent == 0 {
		cr.ImageInContent = body.Find("img").Length()
	}

	// Richness score (normalized 0-1)
	cr.RichnessScore = 0.15*math.Min(float64(cr.HeadingDepth)/6.0, 1.0) +
		0.20*math.Min(float64(cr.ListCount)/10.0, 1.0) +
		0.15*math.Min(float64(cr.TableCount)/3.0, 1.0) +
		0.20*math.Min(float64(cr.ImageInContent)/5.0, 1.0) +
		0.10*math.Min(float64(cr.VideoEmbeds)/2.0, 1.0) +
		0.10*math.Min(float64(cr.CodeBlocks)/5.0, 1.0) +
		0.10*math.Min(float64(cr.BlockquoteCount)/3.0, 1.0)
	cr.RichnessScore = math.Round(cr.RichnessScore*1000) / 1000

	// Information density
	if wordCount > 0 {
		features := float64(cr.HeadingCount + cr.ListCount + cr.TableCount + cr.ImageInContent)
		cr.InformationDensity = math.Round(features/float64(wordCount)*10000) / 10000
	}

	return cr
}

// --- Listing signal extraction ---

// listingClassPatterns matches CSS classes/IDs commonly used for listing containers.
var listingClassPatterns = regexp.MustCompile(`(?i)(search-results|product-list|listing|results-list|item-list|catalog|grid-view|card-list|offers-list|search-list|product-grid)`)

// listingDataAttrPatterns matches data-* attribute values indicating listing containers.
var listingDataAttrPatterns = regexp.MustCompile(`(?i)(results|listing|catalog)`)

// maxAttrLen caps class/id attribute length to avoid slow regex matching on adversarial HTML.
const maxAttrLen = 1000

// extractListingSignals counts HTML elements matching listing/grid/results patterns.
// Returns a capped value (0–5) for consistent scoring in template detection.
func extractListingSignals(doc *goquery.Document) int {
	signals := 0
	body := doc.Find("body")
	if body.Length() == 0 {
		return 0
	}

	// Track listing containers found via class/ID to avoid double-counting
	// in the repeated-children scan below.
	type nodeKey struct{ tag, cls string }
	seen := make(map[nodeKey]bool)

	// Check classes and IDs on block-level/semantic container elements only.
	body.Find("div, section, ul, ol, main, aside, nav, article").Each(func(_ int, s *goquery.Selection) {
		cls, _ := s.Attr("class")
		id, _ := s.Attr("id")
		combined := cls + " " + id
		if len(combined) > maxAttrLen {
			combined = combined[:maxAttrLen]
		}
		if listingClassPatterns.MatchString(combined) {
			signals++
			seen[nodeKey{goquery.NodeName(s), cls}] = true
		}
	})

	// Check data-testid, data-role attributes
	body.Find("[data-testid], [data-role]").Each(func(_ int, s *goquery.Selection) {
		for _, attr := range []string{"data-testid", "data-role"} {
			if val, exists := s.Attr(attr); exists {
				if len(val) > maxAttrLen {
					val = val[:maxAttrLen]
				}
				if listingDataAttrPatterns.MatchString(val) {
					signals++
				}
			}
		}
	})

	// Check for repeated item containers inside listing-like parents.
	// Only scan containers not already counted above.
	body.Find("div[class], section[class], ul[class], ol[class]").Each(func(_ int, s *goquery.Selection) {
		cls, _ := s.Attr("class")
		if seen[nodeKey{goquery.NodeName(s), cls}] {
			return // already counted
		}
		if len(cls) > maxAttrLen {
			cls = cls[:maxAttrLen]
		}
		if !listingClassPatterns.MatchString(cls) {
			return
		}
		children := s.Children()
		if children.Length() >= 3 {
			itemCount := 0
			children.Each(func(_ int, child *goquery.Selection) {
				tag := goquery.NodeName(child)
				if tag == "article" || tag == "li" || tag == "div" {
					itemCount++
				}
			})
			if itemCount >= 3 {
				signals++
			}
		}
	})

	// Cap to 0–5 for consistent scoring
	if signals > 5 {
		signals = 5
	}
	return signals
}

// --- Ad detail signal extraction ---

// adDetailClassPatterns matches CSS classes/IDs for classifieds/marketplace ad detail pages.
var adDetailClassPatterns = regexp.MustCompile(`(?i)(contact-seller|message-seller|seller-info|seller-profile|seller-card|ad-params|ad-attributes|report-ad|flag-ad|ad-gallery|offer-params|offer-details|listing-details|ad-description|seller-details|vendor-info)`)

// ecommerceClassPatterns matches CSS classes/IDs for e-commerce product pages (negative signal).
var ecommerceClassPatterns = regexp.MustCompile(`(?i)(add-to-cart|add-to-bag|add-to-basket|buy-now|buy-it-now|checkout|shopping-cart|cart-icon|quantity-selector|qty-selector|product-variants|size-selector|color-selector)`)

// extractAdDetailSignals returns a net score: ad detail signals minus e-commerce signals.
// Positive = classifieds/marketplace ad, negative = e-commerce product page.
func extractAdDetailSignals(doc *goquery.Document) int {
	body := doc.Find("body")
	if body.Length() == 0 {
		return 0
	}

	adSignals := 0
	ecomSignals := 0

	body.Find("div, section, aside, form, button, a").Each(func(_ int, s *goquery.Selection) {
		cls, _ := s.Attr("class")
		id, _ := s.Attr("id")
		combined := cls + " " + id
		if len(combined) > maxAttrLen {
			combined = combined[:maxAttrLen]
		}
		if adDetailClassPatterns.MatchString(combined) {
			adSignals++
		}
		if ecommerceClassPatterns.MatchString(combined) {
			ecomSignals++
		}
	})

	// Check data-testid attributes
	body.Find("[data-testid]").Each(func(_ int, s *goquery.Selection) {
		if val, exists := s.Attr("data-testid"); exists {
			if len(val) > maxAttrLen {
				val = val[:maxAttrLen]
			}
			if adDetailClassPatterns.MatchString(val) {
				adSignals++
			}
			if ecommerceClassPatterns.MatchString(val) {
				ecomSignals++
			}
		}
	})

	// Cap each side
	if adSignals > 5 {
		adSignals = 5
	}
	if ecomSignals > 5 {
		ecomSignals = 5
	}

	return adSignals - ecomSignals
}

// --- E-E-A-T extraction ---

var (
	authorityDomains = map[string]bool{
		"edu": true, "gov": true, "org": true,
	}
	aboutPaths    = regexp.MustCompile(`(?i)^/(about|about-us|team|who-we-are)(/|$)`)
	contactPaths  = regexp.MustCompile(`(?i)^/(contact|contact-us|get-in-touch)(/|$)`)
	authorPaths   = regexp.MustCompile(`(?i)^/(author|authors|contributor|writer)s?/`)
	editorialPaths = regexp.MustCompile(`(?i)^/(editorial|editorial-policy|fact-check|corrections|ethics|standards)(/|$)`)
	bylinePattern  = regexp.MustCompile(`(?i:by|author|written by|reviewed by)[:\s]+([A-Z][a-z]+ [A-Z][a-z]+)`)
)

func extractEEAT(doc *goquery.Document, sd []types.StructuredDataEntry, anchors []types.AnchorData, externalLinks []string, baseURL string) *types.EEATData {
	e := &types.EEATData{}

	// 1. Author from schema (JSON-LD)
	for _, entry := range sd {
		if entry.Format != types.FormatJSONLD || e.HasAuthor {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(entry.Raw), &obj); err != nil {
			continue
		}
		if raw, ok := obj["author"]; ok {
			// Author can be a string, object {name:...}, or array [{name:...}]
			var authorObj map[string]json.RawMessage
			if json.Unmarshal(raw, &authorObj) == nil {
				if nameRaw, ok := authorObj["name"]; ok {
					var name string
					if json.Unmarshal(nameRaw, &name) == nil && name != "" {
						e.HasAuthor = true
						e.AuthorName = name
					}
				}
				if typeRaw, ok := authorObj["@type"]; ok {
					var t string
					json.Unmarshal(typeRaw, &t)
					e.AuthorSchemaType = t
				}
			} else {
				// Try as array of objects
				var authorArr []map[string]json.RawMessage
				if json.Unmarshal(raw, &authorArr) == nil && len(authorArr) > 0 {
					if nameRaw, ok := authorArr[0]["name"]; ok {
						var name string
						if json.Unmarshal(nameRaw, &name) == nil && name != "" {
							e.HasAuthor = true
							e.AuthorName = name
						}
					}
					if typeRaw, ok := authorArr[0]["@type"]; ok {
						var t string
						json.Unmarshal(typeRaw, &t)
						e.AuthorSchemaType = t
					}
				} else {
					// Try as string
					var name string
					if json.Unmarshal(raw, &name) == nil && name != "" {
						e.HasAuthor = true
						e.AuthorName = name
					}
				}
			}
		}
	}

	// 2. <meta name="author">
	if !e.HasAuthor {
		if content, exists := doc.Find(`meta[name="author"]`).Attr("content"); exists && strings.TrimSpace(content) != "" {
			e.HasAuthor = true
			e.AuthorName = strings.TrimSpace(content)
		}
	}

	// 3. Visible byline pattern (check first 2KB of body text)
	if !e.HasAuthor {
		bodyClone := doc.Find("body").Clone()
		bodyClone.Find("script, style, noscript").Remove()
		text := bodyClone.Text()
		if len(text) > 2048 {
			text = text[:2048]
		}
		if matches := bylinePattern.FindStringSubmatch(text); len(matches) > 1 {
			e.HasAuthor = true
			e.AuthorName = matches[1]
		}
	}

	// 4. Check internal links for special pages
	for _, anchor := range anchors {
		if !anchor.IsInternal {
			continue
		}
		p, err := url.Parse(anchor.Href)
		if err != nil {
			continue
		}
		path := p.Path
		if aboutPaths.MatchString(path) {
			e.HasAboutPage = true
		}
		if contactPaths.MatchString(path) {
			e.HasContactPage = true
		}
		if authorPaths.MatchString(path) {
			e.HasAuthorPage = true
		}
		if editorialPaths.MatchString(path) {
			e.HasEditorialPolicy = true
		}
	}

	// 5. Citation count (external links to .edu, .gov, .org)
	e.ExternalRefCount = len(externalLinks)
	for _, link := range externalLinks {
		p, err := url.Parse(link)
		if err != nil {
			continue
		}
		host := p.Hostname()
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			tld := parts[len(parts)-1]
			if authorityDomains[tld] {
				e.CitationCount++
			}
		}
	}

	return e
}

// --- Passage readiness extraction ---

var (
	definitionPattern = regexp.MustCompile(`(?i)(is a|is an|refers to|defined as|means|is the)`)
	faqHeadingPattern = regexp.MustCompile(`(?i)^(what|how|why|when|where|who|can|does|is|are|do|should|will|which)\s`)
)

func extractPassageReadiness(doc *goquery.Document, sd []types.StructuredDataEntry, headings types.HeadingData, bodyText string, wordCount int) *types.PassageReadinessData {
	if wordCount < 30 {
		return nil
	}
	pr := &types.PassageReadinessData{}

	// Count sections (delimited by h2/h3 headings)
	pr.SectionsCount = len(headings.H2) + len(headings.H3)
	if pr.SectionsCount == 0 {
		pr.SectionsCount = 1
	}
	pr.AvgWordsPerSection = math.Round(float64(wordCount)/float64(pr.SectionsCount)*10) / 10

	// Definitions: check first 500 words
	truncated := bodyText
	words := strings.Fields(bodyText)
	if len(words) > 500 {
		truncated = strings.Join(words[:500], " ")
	}
	pr.HasDefinitions = definitionPattern.MatchString(truncated)

	// Direct answers: sentences under 50 words that look like definitions
	sentences := strings.FieldsFunc(bodyText, func(r rune) bool { return r == '.' || r == '!' || r == '?' })
	for _, sent := range sentences {
		sent = strings.TrimSpace(sent)
		wc := len(strings.Fields(sent))
		if wc >= 5 && wc <= 50 && definitionPattern.MatchString(sent) {
			pr.HasDirectAnswers++
			if pr.HasDirectAnswers >= 5 {
				break
			}
		}
	}

	// FAQ structure: headings that look like questions
	allH := make([]string, 0, len(headings.H2)+len(headings.H3))
	allH = append(append(allH, headings.H2...), headings.H3...)
	questionCount := 0
	for _, h := range allH {
		if faqHeadingPattern.MatchString(h) || strings.HasSuffix(h, "?") {
			questionCount++
		}
	}
	pr.HasFAQStructure = questionCount >= 3

	// HowTo structure: from schema
	for _, entry := range sd {
		t := strings.ToLower(entry.Type)
		if t == "faqpage" {
			pr.HasFAQStructure = true
		}
		if t == "howto" {
			pr.HasHowToStructure = true
		}
	}

	// Passage score composite
	score := 0.0
	if pr.SectionsCount >= 3 {
		score += 0.25
	}
	if pr.AvgWordsPerSection >= 50 && pr.AvgWordsPerSection <= 300 {
		score += 0.25
	}
	if pr.HasDefinitions {
		score += 0.15
	}
	if pr.HasDirectAnswers > 0 {
		score += 0.15 * math.Min(float64(pr.HasDirectAnswers)/3.0, 1.0)
	}
	if pr.HasFAQStructure {
		score += 0.1
	}
	if pr.HasHowToStructure {
		score += 0.1
	}
	pr.PassageScore = math.Round(score*1000) / 1000

	return pr
}

// --- AI readiness extraction ---

func extractAIReadiness(doc *goquery.Document, sd []types.StructuredDataEntry, headings types.HeadingData, bodyText string, wordCount int) *types.AIReadinessData {
	if wordCount < 30 {
		return nil
	}
	ai := &types.AIReadinessData{}

	// Concise definition: first paragraph <= 50 words, complete sentence
	paragraphs := doc.Find("p")
	paragraphs.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text := strings.TrimSpace(s.Text())
		wc := len(strings.Fields(text))
		if wc >= 10 && wc <= 50 {
			ai.HasConciseDefinition = true
			ai.TopPassageLength = wc
			return false
		}
		if wc > 50 {
			ai.TopPassageLength = wc
			return false
		}
		return true
	})

	// Structured answer: list or table near top of content
	firstH2 := doc.Find("h2").First()
	if firstH2.Length() > 0 {
		// Check for lists/tables before first h2
		firstH2.PrevAll().Each(func(_ int, s *goquery.Selection) {
			tag := goquery.NodeName(s)
			if tag == "ul" || tag == "ol" || tag == "table" {
				ai.HasStructuredAnswer = true
			}
		})
	}
	// Also check if content starts with a list
	if !ai.HasStructuredAnswer {
		body := doc.Find("main, article, [role='main']")
		if body.Length() == 0 {
			body = doc.Find("body")
		}
		first := body.Children().First()
		tag := goquery.NodeName(first)
		if tag == "ul" || tag == "ol" || tag == "table" {
			ai.HasStructuredAnswer = true
		}
	}

	// Schema: FAQ and HowTo
	for _, entry := range sd {
		t := strings.ToLower(entry.Type)
		if t == "faqpage" {
			ai.HasFAQSchema = true
		}
		if t == "howto" {
			ai.HasHowToSchema = true
		}
	}

	// AI readiness score
	score := 0.0
	if ai.HasConciseDefinition {
		score += 0.3
	}
	if ai.HasStructuredAnswer {
		score += 0.2
	}
	if ai.HasFAQSchema {
		score += 0.2
	}
	if ai.HasHowToSchema {
		score += 0.15
	}
	if len(headings.H2)+len(headings.H3) >= 3 {
		score += 0.15
	}
	ai.AIReadinessScore = math.Round(score*1000) / 1000

	return ai
}

// --- Consensus readiness extraction ---
// Google evaluates passages for agreement/contradiction with "general consensus."
// Source: Candour Agency exploit (Mark Williams-Cook, Dec 2024).
// Confidence: CONFIRMED (exploit data, $13,337 Google VRP bounty, 800K domains).

// consensusClaimPatterns detects claim-like sentences (assertions, statements of fact).
var consensusClaimPatterns = []string{
	"according to", "research shows", "studies show", "evidence suggests",
	"experts agree", "data shows", "scientists found", "it is known that",
	"it has been shown", "is widely accepted", "the consensus is",
}

// consensusMythPatterns detects myth-busting / fact-correction patterns.
var consensusMythPatterns = []string{
	"contrary to popular belief", "common misconception", "myth:", "fact:",
	"actually,", "in reality,", "the truth is", "despite popular belief",
	"this is a myth", "widely believed but", "not true that",
}

func extractConsensusReadiness(doc *goquery.Document, bodyText string, wordCount int) *types.ConsensusReadinessData {
	if wordCount < 100 {
		return nil
	}
	c := &types.ConsensusReadinessData{}
	lower := strings.ToLower(bodyText)

	// Count claim-like passages
	for _, pattern := range consensusClaimPatterns {
		c.ClaimCount += strings.Count(lower, pattern)
	}

	// Count myth-busting patterns
	for _, pattern := range consensusMythPatterns {
		c.MythBustCount += strings.Count(lower, pattern)
	}

	// Count evidence patterns (citations, references)
	c.EvidenceCount += strings.Count(lower, "source:")
	c.EvidenceCount += strings.Count(lower, "reference:")
	c.EvidenceCount += strings.Count(lower, "citation:")
	c.EvidenceCount += strings.Count(lower, "[source]")
	// Inline citations like (Smith, 2024) or [1]
	for i := 0; i < len(lower)-3; i++ {
		if lower[i] == '[' && lower[i+1] >= '0' && lower[i+1] <= '9' {
			c.EvidenceCount++
		}
	}

	// FAQ-like Q&A pairs
	doc.Find("h2, h3, dt, summary").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if strings.HasSuffix(text, "?") {
			c.FAQCount++
		}
	})

	// Composite consensus score (0-1)
	score := 0.0
	if c.ClaimCount >= 3 {
		score += 0.25
	} else if c.ClaimCount >= 1 {
		score += 0.10
	}
	if c.EvidenceCount >= 3 {
		score += 0.25
	} else if c.EvidenceCount >= 1 {
		score += 0.10
	}
	if c.FAQCount >= 3 {
		score += 0.25
	} else if c.FAQCount >= 1 {
		score += 0.10
	}
	if c.MythBustCount >= 1 {
		score += 0.25 // Strong consensus alignment signal
	}
	c.ConsensusScore = math.Round(score*1000) / 1000

	if c.ConsensusScore == 0 {
		return nil // No consensus signals detected
	}
	return c
}

// googlebotHTMLLimit is Googlebot's maximum fetch size for HTML documents (uncompressed).
// Announced in https://developers.google.com/search/blog/2026/03/crawler-blog-post
const googlebotHTMLLimit = 2 * 1024 * 1024

// truncationElement defines an SEO element to check for Googlebot truncation risk.
type truncationElement struct {
	Label    string
	Pattern  string
	FindLast bool // true = find last occurrence instead of first
}

// truncationElements lists critical SEO elements that could be lost if Googlebot
// truncates the HTML at 2MB.
var truncationElements = []truncationElement{
	// Critical SEO
	{Label: "title", Pattern: "<title"},
	{Label: "meta description", Pattern: `<meta name="description"`},
	{Label: "canonical", Pattern: `<link rel="canonical"`},
	{Label: "JSON-LD structured data", Pattern: `<script type="application/ld+json"`},
	{Label: "meta robots", Pattern: `<meta name="robots"`},
	{Label: "hreflang", Pattern: `<link rel="alternate" hreflang`},
	// Content & structure
	{Label: "h1", Pattern: "<h1"},
	{Label: "main content open", Pattern: "<main"},
	{Label: "main content close", Pattern: "</main>"},
	{Label: "article open", Pattern: "<article"},
	{Label: "article close", Pattern: "</article>"},
	// Technical SEO
	{Label: "pagination next", Pattern: `<link rel="next"`},
	{Label: "pagination prev", Pattern: `<link rel="prev"`},
	{Label: "Open Graph", Pattern: `<meta property="og:`},
	// Link discovery (last occurrence matters)
	{Label: "last internal link", Pattern: `<a href`, FindLast: true},
}

// GooglebotTruncationCheck reports which critical SEO elements appear after
// Googlebot's 2MB byte offset in the raw HTML. Returns nil for pages under 2MB.
func GooglebotTruncationCheck(html string) []string {
	if len(html) <= googlebotHTMLLimit {
		return nil
	}

	lowerHTML := strings.ToLower(html)
	var truncated []string

	for _, elem := range truncationElements {
		pattern := strings.ToLower(elem.Pattern)
		var pos int
		if elem.FindLast {
			pos = strings.LastIndex(lowerHTML, pattern)
		} else {
			pos = strings.Index(lowerHTML, pattern)
		}
		if pos >= googlebotHTMLLimit {
			mb := float64(pos) / (1024 * 1024)
			truncated = append(truncated, fmt.Sprintf("%s (at %.1fMB)", elem.Label, mb))
		}
	}
	return truncated
}

// ExtractResourceRefs extracts references to page resources (images, scripts, stylesheets, etc.)
// for use by the page-weight analysis feature.
func ExtractResourceRefs(htmlContent, pageURL string) []types.ResourceEntry {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var resources []types.ResourceEntry

	type selectorDef struct {
		selector string
		attr     string
		resType  types.ResourceType
	}

	selectors := []selectorDef{
		{"img[src]", "src", types.ResourceImage},
		{"script[src]", "src", types.ResourceScript},
		{`link[rel="stylesheet"][href]`, "href", types.ResourceStylesheet},
		{`link[rel="preload"][as="font"][href]`, "href", types.ResourceFont},
		{"iframe[src]", "src", types.ResourceOther},
		{"video[src]", "src", types.ResourceVideo},
		{"audio[src]", "src", types.ResourceAudio},
		{"source[src]", "src", types.ResourceOther},
	}

	for _, sel := range selectors {
		doc.Find(sel.selector).Each(func(_ int, s *goquery.Selection) {
			raw, exists := s.Attr(sel.attr)
			if !exists || raw == "" || strings.HasPrefix(raw, "data:") {
				return
			}
			absURL := resolveRef(pageURL, raw)
			if absURL == "" || seen[absURL] {
				return
			}
			seen[absURL] = true
			resources = append(resources, types.ResourceEntry{
				URL:  absURL,
				Type: sel.resType,
			})
		})
	}

	return resources
}
