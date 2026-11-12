package storage

import (
	"bytes"
	"encoding/json"

	"github.com/micelio/micelio/internal/intern"
	"github.com/micelio/micelio/internal/types"
)

// stringArrayCount validates like []string, but does not allocate a string and
// slice entry for every link that the analysis loader immediately discards.
// encoding/json validates syntax before invoking this method.
type stringArrayCount int

func (c *stringArrayCount) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	fallback := func() error {
		var values []string
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		*c = stringArrayCount(len(values))
		return nil
	}
	if bytes.Equal(data, []byte("null")) {
		*c = 0
		return nil
	}
	if len(data) < 2 || data[0] != '[' {
		return fallback()
	}
	n := 0
	for i := 1; i < len(data); {
		for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
			i++
		}
		if i >= len(data) {
			return fallback()
		}
		if data[i] == ']' {
			*c = stringArrayCount(n)
			return nil
		}
		switch data[i] {
		case '"':
			i++
			for i < len(data) {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '"' {
					i++
					break
				}
				i++
			}
		case 'n':
			if !bytes.HasPrefix(data[i:], []byte("null")) {
				return fallback()
			}
			i += 4
		default:
			return fallback()
		}
		n++
		for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
			i++
		}
		if i < len(data) && data[i] == ',' {
			i++
		}
	}
	return fallback()
}

type analysisPageData types.PageData

// skipJSON accepts any (already validated) JSON value and keeps nothing.
type skipJSON struct{}

func (*skipJSON) UnmarshalJSON([]byte) error { return nil }

// schemaEntryNoRaw decodes a structuredData entry without its raw JSON-LD
// text: analysis after a reload reads only Type and Format (Raw is read by
// schema validation at crawl time), and Raw is ~1/4 of a reloaded crawl's
// live heap. The outer decoder fills it directly (no second scan).
type schemaEntryNoRaw struct {
	Type   string                     `json:"type"`
	Format types.StructuredDataFormat `json:"format"`
	Raw    skipJSON                   `json:"raw"`
}

// schemaEntries converts decoded entries; nil and empty keep their shape.
func schemaEntries(in []schemaEntryNoRaw) []types.StructuredDataEntry {
	if in == nil {
		return nil
	}
	out := make([]types.StructuredDataEntry, len(in))
	for i, e := range in {
		out[i] = types.StructuredDataEntry{Type: e.Type, Format: e.Format}
	}
	return out
}

// pageLoadOpts selects what a reload leaves undecoded.
type pageLoadOpts struct {
	skipAnchors   bool // Anchors stays nil
	dropSchemaRaw bool // StructuredData[].Raw stays ""
	// cacheOnly (with dropSchemaRaw) also leaves undecoded what a results
	// load drops in stripPagesForCache before anything reads it: Images,
	// OpenGraph, TwitterCard, SchemaValidation, RenderDiffs (images alone
	// are ~1/4 of a crawl's page JSON).
	cacheOnly bool
	// imageSummary folds Images into ImageSummary (Images nil): post-crawl
	// analysis reads only counts and long alts from them.
	imageSummary bool
}

func decodeAnalysisPage(data []byte) (*types.PageData, error) {
	return decodeAnalysisPageOpt(data, pageLoadOpts{})
}

