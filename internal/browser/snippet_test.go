package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A raw expression is left alone; a TypeScript crawler snippet is wrapped.
func TestSnippetExpression(t *testing.T) {
	if got := snippetExpression(`document.title`); got != `document.title` {
		t.Errorf("raw expression changed: %q", got)
	}
	got := snippetExpression("/** doc */\nexport default async (page) => page.url();\n")
	if strings.Contains(got, "export") || !strings.Contains(got, "__micelioSnippet(page)") {
		t.Errorf("legacy snippet not wrapped: %s", got)
	}
}

// The bundled snippets/ run in the Go crawler (they were written for the
// TypeScript crawler's Playwright page), and performance.js reports LCP.
func TestBundledSnippetsRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>T</title></head><body><h1>Hello</h1><p>`+strings.Repeat("text ", 200)+`</p></body></html>`)
	}))
	defer srv.Close()
	r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second})

	for _, name := range []string{"analytics", "cookie-consent", "tech-stack", "accessibility", "performance"} {
		code, err := os.ReadFile(filepath.Join("..", "..", "snippets", name+".js"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.RunSnippet(context.Background(), srv.URL+"/", "Micelio/1.0", string(code), nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		m, ok := res.(map[string]any)
		if !ok || len(m) == 0 {
			t.Errorf("%s: result %T %v, want an object", name, res, res)
			continue
		}
		if name == "performance" {
			if m["lcp_ms"] == nil {
				t.Errorf("performance: lcp_ms is null: %v", m)
			}
			if _, ok := m["cls"]; !ok {
				t.Errorf("performance: no cls: %v", m)
			}
		}
	}

	// Page APIs the stand-in does not have fail with a clear error.
	code, _ := os.ReadFile(filepath.Join("..", "..", "snippets", "console-errors.js"))
	if _, err := r.RunSnippet(context.Background(), srv.URL+"/", "Micelio/1.0", string(code), nil); err == nil || !strings.Contains(err.Error(), "not available to snippets") {
		t.Errorf("console-errors: err = %v, want a not-available error", err)
	}
}
