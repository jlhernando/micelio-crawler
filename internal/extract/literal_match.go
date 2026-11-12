package extract

import (
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// foldWords matches like the regexp `(?i)(w1|w2|...)` for lowercase ASCII
// words, without the backtracking engine: one pass over the input, folding
// ASCII letters on the fly and trying only the words that start with the
// folded byte (no lowercased copy, no per-word scan). Non-ASCII bytes are
// compared as they are: they never belong to an ASCII word, in valid or
// invalid UTF-8, and the only non-ASCII runes that fold to ASCII letters
// are ſ (U+017F, s) and K (U+212A, k). Input holding one of those two goes
// to the regexp, which keeps Unicode case folding exactly as before. A word
// found before such a rune is a match for the regexp too.
type foldWords struct {
	words [][]byte
	first [256][][]byte // words by first byte
	re    *regexp.Regexp
}

// asciiLower maps 'A'..'Z' to 'a'..'z' and every other byte to itself.
var asciiLower = func() (t [256]byte) {
	for i := range t {
		t[i] = byte(i)
		if 'A' <= i && i <= 'Z' {
			t[i] = byte(i) + 'a' - 'A'
		}
	}
	return t
}()

// newFoldWords panics on a word that is empty or not lowercase ASCII: the
// fast path folds only ASCII and looks words up by their lowercase first
// byte, so such a word would never match there. Words are quoted for the
// regexp fallback, so punctuation matches literally on both paths.
func newFoldWords(words ...string) *foldWords {
	quoted := make([]string, len(words))
	for i, w := range words {
		if !isLowerASCIIWord(w) {
			panic("extract: foldWords word must be non-empty lowercase ASCII: " + strconv.Quote(w))
		}
		quoted[i] = regexp.QuoteMeta(w)
	}
	f := &foldWords{re: regexp.MustCompile(`(?i)(` + strings.Join(quoted, "|") + `)`)}
	for _, w := range words {
		f.words = append(f.words, []byte(w))
		f.first[w[0]] = append(f.first[w[0]], []byte(w))
	}
	return f
}

func (f *foldWords) MatchString(s string) bool {
	checked := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			if !checked {
				if strings.Contains(s, "\u017f") || strings.Contains(s, "\u212a") {
					return f.re.MatchString(s)
				}
				checked = true
			}
			continue
		}
	next:
		for _, w := range f.first[asciiLower[c]] {
			if len(s)-i < len(w) {
				continue
			}
			for j := 1; j < len(w); j++ {
				if asciiLower[s[i+j]] != w[j] {
					continue next
				}
			}
			return true
		}
	}
	return false
}

// firstBytes returns which bytes can start a match of pattern: ASCII bytes
// as the regexp allows them (both cases under (?i)), and every byte >= 0x80
// as soon as any non-ASCII rune can start one. Gates built from it stay in
// step with the regexp when an alternative is added or changed.
func firstBytes(pattern string) (set [256]bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		panic(err)
	}
	addFirst(re.Simplify(), &set)
	return set
}

// addFirst adds the possible first bytes of re to set and reports whether re
// can match the empty string (so what follows it can start the match too).
func addFirst(re *syntax.Regexp, set *[256]bool) (nullable bool) {
	addRune := func(lo, hi rune) {
		for r := lo; r <= hi && r < utf8.RuneSelf; r++ {
			set[r] = true
		}
		if hi >= utf8.RuneSelf {
			for b := 0x80; b < 256; b++ {
				set[b] = true
			}
		}
	}
	switch re.Op {
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return true
		}
		r := re.Rune[0]
		addRune(r, r)
		if re.Flags&syntax.FoldCase != 0 {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				addRune(f, f)
			}
		}
		return false
	case syntax.OpCharClass:
		for i := 0; i+1 < len(re.Rune); i += 2 {
			addRune(re.Rune[i], re.Rune[i+1])
		}
		return false
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		addRune(0, unicode.MaxRune)
		return false
	case syntax.OpCapture, syntax.OpPlus:
		return addFirst(re.Sub[0], set)
	case syntax.OpStar, syntax.OpQuest:
		addFirst(re.Sub[0], set)
		return true
	case syntax.OpRepeat:
		n := addFirst(re.Sub[0], set)
		return n || re.Min == 0
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !addFirst(sub, set) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if addFirst(sub, set) {
				nullable = true
			}
		}
		return nullable
	case syntax.OpNoMatch:
		return false
	default: // empty match, anchors, word boundaries
		return true
	}
}

// requiredLiteral returns the longest literal every match of pattern must
// contain, lowercased, for a containsFoldASCII gate in front of the regexp.
// It looks only at the top-level sequence and panics when no ASCII literal
// without 's' or 'k' is found there, so a regexp edit that drops the literal fails
// at start-up instead of silently skipping matches.
func requiredLiteral(pattern string) string {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		panic(err)
	}
	re = re.Simplify()
	subs := []*syntax.Regexp{re}
	if re.Op == syntax.OpConcat {
		subs = re.Sub
	}
	best := ""
	for _, sub := range subs {
		if sub.Op != syntax.OpLiteral || sub.Flags&syntax.FoldCase == 0 {
			continue
		}
		lit := strings.ToLower(string(sub.Rune))
		// 's' and 'k' also fold from ſ and K, which containsFoldASCII
		// does not see: such a literal would skip matches the regexp finds.
		if len(lit) > len(best) && isLowerASCIIWord(lit) && !strings.ContainsAny(lit, "sk") {
			best = lit
		}
	}
	if best == "" {
		panic("extract: no required case-insensitive ASCII literal in " + pattern)
	}
	return best
}

func isLowerASCIIWord(w string) bool {
	if w == "" {
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c >= utf8.RuneSelf || asciiLower[c] != c {
			return false
		}
	}
	return true
}
