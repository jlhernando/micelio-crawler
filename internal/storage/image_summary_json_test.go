package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

var long = strings.Repeat("a", 126)

var imageSummaryCases = []string{
	`{"url":"u","images":[{"src":"/a.png","alt":"x","width":"1","height":"2"},{"src":"/b","alt":""},{"src":"/c"},null,{"src":"/d","alt":"` + long + `","width":"3"}]}`,
	`{"url":"u","images":[{"src":"/e","alt":"` + strings.Repeat("a", 125) + `","width":"1","height":"1"}]}`,
	`{"url":"u","images":[{"src":"/\u00e9\\n","alt":"\u00e9` + long + `"}]}`,
	`{"url":"u","images":[{"src":"/f","alt":"` + strings.Repeat("\\u00e9", 63) + `"}]}`,
	"{\"url\":\"u\",\"images\":[{\"src\":\"/g\xff\",\"alt\":\"" + strings.Repeat("\xff", 42) + "\"}]}",
	`{"url":"u","images":[{"src":"/h","alt":"x","alt":null,"width":"1","width":null,"height":"1"}]}`,
	`{"url":"u","images":[{"src":"/i","src":null,"alt":null,"alt":"` + long + `"}]}`,
	`{"url":"u","images":[{"SRC":"/j","Alt":"` + long + `"}]}`,
	`{"url":"u","images":[{"ſrc":"/k","alt":"` + long + `"}]}`,
	`{"url":"u","images":[{"\u0061lt":"y"}]}`,
	`{"url":"u","images":[{"src":"/l","hasAlt":true,"missingDimensions":false,"altTooLong":{"x":[1]},"alt":"z"}]}`,
	`{"url":"u","images":[{"src":"/m","inlineBytes":12345,"width":"1","height":"1"}]}`,
	`{"url":"u","images":[{"src":"/n","inlineBytes":1e999}]}`,
	`{"url":"u","images":[{"src":"/o","inlineBytes":1.5}]}`,
	`{"url":"u","images":[{"src":"/p","inlineBytes":"7"}]}`,
	`{"url":"u","images":[{"src":"/r","inlineBytes":null,"width":3}]}`,
	`{"url":"u","images":[{"src":1,"alt":"x"}]}`,
	`{"url":"u","images":[{"src":"/t","height":{}}]}`,
	`{"url":"u","images":[1,"x"]}`,
	`{"url":"u","images":{"src":"/u"}}`,
	`{"url":"u","images":null}`,
	`{"url":"u","images":[]}`,
	`{"url":"u","images":[{"src":"/v","alt":"` + long + `"}],"images":[{"src":"/w"}]}`,
	`{"url":"u","images":[{"src":"/x"}],"Images":null}`,
	` { "url" : "u" , "images" : [ { "src" : "/y" , "alt" : "` + long + `" , "width" : "1" , "height" : "1" } , null , { } ] } `,
	`{"url":"u","images":[{"src":"/z","alt":"x"}],"statusCode":"bad"}`,
	`{"url":"u","images":[{"src":"/z"}],"anchors":[{"href":"/a","text":"t"}],"internalLinks":["/a",null],"structuredData":[{"type":"P","format":"json-ld","raw":"{}"}]}`,
}

func imageSummaryModes() []pageLoadOpts {
	return []pageLoadOpts{
		{dropSchemaRaw: true, imageSummary: true},
		{skipAnchors: true, dropSchemaRaw: true, imageSummary: true},
	}
}

// checkImageSummary compares the trimmed path (scanner) with the full decode
// of Images followed by SummarizeImages: same page, same error text.
func checkImageSummary(t *testing.T, c string, o pageLoadOpts) {
	t.Helper()
	got, gotErr := decodeAnalysisPageOpt([]byte(c), o)
	ref := o
	ref.noTrim = true
	want, wantErr := decodeAnalysisPageOpt([]byte(c), ref)
	if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
		t.Fatalf("%+v %q: errors differ: %v / %v", o, c, gotErr, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %q:\n got %+v\nwant %+v", o, c, got, want)
	}
	if got != nil && got.Images != nil {
		t.Fatalf("%q: Images not folded", c)
	}
}

func TestImageSummaryJSONMatchesFullDecode(t *testing.T) {
	for _, c := range append(imageSummaryCases, trimCases...) {
		for _, o := range imageSummaryModes() {
			checkImageSummary(t, c, o)
			o.scratch = newPageScratch()
			checkImageSummary(t, c, o)
		}
	}
}

// The common shapes must take the scanner, not the fallback.
func TestImageSummaryJSONFastPath(t *testing.T) {
	for _, c := range imageSummaryCases[:7] {
		if _, _, _, ok := trimUnreadPageJSON([]byte(c), nil, nil, new(imageFold)); !ok {
			t.Fatalf("slow path: %s", c)
		}
	}
}

func FuzzImageSummaryJSON(f *testing.F) {
	for _, c := range append(imageSummaryCases, trimCases...) {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, c string) {
		for _, o := range imageSummaryModes() {
			checkImageSummary(t, c, o)
		}
	})
}

func TestImageSummaryJSONRejectsExcessiveDocumentDepth(t *testing.T) {
	// The images value alone is valid at depth 10000. The enclosing page
	// pushes it past encoding/json's document limit and must still fail.
	nested := strings.Repeat("[", 9998) + "0" + strings.Repeat("]", 9998)
	c := `{"images":[{"unknown":` + nested + `}]}`
	for _, o := range imageSummaryModes() {
		checkImageSummary(t, c, o)
	}
}

// Exercise valid image objects directly, including duplicate keys. Unlike
// arbitrary document bytes, every generated input reaches image decoding.
func FuzzImageSummaryFields(f *testing.F) {
	f.Add("/image.png", "", uint8(0))
	f.Add("/\u00e9", strings.Repeat("é", 63), uint8(7))
	f.Fuzz(func(t *testing.T, src, alt string, shape uint8) {
		qs, _ := json.Marshal(src)
		qa, _ := json.Marshal(alt)
		keys := []string{"src", "alt", "width", "height", "inlineBytes", "unknown", "ALT", `\u0061lt`}
		values := []string{"null", string(qs), string(qa), "0", "2147483648", "9223372036854775808", "{}", "[]"}
		raw := `[{"src":` + string(qs) + `,"alt":` + string(qa) + `,"` + keys[int(shape)%len(keys)] + `":` + values[(int(shape)/len(keys))%len(values)] + `},null,{}]`
		var images []types.ImageData
		err := json.Unmarshal([]byte(raw), &images)
		if got, ok := summarizeImagesJSON([]byte(raw)); ok {
			if err != nil || !reflect.DeepEqual(got, types.SummarizeImages(images)) {
				t.Fatalf("summary differs for %q: error %v, got %+v, want %+v", raw, err, got, types.SummarizeImages(images))
			}
		}
		for _, o := range imageSummaryModes() {
			checkImageSummary(t, `{"images":`+raw+`}`, o)
		}
	})
}
