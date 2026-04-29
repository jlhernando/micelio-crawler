package analysis

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// TemplateType classifies a page's purpose.
const (
	TemplateHomepage = "homepage"
	TemplateListing  = "listing"
	TemplateProduct  = "product"
	TemplateAdDetail = "ad_detail"
	TemplateArticle  = "article"
	TemplateLegal    = "legal"
	TemplateContact  = "contact"
	TemplateFAQ      = "faq"
	TemplateSearch   = "search"
	TemplateLogin    = "login"
	TemplateOther    = "other"
)

// URL pattern regexes for template detection
var (
	listingURLRe    = regexp.MustCompile(`(?i)/categor|/collections?/|/shop/|/archive|/tags?/|/browse|/listings?/|/page/\d+|\?page=\d+|/classified|/results/|/strona-\d+|/pagina-\d+|/seite-\d+`)
	listingParamsRe = regexp.MustCompile(`(?i)[?&](sort|order|filter|price|brand|make|category)=`)
	productURLRe    = regexp.MustCompile(`(?i)/products?/|/items?/|/p/|/pd/|/sku/|/goods/|/products/[^/]+$`)
	adDetailURLRe   = regexp.MustCompile(`(?i)/(oferta|offer|annonce|anuncio|oglas|ilan|inserat)/[^/?]+|/d/oferta/|/marketplace/item/|/(itm|item|listing)/[^/]*-?\d{5,}|/classified/[^/?]+`)
	articleURLRe = regexp.MustCompile(`(?i)/blog/|/articles?/|/posts?/|/news/|/stories/|/journal/|/magazine/|/editorial/|/\d{4}/\d{2}/`)
	legalURLRe   = regexp.MustCompile(`(?i)/privacy|/terms|/tos|/legal|/disclaimer|/cookie|/gdpr|/imprint|/impressum|/agb|/datenschutz|/conditions|/compliance|/policy|/terms-of-service|/privacy-policy`)
	contactURLRe = regexp.MustCompile(`(?i)/contacts?/|/contacto/|/kontakt|/get-in-touch|/reach-us|/support|/help|/feedback`)
	faqURLRe     = regexp.MustCompile(`(?i)/faqs?/|/frequently-asked|/help-center|/knowledge-base|/kb/|/questions`)
	searchURLRe  = regexp.MustCompile(`(?i)/search|/results|\?q=|\?query=|\?search=|\?s=|\?keyword=`)
	loginURLRe   = regexp.MustCompile(`(?i)/login|/signin|/sign-in|/log-in|/register|/signup|/sign-up|/auth|/authenticate|/account/login|/my-account`)

	h1FAQRe     = regexp.MustCompile(`(?i)\b(faq|frequently\s+asked|questions)\b`)
	h1ContactRe = regexp.MustCompile(`(?i)\b(contact|get\s+in\s+touch|reach\s+us)\b`)
	h1LoginRe   = regexp.MustCompile(`(?i)\b(login|log\s+in|sign\s+in|register|sign\s+up|create\s+account)\b`)
	h1SearchRe  = regexp.MustCompile(`(?i)\b(search\s+results?|results?\s+for)\b`)
	h1LegalRe   = regexp.MustCompile(`(?i)\b(privacy|terms|legal|disclaimer|cookie)\b`)
)

