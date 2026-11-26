package storage

import (
	"reflect"
	"testing"
)

var trimCases = []string{
	`{"url":"https://example.com/a","statusCode":200,"title":{"text":"Product","length":7},"internalLinks":["/a","/b"],"externalLinks":["https://elsewhere.com"],"anchors":[{"href":"/a","text":"A \"q\" ]}","position":"nav"}],"schemaValidation":[{"type":"Product"}],"renderDiffs":[{"field":"title","original":"Old","rendered":"New"}],"images":[{"src":"/image.png","alt":"x"}],"openGraph":{"og:title":"T"},"twitterCard":{"twitter:card":"s"},"structuredData":[{"type":"Product","format":"json-ld","raw":"{\"@type\":\"Product\"}"}],"canonical":"https://example.com/a","headings":{"h1":["Product"]}}`,
	` { "url" : "u" , "internalLinks" : [ "a" , null ] , "internalLinks":["x"], "anchors" : null } `,
	`{"url":"u","internalLinkCount":17,"internalLinks":["/a"],"externalLinkCount":4}`,
	`{}`,
	`{"url":"u","Anchors":[{"href":"/a"}]}`,            // case-folded key: whole-blob path
	`{"url":"u","InternalLinks":["a","b"]}`,            // case-folded link key
	`{"url":"u","internalLinks":[1,2]}`,                // counter rejects: same error
	`{"url":"u","images":[{"src":"a"}],"wordCount":5}`, // images kept unless cacheOnly
	`{"u\u0072l":"x","internalLinks":["a"]}`,           // escaped key
	`{"url":"u","statusCode":2x0}`,                     // invalid kept scalar
	`{"url":"u","anchors":[{"href":"a"}]`,              // truncated
	`{"url":"u"} trailing`,
	`[1,2]`,
	`{"anchors":[tru]}`,
	`{"images":[{"src":"\\q"}],"anchors":[1,]}`,
	`{"internalLinks":["a",]}`,
	`{"externalLinks":["\\q"]}`,
}

func TestTrimmedDecodeMatchesFullDecode(t *testing.T) {
	modes := []pageLoadOpts{
		{},
		{skipAnchors: true},
		{dropSchemaRaw: true, imageSummary: true},
		{skipAnchors: true, dropSchemaRaw: true},
		{dropSchemaRaw: true, cacheOnly: true},
		{skipAnchors: true, dropSchemaRaw: true, cacheOnly: true},
	}
	for _, c := range trimCases {
		for _, o := range modes {
			got, gotErr := decodeAnalysisPageOpt([]byte(c), o)
			full := o
			full.noTrim = true
			want, wantErr := decodeAnalysisPageOpt([]byte(c), full)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("%+v %s: errors differ: %v / %v", o, c, gotErr, wantErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%+v %s:\n got %+v\nwant %+v", o, c, got, want)
			}
		}
	}
}

func FuzzTrimmedDecode(f *testing.F) {
	for _, c := range trimCases {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, c string) {
		o := pageLoadOpts{skipAnchors: true, dropSchemaRaw: true, cacheOnly: true}
		got, gotErr := decodeAnalysisPageOpt([]byte(c), o)
		o.noTrim = true
		want, wantErr := decodeAnalysisPageOpt([]byte(c), o)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%s: errors differ: %v / %v", c, gotErr, wantErr)
		}
		if gotErr == nil && wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", c, got, want)
		}
	})
}
