package termsafe

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	cases := map[string]string{
		"https://ex.com/a?b=1":                 "https://ex.com/a?b=1",
		"café — ünïcode ✓":                     "café — ünïcode ✓",
		"\x1b[31mred\x1b[0m":                   `\x1b[31mred\x1b[0m`,
		"\x1b]52;c;ZXZpbA==\x07":               `\x1b]52;c;ZXZpbA==\x07`, // OSC 52 clipboard write
		"\x1b]8;;https://evil/\x1b\\x\x1b]8;;": `\x1b]8;;https://evil/\x1b\x\x1b]8;;`,
		"a\nb\rc\td":                           `a\x0ab\x0dc\x09d`,
		"del\x7f":                              `del\x7f`,
		"c1 \u009b31m":                         `c1 \u009b31m`,
		"raw8bit \x9b31m":                      `raw8bit \x9b31m`,
		"bidi \u202egnp.exe":                   `bidi \u202egnp.exe`,
		"iso \u2066x\u2069":                    `iso \u2066x\u2069`,
		"zw\u200bsp":                           `zw\u200bsp`,
		"tag\U000E0041":                        `tag\U000e0041`,
		"bom\ufeff":                            `bom\ufeff`,
		"replacement \ufffd ok":                "replacement \ufffd ok",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
		if got := String(in); strings.ContainsAny(got, "\x1b\x07\u009b\u202e") {
			t.Errorf("String(%q) still holds a control character: %q", in, got)
		}
	}
}

func TestStrip(t *testing.T) {
	cases := map[string]string{
		"plain title":                     "plain title",
		"line1\nline2\tx":                 "line1 line2 x",
		"\x1b[2Jcleared":                  "[2Jcleared",
		"ignore\u200b previous\u202e":     "ignore previous",
		"hidden\U000E0049\U000E0047 tags": "hidden tags",
		"bad utf8 \xff\xfe end":           "bad utf8  end",
		"para\u2029graph":                 "para graph",
		"ünïcode stays":                   "ünïcode stays",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriterKeepsLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	fmt.Fprintf(w, "Changed:\n  %s\t%d\n", "https://ex.com/\x1b]0;pwned\x07", 3)
	want := "Changed:\n  https://ex.com/\\x1b]0;pwned\\x07\t3\n"
	if buf.String() != want {
		t.Errorf("writer = %q, want %q", buf.String(), want)
	}
}

func TestStringsCopies(t *testing.T) {
	in := []string{"a\x1b", "b"}
	out := Strings(in)
	if in[0] != "a\x1b" || out[0] != `a\x1b` || out[1] != "b" {
		t.Errorf("Strings = %q (input %q)", out, in)
	}
}
