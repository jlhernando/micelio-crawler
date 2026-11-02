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
}
