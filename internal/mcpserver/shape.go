package mcpserver

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/micelio/micelio/internal/types"
)

// maxText is how many characters of page text (title, description, h1) an
// agent gets. Page text is data from the crawled site, never instructions.
const maxText = 200

// truncate cuts s to n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

var (
	bearerRe = regexp.MustCompile(`(?i)(bearer\s+)[^\s"',;)]+`)
	// name=value or name: value where the name looks like a credential.
	credentialRe = regexp.MustCompile(`(?i)\b([a-z0-9_-]*(?:token|key|secret|password|passwd|auth)[a-z0-9_-]*)(\s*[=:]\s*"?)([^\s"'&,;)]+)`)
	// Well-known key shapes, wherever they appear.
	keyShapeRe = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|gh[pousr]_[A-Za-z0-9]{20,}|\d{8,10}:[A-Za-z0-9_-]{30,})`)
)

// scrub removes credentials from text shown to the agent.
func scrub(s string) string {
	s = bearerRe.ReplaceAllString(s, "${1}[redacted]")
	s = credentialRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := credentialRe.FindStringSubmatch(m)
		if sub[3] == "[redacted]" || strings.EqualFold(sub[3], "bearer") {
			return m
		}
		return sub[1] + sub[2] + "[redacted]"
	})
	return keyShapeRe.ReplaceAllString(s, "[redacted]")
}

// pageField reads one field of a page for pages_query.
type pageField func(p *types.PageData) any

func text(t *types.TextLength) any {
	if t == nil {
		return nil
	}
	return truncate(t.Text, maxText)
}

func textLen(t *types.TextLength) any {
	if t == nil {
		return 0
	}
	return t.Length
}

func strPtr(s *string) any {
	if s == nil {
		return nil
	}
	return truncate(*s, maxText)
}

// pageFields are the fields pages_query can return. Nothing else (raw
// HTML, body text, headers) ever leaves the dashboard through MCP.
var pageFields = map[string]pageField{
	"url":                   func(p *types.PageData) any { return p.URL },
	"status":                func(p *types.PageData) any { return p.StatusCode },
	"indexable":             func(p *types.PageData) any { return p.Indexability.Indexable },
	"indexabilityReason":    func(p *types.PageData) any { return p.Indexability.Reason },
	"title":                 func(p *types.PageData) any { return text(p.Title) },
	"titleLength":           func(p *types.PageData) any { return textLen(p.Title) },
	"metaDescription":       func(p *types.PageData) any { return text(p.MetaDescription) },
	"metaDescriptionLength": func(p *types.PageData) any { return textLen(p.MetaDescription) },
	"h1": func(p *types.PageData) any {
		if len(p.Headings.H1) == 0 {
			return nil
		}
		return truncate(p.Headings.H1[0], maxText)
	},
	"h1Count":        func(p *types.PageData) any { return len(p.Headings.H1) },
	"depth":          func(p *types.PageData) any { return p.Depth },
	"inlinks":        func(p *types.PageData) any { return p.Inlinks },
	"outlinks":       func(p *types.PageData) any { return max(len(p.InternalLinks), p.InternalLinkCount) },
	"externalLinks":  func(p *types.PageData) any { return max(len(p.ExternalLinks), p.ExternalLinkCount) },
	"wordCount":      func(p *types.PageData) any { return p.WordCount },
	"responseTimeMs": func(p *types.PageData) any { return p.ResponseTimeMs },
	"canonical":      func(p *types.PageData) any { return strPtr(p.Canonical) },
	"metaRobots":     func(p *types.PageData) any { return strPtr(p.MetaRobots) },
	"xRobotsTag":     func(p *types.PageData) any { return strPtr(p.XRobotsTag) },
	"finalUrl":       func(p *types.PageData) any { return p.FinalURL },
	"redirectHops":   func(p *types.PageData) any { return len(p.RedirectChain) },
	"pageRank":       func(p *types.PageData) any { return p.PageRank },
	"templateType":   func(p *types.PageData) any { return p.TemplateType },
	"segments":       func(p *types.PageData) any { return p.Segments },
	"soft404": func(p *types.PageData) any {
		if p.Soft404 == nil {
			return nil
		}
		return p.Soft404.Confidence
	},
	"robotsBlocked": func(p *types.PageData) any { return p.RobotsBlocked },
	"bodySize":      func(p *types.PageData) any { return p.BodySize },
	"error":         func(p *types.PageData) any { return truncate(scrub(p.Error), maxText) },
	"htmlLang":      func(p *types.PageData) any { return p.HTMLLang },
	"inSitemap": func(p *types.PageData) any {
		if p.SitemapData == nil {
			return nil
		}
		return p.SitemapData.InSitemap
	},
	"classification": func(p *types.PageData) any { return p.URLClassification },
}

// defaultFields is what pages_query returns when no fields are asked for.
var defaultFields = []string{"url", "status", "indexable", "title", "depth", "inlinks"}

// allFieldNames lists the page fields pages_query can return.
func allFieldNames() []string {
	out := make([]string, 0, len(pageFields))
	for k := range pageFields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pageRow returns the requested fields of p.
func pageRow(p *types.PageData, fields []string) map[string]any {
	row := make(map[string]any, len(fields))
	for _, f := range fields {
		row[f] = pageFields[f](p)
	}
	return row
}
