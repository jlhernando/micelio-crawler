package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

// rewriteTransport routes api.github.com requests to a test server.
type rewriteTransport struct{ baseHost string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "api.github.com") {
		req.URL.Scheme = "http"
		req.URL.Host = rt.baseHost
		req.Host = rt.baseHost
	}
	return http.DefaultTransport.RoundTrip(req)
}

func newTestServer(t *testing.T, rel ghRelease, dlBytes []byte) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(dlBytes)
	})
	srv := httptest.NewServer(mux)
	host := strings.TrimPrefix(srv.URL, "http://")
	return srv, host
}

func TestStatusDetectsUpdateAvailable(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)
	srv, host := newTestServer(t, ghRelease{
		TagName: "v9.9.9",
		Name:    "Release v9.9.9",
		HTMLURL: "https://example.test/release/9.9.9",
		Assets: []ghAsset{
			{Name: wantAsset, BrowserDownloadURL: "http://" + "PLACEHOLDER" + "/dl/asset"},
			{Name: "micelio-other-arch", BrowserDownloadURL: "http://example.test/x"},
		},
	}, []byte("ignored"))
	defer srv.Close()

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
	srv, host := newTestServer(t, ghRelease{
		TagName: "v1.0.0",
		Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: "http://example.test/x"}},
	}, nil)
	defer srv.Close()

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

func TestInstallReplacesBinary(t *testing.T) {
	wantAsset := assetName(runtime.GOOS, runtime.GOARCH)
	newContents := []byte("new binary contents v9.9.9")

	// Build the server first so we know its address, then have the
	// release-metadata handler emit the matching asset URL.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		rel := ghRelease{
			TagName: "v9.9.9",
			Assets:  []ghAsset{{Name: wantAsset, BrowserDownloadURL: srv.URL + "/dl/asset"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(newContents)
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "micelio")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := New("test/repo", "1.0.0", bin, dir)
	u.httpClient = &http.Client{Transport: rewriteTransport{baseHost: host}}

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
}
