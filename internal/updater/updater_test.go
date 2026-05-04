package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIsDev(t *testing.T) {
	cases := map[string]bool{
		"":                 true,
		"dev":              true,
		"1.2.3":            false,
		"1.2.3-rc1":        false,
		"1.2.3-5-gabc1234": true, // git describe ahead-of-tag
		"1.2.3-dirty":      true,
		"abc1234":          true,
		"abc1234-dirty":    true,
	}
	for input, want := range cases {
		got := isDev(strings.TrimPrefix(input, "v"))
		if got != want {
			t.Errorf("isDev(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestAssetNameByPlatform(t *testing.T) {
	if got, want := assetName("darwin", "arm64"), "micelio-darwin-arm64"; got != want {
		t.Errorf("darwin/arm64: %q want %q", got, want)
	}
	if got, want := assetName("linux", "amd64"), "micelio-linux-amd64"; got != want {
		t.Errorf("linux/amd64: %q want %q", got, want)
	}
	if got, want := assetName("windows", "amd64"), "micelio-windows-amd64.exe"; got != want {
		t.Errorf("windows/amd64: %q want %q", got, want)
	}
}

func TestSemverNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.0.0", "1.0.0", true},
		{"1.1.0", "1.0.0", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "2.0.0", false},
		{"1.0.0", "1.0.1", false},
		// Pre-release suffix stripped for comparison
		{"1.1.0-rc1", "1.0.0", true},
	}
	for _, tc := range cases {
		got := semverNewer(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseChecksumFor(t *testing.T) {
	content := "abc123def456abc123def456abc123def456abc123def456abc123def456abc123de  micelio-linux-amd64\n" +
		"1111111111111111111111111111111111111111111111111111111111111111  micelio-darwin-arm64\n"
	hash, err := parseChecksumFor(strings.NewReader(content), "micelio-darwin-arm64")
	if err != nil {
		t.Fatalf("parseChecksumFor: %v", err)
	}
	if hash != "1111111111111111111111111111111111111111111111111111111111111111" {
		t.Errorf("unexpected hash: %s", hash)
	}

	_, err = parseChecksumFor(strings.NewReader(content), "micelio-windows-amd64.exe")
	if err == nil {
		t.Error("expected error for missing asset, got nil")
	}
}

// rewriteTransport routes all HTTPS requests to the local test server over HTTP.
// This allows tests to exercise the HTTPS-only validation path while using
// httptest.NewServer (plain HTTP).
type rewriteTransport struct{ baseHost string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite any request targeting the test host or known GitHub domains.
	if strings.Contains(req.URL.Host, "api.github.com") ||
		strings.Contains(req.URL.Host, "github.com") ||
		req.URL.Host == rt.baseHost {
		req.URL.Scheme = "http"
		req.URL.Host = rt.baseHost
		req.Host = rt.baseHost
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestStatusDetectsUpdateAvailable(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			Name:    "Release v9.9.9",
			HTMLURL: "https://example.test/release/9.9.9",
			Assets: []ghAsset{
				{Name: wantAsset, BrowserDownloadURL: "https://" + host + "/dl/asset"},
				{Name: "micelio-other-arch", BrowserDownloadURL: "https://example.test/x"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: rewriteTransport{baseHost: host}}

	st, err := u.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Current != "1.0.0" || st.Latest != "9.9.9" {
		t.Errorf("versions: current=%q latest=%q", st.Current, st.Latest)
	}
	if !st.UpdateAvailable {
		t.Error("UpdateAvailable should be true")
	}
	if !st.Downloadable {
		t.Errorf("Downloadable should be true (asset %q present)", wantAsset)
	}
	if st.AssetName != wantAsset {
		t.Errorf("AssetName = %q, want %q", st.AssetName, wantAsset)
	}
	if st.IsDevBuild {
		t.Error("IsDevBuild should be false")
	}
}

func TestStatusSameVersionMeansNoUpdate(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v1.0.0",
			Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: "https://example.test/x"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})

	dir := t.TempDir()
	u := New("test/repo", "1.0.0", filepath.Join(dir, "micelio"), dir)
	u.httpClient = &http.Client{Transport: rewriteTransport{baseHost: host}}

	st, err := u.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.UpdateAvailable {
		t.Error("UpdateAvailable should be false when versions match")
	}
}

func TestStatusDevBuildSkipsNetwork(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	dir := t.TempDir()
	u := New("test/repo", "ac6a19e-dirty", filepath.Join(dir, "micelio"), dir)
	u.httpClient = &http.Client{Transport: rewriteTransport{baseHost: host}}

	st, err := u.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.IsDevBuild {
		t.Error("IsDevBuild should be true")
	}
	if st.UpdateAvailable {
		t.Error("UpdateAvailable should be false for dev build")
	}
	if called {
		t.Error("dev build should not hit GitHub")
	}
}

func TestInstallReplacesBinaryWithChecksum(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)
	newContents := []byte("new binary contents v9.9.9")

	// Compute expected checksum
	h := sha256.Sum256(newContents)
	checksum := hex.EncodeToString(h[:])

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: "https://" + host + "/dl/asset"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(newContents)
	})
	mux.HandleFunc("/test/repo/releases/latest/download/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, wantAsset)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	transport := rewriteTransport{baseHost: host}
	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: transport}
	u.dlClient = &http.Client{Transport: transport, Timeout: 30 * time.Second}

	if _, err := u.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newContents) {
		t.Errorf("binary not replaced. got %q want %q", string(got), string(newContents))
	}

	// Verify backup was created
	bakPath := bin + ".bak"
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if string(bakContent) != "OLD" {
		t.Errorf("backup content = %q, want %q", string(bakContent), "OLD")
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)
	newContents := []byte("new binary contents v9.9.9")

	// Use a WRONG checksum
	wrongChecksum := "0000000000000000000000000000000000000000000000000000000000000000"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: "https://" + host + "/dl/asset"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(newContents)
	})
	mux.HandleFunc("/test/repo/releases/latest/download/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", wrongChecksum, wantAsset)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	transport := rewriteTransport{baseHost: host}
	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: transport}
	u.dlClient = &http.Client{Transport: transport, Timeout: 30 * time.Second}

	_, err := u.Install(context.Background())
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Errorf("unexpected error: %v", err)
	}

	// Binary should NOT be replaced
	got, _ := os.ReadFile(bin)
	if string(got) != "OLD" {
		t.Errorf("binary was replaced despite checksum mismatch! got %q", string(got))
	}
}

