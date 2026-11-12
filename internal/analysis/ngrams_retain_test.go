package analysis

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// Unigram keys kept in the totals must not point into a page's text: that
// pinned one whole cleaned body per page that introduced a new word.
func TestNgramUnigramKeysDoNotPinPageText(t *testing.T) {
	// Words longer than 16 bytes skip the tiny allocator, so separate
	// copies (24-byte size class) are never 21 bytes apart like substrings.
	words := []string{"aaaaaaaaaaaaaaaaaone", "bbbbbbbbbbbbbbbbbtwo", "ccccccccccccccethree"}
	a := NewNgramAnalyzer("en")
	a.AddPage(strings.Repeat(strings.Join(words, " ")+" ", 3))
	ptr := func(s string) uintptr { return uintptr(unsafe.Pointer(unsafe.StringData(s))) }
	var keys []string
	for _, w := range words {
		for k := range a.unigrams {
			if k == w {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) != 3 {
		t.Fatalf("unigrams missing: %v", a.unigrams)
	}
	if ptr(keys[1])-ptr(keys[0]) == uintptr(len(words[0])+1) && ptr(keys[2])-ptr(keys[1]) == uintptr(len(words[1])+1) {
		t.Fatal("unigram keys are substrings of the page text")
	}
}

func TestNgramResultsUnchangedByKeyCopy(t *testing.T) {
	pages := []string{
		"The quick brown fox jumps over the lazy dog near the river bank today.",
		"A quick brown fox ran over the lazy cat near the river bank yesterday!",
		"Brown foxes and lazy dogs share the river bank, quick and lazy alike.",
	}
	a := NewNgramAnalyzer("en")
	b := NewNgramAnalyzer("en")
	for _, p := range pages {
		a.AddPage(p)
		// Reference path: merge without copying keys.
		pn := b.PreparePage(p)
		b.totalPages++
		b.totalTokens += pn.tokens
		mergeNgrams(b.unigrams, pn.unigrams, false)
		mergeNgrams(b.bigrams, pn.bigrams, false)
		mergeNgrams(b.trigrams, pn.trigrams, false)
	}
	if !reflect.DeepEqual(a.GetResults(100), b.GetResults(100)) {
		t.Fatal("results differ")
	}
}
