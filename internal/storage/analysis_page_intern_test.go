package storage

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestPageInternerKeepsValues(t *testing.T) {
	raw := []byte(`{"url":"https://a.test/x","templateType":"t","htmlLang":"en",
	"anchors":[{"href":"https://a.test/","text":"Home","position":"navigation","isInternal":true},{"href":"https://a.test/b","text":"","position":"footer"},{"href":"https://a.test/c","text":"C"}],
	"images":[{"src":"/logo.png","alt":"L","hasAltAttribute":true}],
	"hreflang":[{"lang":"es","href":"https://a.test/es"}],
	"structuredData":[{"type":"Organization","format":"json-ld","raw":"{}"}],
	"schemaValidation":[{"type":"Organization","format":"json-ld","issues":[]}],
	"segments":["s1"],"openGraph":{"og:type":"website"},"urlIssues":[]}`)
	want, err := decodeAnalysisPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	in := newPageInterner()
	for i := 0; i < 2; i++ {
		got, err := decodeAnalysisPage(raw)
		if err != nil {
			t.Fatal(err)
		}
		in.compact(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("compact changed the page:\n got %+v\nwant %+v", got, want)
		}
		if cap(got.Anchors) != len(got.Anchors) {
			t.Fatalf("anchors cap %d len %d", cap(got.Anchors), len(got.Anchors))
		}
		if got.URLIssues == nil || len(got.URLIssues) != 0 {
			t.Fatal("empty slice must stay empty, not nil")
		}
	}
}

func TestPageInternerSharesStrings(t *testing.T) {
	raw := []byte(`{"anchors":[{"href":"https://a.test/","text":"Home"}]}`)
	in := newPageInterner()
	p1, _ := decodeAnalysisPage(raw)
	p2, _ := decodeAnalysisPage(raw)
	in.compact(p1)
	in.compact(p2)
	if unsafe.StringData(p1.Anchors[0].Href) != unsafe.StringData(p2.Anchors[0].Href) {
		t.Fatal("href not shared")
	}
	if p1.Anchors[0].Href != "https://a.test/" || p2.Anchors[0].Text != "Home" {
		t.Fatal("values changed")
	}
}

// Headings keep their values, order and nil/empty shape; repeated headings
// share one copy and slices lose spare capacity.
func TestPageInternerSharesHeadings(t *testing.T) {
	raw := []byte(`{"headings":{"h1":["Title"],"h2":["Related","Reviews","Related"],"h3":[],"h4":["Footer","About","Contact","Legal","Help"]}}`)
	want, _ := decodeAnalysisPage(raw)
	in := newPageInterner()
	p1, _ := decodeAnalysisPage(raw)
	p2, _ := decodeAnalysisPage(raw)
	in.compact(p1)
	in.compact(p2)
	if !reflect.DeepEqual(p1, want) || !reflect.DeepEqual(p2, want) {
		t.Fatalf("compact changed headings: %+v", p1.Headings)
	}
	if p1.Headings.H3 == nil || p1.Headings.H5 != nil {
		t.Fatal("empty must stay empty, nil must stay nil")
	}
	if unsafe.StringData(p1.Headings.H2[0]) != unsafe.StringData(p2.Headings.H2[2]) {
		t.Fatal("repeated heading not shared")
	}
	if h := p1.Headings.H4; cap(h) != len(h) {
		t.Fatalf("h4 cap %d len %d", cap(h), len(h))
	}
}