func TestInstallRejectsNonHTTPS(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			// Intentionally use http:// URL
			Assets: []ghAsset{{Name: wantAsset, BrowserDownloadURL: "http://" + host + "/dl/asset"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: rewriteTransport{baseHost: host}}

	_, err := u.Install(context.Background())
	if err == nil {
		t.Fatal("expected HTTPS rejection, got nil")
	}
	if !strings.Contains(err.Error(), "non-HTTPS") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRollback(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	bakPath := bin + ".bak"

	// Create "current" and "backup" binaries
	if err := os.WriteFile(bin, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bakPath, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("test/repo", "2.0.0", bin, dir)
	if err := u.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "OLD" {
		t.Errorf("rollback did not restore backup. got %q", string(got))
	}

	// Backup file should be gone (it was renamed)
	if _, err := os.Stat(bakPath); !os.IsNotExist(err) {
		t.Error("backup file should not exist after rollback")
	}
}

func TestRollbackNoBackup(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("CURRENT"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("test/repo", "1.0.0", bin, dir)
	err := u.Rollback()
	if err == nil {
		t.Fatal("expected error when no backup exists")
	}
	if !strings.Contains(err.Error(), "no backup") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInstallPreservesPermissions(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)
	newContents := []byte("new binary")

	h := sha256.Sum256(newContents)
	checksum := hex.EncodeToString(h[:])

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: "https://" + host + "/dl/asset"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(newContents)
	})
	mux.HandleFunc("/test/repo/releases/latest/download/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, wantAsset)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o700); err != nil {
		t.Fatal(err)
	}

	transport := rewriteTransport{baseHost: host}
	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: transport}
	u.dlClient = &http.Client{Transport: transport, Timeout: 30 * time.Second}

	if _, err := u.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("permissions not preserved: got %o, want %o", info.Mode().Perm(), 0o700)
	}
}
