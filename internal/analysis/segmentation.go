package analysis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Segment defines a named URL pattern for grouping.
type Segment struct {
	Pattern *regexp.Regexp
	Name    string
}

// ParseSegment parses a "name:pattern" string into a Segment.
func ParseSegment(value string) (Segment, error) {
	idx := strings.Index(value, ":")
	if idx < 0 {
		return Segment{}, fmt.Errorf("segment must be 'name:pattern', got %q", value)
	}
	name := strings.TrimSpace(value[:idx])
	pattern := strings.TrimSpace(value[idx+1:])
	if name == "" || pattern == "" {
		return Segment{}, fmt.Errorf("segment name and pattern must not be empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Segment{}, fmt.Errorf("invalid segment pattern %q: %w", pattern, err)
	}
	return Segment{Name: name, Pattern: re}, nil
}

// AssignSegments returns the names of all segments matching a URL.
func AssignSegments(rawURL string, segments []Segment) []string {
	var matched []string
	for _, seg := range segments {
		if seg.Pattern.MatchString(rawURL) {
			matched = append(matched, seg.Name)
		}
	}
	return matched
}

// SegmentStats holds aggregate statistics for a URL segment.
type SegmentStats struct {
	StatusCodes        map[int]int `json:"statusCodes"`
	Name               string      `json:"name"`
	PageCount          int         `json:"pageCount"`
	AvgResponseTimeMs  float64     `json:"avgResponseTimeMs"`
	Indexable          int         `json:"indexable"`
	NonIndexable       int         `json:"nonIndexable"`
	AvgWordCount       float64     `json:"avgWordCount"`
	TotalInternalLinks int         `json:"totalInternalLinks"`
	TotalExternalLinks int         `json:"totalExternalLinks"`
	PagesWithErrors    int         `json:"pagesWithErrors"`
}

// BuildSegmentStats computes per-segment statistics from crawled pages.
func BuildSegmentStats(pages []*types.PageData, segments []Segment) []SegmentStats {
	statsMap := make(map[string]*SegmentStats)
	for _, seg := range segments {
		statsMap[seg.Name] = &SegmentStats{
			Name:        seg.Name,
			StatusCodes: make(map[int]int),
		}
	}

	for _, p := range pages {
		matched := AssignSegments(p.URL, segments)
		for _, name := range matched {
			s := statsMap[name]
			s.PageCount++
			s.StatusCodes[p.StatusCode]++
			s.AvgResponseTimeMs += float64(p.ResponseTimeMs)
			if p.Indexability.Indexable {
				s.Indexable++
			} else {
				s.NonIndexable++
			}
			s.AvgWordCount += float64(p.WordCount)
			if len(p.InternalLinks) > 0 {
				s.TotalInternalLinks += len(p.InternalLinks)
			} else {
				s.TotalInternalLinks += p.InternalLinkCount
			}
			if len(p.ExternalLinks) > 0 {
				s.TotalExternalLinks += len(p.ExternalLinks)
			} else {
				s.TotalExternalLinks += p.ExternalLinkCount
			}
			if p.Error != "" {
				s.PagesWithErrors++
			}
		}
	}

	var result []SegmentStats
	for _, seg := range segments {
		s := statsMap[seg.Name]
		if s.PageCount == 0 {
			continue
		}
		s.AvgResponseTimeMs /= float64(s.PageCount)
		s.AvgWordCount /= float64(s.PageCount)
		result = append(result, *s)
	}
	return result
}
