package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rows written before the derived image fields were dropped still decode,
// and the methods give the answers the old booleans stored.
func TestImageDataDecodesOldCrawls(t *testing.T) {
	old := `{"url":"https://example.com/","statusCode":200,"images":[
{"alt":"Logo","width":"120","height":"40","src":"https://example.com/logo.png","altLength":4,"hasAltAttribute":true},
{"alt":null,"src":"https://example.com/a.jpg","missingAlt":true,"hasAltAttribute":false,"missingWidth":true,"missingHeight":true},
{"alt":"","width":"10","src":"https://example.com/b.gif","hasAltAttribute":true,"missingHeight":true}]}`
	var p PageData
	if err := json.Unmarshal([]byte(old), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Images) != 3 {
		t.Fatalf("decoded %d images, want 3", len(p.Images))
	}
	a, b, c := p.Images[0], p.Images[1], p.Images[2]
	if a.MissingAlt() || *a.Alt != "Logo" || a.MissingDimensions() || a.Src != "https://example.com/logo.png" {
		t.Errorf("image 0 = %+v", a)
	}
	if !b.MissingAlt() || !b.MissingDimensions() {
		t.Errorf("image 1 = %+v", b)
	}
	if c.MissingAlt() || *c.Alt != "" || !c.MissingDimensions() {
		t.Errorf("image 2 = %+v", c)
	}
}

// A new row stores only the reference: no derived keys, no "alt":null.
func TestImageDataEncodesReferenceOnly(t *testing.T) {
	alt, w := "Logo", "120"
	b, _ := json.Marshal([]ImageData{{Src: "https://example.com/logo.png", Alt: &alt, Width: &w}, {Src: "https://example.com/a.jpg"}})
	got := string(b)
	want := `[{"src":"https://example.com/logo.png","alt":"Logo","width":"120"},{"src":"https://example.com/a.jpg"}]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, k := range []string{"altLength", "missingAlt", "hasAltAttribute", "altTooLong", "missingWidth", "missingHeight"} {
		if strings.Contains(got, k) {
			t.Errorf("derived key %q still written", k)
		}
	}
}
