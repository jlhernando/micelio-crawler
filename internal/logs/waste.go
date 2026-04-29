package logs

import (
	"regexp"
	"strings"
)

// WasteType categorizes crawl budget waste.
type WasteType string

const (
	WasteFacetedNav   WasteType = "faceted_nav"
	WastePagination   WasteType = "pagination"
	WasteSessionIDs   WasteType = "session_ids"
	WasteSearch       WasteType = "search"
	WasteCalendar     WasteType = "calendar"
	WasteResources    WasteType = "resources"
	WasteAPI          WasteType = "api"
	WasteTaxonomy     WasteType = "taxonomy"
)

// WasteEntry tracks waste for a single URL pattern.
type WasteEntry struct {
	Type     WasteType `json:"type"`
	Hits     int64     `json:"hits"`
	URLs     int       `json:"urls"`
	TopURLs  []string  `json:"topUrls"`
	BotHits  int64     `json:"botHits"`
}

// WasteAnalysis holds the results of crawl waste detection.
type WasteAnalysis struct {
	TotalBotHits  int64                   `json:"totalBotHits"`
	WasteHits     int64                   `json:"wasteHits"`
	WasteRatio    float64                 `json:"wasteRatio"`
	ByType        map[WasteType]*WasteEntry `json:"byType"`
}

var (
	// Faceted navigation params
	reFaceted = regexp.MustCompile(`[?&](color|size|sort|order|filter|brand|material|style|type|category|min|max|page_size|per_page|view|display|layout)=`)
	// Pagination patterns
	rePagination = regexp.MustCompile(`(/page/\d+|[?&]page=\d+|[?&]p=\d+|[?&]offset=\d+|[?&]start=\d+)`)
	// Session/tracking IDs
	reSessionIDs = regexp.MustCompile(`[?&](sid|session|sessionid|jsessionid|phpsessid|token|nonce|csrf|fbclid|gclid|msclkid|_ga|_gl|ref|affiliate|trk)=|[?&](utm_|mc_)\w+=`)
	// Internal search
	reSearch = regexp.MustCompile(`[?&](q|query|search|s|keyword|term|text)=|/search[/?]`)
	// Calendar traps
	reCalendar = regexp.MustCompile(`/\d{4}/\d{2}(/\d{2})?/?$|[?&](year|month|date|day)=\d`)
	// Resource files
	reResources = regexp.MustCompile(`\.(css|js|woff2?|ttf|eot|svg|png|jpg|jpeg|gif|webp|ico|mp4|mp3|pdf|zip|gz|map|json)(\?|$)`)
	// API endpoints
	reAPI = regexp.MustCompile(`/(api|graphql|rest|v\d+)/`)
	// Taxonomy
	reTaxonomy = regexp.MustCompile(`/(tag|tags|category|categories|topic|topics|archive|archives|author|label|labels)/`)
)

// ClassifyWaste returns the waste type for a URL, or empty string if not waste.
func ClassifyWaste(path string) WasteType {
	lower := strings.ToLower(path)
	if reResources.MatchString(lower) {
		return WasteResources
	}
	if reAPI.MatchString(lower) {
		return WasteAPI
	}
	if reSessionIDs.MatchString(lower) {
		return WasteSessionIDs
	}
	if reFaceted.MatchString(lower) {
		return WasteFacetedNav
	}
	if reSearch.MatchString(lower) {
		return WasteSearch
	}
	if rePagination.MatchString(lower) {
		return WastePagination
	}
	if reCalendar.MatchString(lower) {
		return WasteCalendar
	}
	if reTaxonomy.MatchString(lower) {
		return WasteTaxonomy
	}
	return ""
}

// AnalyzeWaste classifies URL stats into waste categories.
func AnalyzeWaste(urls []URLStat, totalBotHits int64) *WasteAnalysis {
	wa := &WasteAnalysis{
		TotalBotHits: totalBotHits,
		ByType:       make(map[WasteType]*WasteEntry),
	}
	for _, u := range urls {
		wt := ClassifyWaste(u.Path)
		if wt == "" {
			continue
		}
		e, ok := wa.ByType[wt]
		if !ok {
			e = &WasteEntry{Type: wt}
			wa.ByType[wt] = e
		}
		e.Hits += u.Hits
		e.BotHits += u.BotHits
		e.URLs++
		if len(e.TopURLs) < 5 {
			e.TopURLs = append(e.TopURLs, u.Path)
		}
		wa.WasteHits += u.BotHits
	}
	if totalBotHits > 0 {
		wa.WasteRatio = float64(wa.WasteHits) / float64(totalBotHits)
	}
	return wa
}
