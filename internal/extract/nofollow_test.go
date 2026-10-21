package extract

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestCountNofollowInternal(t *testing.T) {
	rel := func(s string) *string { return &s }
	anchors := []types.AnchorData{
		{Href: "https://ex.com/a", IsInternal: true, Rel: rel("NoFollow noopener")},
		{Href: "https://ex.com/a", IsInternal: true, Rel: rel("nofollow")}, // same href: counted once
		{Href: "https://ex.com/b", IsInternal: true, Rel: rel("ugc")},
		{Href: "https://ex.com/c", IsInternal: true},
		{Href: "https://other.com/", Rel: rel("nofollow")}, // external: ignored
		{Href: "https://ex.com/d", IsInternal: true, Rel: rel("sponsored NOFOLLOW")},
	}
	if got := countNofollowInternal(anchors); got != 2 {
		t.Errorf("countNofollowInternal = %d, want 2", got)
	}
	if got := countNofollowInternal(nil); got != 0 {
		t.Errorf("nil anchors = %d", got)
	}
}
