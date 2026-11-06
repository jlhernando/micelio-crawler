package report

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// hostile is crawled text built to break out of HTML: a quoted attribute
// (title="..."), a tag and the <script> holding the report data.
const hostile = `x" onmouseover="alert(1)" a='</script><script>alert(2)</script>` + "\u2028"

func hostileReport(t *testing.T) string {
	t.Helper()
	stats := &types.CrawlStats{
		TotalPages:  1,
		StatusCodes: map[int]int{200: 1},
		Hreflang: &types.HreflangStats{
			PagesWithHreflang: 1,
			Codes:             []types.HreflangCodeCount{{Code: hostile, Pages: 1, Hint: "unknown language code " + hostile}},
		},
	}
	pages := []*types.PageData{{URL: "https://ex.com/" + hostile, StatusCode: 200,
		Title: &types.TextLength{Text: hostile}}}
	var buf bytes.Buffer
	if err := GenerateHTML(&buf, "https://ex.com/", stats, pages); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The page and stats JSON sit in a <script>: the crawled text must not be
// able to close it or end a JS line (U+2028), like the TS jsonForScript.
func TestReportJSONStaysInsideScript(t *testing.T) {
	html := hostileReport(t)
	if strings.Contains(html, "<script>alert(2)") || strings.Contains(html, "</script><script>alert") {
		t.Fatal("crawled text closed the report's <script>")
	}
	if strings.Contains(html, "\u2028") {
		t.Error("raw U+2028 in the report script")
	}
	if !strings.Contains(html, `\u003c/script\u003e`) {
		t.Error("expected < and > of crawled text escaped as \\u003c / \\u003e")
	}
}

// esc() builds innerHTML strings, crawled text included, and some of them
// go into quoted attributes (title="'+esc(c.hint)+'"). It must escape quotes.
func TestReportEscEscapesAttributeQuotes(t *testing.T) {
	html := hostileReport(t)
	m := regexp.MustCompile(`(?m)^function esc\(s\)\{.*\}$`).FindString(html)
	if m == "" {
		t.Fatal("esc() not found in the report")
	}
	if !strings.Contains(m, `&quot;`) || !strings.Contains(m, `&#39;`) {
		t.Fatalf("esc() does not escape quotes: %s", m)
	}
	diff, err := GenerateDiffHTMLString(&DiffResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, `&quot;`) {
		t.Error("diff report esc() does not escape quotes")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: esc() checked by source only")
	}
	script := m + "\nconst out = esc(process.argv[1]);\n" +
		"if (/[\"'<>]/.test(out)) { console.error(out); process.exit(1) }\n" +
		"const tpl = '<span title=\"' + out + '\">x</span>';\n" +
		"if ((tpl.match(/\"/g) || []).length !== 2) { console.error(tpl); process.exit(1) }\n"
	cmd := exec.Command(node, "-e", script, "--", hostile)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("esc() let a quote through: %v\n%s", err, b)
	}
}
