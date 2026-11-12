package crawler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/types"
)

// compareDevice fetches e a second time as the compare device (and renders
// it with JS rendering on), crawls the links only that device sees, and
// stores what differs from the crawl's device in page.DeviceCompare.
// primaryRendered is the crawl device's rendered DOM ("" without JS).
func (s *crawlSession) compareDevice(ctx context.Context, e types.QueueEntry, primary types.FetchResult, page *types.PageData, primaryRendered string) {
	dev := s.config.CompareDevice
	cmp := &types.DeviceComparison{Device: dev.ID}
	if s.config.Device != nil {
		cmp.PrimaryDevice = s.config.Device.ID
	}
	pv := s.deviceView(e, primary, page, primaryRendered)
	pv.snap.JSErrors = len(page.JSErrors)
	cmp.Primary = pv.snap
	cmp.PrimaryJS, cmp.PrimaryJSLinks = jsChanges(pv)

	ua := dev.UserAgent
	if s.config.RespectRobots && s.robotsChecker != nil && sameHost(e.URL, s.seedURL) && !s.robotsChecker.IsAllowed(e.URL, ua) {
		cmp.Secondary.Error = "blocked by robots.txt for " + dev.Label
		cmp.Diffs = []string{types.DeviceFetchFailed}
		page.DeviceCompare = cmp
		return
	}
	opts := s.fetchOpts
	opts.UserAgent = ua
	// No FollowRedirect: this fetch is never stored as a page, and a site
	// may send one device to another host (www -> m.example.com).
	res := FetchPage(ctx, e.URL, opts)
	if ctx.Err() != nil && res.StatusCode == 0 {
		return // stopping: leave the page without a comparison
	}
	if res.StatusCode == 0 || res.Error != "" && res.HTML == "" {
		cmp.Secondary.Error = res.Error
		if cmp.Secondary.Error == "" {
			cmp.Secondary.Error = "no response"
		}
		cmp.Diffs = []string{types.DeviceFetchFailed}
		page.DeviceCompare = cmp
		return
	}
	raw := s.extractPage(e, res)
	// Links only the compare device sees are part of the site too.
	s.enqueueLinks(e, &raw)

	var rendered string
	var jsErrors int
	if s.renderer != nil && res.HTML != "" && res.Error == "" && res.StatusCode == 200 {
		s.browserSem <- struct{}{}
		renderURL := e.URL
		if res.FinalURL != "" {
			renderURL = res.FinalURL
		}
		rr, err := s.renderer.RenderPageAs(ctx, renderURL, ua, dev, &browser.Prefetched{HTML: res.HTML, Headers: res.Headers})
		<-s.browserSem
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] %s render failed for %s: %v\n", dev.Label, e.URL, err)
		} else if rr != nil {
			rendered = rr.HTML
			jsErrors = len(rr.JSErrors)
		}
	}
	sv := s.deviceView(e, res, &raw, rendered)
	sv.snap.JSErrors = jsErrors
	cmp.Secondary = sv.snap
	cmp.SecondaryJS, cmp.SecondaryJSLinks = jsChanges(sv)

	compareViews(cmp, pv, sv)
	cmp.Diffs = deviceDiffs(cmp, s.config.Device, dev, len(pv.links), len(sv.links))
	if pv.raw != nil && sv.raw != nil {
		rc := &types.DeviceComparison{Primary: pv.raw.snap, Secondary: sv.raw.snap}
		compareViews(rc, *pv.raw, *sv.raw)
		cmp.RawDiffs = deviceDiffs(rc, s.config.Device, dev, len(pv.raw.links), len(sv.raw.links))
	}
	page.DeviceCompare = cmp
}

// compareViews fills c's one-sided links and text similarity from a and b.
func compareViews(c *types.DeviceComparison, a, b deviceSide) {
	c.LinksOnlyPrimary, c.LinksOnlyPrimaryCount = linksOnlyIn(a.links, b.links)
	c.LinksOnlySecondary, c.LinksOnlySecondaryCount = linksOnlyIn(b.links, a.links)
	c.LinksParamOnly = paramOnlyLinks(a.links, b.links) + paramOnlyLinks(b.links, a.links)
	c.TextSimilarity = 100
	if a.text != "" || b.text != "" {
		c.TextSimilarity = analysis.SimhashSimilarity(analysis.Simhash(a.text), analysis.Simhash(b.text))
	}
}

