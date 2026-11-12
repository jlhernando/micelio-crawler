package extract

import (
	"reflect"
	"testing"
)

// ExtractPageDataWithRefs reads resource refs from the parse it already does;
// it must return exactly what a separate ExtractResourceRefs parse returns,
// and the page data must not change.
func TestExtractPageDataWithRefsMatchesSeparateParse(t *testing.T) {
	pages := []string{
		buildHTML(200, 20),
		`<html><head><link rel="stylesheet" href="/a.css"><link rel="preload" as="font" href="f.woff2">
<script src="/app.js"></script><script src="/app.js"></script><style>x{}</style></head>
<body><noscript><img src="/ns.png"></noscript><svg><image href="/s.png"/></svg>
<img src="data:image/png;base64,AA"><img src=""><img src="../up.jpg"><img src="//cdn.example.org/c.jpg">
<picture><source src="/p.webp"><img src="/p.jpg"></picture><video src="v.mp4"></video><audio src="a.mp3"></audio>
<iframe src="https://other.example/x"></iframe><a href="/link">t</a><table><img src="/in-table.gif"></table>
<custom-el><img src="/c.png"></custom-el><p>text by John Smith</p></body></html>`,
		`<p>no head <img src=rel.png><script src=x.js>`,
		``,
	}
	opts := []ExtractionOptions{
		{PageURL: "https://example.com/a/b", StatusCode: 200},
		{PageURL: "https://example.com/a/b", FinalURL: "https://www.example.com/c/d?q=1", StatusCode: 200},
	}
	for i, html := range pages {
		for _, o := range opts {
			base := o.FinalURL
			if base == "" {
				base = o.PageURL
			}
			want := ExtractResourceRefs(html, base)
			wantPage := ExtractPageData(html, o)
			page, refs := ExtractPageDataWithRefs(html, o, true)
			if !reflect.DeepEqual(refs, want) {
				t.Errorf("page %d base %s: refs\n got %v\nwant %v", i, base, refs, want)
			}
			if !reflect.DeepEqual(page, wantPage) {
				t.Errorf("page %d base %s: page data differs", i, base)
			}
			if _, none := ExtractPageDataWithRefs(html, o, false); none != nil {
				t.Errorf("page %d: refs without withRefs", i)
			}
		}
	}
}
