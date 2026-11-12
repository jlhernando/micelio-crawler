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

func decodeAnalysisPage(data []byte) (*types.PageData, error) {
	page := new(types.PageData)
	projection := struct {
		*analysisPageData
		InternalLinks stringArrayCount `json:"internalLinks"`
		ExternalLinks stringArrayCount `json:"externalLinks"`
	}{analysisPageData: (*analysisPageData)(page)}
	if err := json.Unmarshal(data, &projection); err != nil {
		return nil, err
	}
	if page.InternalLinkCount == 0 {
		page.InternalLinkCount = int(projection.InternalLinks)
	}
	if page.ExternalLinkCount == 0 {
		page.ExternalLinkCount = int(projection.ExternalLinks)
	}
	return page, nil
}

// pageInterner shares the string data that repeats across reloaded pages
// (menu and footer anchors, logo images, hreflang sets, Open Graph values)
// and trims the spare capacity encoding/json leaves on decoded slices. Only
// memory changes: every field keeps its value, nil and empty slices stay as
// they are. One interner per load, dropped with it. The table is capped
// (intern.DefaultMax) so mostly-unique strings such as content links and
// per-product JSON-LD cannot make it grow with the crawl size.
type pageInterner struct{ t *intern.Table }

func newPageInterner() pageInterner { return pageInterner{intern.New(0)} }

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