// decodeAnalysisPageOpt decodes like decodeAnalysisPage; with skipAnchors the
// anchors array is validated but never built (Anchors stays nil), with
// dropSchemaRaw structured data entries keep Type and Format only.
func decodeAnalysisPageOpt(data []byte, o pageLoadOpts) (*types.PageData, error) {
	page := new(types.PageData)
	base := (*analysisPageData)(page)
	var in, ex stringArrayCount
	var err error
	switch {
	case o.cacheOnly && o.skipAnchors:
		p := struct {
			*analysisPageData
			InternalLinks    stringArrayCount   `json:"internalLinks"`
			ExternalLinks    stringArrayCount   `json:"externalLinks"`
			Anchors          skipJSON           `json:"anchors"`
			StructuredData   []schemaEntryNoRaw `json:"structuredData"`
			Images           skipJSON           `json:"images"`
			OpenGraph        skipJSON           `json:"openGraph"`
			TwitterCard      skipJSON           `json:"twitterCard"`
			SchemaValidation skipJSON           `json:"schemaValidation"`
			RenderDiffs      skipJSON           `json:"renderDiffs"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex, page.StructuredData = p.InternalLinks, p.ExternalLinks, schemaEntries(p.StructuredData)
	case o.cacheOnly:
		p := struct {
			*analysisPageData
			InternalLinks    stringArrayCount   `json:"internalLinks"`
			ExternalLinks    stringArrayCount   `json:"externalLinks"`
			StructuredData   []schemaEntryNoRaw `json:"structuredData"`
			Images           skipJSON           `json:"images"`
			OpenGraph        skipJSON           `json:"openGraph"`
			TwitterCard      skipJSON           `json:"twitterCard"`
			SchemaValidation skipJSON           `json:"schemaValidation"`
			RenderDiffs      skipJSON           `json:"renderDiffs"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex, page.StructuredData = p.InternalLinks, p.ExternalLinks, schemaEntries(p.StructuredData)
	case o.skipAnchors && o.dropSchemaRaw:
		p := struct {
			*analysisPageData
			InternalLinks  stringArrayCount   `json:"internalLinks"`
			ExternalLinks  stringArrayCount   `json:"externalLinks"`
			Anchors        skipJSON           `json:"anchors"`
			StructuredData []schemaEntryNoRaw `json:"structuredData"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex, page.StructuredData = p.InternalLinks, p.ExternalLinks, schemaEntries(p.StructuredData)
	case o.dropSchemaRaw:
		p := struct {
			*analysisPageData
			InternalLinks  stringArrayCount   `json:"internalLinks"`
			ExternalLinks  stringArrayCount   `json:"externalLinks"`
			StructuredData []schemaEntryNoRaw `json:"structuredData"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex, page.StructuredData = p.InternalLinks, p.ExternalLinks, schemaEntries(p.StructuredData)
	case o.skipAnchors:
		p := struct {
			*analysisPageData
			InternalLinks stringArrayCount `json:"internalLinks"`
			ExternalLinks stringArrayCount `json:"externalLinks"`
			Anchors       skipJSON         `json:"anchors"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex = p.InternalLinks, p.ExternalLinks
	default:
		p := struct {
			*analysisPageData
			InternalLinks stringArrayCount `json:"internalLinks"`
			ExternalLinks stringArrayCount `json:"externalLinks"`
		}{analysisPageData: base}
		err = json.Unmarshal(data, &p)
		in, ex = p.InternalLinks, p.ExternalLinks
	}
	page.InternalLinkCount, page.ExternalLinkCount = linkCounts(page, in, ex)
	if err != nil {
		return nil, err
	}
	return page, nil
}

// linkCounts keeps stored counts and falls back to the array lengths.
func linkCounts(page *types.PageData, internal, external stringArrayCount) (int, int) {
	in, ex := page.InternalLinkCount, page.ExternalLinkCount
	if in == 0 {
		in = int(internal)
	}
	if ex == 0 {
		ex = int(external)
	}
	return in, ex
}

// pageInterner shares the string data that repeats across reloaded pages
// (menu and footer anchors, logo images, hreflang sets, Open Graph values)
// and trims the spare capacity encoding/json leaves on decoded slices. Only
// memory changes: every field keeps its value, nil and empty slices stay as
// they are. One interner per load, dropped with it. The table is capped
// (intern.DefaultMax) so mostly-unique strings such as content links and
// per-product JSON-LD cannot make it grow with the crawl size. Headings get
// their own table: a 100K-page site crawl held 1.1M distinct headings, which
// would fill the shared cap and leave later repeated strings (image srcs,
// hreflang, Open Graph) un-interned.
type pageInterner struct{ t, heads *intern.Table }

func newPageInterner() pageInterner { return pageInterner{intern.New(0), intern.New(0)} }

func (in pageInterner) str(s string) string { return in.t.Str(s) }

func clip[T any](s []T) []T { return intern.Clip(s) }

func (in pageInterner) compact(p *types.PageData) {
	p.Anchors = clip(p.Anchors)
	for i := range p.Anchors {
		a := &p.Anchors[i]
		a.Href = in.str(a.Href)
		a.Text = in.str(a.Text)
		a.Position = types.LinkPosition(in.str(string(a.Position)))
	}
	// Template headings (menus, "Related products", footers) repeat on
	// most pages: a 100K-page site crawl held 6.8M headings, 1.1M distinct.
	h := &p.Headings
	for _, hs := range []*[]string{&h.H1, &h.H2, &h.H3, &h.H4, &h.H5, &h.H6} {
		*hs = clip(*hs)
		for i, v := range *hs {
			(*hs)[i] = in.heads.Str(v)
		}
	}
	p.Images = clip(p.Images)
	for i := range p.Images {
		p.Images[i].Src = in.str(p.Images[i].Src)
	}
	p.Hreflang = clip(p.Hreflang)
	for i := range p.Hreflang {
		h := &p.Hreflang[i]
		h.Lang = in.str(h.Lang)
		h.Href = in.str(h.Href)
		h.Source = in.str(h.Source)
	}
	p.StructuredData = clip(p.StructuredData)
	for i := range p.StructuredData {
		d := &p.StructuredData[i]
		d.Type = in.str(d.Type)
		d.Format = types.StructuredDataFormat(in.str(string(d.Format)))
		d.Raw = in.str(d.Raw)
	}
	p.SchemaValidation = clip(p.SchemaValidation)
	for i := range p.SchemaValidation {
		v := &p.SchemaValidation[i]
		v.Type = in.str(v.Type)
		v.Format = types.StructuredDataFormat(in.str(string(v.Format)))
		v.Issues = clip(v.Issues)
	}
	p.Segments = clip(p.Segments)
	for i, s := range p.Segments {
		p.Segments[i] = in.str(s)
	}
	p.URLIssues = clip(p.URLIssues)
	p.RedirectChain = clip(p.RedirectChain)
	p.RenderDiffs = clip(p.RenderDiffs)
	p.TruncatedElements = clip(p.TruncatedElements)
	for k, v := range p.OpenGraph {
		p.OpenGraph[k] = in.str(v)
	}
	for k, v := range p.TwitterCard {
		p.TwitterCard[k] = in.str(v)
	}
	p.TemplateType = in.str(p.TemplateType)
	p.HTMLLang = in.str(p.HTMLLang)
	p.URLClassification = in.str(p.URLClassification)
}
