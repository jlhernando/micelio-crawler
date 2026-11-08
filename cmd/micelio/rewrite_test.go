package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

type closeFailRewriter struct {
	path string
}

func (f closeFailRewriter) RewriteAll(pages []*types.PageData) error {
	// A partial stream reached the disk before the final flush failed.
	return os.WriteFile(f.path, []byte(`{"url":"https://exa`), 0o644)
}

func (f closeFailRewriter) Close() error { return errors.New("no space left on device") }

func TestRewriteEnrichedOutputKeepsOriginalWhenCloseFails(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "crawl.jsonl.gz")
	original := []byte("valid original output")
	if err := os.WriteFile(out, original, 0o644); err != nil {
		t.Fatal(err)
	}
	prev := newRewriteWriter
	t.Cleanup(func() { newRewriteWriter = prev })
	var tmp string
	newRewriteWriter = func(path, format string) (pageRewriter, error) {
		tmp = path
		return closeFailRewriter{path: path}, nil
	}

	err := rewriteEnrichedOutput(out, "jsonl", []*types.PageData{{URL: "https://example.com/"}})
	if err == nil {
		t.Fatal("expected an error when Close fails")
	}
	got, _ := os.ReadFile(out)
	if string(got) != string(original) {
		t.Fatalf("original output replaced: %q", got)
	}
	if _, statErr := os.Stat(tmp); !os.IsNotExist(statErr) {
		t.Fatalf("temp file %s left behind", tmp)
	}
}

func TestRewriteEnrichedOutputReplacesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "crawl.jsonl")
	if err := os.WriteFile(out, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pages := []*types.PageData{{URL: "https://example.com/", StatusCode: 200}}
	if err := rewriteEnrichedOutput(out, "jsonl", pages); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if len(got) == 0 || string(got) == "old\n" {
		t.Fatalf("output not rewritten: %q", got)
	}
}
