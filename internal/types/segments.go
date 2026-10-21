package types

// LinkZoneCounts counts the links on a page by the page region they sit in.
// Computed at extraction time because anchors are dropped before analysis.
type LinkZoneCounts struct {
	Content    int `json:"content,omitempty"`
	Navigation int `json:"navigation,omitempty"`
	Header     int `json:"header,omitempty"`
	Footer     int `json:"footer,omitempty"`
	Sidebar    int `json:"sidebar,omitempty"`
	Breadcrumb int `json:"breadcrumb,omitempty"`
	Comments   int `json:"comments,omitempty"`
	Related    int `json:"related,omitempty"`
	Other      int `json:"other,omitempty"`
}

// Total returns the number of links across all zones.
func (z *LinkZoneCounts) Total() int {
	if z == nil {
		return 0
	}
	return z.Content + z.Navigation + z.Header + z.Footer + z.Sidebar + z.Breadcrumb + z.Comments + z.Related + z.Other
}

// Boilerplate returns the number of links in site-wide chrome (nav, header, footer, sidebar).
func (z *LinkZoneCounts) Boilerplate() int {
	if z == nil {
		return 0
	}
	return z.Navigation + z.Header + z.Footer + z.Sidebar
}

// Add increments the counter for a link position.
func (z *LinkZoneCounts) Add(pos LinkPosition) {
	switch pos {
	case LinkPosContent:
		z.Content++
	case LinkPosNavigation:
		z.Navigation++
	case LinkPosHeader:
		z.Header++
	case LinkPosFooter:
		z.Footer++
	case LinkPosSidebar:
		z.Sidebar++
	case LinkPosBreadcrumb:
		z.Breadcrumb++
	case LinkPosComments:
		z.Comments++
	case LinkPosRelated:
		z.Related++
	default:
		z.Other++
	}
}

// PageSegments holds the automatic segment value of a page for each dimension.
// Every value is a short label so pages can be grouped and filtered by it.
type PageSegments struct {
	Dir1        string `json:"dir1,omitempty"`
	Dir2        string `json:"dir2,omitempty"`
	Dir3        string `json:"dir3,omitempty"`
	Extension   string `json:"extension,omitempty"`
	Template    string `json:"template,omitempty"`
	TextAmount  string `json:"textAmount,omitempty"`
	Locale      string `json:"locale,omitempty"`
	URLParams   string `json:"urlParams,omitempty"`
	DepthBand   string `json:"depthBand,omitempty"`
	Authority   string `json:"authority,omitempty"`
	IndexState  string `json:"indexState,omitempty"`
	SchemaType  string `json:"schemaType,omitempty"`
	Subdomain   string `json:"subdomain,omitempty"`
	Freshness   string `json:"freshness,omitempty"`
	LinkProfile string `json:"linkProfile,omitempty"`
}

// SegmentDimensions lists the automatic segment dimensions in display order.
var SegmentDimensions = []string{
	"template", "dir1", "dir2", "dir3", "extension", "textAmount", "linkProfile",
	"locale", "urlParams", "depthBand", "authority", "indexState", "schemaType",
	"subdomain", "freshness",
}

// Value returns the segment value for a dimension key, or "" when unknown.
func (s *PageSegments) Value(dim string) string {
	if s == nil {
		return ""
	}
	switch dim {
	case "dir1":
		return s.Dir1
	case "dir2":
		return s.Dir2
	case "dir3":
		return s.Dir3
	case "extension":
		return s.Extension
	case "template":
		return s.Template
	case "textAmount":
		return s.TextAmount
	case "locale":
		return s.Locale
	case "urlParams":
		return s.URLParams
	case "depthBand":
		return s.DepthBand
	case "authority":
		return s.Authority
	case "indexState":
		return s.IndexState
	case "schemaType":
		return s.SchemaType
	case "subdomain":
		return s.Subdomain
	case "freshness":
		return s.Freshness
	case "linkProfile":
		return s.LinkProfile
	}
	return ""
}

// SegmentValueStats aggregates the pages that share one segment value.
type SegmentValueStats struct {
	Value         string  `json:"value"`
	Pages         int     `json:"pages"`
	IndexablePct  float64 `json:"indexablePct"`
	ErrorPct      float64 `json:"errorPct"`
	AvgWords      float64 `json:"avgWords"`
	AvgInlinks    float64 `json:"avgInlinks"`
	AvgResponseMs int64   `json:"avgResponseMs"`
	SampleURL     string  `json:"sampleUrl,omitempty"`
}

// SegmentDimensionStats holds the value breakdown for one dimension.
type SegmentDimensionStats struct {
	Dimension string              `json:"dimension"`
	Values    []SegmentValueStats `json:"values"`
	// Unassigned counts pages with no value for this dimension (data missing).
	Unassigned int `json:"unassigned,omitempty"`
	// TotalValues is the number of distinct values before capping Values.
	TotalValues int `json:"totalValues"`
}
