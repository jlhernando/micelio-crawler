package server

import "strings"

// containsFold reports whether s contains lowerQ, ignoring case. lowerQ must
// already be lower-cased (strings.ToLower). ASCII text, the common case for
// URLs and most titles, is matched without allocating; other text falls
// back to strings.ToLower so Unicode case rules still apply.
func containsFold(s, lowerQ string) bool {
	if lowerQ == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return strings.Contains(strings.ToLower(s), lowerQ)
		}
	}
	for i := 0; i < len(lowerQ); i++ {
		if lowerQ[i] >= 0x80 {
			return false // non-ASCII query cannot match ASCII text
		}
	}
	n := len(lowerQ)
	for i := 0; i+n <= len(s); i++ {
		j := 0
		for ; j < n; j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerQ[j] {
				break
			}
		}
		if j == n {
			return true
		}
	}
	return false
}
