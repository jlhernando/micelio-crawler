package types

// Soft404Probe is the answer to a request for a URL that cannot exist on a
// host. A 200 means the site serves "not found" pages as soft 404s; the
// fingerprint lets analysis find crawled pages that look the same.
type Soft404Probe struct {
	Host           string `json:"host"`
	URL            string `json:"url"`
	StatusCode     int    `json:"statusCode"`
	FinalURL       string `json:"finalUrl,omitempty"`
	Title          string `json:"title,omitempty"`
	WordCount      int    `json:"wordCount,omitempty"`
	DOMFingerprint uint64 `json:"domFingerprint,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Soft404 confidence levels.
const (
	Soft404High   = "high"
	Soft404Medium = "medium"
	Soft404Low    = "low"
)

// Soft404 reasons.
const (
	Soft404MatchesErrorPage = "matches-error-page"   // looks like the site's own "not found" page
	Soft404EmptyListing     = "empty-listing"        // listing with no product/ad links while its peers have them
	Soft404ErrorText        = "error-text"           // "not found" / "0 results" wording in title or H1
	Soft404ErrorTextThin    = "error-text-thin-body" // that wording in a page with very little text
	Soft404RedirectHome     = "redirects-to-home"    // deep URL redirected to the home page
	Soft404MassRedirect     = "mass-redirect"        // many URLs redirected to the same page
	Soft404ThinForTemplate  = "thin-for-template"    // far less text than pages of the same template
)

// Soft404Signal is a page's soft-404 assessment. It is a candidate for
// review, not a verdict: Search Console's page indexing report confirms.
type Soft404Signal struct {
	Confidence string   `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

// Soft404Candidate is one page in Soft404Stats.
type Soft404Candidate struct {
	URL        string   `json:"url"`
	StatusCode int      `json:"statusCode"`
	Confidence string   `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

// Soft404Stats summarises soft-404 candidates for a crawl.
type Soft404Stats struct {
	Total        int                `json:"total"`
	ByConfidence map[string]int     `json:"byConfidence"`
	ByReason     map[string]int     `json:"byReason"`
	Candidates   []Soft404Candidate `json:"candidates"` // most confident first, capped
	Probes       []Soft404Probe     `json:"probes,omitempty"`
}
