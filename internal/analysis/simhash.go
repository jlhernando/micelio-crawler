package analysis

import (
	"crypto/md5"
	"encoding/binary"
	"math/bits"
	"strings"
)

// Simhash computes a 64-bit SimHash fingerprint for a text string.
// Near-duplicate texts produce fingerprints with small Hamming distance.
func Simhash(text string) uint64 {
	if text == "" {
		return 0
	}

	words := strings.Fields(strings.ToLower(text))
	if len(words) < 5 {
		return hashToken(text)
	}

	// Build 3-word shingles
	v := make([]int, 64)
	for i := 0; i <= len(words)-3; i++ {
		shingle := words[i] + " " + words[i+1] + " " + words[i+2]
		hash := hashToken(shingle)
		for bit := 0; bit < 64; bit++ {
			if (hash>>uint(bit))&1 == 1 {
				v[bit]++
			} else {
				v[bit]--
			}
		}
	}

	// Reduce to fingerprint
	var fingerprint uint64
	for bit := 0; bit < 64; bit++ {
		if v[bit] > 0 {
			fingerprint |= 1 << uint(bit)
		}
	}
	return fingerprint
}

// hashToken hashes a string to a 64-bit value using MD5.
func hashToken(token string) uint64 {
	sum := md5.Sum([]byte(token))
	return binary.LittleEndian.Uint64(sum[:8])
}

// HammingDistance computes the Hamming distance between two 64-bit fingerprints.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// SimhashSimilarity computes similarity (0-100%) from Hamming distance.
func SimhashSimilarity(a, b uint64) int {
	dist := HammingDistance(a, b)
	return (64 - dist) * 100 / 64
}

// DuplicateGroup is a group of near-duplicate pages.
type DuplicateGroup struct {
	URLs       []string `json:"urls"`
	Similarity int      `json:"similarity"`
}

// FindNearDuplicates finds groups of near-duplicate fingerprints using greedy grouping.
// Threshold is minimum similarity percentage (default 90 = max ~6 bits different).
func FindNearDuplicates(items []SimhashItem, threshold int) []DuplicateGroup {
	if threshold == 0 {
		threshold = 90
	}
	maxDistance := 64 * (100 - threshold) / 100

	assigned := make(map[string]bool)
	var groups []DuplicateGroup

	for i := 0; i < len(items); i++ {
		if assigned[items[i].URL] {
			continue
		}

		var group []string
		minSimilarity := 100

		for j := i + 1; j < len(items); j++ {
			if assigned[items[j].URL] {
				continue
			}

			dist := HammingDistance(items[i].Fingerprint, items[j].Fingerprint)
			if dist <= maxDistance {
				if len(group) == 0 {
					group = append(group, items[i].URL)
				}
				group = append(group, items[j].URL)
				sim := (64 - dist) * 100 / 64
				if sim < minSimilarity {
					minSimilarity = sim
				}
			}
		}

		if len(group) >= 2 {
			for _, url := range group {
				assigned[url] = true
			}
			groups = append(groups, DuplicateGroup{URLs: group, Similarity: minSimilarity})
		}
	}

	return groups
}

// SimhashItem pairs a URL with its SimHash fingerprint.
type SimhashItem struct {
	URL         string
	Fingerprint uint64
}
