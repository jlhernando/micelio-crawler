package extract

import (
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// cloneText is the old implementation: clone, remove skipped tags, Text().
func cloneText(doc *goquery.Document, sel string) string {
	c := doc.Find("body").Clone()
	c.Find(sel).Remove()
	return c.Text()
}

func TestTextSkippingMatchesCloneRemove(t *testing.T) {
	pages := []string{
		``,
		`<p>no body tag</p>`,
		`<html><body></body></html>`,
		`<body>a<script>x()</script>b<style>.c{}</style>c<noscript>ns</noscript>d<svg><style>s</style><text>svg text</text><script>y</script></svg>e</body>`,
		`<body><div>By <b>John Smith</b><template><p>tpl</p><script>t</script></template><noscript><p>inner</p></noscript></div><math><mi>x</mi></math><SCRIPT>up</SCRIPT><Svg>s</Svg>tail</body>`,
		`<body>` + strings.Repeat(`<p>Written by Jane Doe </p><script>z</script>`, 400) + `<body>nested</body></body>`,
	}
	if b, err := os.ReadFile("../../docs/interface-concepts/micelio.html"); err == nil {
		pages = append(pages, string(b))
	}
	for i, p := range pages {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(p))
		if err != nil {
			t.Fatal(err)
		}
		nodes := doc.Find("body").Nodes
		if got, want := textSkipping(nodes, skipBodyTextTag, 0), cloneText(doc, "script, style, noscript, svg"); got != want {
			t.Fatalf("page %d body text:\n got %q\nwant %q", i, got, want)
		}
		want := cloneText(doc, "script, style, noscript")
		if got := textSkipping(nodes, skipBylineTag, 0); got != want {
			t.Fatalf("page %d byline text:\n got %q\nwant %q", i, got, want)
		}
		got := textSkipping(nodes, skipBylineTag, 2048)
		if len(want) > 2048 {
			want = want[:2048]
		}
		if len(got) > 2048 {
			got = got[:2048]
		}
		if got != want {
			t.Fatalf("page %d byline 2048 prefix differs", i)
		}
	}
}
