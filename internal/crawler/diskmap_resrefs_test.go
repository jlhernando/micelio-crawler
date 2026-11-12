package crawler

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/micelio/micelio/internal/types"
)

// Load keeps one copy of each resource URL (shared stylesheets, scripts and
// logos repeat on every page) and gives back exactly what was added.
func TestResourceRefsDiskLoadSharesURLs(t *testing.T) {
	d, err := newResourceRefsDisk()
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 50; p++ {
		d.Add(fmt.Sprintf("https://site.test/p%d", p), []types.ResourceEntry{
			{URL: "https://cdn.test/app.css", Type: "stylesheet"},
			{URL: fmt.Sprintf("https://cdn.test/img%d.png", p), Type: "image"},
		})
	}
	got, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("pages %d, want 50", len(got))
	}
	first := unsafe.StringData(got["https://site.test/p0"][0].URL)
	for p := 0; p < 50; p++ {
		refs := got[fmt.Sprintf("https://site.test/p%d", p)]
		if len(refs) != 2 || refs[0].URL != "https://cdn.test/app.css" || refs[0].Type != "stylesheet" ||
			refs[1].URL != fmt.Sprintf("https://cdn.test/img%d.png", p) || refs[1].Type != "image" {
			t.Fatalf("page %d refs %+v", p, refs)
		}
		if unsafe.StringData(refs[0].URL) != first {
			t.Errorf("page %d holds its own copy of the shared URL", p)
		}
	}
}
