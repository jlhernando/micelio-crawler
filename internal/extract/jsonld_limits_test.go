package extract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func jsonldPage(scripts ...string) string {
	var b strings.Builder
	b.WriteString("<html><head>")
	for _, s := range scripts {
		b.WriteString(`<script type="application/ld+json">` + s + `</script>`)
	}
	b.WriteString("</head><body></body></html>")
	return b.String()
}

func graphOf(n int, item string) string {
	var b strings.Builder
	b.WriteString(`{"@context":"https://schema.org","@graph":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(item)
	}
	b.WriteString(`]}`)
	return b.String()
}

func structured(html string) []types.StructuredDataEntry {
	return ExtractPageData(html, ExtractionOptions{PageURL: "https://example.com/", StatusCode: 200}).StructuredData
}

// A @graph with many items is read up to jsonldMaxGraphItems.
func TestJSONLDGraphItemCap(t *testing.T) {
	got := structured(jsonldPage(graphOf(jsonldMaxGraphItems*10, `{"@type":"Thing","name":"n"}`)))
	if len(got) != jsonldMaxGraphItems {
		t.Errorf("%d entries, want cap %d", len(got), jsonldMaxGraphItems)
	}
}

// A large author referenced by every item is not copied into each one.
func TestJSONLDLargeAuthorNotInlined(t *testing.T) {
	big := `{"@type":"Person","@id":"#p","name":"A","bio":"` + strings.Repeat("x", 50_000) + `"}`
	small := `{"@type":"Person","@id":"#s","name":"Small"}`
	item := `{"@type":"Article","author":{"@id":"#p"}}`
	got := structured(jsonldPage(`{"@graph":[` + big + `,` + small + `,` + item + `,{"@type":"Article","author":{"@id":"#s"}}]}`))
	var articles []string
	for _, e := range got {
		if e.Type == "Article" {
			articles = append(articles, e.Raw)
		}
	}
	if len(articles) != 2 {
		t.Fatalf("articles: %v", articles)
	}
	if strings.Contains(articles[0], "xxxx") || !strings.Contains(articles[0], `"#p"`) {
		t.Errorf("large author inlined: %.120s", articles[0])
	}
	if !strings.Contains(articles[1], `"Small"`) {
		t.Errorf("small author not inlined: %s", articles[1])
	}
}

// A long Article keeps valid Raw JSON with its dates and author, so
// freshness and E-E-A-T still read them (a byte prefix did not parse).
func TestJSONLDLargeArticleStaysValid(t *testing.T) {
	art := `{"@context":"https://schema.org","@type":"Article","headline":"H",` +
		`"datePublished":"2026-01-02","dateModified":"2026-02-03","author":{"@type":"Person","name":"Ana"},` +
		`"articleBody":"` + strings.Repeat("é", 8000) + `","description":"` + strings.Repeat("d", 3000) + `"}`
	for _, script := range []string{art, `{"@graph":[` + art + `]}`} {
		got := structured(jsonldPage(script))
		if len(got) != 1 || got[0].Type != "Article" {
			t.Fatalf("entries: %+v", got)
		}
		raw := got[0].Raw
		if len(raw) > jsonldMaxRaw || !json.Valid([]byte(raw)) {
			t.Fatalf("raw %d bytes, valid=%v", len(raw), json.Valid([]byte(raw)))
		}
		for _, want := range []string{`"datePublished":"2026-01-02"`, `"dateModified"`, `"Ana"`, `"description"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("raw lost %s: %.200s", want, raw)
			}
		}
		if strings.Contains(raw, "articleBody") {
			t.Errorf("largest field kept: %.100s", raw)
		}
	}
}

// Scripts past the per-script or per-page graph budget give one entry
// with their top-level @type, read without building the document.
func TestJSONLDOversizedScriptTypeOnly(t *testing.T) {
	huge := graphOf(jsonldMaxGraphBytes/20, `{"@type":"Thing","n":1}`)
	huge = `{"@type":"WebSite",` + strings.TrimPrefix(huge, "{")
	if len(huge) <= jsonldMaxGraphBytes {
		t.Fatalf("test script only %d bytes", len(huge))
	}
	got := structured(jsonldPage(huge))
	if len(got) != 1 || got[0].Type != "WebSite" || len(got[0].Raw) > jsonldMaxRaw || !json.Valid([]byte(got[0].Raw)) {
		t.Fatalf("got %d entries, first %+v", len(got), got[0].Type)
	}
	if extractJSONLDType(`{"@type":"A"`) != "Unknown" {
		t.Error("invalid JSON must give Unknown")
	}
	if extractJSONLDType(`[{"@type":["B","C"]}]`) != "B" {
		t.Error("array form")
	}
	if extractJSONLDType(`{"@type":"A","@type":"Z"}`) != "Z" {
		t.Error("last @type wins, as with json.Unmarshal")
	}
}

// One page cannot produce more than maxStructuredEntries entries.
func TestStructuredDataEntryCap(t *testing.T) {
	scripts := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		scripts = append(scripts, graphOf(jsonldMaxGraphItems, `{"@type":"Thing"}`))
	}
	if got := structured(jsonldPage(scripts...)); len(got) != maxStructuredEntries {
		t.Errorf("%d entries, want cap %d", len(got), maxStructuredEntries)
	}
}
