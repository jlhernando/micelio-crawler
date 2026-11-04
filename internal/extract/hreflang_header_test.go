package extract

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestHreflangFromLinkHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   []types.HreflangEntry
	}{
		{"empty", "", nil},
		{"canonical only", `<https://ex.com/a>; rel="canonical"`, nil},
		{
			"two alternates and a canonical",
			`<https://ex.com/de/>; rel="alternate"; hreflang="de", <https://ex.com/>; rel="canonical", <https://ex.com/en/?a=1,2>; rel=alternate; hreflang=en-GB`,
			[]types.HreflangEntry{
				{Lang: "de", Href: "https://ex.com/de/", Source: "header"},
				{Lang: "en-GB", Href: "https://ex.com/en/?a=1,2", Source: "header"},
			},
		},
		{"alternate without hreflang", `<https://ex.com/feed>; rel="alternate"; type="application/rss+xml"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HreflangFromLinkHeader(tt.header)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractPageDataReadsHreflangHeader(t *testing.T) {
	page := ExtractPageData(`<html><head><link rel="alternate" hreflang="es" href="https://ex.com/es/"></head><body></body></html>`, ExtractionOptions{
		PageURL: "https://ex.com/", FinalURL: "https://ex.com/", StatusCode: 200,
		Headers: map[string]string{"link": `<https://ex.com/de/>; rel="alternate"; hreflang="de"`},
	})
	if len(page.Hreflang) != 2 || page.Hreflang[0].SourceOrHTML() != "html" || page.Hreflang[1].Source != "header" {
		t.Errorf("hreflang = %+v", page.Hreflang)
	}
}
