package intern

import (
	"strings"
	"testing"
	"unsafe"
)

func TestStrSharesRepeatedStrings(t *testing.T) {
	tb := New(0)
	a := tb.Str(strings.Repeat("x", 8))
	b := tb.Str(strings.Repeat("x", 8)) // equal value, different backing array
	if unsafe.StringData(a) != unsafe.StringData(b) {
		t.Fatal("repeated string not shared")
	}
	if tb.Str("") != "" || tb.Len() != 1 {
		t.Fatalf("empty string must pass through, len=%d", tb.Len())
	}
}

func TestStrStopsGrowingAtMax(t *testing.T) {
	tb := New(2)
	first := tb.Str("ab")
	tb.Str("cd")
	if got := tb.Str("ef"); got != "ef" {
		t.Fatalf("Str(c) = %q", got)
	}
	if tb.Len() != 2 {
		t.Fatalf("len = %d, want 2", tb.Len())
	}
	copyOfFirst := string([]byte("ab"))
	if unsafe.StringData(tb.Str(copyOfFirst)) != unsafe.StringData(first) {
		t.Fatal("full table must still return the shared copy of known strings")
	}
}

func TestClip(t *testing.T) {
	s := make([]int, 2, 8)
	if c := Clip(s); cap(c) != 2 || len(c) != 2 {
		t.Fatalf("cap=%d len=%d", cap(c), len(c))
	}
	exact := []int{1, 2}
	if c := Clip(exact); &c[0] != &exact[0] {
		t.Fatal("exact-size slice must not be copied")
	}
	if Clip([]int(nil)) != nil {
		t.Fatal("nil must stay nil")
	}
	if e := Clip(make([]int, 0, 4)); e == nil || len(e) != 0 {
		t.Fatal("empty must stay empty, not nil")
	}
}
