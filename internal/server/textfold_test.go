package server

import (
	"strings"
	"testing"
)

func TestContainsFoldMatchesToLowerContains(t *testing.T) {
	texts := []string{"https://Ex.com/Blog/Post", "Ünïcode Títle", "", "ABC", "straße", "İstanbul"}
	queries := []string{"", "blog", "ex.com/b", "títle", "ünï", "abc", "abcd", "ss", "straße", "z", "istanbul"}
	for _, s := range texts {
		for _, q := range queries {
			want := strings.Contains(strings.ToLower(s), q)
			if got := containsFold(s, q); got != want {
				t.Errorf("containsFold(%q, %q) = %v, want %v", s, q, got, want)
			}
		}
	}
}
