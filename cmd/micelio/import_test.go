package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/server"
)

// fakeDashboard serves h behind the real identity check, as the dashboard
// of a temporary HOME (which holds its key).
func fakeDashboard(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := server.NewUiStore("test dashboard") // takes the lock, writes the key
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mux := http.NewServeMux()
	mux.HandleFunc("/api/identity", server.HandleIdentity)
	mux.Handle("/", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestUploadImportTokenTransport: `micelio import --auth-token` sends the
// token only over https or to this computer, and never follows a redirect.
func TestUploadImportTokenTransport(t *testing.T) {
	file := filepath.Join(t.TempDir(), "site.db")
	os.WriteFile(file, []byte("x"), 0o600)

	// Plain http to another computer: refused before any request.
	for _, base := range []string{"http://192.0.2.10:3100", "http://dashboard.example:3100"} {
		err := uploadImport(context.Background(), base, "TOKEN", file, false)
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
	srv := fakeDashboard(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, elsewhere.URL+"/api/crawls/import", http.StatusTemporaryRedirect)
	})
	if err := uploadImport(context.Background(), srv.URL, "TOKEN", file, false); err == nil {
		t.Error("a redirect answer should fail the import")
	}
	if gotAuth != "Bearer TOKEN" {
		t.Errorf("loopback dashboard got Authorization %q", gotAuth)
	}
}

// A dashboard that takes the file but never answers does not hang the
// import, and the command's context cancels the upload.
func TestUploadImportDoesNotHang(t *testing.T) {
	file := filepath.Join(t.TempDir(), "site.db")
	os.WriteFile(file, []byte("x"), 0o600)
	release := make(chan struct{})
	srv := fakeDashboard(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	defer close(release)
	old := importAnswerTimeout
	importAnswerTimeout = 200 * time.Millisecond
	defer func() { importAnswerTimeout = old }()
	start := time.Now()
	if err := uploadImport(context.Background(), srv.URL, "", file, false); err == nil {
		t.Fatal("silent dashboard: no error")
	}
	importAnswerTimeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := uploadImport(ctx, srv.URL, "", file, false); err == nil {
		t.Fatal("cancelled context: no error")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %s", d)
	}
}

// A program that sits on the dashboard's port but cannot prove it is this
// user's dashboard gets neither the crawl file nor the token.
func TestImportRefusesUnverifiedDashboard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "site.db")
	os.WriteFile(file, []byte("CRAWL-DATA"), 0o600)
	got := make(chan string, 4)
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Method == http.MethodPost {
			got <- r.Method + " " + r.URL.Path + " " + r.Header.Get("Authorization")
		}
		w.WriteHeader(http.StatusUnauthorized) // "a dashboard that needs a token"
	}))
	defer impostor.Close()

	if state, err := probeDashboard(impostor.URL); state != dashUnverified || !errors.Is(err, server.ErrNotDashboard) {
		t.Fatalf("probe = %v, %v; want unverified", state, err)
	}
	if err := uploadImport(context.Background(), impostor.URL, "TOKEN", file, false); !errors.Is(err, server.ErrNotDashboard) {
		t.Fatalf("upload err = %v, want ErrNotDashboard", err)
	}
	cmd := importCmd()
	cmd.SetArgs([]string{file, "--server", impostor.URL, "--auth-token", "TOKEN"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "refusing to send the crawl") {
		t.Fatalf("import --server impostor: err = %v", err)
	}
	select {
	case r := <-got:
		t.Fatalf("impostor received %s", r)
	default:
	}

	// mcp does not use it either (and does not try to start a dashboard).
	err := ensureDashboard(context.Background(), impostor.URL, "TOKEN", true)
	if err == nil || !strings.Contains(err.Error(), "refusing to use") {
		t.Fatalf("ensureDashboard: err = %v", err)
	}

	// The real dashboard of this user passes.
	srv := fakeDashboard(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if state, err := probeDashboard(srv.URL); state != dashVerified {
		t.Fatalf("real dashboard: probe = %v, %v", state, err)
	}
	if err := ensureDashboard(context.Background(), srv.URL, "", false); err != nil {
		t.Fatalf("ensureDashboard real: %v", err)
	}
}
