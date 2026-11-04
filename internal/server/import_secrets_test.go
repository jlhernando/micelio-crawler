package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/storage"
)

// TestImportScrubsCrawlDBSecrets: a crawl database made by an older version
// (config with credentials) is imported without them: not in the copy, not
// in the job config, not in the analysis config.
func TestImportScrubsCrawlDBSecrets(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	src := filepath.Join(t.TempDir(), "old.db")
	writeTestCrawlDB(t, src)
	cs, err := storage.NewCrawlStore(src)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"seedUrl":"https://shop.example/","mode":"spider","maxPages":100,"cookies":"sid=COOKIE-SECRET","customHeaders":{"Authorization":"Bearer HEADER-SECRET"},"proxy":"http://bob:PROXY-SECRET@proxy.example:8080","embeddingKey":"EMBED-SECRET","psiKey":"PSI-SECRET"}`
	cs.SaveMeta("config", legacy)
	cs.Close()
	secretWords := []string{"COOKIE-SECRET", "HEADER-SECRET", "PROXY-SECRET", "EMBED-SECRET", "PSI-SECRET"}

	p, err := m.prepareImport(src, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfgJSON, _ := json.Marshal(p.cfg)
	jobJSON, _ := json.Marshal(p.job.Config)
	for _, w := range secretWords {
		if strings.Contains(string(cfgJSON), w) {
			t.Errorf("analysis config holds %s", w)
		}
		if strings.Contains(string(jobJSON), w) {
			t.Errorf("job config holds %s", w)
		}
	}
	if err := m.runImport(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	m.WaitBackground()
	for _, f := range []string{p.job.DBPath, p.job.DBPath + "-wal"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, w := range secretWords {
			if bytes.Contains(data, []byte(w)) {
				t.Errorf("imported copy %s holds %s", filepath.Base(f), w)
			}
		}
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(p.job.DBPath); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("imported copy mode = %v (%v), want 0600", fi.Mode().Perm(), err)
		}
	}
	if !strings.Contains(readFile(t, src), "COOKIE-SECRET") {
		t.Error("the source database must not be modified")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestCrawlDirOwnerOnly: the dashboard's crawls/ directory is owner-only,
// also when an older version created it 0755.
func TestCrawlDirOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".micelio", "ui", "crawls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := OpenUiStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("crawls dir mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
}
