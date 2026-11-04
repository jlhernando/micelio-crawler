package types

// Core Web Vitals and CDN reports, gathered when a report is exported (not
// during the crawl), so older crawls get them too.

// CWVMetric is one Core Web Vital for real users (CrUX, p75 over 28 days).
type CWVMetric struct {
	ID   string  `json:"id"`  // lcp, inp, cls, fcp, ttfb
	P75  float64 `json:"p75"` // ms, or unitless for CLS
	Good float64 `json:"good"`
	NI   float64 `json:"ni"` // needs improvement (share 0-1)
	Poor float64 `json:"poor"`
}

// CWVField is CrUX field data for one form factor.
type CWVField struct {
	FormFactor string      `json:"formFactor"` // phone, desktop, all
	Scope      string      `json:"scope"`      // origin or url
	Metrics    []CWVMetric `json:"metrics"`
}

// CWVLab is a Lighthouse run (PageSpeed Insights, mobile).
type CWVLab struct {
	URL   string  `json:"url"`
	Score float64 `json:"score"` // 0-100
	LCP   float64 `json:"lcp"`   // ms
	TBT   float64 `json:"tbt"`   // ms
	CLS   float64 `json:"cls"`
	FCP   float64 `json:"fcp"`
	SI    float64 `json:"si"`
	TTFB  float64 `json:"ttfb"`
}

// CWVReport is what the export found about Core Web Vitals.
type CWVReport struct {
	Origin string     `json:"origin"`
	Field  []CWVField `json:"field,omitempty"`
	Lab    *CWVLab    `json:"lab,omitempty"`
	Source string     `json:"source"` // crux, psi, none
	// Why there is no data: "no-key", "no-data" (site too small for
	// CrUX), or an error.
	Missing string `json:"missing,omitempty"`
}

// CDNCache is the cache result for one request.
type CDNCache struct {
	URL          string `json:"url"`
	Status       string `json:"status"` // HIT, MISS, DYNAMIC, BYPASS, EXPIRED, ... or ""
	CacheControl string `json:"cacheControl"`
	Age          int    `json:"age"`
	TTFBMs       int64  `json:"ttfbMs"`
	Code         int    `json:"code"`
}

// CDNReport is what the export found about the site's CDN and caching.
type CDNReport struct {
	URL         string     `json:"url"`
	Provider    string     `json:"provider"` // "" when none was detected
	Evidence    string     `json:"evidence"` // the header that gave it away
	HTML        []CDNCache `json:"html"`     // the page, twice
	Asset       *CDNCache  `json:"asset,omitempty"`
	Compression string     `json:"compression"` // br, zstd, gzip or ""
	Protocol    string     `json:"protocol"`    // HTTP/2.0, ...
	HTTP3       bool       `json:"http3"`
	EarlyHints  bool       `json:"earlyHints"`
	HSTS        bool       `json:"hsts"`
	Error       string     `json:"error,omitempty"`
}
