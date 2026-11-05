package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUploadImportTokenTransport: `micelio import --auth-token` sends the
// token only over https or to this computer, and never follows a redirect.
func TestUploadImportTokenTransport(t *testing.T) {
	file := filepath.Join(t.TempDir(), "site.db")
	os.WriteFile(file, []byte("x"), 0o600)

	// Plain http to another computer: refused before any request.
	for _, base := range []string{"http://192.0.2.10:3100", "http://dashboard.example:3100"} {
		err := uploadImport(base, "TOKEN", file, false)
		if err == nil || !strings.Contains(err.Error(), "refusing to send the dashboard token") {
			t.Errorf("%s: err = %v, want refusal", base, err)
		}
	}

	// Loopback http is fine; a redirect is not followed.
	var gotAuth string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect followed with Authorization %q", r.Header.Get("Authorization"))
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, elsewhere.URL+"/api/crawls/import", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if err := uploadImport(srv.URL, "TOKEN", file, false); err == nil {
		t.Error("a redirect answer should fail the import")
	}
	if gotAuth != "Bearer TOKEN" {
		t.Errorf("loopback dashboard got Authorization %q", gotAuth)
	}
}