// jsChanges lists what JavaScript changes on one device (DeviceDiff ids of
// its fetched HTML vs its rendered DOM) and how many internal links only
// the rendered DOM has. Nothing without a rendered DOM.
func jsChanges(v deviceSide) ([]string, int) {
	if v.raw == nil {
		return nil, 0
	}
	c := &types.DeviceComparison{Primary: v.raw.snap, Secondary: v.snap}
	compareViews(c, *v.raw, v)
	return deviceDiffs(c, nil, nil, len(v.raw.links), len(v.links)), c.LinksOnlySecondaryCount
}

// deviceSide is what one device saw: its snapshot, internal links and body
// text, and with a rendered DOM the same from the fetched HTML (raw).
type deviceSide struct {
	snap  types.DeviceSnapshot
	links []string
	text  string
	raw   *deviceSide
}

// deviceView is what one device saw: from the rendered DOM when there is
// one (with the fetched HTML as raw), else from the fetched HTML (already
// extracted into page).
func (s *crawlSession) deviceView(e types.QueueEntry, res types.FetchResult, page *types.PageData, rendered string) deviceSide {
	if rendered == "" {
		snap, links, text := s.snapshot(e, res, page, "")
		return deviceSide{snap: snap, links: links, text: text}
	}
	snap, links, text := s.snapshot(e, res, page, rendered)
	raw := s.deviceView(e, res, page, "")
	snap.Raw = &raw.snap
	return deviceSide{snap: snap, links: links, text: text, raw: &raw}
}

// snapshot reads one device's page from rendered, or from the fetched HTML
// in page when rendered is "".
func (s *crawlSession) snapshot(e types.QueueEntry, res types.FetchResult, page *types.PageData, rendered string) (types.DeviceSnapshot, []string, string) {
	src, html := page, res.HTML
	if rendered != "" {
		pd := extract.ExtractPageData(rendered, extract.ExtractionOptions{
			PageURL: e.URL, FinalURL: res.FinalURL, StatusCode: res.StatusCode, Headers: res.Headers, Depth: e.Depth,
		})
		src, html = &pd, rendered
	}
	snap := types.DeviceSnapshot{
		StatusCode:     res.StatusCode,
		FinalURL:       res.FinalURL,
		WordCount:      src.WordCount,
		InternalLinks:  len(src.InternalLinks),
		H2Count:        len(src.Headings.H2),
		Hreflang:       len(src.Hreflang),
		Images:         len(src.Images),
		Viewport:       hasViewportMeta(html),
		Bytes:          int64(len(html)),
		Rendered:       rendered != "",
		ResponseTimeMs: res.ResponseTimeMs,
		VaryUserAgent:  strings.Contains(strings.ToLower(res.Headers["vary"]), "user-agent"),
		Error:          res.Error,
	}
	if snap.InternalLinks == 0 {
		snap.InternalLinks = src.InternalLinkCount
	}
	if src.Title != nil {
		snap.Title = src.Title.Text
	}
	if src.MetaDescription != nil {
		snap.Description = src.MetaDescription.Text
	}
	if src.Canonical != nil {
		snap.Canonical = *src.Canonical
	}
	if src.MetaRobots != nil {
		snap.MetaRobots = *src.MetaRobots
	}
	snap.H1 = src.Headings.H1[:min(len(src.Headings.H1), 3)]
	for _, im := range src.Images {
		if im.MissingAlt() {
			snap.ImagesNoAlt++
		}
	}
	for _, sd := range src.StructuredData {
		if sd.Type != "" && !slices.Contains(snap.SchemaTypes, sd.Type) {
			snap.SchemaTypes = append(snap.SchemaTypes, sd.Type)
		}
	}
	slices.Sort(snap.SchemaTypes)
	return snap, src.InternalLinks, src.BodyText
}

var viewportMetaRe = regexp.MustCompile(`(?i)<meta[^>]+name\s*=\s*["']?viewport`)

func hasViewportMeta(html string) bool {
	head := html
	if i := strings.Index(strings.ToLower(html), "</head>"); i >= 0 {
		head = html[:i]
	}
	return viewportMetaRe.MatchString(head)
}

