package storage

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Pages decoded one after another through a shared scratch must equal pages
// decoded on their own: nil/empty/absent kept, no stale element fields, no
// page left pointing at scratch memory.
func TestScratchDecodeMatchesPlainDecode(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"url":"big","anchors":[`)
	for i := range 150 {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprintf(&big, `{"href":"/p%d","text":"t%d","position":"nav","isInternal":true,"isNonDescriptive":true}`, i, i)
	}
	big.WriteString(`],"images":[`)
	for i := range 90 {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprintf(&big, `{"src":"/i%d.png","alt":"a%d","width":"%d","height":"1"}`, i, i, i)
	}
	big.WriteString(`]}`)
	seq := []string{
		big.String(),
		`{"url":"few","anchors":[{"href":"/x"}],"images":[{"src":"/y"}]}`,
		`{"url":"empty","anchors":[],"images":[]}`,
		`{"url":"null","anchors":null,"images":null}`,
		`{"url":"absent"}`,
		big.String(),
		`{"url":"noalt","images":[{"src":"/z"},{"src":"/w","alt":""}],"anchors":[{"text":"only text"}]}`,
	}
	for _, c := range trimCases {
		if !strings.Contains(c, "2x0") && !strings.Contains(c, "trailing") && c != `[1,2]` && !strings.HasSuffix(c, `}]`) {
			seq = append(seq, c)
		}
	}
	seq = append(seq, seq...)
	modes := []pageLoadOpts{
		{},
		{skipAnchors: true},
		{dropSchemaRaw: true, imageSummary: true},
		{skipAnchors: true, dropSchemaRaw: true},
		{dropSchemaRaw: true, cacheOnly: true},
	}
	for _, o := range modes {
		for _, drop := range []bool{false, true} {
			so := o
			so.scratch = newPageScratch()
			for i, c := range seq {
				want, wantErr := decodeAnalysisPageOpt([]byte(c), o)
				got, gotErr := decodeAnalysisPageOpt([]byte(c), so)
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("%+v #%d: errors differ: %v / %v", o, i, gotErr, wantErr)
				}
				if gotErr != nil {
					continue
				}
				if drop && len(got.Anchors) > 0 { // like the import hooks
					got.Anchors, want.Anchors = nil, nil
				}
				so.scratch.release(got)
				if sameArray(so.scratch.anchors, got.Anchors) || sameArray(so.scratch.images, got.Images) {
					t.Fatalf("%+v #%d: page keeps scratch memory", o, i)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%+v drop=%v #%d %.60s:\n got %+v\nwant %+v", o, drop, i, c, got, want)
				}
			}
		}
	}
}

func TestScratchDoesNotLeakDuplicateArrayTailsIntoNextPage(t *testing.T) {
	for _, first := range []string{
		`{"anchors":[{"href":"a","text":"a"},{"href":"b","text":"old"}],"anchors":[{"href":"final"}],"images":[{"src":"a"},{"src":"b","alt":"old"}],"images":[{"src":"final"}]}`,
		`{"anchors":[{"text":"old"}],"anchors":null,"images":[{"alt":"old"}],"images":null}`,
		`{"anchors":[{"text":"old"}],"anchors":[],"images":[{"alt":"old"}],"images":[]}`,
	} {
		o := pageLoadOpts{scratch: newPageScratch()}
		p, err := decodeAnalysisPageOpt([]byte(first), o)
		if err != nil {
			t.Fatal(err)
		}
		o.scratch.release(p)
		next := []byte(`{"anchors":[{"href":"x"},{"href":"y"}],"images":[{"src":"x"},{"src":"y"}]}`)
		got, err := decodeAnalysisPageOpt(next, o)
		if err != nil {
			t.Fatal(err)
		}
		o.scratch.release(got)
		want, err := decodeAnalysisPageOpt(next, pageLoadOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scratch leaked previous page after %s", first)
		}
	}
}
