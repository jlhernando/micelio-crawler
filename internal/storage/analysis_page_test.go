package storage

import (
	"encoding/json"
	"github.com/micelio/micelio/internal/types"
	"reflect"
	"testing"
)

func FuzzStringArrayCount(f *testing.F) {
	for _, s := range []string{`[]`, `null`, `["a","b"]`, `[null,"a"]`, `["\\\"","\\u0022","é"]`, `[{},"a"]`, `[true]`, `{"a":1}`, `["a",]`, `"[]"`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var want []string
		oldErr := json.Unmarshal([]byte(input), &want)
		var got stringArrayCount
		err := json.Unmarshal([]byte(input), &got)
		if (err == nil) != (oldErr == nil) {
			t.Fatalf("errors differ for %q: %v / %v", input, oldErr, err)
		}
		if err == nil && int(got) != len(want) {
			t.Fatalf("%q count=%d want=%d", input, got, len(want))
		}
	})
}
func TestAnalysisProjectionPreservesMetadata(t *testing.T) {
	input := []byte(`{"url":"https://example.com/a","statusCode":200,"title":{"text":"Product","length":7},"internalLinks":["/a","/b"],"externalLinks":["https://elsewhere.com"],"schemaValidation":[{"type":"Product"}],"renderDiffs":[{"field":"title","original":"Old","rendered":"New"}],"images":[{"src":"/image.png"}],"canonical":"https://example.com/a","headings":{"h1":["Product"]}}`)
	var full types.PageData
	if err := json.Unmarshal(input, &full); err != nil {
		t.Fatal(err)
	}
	got, err := decodeAnalysisPage(input)
	if err != nil {
		t.Fatal(err)
	}
	full.InternalLinkCount = len(full.InternalLinks)
	full.ExternalLinkCount = len(full.ExternalLinks)
	full.InternalLinks = nil
	full.ExternalLinks = nil
	if !reflect.DeepEqual(&full, got) {
		t.Fatalf("metadata or link counts changed: %+v / %+v", full, got)
	}
}
func TestAnalysisProjectionKeepsStoredLinkCounts(t *testing.T) {
	page, err := decodeAnalysisPage([]byte(`{"url":"https://example.com","internalLinkCount":17,"internalLinks":["/a"],"externalLinkCount":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if page.InternalLinkCount != 17 || page.ExternalLinkCount != 4 {
		t.Fatalf("counts=%d/%d", page.InternalLinkCount, page.ExternalLinkCount)
	}
}
