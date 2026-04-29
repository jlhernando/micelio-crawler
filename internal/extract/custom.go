package extract

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/micelio/micelio/internal/types"
)

const regexSampleLimit = 100_000 // max bytes to test regex against (ReDoS protection)

// RunCustomExtractions runs CSS selector extractions against a parsed document.
func RunCustomExtractions(doc *goquery.Document, rules []types.CustomExtractionRule) map[string][]string {
	if len(rules) == 0 {
		return nil
	}
	results := make(map[string][]string, len(rules))
	for _, rule := range rules {
		var values []string
		doc.Find(rule.Selector).Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text != "" {
				values = append(values, text)
			}
		})
		results[rule.Name] = values
	}
	return results
}

// RunCustomSearches runs search patterns against raw HTML.
func RunCustomSearches(html string, rules []types.CustomSearchRule) map[string]bool {
	if len(rules) == 0 {
		return nil
	}
	results := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if rule.IsRegex {
			results[rule.Name] = regexSearch(html, rule.Pattern)
		} else {
			results[rule.Name] = strings.Contains(html, rule.Pattern)
		}
	}
	return results
}

// regexSearch tests a regex against a truncated sample with a timeout.
// Note: on timeout, the goroutine running MatchString may continue until completion
// since Go's regexp engine doesn't support cancellation. The sample limit mitigates this.
func regexSearch(html, pattern string) bool {
	sample := html
	if len(sample) > regexSampleLimit {
		sample = sample[:regexSampleLimit]
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}

	// Run with timeout to protect against ReDoS
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan bool, 1)
	go func() {
		ch <- re.MatchString(sample)
	}()

	select {
	case result := <-ch:
		return result
	case <-ctx.Done():
		return false // timed out
	}
}

// ParseExtractionRule parses "--extract name:selector" or "name:css:selector".
func ParseExtractionRule(value string) (types.CustomExtractionRule, error) {
	firstColon := strings.Index(value, ":")
	if firstColon == -1 {
		return types.CustomExtractionRule{}, fmt.Errorf("invalid extraction format %q: use \"name:selector\"", value)
	}

	name := value[:firstColon]
	if name == "" {
		return types.CustomExtractionRule{}, fmt.Errorf("invalid extraction format %q: name cannot be empty", value)
	}
	rest := value[firstColon+1:]

	// Check if second segment is a type specifier
	if secondColon := strings.Index(rest, ":"); secondColon != -1 {
		maybeType := strings.ToLower(rest[:secondColon])
		if maybeType == "css" {
			return types.CustomExtractionRule{Name: name, Type: "css", Selector: rest[secondColon+1:]}, nil
		}
	}

	return types.CustomExtractionRule{Name: name, Type: "css", Selector: rest}, nil
}

// ParseSearchRule parses "--search pattern" or "--search /regex/".
func ParseSearchRule(value string) (types.CustomSearchRule, error) {
	if strings.HasPrefix(value, "/") && strings.HasSuffix(value, "/") && len(value) > 2 {
		pattern := value[1 : len(value)-1]
		if _, err := regexp.Compile(pattern); err != nil {
			return types.CustomSearchRule{}, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		return types.CustomSearchRule{Name: pattern, Pattern: pattern, IsRegex: true}, nil
	}
	return types.CustomSearchRule{Name: value, Pattern: value, IsRegex: false}, nil
}