// DetectTemplateType classifies a page into a template type using weighted heuristic scoring.
func DetectTemplateType(page *types.PageData, seedURL string) string {
	scores := map[string]int{
		TemplateHomepage: 0,
		TemplateListing:  0,
		TemplateProduct:  0,
		TemplateAdDetail: 0,
		TemplateArticle:  0,
		TemplateLegal:    0,
		TemplateContact:  0,
		TemplateFAQ:      0,
		TemplateSearch:   0,
		TemplateLogin:    0,
	}

	urlToCheck := page.FinalURL
	if urlToCheck == "" {
		urlToCheck = page.URL
	}

	// Homepage: exact root path on seed domain
	if isHomepagePath(urlToCheck, seedURL) {
		return TemplateHomepage
	}

	// URL patterns (weight 3)
	if listingURLRe.MatchString(urlToCheck) {
		scores[TemplateListing] += 3
	}
	// Filter/sort query params are weaker signals (common in non-listing contexts too)
	if listingParamsRe.MatchString(urlToCheck) {
		scores[TemplateListing] += 2
	}
	if adDetailURLRe.MatchString(urlToCheck) {
		scores[TemplateAdDetail] += 3
	}
	if productURLRe.MatchString(urlToCheck) {
		scores[TemplateProduct] += 3
	}
	if articleURLRe.MatchString(urlToCheck) {
		scores[TemplateArticle] += 3
	}
	if legalURLRe.MatchString(urlToCheck) {
		scores[TemplateLegal] += 3
	}
	if contactURLRe.MatchString(urlToCheck) {
		scores[TemplateContact] += 3
	}
	if faqURLRe.MatchString(urlToCheck) {
		scores[TemplateFAQ] += 3
	}
	if searchURLRe.MatchString(urlToCheck) {
		scores[TemplateSearch] += 3
	}
	if loginURLRe.MatchString(urlToCheck) {
		scores[TemplateLogin] += 3
	}

	// Structured data (weight 2-3)
	for _, sd := range page.StructuredData {
		sdType := strings.ToLower(sd.Type)
		switch {
		case sdType == "product" || sdType == "offer" || sdType == "aggregateoffer" || sdType == "productgroup":
			scores[TemplateProduct] += 3
		case sdType == "vehicle" || sdType == "car":
			scores[TemplateAdDetail] += 3
		case sdType == "realestatelisting" || sdType == "jobposting":
			scores[TemplateAdDetail] += 3
		case sdType == "apartment" || sdType == "singlefamilyresidence" || sdType == "house":
			scores[TemplateAdDetail] += 2
		case sdType == "article" || sdType == "newsarticle" || sdType == "blogposting" || sdType == "techarticle":
			scores[TemplateArticle] += 3
		case sdType == "faqpage" || sdType == "qapage" || sdType == "question":
			scores[TemplateFAQ] += 3
		case sdType == "searchresultspage":
			scores[TemplateSearch] += 3
		case sdType == "collectionpage" || sdType == "itemlist":
			scores[TemplateListing] += 3
		case sdType == "contactpage":
			scores[TemplateContact] += 3
		}
	}

	// OG type (weight 2)
	if ogType, ok := page.OpenGraph["type"]; ok {
		switch strings.ToLower(ogType) {
		case "article", "blog":
			scores[TemplateArticle] += 2
		case "product", "product.item":
			scores[TemplateProduct] += 2
		case "website":
			if page.Depth == 0 {
				scores[TemplateHomepage] += 1
			}
		}
	}

	// Content metrics (weight 1-2)
	if page.WordCount > 800 {
		scores[TemplateArticle] += 2
	} else if page.WordCount > 400 {
		scores[TemplateArticle] += 1
	}
	if page.WordCount < 300 && len(page.InternalLinks) > 15 {
		scores[TemplateListing] += 2
	}
	if page.WordCount >= 100 && page.WordCount <= 600 && len(page.Images) >= 2 {
		scores[TemplateProduct] += 1
	}
	if page.WordCount < 100 && page.Depth > 1 {
		scores[TemplateLogin] += 1
		scores[TemplateSearch] += 1
	}

	// Link patterns (weight 1-2)
	internalCount := len(page.InternalLinks)
	if internalCount == 0 {
		internalCount = page.InternalLinkCount
	}
	externalCount := len(page.ExternalLinks)
	if externalCount == 0 {
		externalCount = page.ExternalLinkCount
	}
	if internalCount > 30 {
		scores[TemplateListing] += 2
		scores[TemplateHomepage] += 1
	} else if internalCount > 15 {
		scores[TemplateListing] += 1
	}
	if internalCount < 5 && page.WordCount > 200 {
		scores[TemplateLegal] += 1
	}
	if externalCount > 3 && page.WordCount > 500 {
		scores[TemplateArticle] += 1
	}

	// Heading patterns (weight 1-3)
	h1Count := len(page.Headings.H1)
	h2Count := len(page.Headings.H2)
	h3Count := len(page.Headings.H3)
	if h1Count == 1 && h2Count >= 3 {
		scores[TemplateArticle] += 2
	}
	if h2Count >= 5 || h3Count >= 8 {
		scores[TemplateFAQ] += 1
	}

	// HTML listing signals from extractor (weight 3)
	if page.ListingSignals >= 1 {
		scores[TemplateListing] += 3
	}

	// Ad detail signals from extractor (weight 3)
	// Positive = classifieds/marketplace signals, negative = e-commerce signals
	if page.AdDetailSignals > 0 {
		scores[TemplateAdDetail] += 3
	}
	if page.AdDetailSignals < 0 {
		// E-commerce signals detected — boost product, suppress ad_detail
		scores[TemplateProduct] += 3
		scores[TemplateAdDetail] -= 2
	}

	// Ad detail content heuristic: moderate text + many images = typical ad page
	if page.WordCount >= 100 && page.WordCount <= 800 && len(page.Images) >= 5 {
		scores[TemplateAdDetail] += 1
	}

	// Link density heuristic: many internal links relative to text content (weight 2).
	// Threshold: < 30 words per internal link indicates a link-heavy page (typical listings
	// have 10-25 words per item link; articles average 100+ words per link).
	if internalCount > 20 && page.WordCount > 0 && page.WordCount < internalCount*30 {
		scores[TemplateListing] += 2
	}

	// Sibling URL heuristic: group internal links by parent path (weight 2).
	// Threshold >= 15 to avoid false positives from site-wide nav menus (typically 5-12 items).
	if internalCount >= 15 {
		parentGroups := make(map[string]int)
		for _, link := range page.InternalLinks {
			if parsed, err := url.Parse(link); err == nil {
				path := parsed.Path
				if idx := strings.LastIndex(path, "/"); idx > 0 {
					parentGroups[path[:idx]]++
				}
			}
		}
		maxGroup := 0
		for _, count := range parentGroups {
			if count > maxGroup {
				maxGroup = count
			}
		}
		if maxGroup >= 15 {
			scores[TemplateListing] += 2
		}
	}

	// H1 text analysis
	if h1Count > 0 {
		h1Text := page.Headings.H1[0]
		if h1FAQRe.MatchString(h1Text) {
			scores[TemplateFAQ] += 3
		}
		if h1ContactRe.MatchString(h1Text) {
			scores[TemplateContact] += 3
		}
		if h1LoginRe.MatchString(h1Text) {
			scores[TemplateLogin] += 3
		}
		if h1SearchRe.MatchString(h1Text) {
			scores[TemplateSearch] += 3
		}
		if h1LegalRe.MatchString(h1Text) {
			scores[TemplateLegal] += 2
		}
	}

	// Find best
	bestType := TemplateOther
	bestScore := 0
	for tpl, score := range scores {
		if score > bestScore {
			bestScore = score
			bestType = tpl
		}
	}
	if bestScore < 2 {
		return TemplateOther
	}
	return bestType
}

func isHomepagePath(pageURL, seedURL string) bool {
	// Check if the page's path is exactly "/"
	// Simple check: strip scheme+host, compare path
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(pageURL, prefix) {
			rest := pageURL[len(prefix):]
			slashIdx := strings.Index(rest, "/")
			if slashIdx < 0 || rest[slashIdx:] == "/" {
				return true
			}
			return false
		}
	}
	return false
}

// BuildTemplateStats counts template type distribution across pages.
func BuildTemplateStats(pages []*types.PageData) map[string]int {
	dist := make(map[string]int)
	for _, p := range pages {
		if p.TemplateType != "" {
			dist[p.TemplateType]++
		}
	}
	return dist
}
