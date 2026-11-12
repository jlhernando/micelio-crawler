package storage

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// decodePageLinks must give exactly what json.Unmarshal gives, error or not.
func TestDecodePageLinksMatchesUnmarshal(t *testing.T) {
	alt := "a \"quoted\" alt ]}"
	page := types.PageData{
		URL:           "https://example.com/a?b=\"c\"&d=\\e",
		InternalLinks: []string{"https://example.com/", "https://example.com/x]}{["},
		Images:        []types.ImageData{{Src: "/i.png", Alt: &alt}},
		FinalURL:      "c {[",
	}
	full, _ := json.Marshal(page)
	empty, _ := json.Marshal(types.PageData{URL: "https://example.com/"})
	cases := []string{
		string(full),
		string(empty),
		`{}`,
		` { "url" : "u1" , "internalLinks" : [ "a" , "b" ] , "x" : { "y" : [ 1 , true , null , "]" ] } } `,
		`{"url":"u1","internalLinks":["a"],"url":"u2","internalLinks":null}`,
		`{"internalLinks":["a"],"internalLinks":[]}`,
		`{"url":null,"internalLinks":["a"]}`,
		`{"URL":"upper","InternalLinks":["b"]}`,
		`{"url":"escaped key"}`,
		`{"url":"é😀","internalLinks":["\/x"]}`,
		`{"url":5,"internalLinks":["a"]}`,
		`{"url":"u","internalLinks":["a",1]}`,
		`{"url":"u","x":[1,2}`,
		`{"url":"u"`,
		`{"url":"u",}`,
		`{"url":"u"} trailing`,
		`{"url":"https://x/","status":tru,"internalLinks":["/a"]}`,
		`{"url":"u","x":1 2,"internalLinks":[]}`,
		`{"url":"u","n":-0.5e+3,"m":0,"internalLinks":["a"]}`,
		`{"url":"u","n":01,"internalLinks":["a"]}`,
		`{"url":"u","n":1.,"internalLinks":["a"]}`,
		`{"url":"u","n":-,"internalLinks":["a"]}`,
		`[]`,
		`null`,
		``,
	}
	for _, c := range cases {
		var want, got pageLinks
		wantErr := json.Unmarshal([]byte(c), &want)
		gotErr := decodePageLinks([]byte(c), &got)
		if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
			t.Errorf("%q: err %v, want %v", c, gotErr, wantErr)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %#v, want %#v", c, got, want)
		}
	}
}
