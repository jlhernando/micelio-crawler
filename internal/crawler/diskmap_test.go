package crawler

import (
	"testing"
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
