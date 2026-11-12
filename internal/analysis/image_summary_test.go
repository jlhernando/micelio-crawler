package analysis

import (
	"reflect"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Pages reloaded with ImageSummary instead of Images must give the same
// image stats as pages that still hold their images.
func TestImageSummaryMatchesImages(t *testing.T) {
	s := func(v string) *string { return &v }
	long := strings.Repeat("a", types.AltTooLongLimit+1)
	mk := func() []*types.PageData {
		return []*types.PageData{
			{URL: "https://x.test/a", StatusCode: 200, WordCount: 200, Images: []types.ImageData{
				{Src: "/1.png"}, {Src: "/2.png", Alt: s(""), Width: s("1"), Height: s("2")},
				{Src: "/3.png", Alt: s(long), Width: s("1")}, {Src: "/4.png", Alt: s("ok"), Height: s("2")},
				{Src: "/5.png", Alt: s(long + "b"), Width: s("1"), Height: s("1")},
			}},
			{URL: "https://x.test/b", StatusCode: 404, Images: []types.ImageData{{Src: "/1.png"}}},
			{URL: "https://x.test/c", StatusCode: 200, Images: []types.ImageData{}},
			{URL: "https://x.test/d", StatusCode: 200},
		}
	}
	full, summ := mk(), mk()
	for _, p := range summ {
		p.ImageSummary = types.SummarizeImages(p.Images)
		p.Images = nil
	}
	if a, b := buildImageAuditStats(full), buildImageAuditStats(summ); !reflect.DeepEqual(a, b) {
		t.Fatalf("audit differs:\n%+v\n%+v", a, b)
	}
	for i := range full {
		if full[i].ImageCount() != summ[i].ImageCount() {
			t.Fatalf("page %d image count differs", i)
		}
	}
	a := buildImageAuditStats(full)
	if a.TotalImages != 5 || a.MissingAltAttribute != 1 || a.EmptyAlt != 1 || a.MissingDimensions != 3 ||
		len(a.AltTooLong) != 2 || a.AltTooLong[1].Src != "/5.png" || a.AltTooLong[1].URL != "https://x.test/a" {
		t.Fatalf("unexpected audit %+v", a)
	}
}
