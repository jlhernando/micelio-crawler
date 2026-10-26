package analysis

import (
	"fmt"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func soft404Page(url string, words int) *types.PageData {
	return &types.PageData{URL: url, FinalURL: url, StatusCode: 200, WordCount: words, Title: &types.TextLength{Text: url}}
}

func soft404Of(p *types.PageData) string {
	if p.Soft404 == nil {
		return ""
	}
	return fmt.Sprintf("%s %v", p.Soft404.Confidence, p.Soft404.Reasons)
}

func TestSoft404ProbeMatch(t *testing.T) {
	probe := types.Soft404Probe{Host: "shop.example", URL: "https://shop.example/micelio-404-check-x", StatusCode: 200,
		Title: "Oops | Shop", WordCount: 80, DOMFingerprint: 0xF0F0}
	gone := soft404Page("https://shop.example/c/old-category", 82)
	gone.Title.Text = "Oops | Shop"
	gone.DOMFingerprint = 0xF0F1 // 1 bit away
	real := soft404Page("https://shop.example/c/sofas", 600)
	real.DOMFingerprint = 0x0F0F

	st := DetectSoft404([]*types.PageData{gone, real}, []types.Soft404Probe{probe}, "https://shop.example/")
	if got := soft404Of(gone); got != "high [matches-error-page]" || !gone.IsSoft404 {
		t.Errorf("gone page = %q", got)
	}
	if real.Soft404 != nil {
		t.Errorf("real page flagged: %s", soft404Of(real))
	}
	if st.Total != 1 || st.ByReason[types.Soft404MatchesErrorPage] != 1 {
		t.Errorf("stats = %+v", st)
	}

	// A probe that redirected (e.g. to the home page) is not used for matching.
	probe.FinalURL = "https://shop.example/"
	gone.Soft404 = nil
	DetectSoft404([]*types.PageData{gone}, []types.Soft404Probe{probe}, "https://shop.example/")
	if gone.Soft404 != nil {
		t.Errorf("redirected probe must not match pages: %s", soft404Of(gone))
	}
}

func TestSoft404EmptyListing(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 12; i++ {
		p := soft404Page(fmt.Sprintf("https://ads.example/cars/make%d", i), 400)
		p.TemplateType = TemplateListing
		p.ItemLinks = 30
		p.InternalLinkCount = 120
		pages = append(pages, p)
	}
	empty := pages[0]
	empty.ItemLinks = 0
	empty.InternalLinkCount = 90 // the grid's links are gone
	hub := pages[3]
	hub.ItemLinks = 0
	hub.InternalLinkCount = 200 // links to subcategories, not items
	few := pages[1]
	few.ItemLinks = 2 // few items is not empty
	emptyWithText := pages[2]
	emptyWithText.ItemLinks = 0
	emptyWithText.InternalLinkCount = 90
	emptyWithText.Soft404Text = "h1" // "0 ogłoszeń" in the H1

	DetectSoft404(pages, nil, "https://ads.example/")
	if got := soft404Of(empty); got != "low [empty-listing]" || empty.IsSoft404 {
		t.Errorf("empty listing = %q (IsSoft404 %v); alone it may be JS-rendered", got, empty.IsSoft404)
	}
	if hub.Soft404 != nil {
		t.Errorf("hub page flagged: %s", soft404Of(hub))
	}
	if few.Soft404 != nil {
		t.Errorf("listing with 2 items flagged: %s", soft404Of(few))
	}
	if got := soft404Of(emptyWithText); got != "high [empty-listing error-text]" {
		t.Errorf("empty listing with error text = %q", got)
	}

	// No baseline: a group where most listings have no item links is left alone.
	for _, p := range pages {
		p.ItemLinks = 0
	}
	empty.Soft404 = nil
	DetectSoft404(pages, nil, "https://ads.example/")
	if empty.Soft404 != nil {
		t.Errorf("no baseline, still flagged: %s", soft404Of(empty))
	}
}

func TestSoft404ErrorText(t *testing.T) {
	thin := soft404Page("https://s.example/a", 40)
	thin.Soft404Text = "title"
	long := soft404Page("https://s.example/b", 900)
	long.Soft404Text = "h1"
	body := soft404Page("https://s.example/c", 30)
	body.Soft404Text = "body"
	DetectSoft404([]*types.PageData{thin, long, body}, nil, "https://s.example/")
	for p, want := range map[*types.PageData]string{
		thin: "high [error-text-thin-body]",
		long: "medium [error-text]",
		body: "medium [error-text-thin-body]",
	} {
		if got := soft404Of(p); got != want {
			t.Errorf("%s = %q, want %q", p.URL, got, want)
		}
	}
}

func TestSoft404Redirects(t *testing.T) {
	redirect := func(from, to string) *types.PageData {
		return &types.PageData{URL: from, FinalURL: to, StatusCode: 301}
	}
	toHome := redirect("https://s.example/cars/old-model/", "https://www.s.example/")
	shallow := redirect("https://s.example/promo", "https://s.example/")
	https := redirect("http://s.example/cars/a/", "https://s.example/cars/a")
	pages := []*types.PageData{toHome, shallow, https}
	for i := 0; i < 10; i++ {
		pages = append(pages, redirect(fmt.Sprintf("https://s.example/ad/%d", i), "https://s.example/cars"))
	}
	DetectSoft404(pages, nil, "https://s.example/")
	if got := soft404Of(toHome); got != "medium [redirects-to-home]" {
		t.Errorf("deep redirect to home = %q", got)
	}
	if shallow.Soft404 != nil {
		t.Errorf("one-level redirect to home flagged: %s", soft404Of(shallow))
	}
	if https.Soft404 != nil {
		t.Errorf("http->https redirect flagged: %s", soft404Of(https))
	}
	if got := soft404Of(pages[3]); got != "low [mass-redirect]" || pages[3].IsSoft404 {
		t.Errorf("mass redirect = %q (IsSoft404 %v)", got, pages[3].IsSoft404)
	}
}

func TestSoft404ThinForTemplate(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 20; i++ {
		p := soft404Page(fmt.Sprintf("https://s.example/p/%d", i), 800)
		p.TemplateType = TemplateProduct
		pages = append(pages, p)
	}
	pages[0].WordCount = 60
	DetectSoft404(pages, nil, "https://s.example/")
	if got := soft404Of(pages[0]); got != "low [thin-for-template]" || pages[0].IsSoft404 {
		t.Errorf("thin product = %q (IsSoft404 %v)", got, pages[0].IsSoft404)
	}
	if pages[1].Soft404 != nil {
		t.Error("normal product flagged")
	}
}