// linksOnlyIn returns up to MaxDeviceLinkSamples links of a missing from b,
// and how many there are. Fragments and query parameter order are ignored:
// a mobile template that writes ?b=2&a=1 links the same page as ?a=1&b=2.
func linksOnlyIn(a, b []string) ([]string, int) {
	in := make(map[string]bool, len(b))
	for _, l := range b {
		in[linkKey(l)] = true
	}
	var out []string
	n := 0
	seen := map[string]bool{}
	for _, l := range a {
		k := linkKey(l)
		if in[k] || seen[k] {
			continue
		}
		seen[k] = true
		n++
		if len(out) < types.MaxDeviceLinkSamples {
			out = append(out, stripFragment(l))
		}
	}
	return out, n
}

// paramOnlyLinks counts links of a missing from b whose path b links with
// other query parameters.
func paramOnlyLinks(a, b []string) int {
	keys := make(map[string]bool, len(b))
	paths := make(map[string]bool, len(b))
	for _, l := range b {
		keys[linkKey(l)] = true
		paths[linkPath(l)] = true
	}
	n := 0
	seen := map[string]bool{}
	for _, l := range a {
		k := linkKey(l)
		if keys[k] || seen[k] {
			continue
		}
		seen[k] = true
		if paths[linkPath(l)] {
			n++
		}
	}
	return n
}

func linkPath(l string) string {
	l = stripFragment(l)
	if i := strings.IndexByte(l, '?'); i >= 0 {
		return l[:i]
	}
	return l
}

// linkKey is l without its fragment and with its query parameters sorted.
func linkKey(l string) string {
	l = stripFragment(l)
	i := strings.IndexByte(l, '?')
	if i < 0 {
		return l
	}
	params := strings.Split(l[i+1:], "&")
	slices.Sort(params)
	return l[:i+1] + strings.Join(params, "&")
}

func stripFragment(l string) string {
	if i := strings.IndexByte(l, '#'); i >= 0 {
		return l[:i]
	}
	return l
}

func sameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && strings.EqualFold(ua.Host, ub.Host)
}

// deviceDiffs lists what differs between the two snapshots. Small link
// differences (a rotating promo, one nav item) are ignored: links count
// when at least 3 and 5% of the union differ.
func deviceDiffs(c *types.DeviceComparison, primaryDev, secondaryDev *types.DeviceProfile, pLinks, sLinks int) []string {
	p, s := c.Primary, c.Secondary
	var d []string
	add := func(cond bool, id string) {
		if cond {
			d = append(d, id)
		}
	}
	add(p.StatusCode != s.StatusCode, types.DeviceDiffStatus)
	add(stripFragment(p.FinalURL) != stripFragment(s.FinalURL), types.DeviceDiffRedirect)
	html := p.StatusCode == 200 && s.StatusCode == 200
	if html {
		add(p.Canonical != s.Canonical, types.DeviceDiffCanonical)
		add(normRobots(p.MetaRobots) != normRobots(s.MetaRobots), types.DeviceDiffRobots)
		add(strings.TrimSpace(p.Title) != strings.TrimSpace(s.Title), types.DeviceDiffTitle)
		add(strings.TrimSpace(p.Description) != strings.TrimSpace(s.Description), types.DeviceDiffDescription)
		add(!slices.Equal(p.H1, s.H1), types.DeviceDiffH1)
		lo, hi := min(p.WordCount, s.WordCount), max(p.WordCount, s.WordCount)
		add(c.TextSimilarity < 80 || (hi-lo >= 50 && float64(lo) < 0.7*float64(hi)), types.DeviceDiffContent)
		only := c.LinksOnlyPrimaryCount + c.LinksOnlySecondaryCount
		union := max(pLinks, sLinks) + min(c.LinksOnlyPrimaryCount, c.LinksOnlySecondaryCount)
		add(only >= 3 && float64(only) >= 0.05*float64(max(union, 1)), types.DeviceDiffLinks)
		add(!slices.Equal(p.SchemaTypes, s.SchemaTypes), types.DeviceDiffSchema)
		add(p.Hreflang != s.Hreflang, types.DeviceDiffHreflang)
		add((isHandheld(primaryDev) && !p.Viewport) || (isHandheld(secondaryDev) && !s.Viewport), types.DeviceNoViewport)
	}
	return d
}

func isHandheld(d *types.DeviceProfile) bool {
	return d != nil && d.Kind != types.DeviceDesktop
}

func normRobots(s string) string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ' ' })
	slices.Sort(parts)
	return strings.Join(slices.Compact(parts), ",")
}
