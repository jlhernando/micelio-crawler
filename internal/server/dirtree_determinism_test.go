package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// TestBuildDirectoryTreeDeterministicOnTies: 120 sibling directories with the
// same page count, more than maxTreeChildren. Which ones make the cut used to
// follow Go's random map order; it must be the same on every build.
func TestBuildDirectoryTreeDeterministicOnTies(t *testing.T) {
	var pages []*types.PageData
	for d := 0; d < 120; d++ {
		for i := 0; i < 3; i++ {
			pages = append(pages, &types.PageData{
				URL:            fmt.Sprintf("https://ex.com/d%03d/p%d", d, i),
				StatusCode:     200,
				ResponseTimeMs: int64(d*10 + i),
				Inlinks:        d,
			})
		}
	}
	build := func() string {
		b, err := json.Marshal(buildDirectoryTree(pages))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := build()
	for run := 1; run < 30; run++ {
		if got := build(); got != first {
			t.Fatalf("run %d: directory tree differs", run)
		}
	}
	root := buildDirectoryTree(pages)["tree"].(*dirNode)
	if root.Children[0].Name != "d000" || root.Children[maxTreeChildren-2].Name != fmt.Sprintf("d%03d", maxTreeChildren-2) {
		t.Fatalf("equal-sized siblings should go by name: first %q, last kept %q",
			root.Children[0].Name, root.Children[maxTreeChildren-2].Name)
	}
}
