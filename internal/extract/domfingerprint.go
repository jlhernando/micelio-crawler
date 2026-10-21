package extract

import (
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/micelio/micelio/internal/analysis"
)

// structuralTags are the elements whose layout identifies a page template.
var structuralTags = map[string]bool{
	"div": true, "section": true, "article": true, "main": true, "ul": true, "ol": true,
	"form": true, "table": true, "figure": true, "h1": true, "h2": true, "h3": true,
	"aside": true, "nav": true, "button": true, "select": true, "input": true, "video": true,
}

// chromeTags are skipped when no main region exists, so site-wide header and
// footer markup does not make every page look like the same template.
var chromeTags = map[string]bool{"header": true, "footer": true, "nav": true, "aside": true, "script": true, "style": true, "noscript": true, "svg": true, "template": true}

const minFingerprintTokens = 5

// domFingerprint returns a SimHash of the main-content skeleton: each token is
// a parent→child pair of tag plus normalized classes. Pages built from the same
// template share most pairs regardless of their text or item counts.
func domFingerprint(doc *goquery.Document) uint64 {
	root := doc.Find("main, [role=main]").First()
	skipChrome := false
	if root.Length() == 0 {
		root = doc.Find("body").First()
		skipChrome = true
	}
	if root.Length() == 0 {
		return 0
	}

	seen := make(map[string]struct{}, 128)
	var walk func(n *html.Node, parent string, depth int)
	walk = func(n *html.Node, parent string, depth int) {
		if depth > 40 || len(seen) > 2000 {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if chromeTags[c.Data] && (skipChrome || c.Data != "nav" && c.Data != "aside") {
				continue
			}
			label := parent
			if structuralTags[c.Data] {
				label = c.Data + classSignature(c)
				seen[parent+">"+label] = struct{}{}
			}
			walk(c, label, depth+1)
		}
	}
	for _, n := range root.Nodes {
		walk(n, "root", 0)
	}
	if len(seen) < minFingerprintTokens {
		return 0
	}
	tokens := make([]string, 0, len(seen))
	for t := range seen {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)
	return analysis.SimhashTokens(tokens)
}

// classSignature returns up to two normalized class names, dropping
// generated or numeric classes that differ between otherwise identical pages.
func classSignature(n *html.Node) string {
	var class string
	for _, a := range n.Attr {
		if a.Key == "class" {
			class = a.Val
			break
		}
	}
	if class == "" {
		return ""
	}
	var out []string
	for _, c := range strings.Fields(strings.ToLower(class)) {
		if len(c) > 30 || hasDigitRun(c) {
			continue
		}
		out = append(out, c)
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "." + strings.Join(out, ".")
}

// hasDigitRun reports whether s contains two or more consecutive digits,
// typical of hashed CSS-in-JS classes and per-item ids.
func hasDigitRun(s string) bool {
	run := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			run++
			if run >= 2 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}
