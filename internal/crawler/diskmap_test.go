package crawler

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestInternalLinksDiskRoundTrip(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	d.AddPage("https://example.com/a", []string{"https://example.com/b", "https://example.com/c"})
	d.AddPage("https://example.com/b", []string{"https://example.com/c"})
	d.AddPage("https://example.com/c", nil) // no links, should be skipped

	got := make(map[string][]string)
	if err := d.Iterate(func(source string, targets []string) {
		got[source] = targets
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(got))
	}
	if len(got["https://example.com/a"]) != 2 {
		t.Errorf("expected 2 targets for /a, got %d", len(got["https://example.com/a"]))
	}
	if len(got["https://example.com/b"]) != 1 {
		t.Errorf("expected 1 target for /b, got %d", len(got["https://example.com/b"]))
	}
}

func TestInternalLinksDiskEscaping(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// URLs with tab and newline chars (edge case)
	d.AddPage("https://example.com/a\tb", []string{"https://example.com/c\nd"})

	var source string
	var targets []string
	d.Iterate(func(s string, ts []string) {
		source = s
		targets = ts
	})

	if source != "https://example.com/a\tb" {
		t.Errorf("source escaping roundtrip failed: %q", source)
	}
	if len(targets) != 1 || targets[0] != "https://example.com/c\nd" {
		t.Errorf("target escaping roundtrip failed: %v", targets)
	}
}

// Repeated URLs come back as the same text, in order, across many pages,
// including non-ASCII URLs, tabs/newlines and duplicate links on one page.
func TestInternalLinksDiskRepeatedRoundTrip(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := map[string][]string{}
	var order []string
	for i := 0; i < 2000; i++ {
		src := fmt.Sprintf("https://example.com/p/%d", i)
		links := []string{"https://example.com/", "https://example.com/menu", fmt.Sprintf("https://example.com/p/%d", (i*7)%2000), "https://example.com/\u00fc?q=a\tb\nc", "https://example.com/menu"}
		d.AddPage(src, links)
		want[src] = links
		order = append(order, src)
	}
	var got []string
	if err := d.Iterate(func(source string, targets []string) {
		got = append(got, source)
		if !reflect.DeepEqual(targets, want[source]) {
			t.Fatalf("%s: targets %v, want %v", source, targets, want[source])
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, order) {
		t.Fatalf("sources out of order or missing: %d of %d", len(got), len(order))
	}
}

func TestInternalLinksDiskEmpty(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	calls := 0
	if err := d.Iterate(func(string, []string) { calls++ }); err != nil || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

// A page with more links than the old 4 MB scanner line limit still round-trips.
func TestInternalLinksDiskHugeLine(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	links := make([]string, 80000)
	for i := range links {
		links[i] = fmt.Sprintf("https://example.com/a-fairly-long-path/item-%d", i)
	}
	d.AddPage("https://example.com/", links)
	got := 0
	if err := d.Iterate(func(_ string, ts []string) { got = len(ts) }); err != nil {
		t.Fatal(err)
	}
	if got != len(links) {
		t.Fatalf("got %d links, want %d", got, len(links))
	}
}

// The temp file holds exactly the TSV lines the plain-text format wrote:
// escaped source, tab-separated escaped targets, newline.
func TestInternalLinksDiskBytes(t *testing.T) {
	d, err := newInternalLinksDisk()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var want bytes.Buffer
	for i := 0; i < 500; i++ {
		src := fmt.Sprintf("https://example.com/p/%d\t", i)
		links := []string{"https://example.com/", fmt.Sprintf("https://example.com/a\\b/%d", i), "https://example.com/x\ny"}
		if i%7 == 0 {
			links = nil // pages without links write nothing
		}
		d.AddPage(src, links)
		if len(links) == 0 {
			continue
		}
		want.WriteString(escapeTSV(src))
		for _, l := range links {
			want.WriteByte('\t')
			want.WriteString(escapeTSV(l))
		}
		want.WriteByte('\n')
	}
	if err := d.zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.buf.Flush(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(d.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("temp file bytes differ: got %d bytes, want %d", len(got), want.Len())
	}
}

func BenchmarkInternalLinksDiskAddPage(b *testing.B) {
	d, err := newInternalLinksDisk()
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	links := make([]string, 200)
	for i := range links {
		links[i] = fmt.Sprintf("https://www.example.com/section-%d/some-page-slug-%d", i%12, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.AddPage("https://www.example.com/source/page", links)
	}
}
