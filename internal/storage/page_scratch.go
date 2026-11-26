package storage

import (
	"slices"
	"unsafe"

	"github.com/micelio/micelio/internal/types"
)

// pageScratch lends a reload one backing array for Anchors and one for
// Images. encoding/json appends into a slice's existing capacity, so a page
// decoded with Anchors = anchors[:0] fills the shared array instead of
// growing a new one by doubling (~2.7 GB of garbage on a 100K-page import).
// Both fields are usually dropped right after decoding (anchor hooks clear
// Anchors, imageSummary folds Images); a page that still holds one gets an
// exact copy, so no page ever keeps scratch memory.
//
// Values are unchanged: a missing key leaves the primed slice untouched
// (cap > 0, len 0: turned back into nil), "[]" makes json set a new
// zero-cap empty slice, null sets nil. Used elements are zeroed before
// reuse because json decodes into existing elements without clearing them.
type pageScratch struct {
	anchors []types.AnchorData
	images  []types.ImageData
}

const pageScratchCap = 64

func newPageScratch() *pageScratch {
	return &pageScratch{
		anchors: make([]types.AnchorData, 0, pageScratchCap),
		images:  make([]types.ImageData, 0, pageScratchCap),
	}
}

// prime points the fields json will decode at the scratch arrays.
func (s *pageScratch) prime(p *types.PageData, o pageLoadOpts) {
	if s == nil {
		return
	}
	if !o.skipAnchors {
		p.Anchors = s.anchors[:0]
	}
	if !o.cacheOnly {
		p.Images = s.images[:0]
	}
}

// settle runs right after decoding: it adopts an array json had to grow
// (the larger one serves later pages) and restores nil for absent keys.
func (s *pageScratch) settle(p *types.PageData) {
	if s == nil {
		return
	}
	s.anchors, p.Anchors = settleScratch(s.anchors, p.Anchors)
	s.images, p.Images = settleScratch(s.images, p.Images)
}

// release runs once the page's hooks are done: a field still holding
// scratch memory gets an exact copy, then the buffers are cleared.
func (s *pageScratch) release(p *types.PageData) {
	if s == nil {
		return
	}
	p.Anchors = releaseScratch(s.anchors, p.Anchors)
	p.Images = releaseScratch(s.images, p.Images)
}

func sameArray[T any](a, b []T) bool {
	return cap(a) > 0 && cap(b) > 0 && unsafe.SliceData(a) == unsafe.SliceData(b)
}

func settleScratch[T any](scratch, v []T) ([]T, []T) {
	if cap(v) > cap(scratch) {
		// json grew past the scratch array: keep the larger one (the old
		// array is dropped with what json wrote into it).
		scratch = v[:0]
	}
	if !sameArray(scratch, v) {
		return scratch, v
	}
	if len(v) == 0 {
		return scratch, nil // key absent: the primed value was never touched
	}
	return scratch, v
}

func releaseScratch[T any](scratch, v []T) []T {
	if sameArray(scratch, v) {
		v = slices.Clone(v)
	}
	// A repeated JSON key can decode a longer array and then replace it
	// with a shorter array, null or []. Its discarded tail still contains
	// decoded fields, so clear the whole buffer before another page uses it.
	clear(scratch[:cap(scratch)])
	return v
}
