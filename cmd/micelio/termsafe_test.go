package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// osc52 writes "evil" to the clipboard of terminals that allow it; the
// CSI sequence clears the screen.
const hostileSeq = "\x1b]52;c;ZXZpbA==\x07\x1b[2J"

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func TestRobotsTestEscapesSiteText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nDisallow: /admin\nSitemap: https://ex.com/s.xml"+hostileSeq+"\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		cmd := robotsTestCmd()
		cmd.SetArgs([]string{srv.URL, "--agents", "Googlebot"})
		if err := cmd.Execute(); err != nil {
			t.Errorf("robots-test: %v", err)
		}
	})
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("robots-test sent control characters to the terminal: %q", out)
	}
	if !strings.Contains(out, `https://ex.com/s.xml\x1b]52;c;ZXZpbA==\x07\x1b[2J`) {
		t.Errorf("sitemap line not shown escaped: %q", out)
	}
}

func TestAgentReportEscapesSiteText(t *testing.T) {
	allowed := true
	h := types.AgentHostReport{
		URL: "https://ex.com/", Score: 50, Level: "partial",
		Categories: []types.AgentCategoryScore{{Category: "robots", Score: 50}},
		Checks: []types.AgentCheck{{Category: "robots", Status: types.AgentWarn, Title: "Sitemap",
			Evidence: "Sitemap: https://ex.com/s.xml" + hostileSeq, Fix: "fix" + hostileSeq}},
		Bots:           []types.AgentBotAccess{{Bot: "GPTBot" + hostileSeq, Allowed: &allowed}},
		ContentSignals: []string{"search=yes" + hostileSeq, "ai-train=no\u202e"},
	}
	var buf bytes.Buffer
	printAgentReport(&buf, h)
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07\u202e") {
		t.Errorf("agent-check sent control characters to the terminal: %q", out)
	}
	for _, want := range []string{`s.xml\x1b]52;c;`, `fix\x1b]52`, `GPTBot\x1b]52`, `search=yes\x1b]52`, `ai-train=no\u202e`} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses escaped %q:\n%s", want, out)
		}
	}
}
