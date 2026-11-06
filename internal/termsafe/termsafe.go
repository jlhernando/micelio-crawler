// Package termsafe makes text from crawled websites safe to show. A site
// controls its robots.txt, titles, URLs and headers, so it can put
// terminal escape sequences (ESC, CSI, OSC: recolour the screen, fake
// output, hidden hyperlinks, clipboard writes), Unicode bidi overrides
// (text that reads differently than it is) or invisible characters (hidden
// instructions for an AI agent) in them.
//
// String and NewWriter escape those characters visibly for a terminal
// (ESC becomes the four characters \x1b); Strip removes them for text
// handed to an AI agent.
package termsafe

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// unsafe reports whether rune r must never reach a terminal or an agent
// as it is: C0 and C1 control characters, DEL, bidi controls, line and
// paragraph separators, zero-width characters and Unicode tag characters.
func unsafe(r rune) bool {
	switch {
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, // Arabic letter mark, LRM, RLM
		r >= 0x202a && r <= 0x202e, // LRE, RLE, PDF, LRO, RLO
		r >= 0x2066 && r <= 0x2069, // LRI, RLI, FSI, PDI
		r == 0x2028, r == 0x2029,   // line and paragraph separators
		r >= 0x200b && r <= 0x200d, r == 0x2060, r == 0xfeff, // zero-width
		r >= 0xe0000 && r <= 0xe007f: // tag characters (hidden ASCII)
		return true
	}
	return false
}

// needs reports whether s has anything to escape or strip (a fast path:
// almost every string is clean).
func needs(s string, keepLines bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			return true // checked rune by rune below
		}
		if c < 0x20 || c == 0x7f {
			if keepLines && (c == '\n' || c == '\t') {
				continue
			}
			return true
		}
	}
	return false
}

func escape(s string, keepLines bool) string {
	if !needs(s, keepLines) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i]) // invalid UTF-8 (a raw 8-bit C1 byte)
		case keepLines && (r == '\n' || r == '\t'):
			b.WriteRune(r)
		case !unsafe(r):
			b.WriteString(s[i : i+size])
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// String returns s fit for one line of terminal output: control and
// formatting characters (newlines included) are shown as \xNN, \uNNNN or
// \UNNNNNNNN instead of being sent to the terminal.
func String(s string) string { return escape(s, false) }

// Strings applies String to every element of ss (a new slice).
func Strings(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = String(s)
	}
	return out
}

// Strip removes control and formatting characters from s, for text handed
// to an AI agent: tabs and line breaks become spaces, the rest (escape
// sequences' ESC, bidi overrides, zero-width and tag characters, invalid
// UTF-8) is dropped.
func Strip(s string) string {
	if !needs(s, false) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
		case r == '\n' || r == '\r' || r == '\t' || r == 0x2028 || r == 0x2029:
			b.WriteByte(' ')
		case !unsafe(r):
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// writer escapes everything written to it except line breaks and tabs.
type writer struct{ w io.Writer }

// NewWriter returns a writer for multi-line reports built from crawl data
// (diff summaries): it passes newlines and tabs through and escapes every
// other control or formatting character like String. Each Write should
// hold whole lines or at least whole UTF-8 characters (fmt.Fprintf does).
func NewWriter(w io.Writer) io.Writer { return writer{w} }

func (w writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.w, escape(string(p), true)); err != nil {
		return 0, err
	}
	return len(p), nil
}
