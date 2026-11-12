package extract

import (
	"regexp"
	"strings"
	"testing"
)

// The gates are derived from the live regexps, so adding an alternative
// widens the gate instead of silently skipping the new links.
func TestFirstBytesFollowsRegexp(t *testing.T) {
	set := firstBytes(itemURLAfterSlash)
	for _, c := range "pPiIsSdDoOaAmMlL" {
		if !set[c] {
			t.Errorf("first byte %q missing", c)
		}
	}
	for _, c := range "bxz/-0" {
		if set[c] {
			t.Errorf("byte %q should not start a match", c)
		}
	}
	if !set[0xC5] { // ſ (U+017F) folds to s
		t.Error("non-ASCII bytes must be tried for Unicode folds")
	}

	wider := firstBytes(itemURLAfterSlash[:len(itemURLAfterSlash)-1] + `|vehicle/[^/?#]+)`)
	if !wider['v'] || !wider['V'] {
		t.Error("new alternative not reflected in the gate")
	}
	re := regexp.MustCompile(`(?i)^/` + itemURLAfterSlash[:len(itemURLAfterSlash)-1] + `|vehicle/[^/?#]+)`)
	if !re.MatchString("/vehicle/123") {
		t.Fatal("test regexp broken")
	}
}

func TestFirstBytesOptionalPrefixes(t *testing.T) {
	set := firstBytes(`(?:a?b*|c{0,2})d`)
	for _, c := range "abcd" {
		if !set[c] {
			t.Errorf("%q missing", c)
		}
	}
	if set['e'] {
		t.Error("e should not be in the set")
	}
}

func TestRequiredLiteral(t *testing.T) {
	if got := requiredLiteral(productItemClass.String()); got != "product" {
		t.Fatalf("product literal = %q", got)
	}
	if got := requiredLiteral(itemURLIDRe.String()); got != ".html" {
		t.Fatalf("ID literal = %q", got)
	}
	mustPanic(t, "no literal", func() { requiredLiteral(`(?i)(prod|item)[-_]?(card|tile)`) })
	mustPanic(t, "only s/k literals", func() { requiredLiteral(`(?i)x?sku`) })
}

func TestNewFoldWordsValidates(t *testing.T) {
	mustPanic(t, "uppercase", func() { newFoldWords("Listing") })
	mustPanic(t, "non-ASCII", func() { newFoldWords("catálogo") })
	mustPanic(t, "empty", func() { newFoldWords("") })
	f := newFoldWords("c++", "a.b")
	for in, want := range map[string]bool{"C++ code": true, "axb": false, "A.B": true} {
		if got := f.MatchString(in); got != want {
			t.Errorf("fast %q = %v", in, got)
		}
		if got := f.re.MatchString(in); got != want {
			t.Errorf("regexp %q = %v", in, got)
		}
	}
	if !f.MatchString("ſ a.b") { // forces the regexp path
		t.Error("fallback should match a.b literally")
	}
	if !strings.Contains(f.re.String(), `c\+\+`) {
		t.Error("words not quoted in the fallback regexp")
	}
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}
