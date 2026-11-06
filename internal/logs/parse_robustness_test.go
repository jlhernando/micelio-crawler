package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A BOM before the first data record must not drop that record.
func TestParseFileBOMFirstRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	line := `66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET /first HTTP/1.1" 200 512 "-" "Googlebot/2.1"`
	body := "\xef\xbb\xbf" + line + "\n" + strings.Replace(line, "/first", "/second", 1) + "\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var paths []string
	state := &ParseState{}
	if _, _, err := ParseFileWithState(p, "", state, func(e *LogEntry) { paths = append(paths, e.Path) }, nil); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/first" {
		t.Fatalf("paths = %v, want [/first /second]", paths)
	}
	if state.ParseErrors != 0 {
		t.Fatalf("ParseErrors = %d, want 0", state.ParseErrors)
	}
}

// Rows that do not parse are counted; a file where none parse is an error.
func TestParseErrorsCounted(t *testing.T) {
	dir := t.TempDir()
	good := `66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET /a HTTP/1.1" 200 512 "-" "Googlebot/2.1"`
	mixed := filepath.Join(dir, "mixed.log")
	if err := os.WriteFile(mixed, []byte(good+"\nnot a log line\n"+good+"\n{broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := AnalyzeFull(mixed, FormatApacheCombined, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.ParseErrors != 2 || res.Stats.TotalHits != 2 {
		t.Fatalf("ParseErrors=%d TotalHits=%d, want 2 and 2", res.Stats.ParseErrors, res.Stats.TotalHits)
	}
	multi, err := AnalyzeMulti([]string{mixed}, FormatApacheCombined, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if multi.Stats.ParseErrors != 2 || multi.Files[0].ParseErrors != 2 {
		t.Fatalf("multi ParseErrors=%d file=%d, want 2", multi.Stats.ParseErrors, multi.Files[0].ParseErrors)
	}

	// Wrong format forced: nothing parses, the analysis must not "complete".
	if _, err := AnalyzeFull(mixed, FormatW3C, nil, nil); err == nil || !strings.Contains(err.Error(), "matched") {
		t.Fatalf("all-rows-failed analysis returned err=%v, want a format error", err)
	}
}

// AnalyzeMulti calls onProgress one at a time, so callers can keep plain
// (unlocked) throttle state. Run with -race.
func TestAnalyzeMultiProgressSerialized(t *testing.T) {
	dir := t.TempDir()
	line := `66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET /a HTTP/1.1" 200 512 "-" "Googlebot/2.1"` + "\n"
	var paths []string
	for i := range 4 {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".log")
		if err := os.WriteFile(p, []byte(strings.Repeat(line, 25_000)), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	calls, inside := 0, 0
	_, err := AnalyzeMulti(paths, "", 4, func(MultiProgress) {
		inside++
		if inside != 1 {
			t.Errorf("progress callbacks overlap")
		}
		calls++
		inside--
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("no progress callbacks")
	}
}

// The W3C cs-uri-query column is part of the URL.
func TestParseW3CQuery(t *testing.T) {
	s := &ParseState{}
	s.SetW3C("#Fields: date time c-ip cs-method cs-uri-stem cs-uri-query sc-status sc-bytes cs(User-Agent)")
	e, err := ParseLineWithState("2026-04-01 08:15:30 66.249.66.1 GET /search q=a 200 10 Googlebot/2.1", FormatW3C, s)
	if err != nil {
		t.Fatal(err)
	}
	if e.Path != "/search?q=a" {
		t.Fatalf("Path = %q, want /search?q=a", e.Path)
	}
	e, _ = ParseLineWithState("2026-04-01 08:15:30 66.249.66.1 GET /about - 200 10 Googlebot/2.1", FormatW3C, s)
	if e.Path != "/about" {
		t.Fatalf("Path = %q, want /about", e.Path)
	}
}
