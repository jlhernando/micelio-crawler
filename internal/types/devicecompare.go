package types

// DeviceSnapshot is what one device saw on a page, enough to compare two
// devices without keeping both pages.
type DeviceSnapshot struct {
	StatusCode     int      `json:"status"`
	FinalURL       string   `json:"finalUrl,omitempty"`
	Title          string   `json:"title,omitempty"`
	Description    string   `json:"description,omitempty"`
	Canonical      string   `json:"canonical,omitempty"`
	MetaRobots     string   `json:"metaRobots,omitempty"`
	H1             []string `json:"h1,omitempty"`
	H2Count        int      `json:"h2Count,omitempty"`
	WordCount      int      `json:"wordCount"`
	InternalLinks  int      `json:"internalLinks"`
	SchemaTypes    []string `json:"schemaTypes,omitempty"`
	Hreflang       int      `json:"hreflang,omitempty"`
	Images         int      `json:"images,omitempty"`
	ImagesNoAlt    int      `json:"imagesNoAlt,omitempty"`
	Viewport       bool     `json:"viewport"` // <meta name="viewport"> present
	Bytes          int64    `json:"bytes,omitempty"`
	JSErrors       int      `json:"jsErrors,omitempty"`
	Rendered       bool     `json:"rendered,omitempty"` // taken from the JS-rendered DOM
	ResponseTimeMs int64    `json:"responseTimeMs,omitempty"`
	VaryUserAgent  bool     `json:"varyUserAgent,omitempty"`
	Error          string   `json:"error,omitempty"`
	// Raw is the same page from the fetched HTML, before JavaScript, when
	// this snapshot comes from the rendered DOM.
	Raw *DeviceSnapshot `json:"raw,omitempty"`
}

// DeviceComparison is a page seen by the crawl's device (Primary) and by
// its compare device (Secondary). Diffs lists the DeviceDiff* ids that
// differ.
type DeviceComparison struct {
	Device         string         `json:"device"`                  // compare device id
	PrimaryDevice  string         `json:"primaryDevice,omitempty"` // crawl device id, "" = Micelio bot
	Primary        DeviceSnapshot `json:"primary"`
	Secondary      DeviceSnapshot `json:"secondary"`
	Diffs          []string       `json:"diffs,omitempty"`
	TextSimilarity int            `json:"textSimilarity"` // 0-100, simhash of the body text
	// Internal links only one device sees: up to MaxDeviceLinkSamples
	// each, with the full counts.
	LinksOnlyPrimary        []string `json:"linksOnlyPrimary,omitempty"`
	LinksOnlySecondary      []string `json:"linksOnlySecondary,omitempty"`
	LinksOnlyPrimaryCount   int      `json:"linksOnlyPrimaryCount,omitempty"`
	LinksOnlySecondaryCount int      `json:"linksOnlySecondaryCount,omitempty"`
	// LinksParamOnly counts the one-sided links whose path the other device
	// links too, with other query parameters.
	LinksParamOnly int `json:"linksParamOnly,omitempty"`
	// With JS rendering: RawDiffs are the differences between the two
	// devices in the fetched HTML (Diffs compares the rendered DOMs), and
	// PrimaryJS/SecondaryJS what JavaScript changes on each device (same
	// ids), with the internal links only the rendered DOM has.
	RawDiffs         []string `json:"rawDiffs,omitempty"`
	PrimaryJS        []string `json:"primaryJs,omitempty"`
	SecondaryJS      []string `json:"secondaryJs,omitempty"`
	PrimaryJSLinks   int      `json:"primaryJsLinks,omitempty"`
	SecondaryJSLinks int      `json:"secondaryJsLinks,omitempty"`
}

// MaxDeviceLinkSamples caps the link lists kept per page.
const MaxDeviceLinkSamples = 20

// Device comparison differences. Each is also a pages issue filter.
const (
	DeviceDiffStatus      = "device-status"      // different HTTP status
	DeviceDiffRedirect    = "device-redirect"    // ends on a different URL
	DeviceDiffCanonical   = "device-canonical"   // different canonical
	DeviceDiffRobots      = "device-robots"      // different meta robots
	DeviceDiffTitle       = "device-title"       // different title
	DeviceDiffDescription = "device-description" // different meta description
	DeviceDiffH1          = "device-h1"          // different h1
	DeviceDiffContent     = "device-content"     // main text differs a lot
	DeviceDiffLinks       = "device-links"       // internal links only one device sees
	DeviceDiffSchema      = "device-schema"      // different structured data types
	DeviceDiffHreflang    = "device-hreflang"    // different number of hreflang links
	DeviceNoViewport      = "device-no-viewport" // a mobile/tablet device got no meta viewport
	DeviceFetchFailed     = "device-failed"      // the compare device could not fetch the page

	// Pages issue filters for rendered crawls (not entries of Diffs).
	DeviceFilterJS  = "device-js"  // JavaScript changes SEO elements on either device
	DeviceFilterRaw = "device-raw" // the fetched HTML differs between the devices
)

// DeviceDiffLabels are short descriptions for reports and the dashboard.
var DeviceDiffLabels = map[string]string{
	DeviceDiffStatus:      "different HTTP status",
	DeviceDiffRedirect:    "redirects to a different URL",
	DeviceDiffCanonical:   "different canonical",
	DeviceDiffRobots:      "different meta robots",
	DeviceDiffTitle:       "different title",
	DeviceDiffDescription: "different meta description",
	DeviceDiffH1:          "different h1",
	DeviceDiffContent:     "different main content",
	DeviceDiffLinks:       "internal links only one device sees",
	DeviceDiffSchema:      "different structured data",
	DeviceDiffHreflang:    "different hreflang",
	DeviceNoViewport:      "mobile page without meta viewport",
	DeviceFetchFailed:     "compare device could not fetch the page",
}

// DeviceParityStats summarises a device comparison crawl.
type DeviceParityStats struct {
	Primary   string `json:"primary"`   // device id ("" = Micelio bot)
	Secondary string `json:"secondary"` // compare device id
	Rendered  bool   `json:"rendered"`  // JS rendering was on
	Compared  int    `json:"compared"`
	Identical int    `json:"identical"` // no difference at all
	// Issues counts pages per DeviceDiff id.
	Issues map[string]int `json:"issues"`
	// Pages where links exist only on one side, and how many such links.
	LinksOnlyPrimary   int `json:"linksOnlyPrimary"`
	LinksOnlySecondary int `json:"linksOnlySecondary"`
	LinksParamOnly     int `json:"linksParamOnly"` // of those, same path with other query parameters
	AvgSimilarity      int `json:"avgSimilarity"`  // 0-100
	// Rendered crawls only: pages per difference in the fetched HTML, pages
	// whose fetched HTML is the same on both devices, and pages per change
	// JavaScript makes on each device.
	RawCompared  int            `json:"rawCompared,omitempty"` // pages with raw HTML on both devices
	RawIssues    map[string]int `json:"rawIssues,omitempty"`
	RawIdentical int            `json:"rawIdentical,omitempty"`
	JSChanged    int            `json:"jsChanged,omitempty"` // JS changes something on either device
	// Pages where JS changes something on each device.
	PrimaryJSPages   int `json:"primaryJsPages,omitempty"`
	SecondaryJSPages int `json:"secondaryJsPages,omitempty"`
	PrimaryJS    map[string]int `json:"primaryJs,omitempty"`
	SecondaryJS  map[string]int `json:"secondaryJs,omitempty"`
}
