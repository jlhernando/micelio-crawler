package server

import (
	"io/fs"
	"net/http"
	"testing"
	"testing/fstest"
)

func TestIsLocalOrigin(t *testing.T) {
	tests := []struct {
		origin string
		name   string
		want   bool
	}{
		{name: "empty origin", origin: "", want: true},
		{name: "localhost http", origin: "http://localhost:3000", want: true},
		{name: "localhost https", origin: "https://localhost:3000", want: true},
		{name: "127.0.0.1 http", origin: "http://127.0.0.1:8080", want: true},
		{name: "127.0.0.1 https", origin: "https://127.0.0.1:8080", want: true},
		{name: "remote origin", origin: "https://evil.com", want: false},
		{name: "localhost substring", origin: "http://notlocalhost:3000", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			got := isLocalOrigin(r)
			if got != tc.want {
				t.Fatalf("isLocalOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestOpenDashboardFile(t *testing.T) {
	dashFS := fstest.MapFS{
		"index.html":          {Data: []byte("<html>index</html>")},
		"assets/style.css":    {Data: []byte("body{}")},
		"assets/app.js":       {Data: []byte("console.log('app')")},
	}

	srv := &Server{}

	t.Run("existing file", func(t *testing.T) {
		f, stat, resolved := srv.openDashboardFile(dashFS, "assets/style.css")
		if f == nil {
			t.Fatal("expected file, got nil")
		}
		defer f.Close()
		if stat == nil {
			t.Fatal("expected stat, got nil")
		}
		if resolved != "assets/style.css" {
			t.Fatalf("expected assets/style.css, got %s", resolved)
		}
	})

	t.Run("index.html", func(t *testing.T) {
		f, stat, resolved := srv.openDashboardFile(dashFS, "index.html")
		if f == nil {
			t.Fatal("expected file, got nil")
		}
		defer f.Close()
		if stat == nil {
			t.Fatal("expected stat, got nil")
		}
		if resolved != "index.html" {
			t.Fatalf("expected index.html, got %s", resolved)
		}
	})

	t.Run("SPA fallback", func(t *testing.T) {
		// Non-existent path should fall back to index.html
		f, stat, resolved := srv.openDashboardFile(dashFS, "some/spa/route")
		if f == nil {
			t.Fatal("expected fallback to index.html, got nil")
		}
		defer f.Close()
		if stat == nil {
			t.Fatal("expected stat, got nil")
		}
		if resolved != "index.html" {
			t.Fatalf("expected index.html fallback, got %s", resolved)
		}
	})

	t.Run("no index.html", func(t *testing.T) {
		emptyFS := fstest.MapFS{}
		f, _, _ := srv.openDashboardFile(emptyFS, "nonexistent")
		if f != nil {
			f.Close()
			t.Fatal("expected nil when no index.html exists")
		}
	})

	t.Run("directory falls back to index", func(t *testing.T) {
		dirFS := fstest.MapFS{
			"index.html":       {Data: []byte("<html>")},
			"assets/style.css": {Data: []byte("body{}")},
		}
		// "assets" is a directory, should fall back to index.html
		f, _, resolved := srv.openDashboardFile(dirFS, "assets")
		if f == nil {
			t.Fatal("expected fallback to index.html for directory")
		}
		defer f.Close()
		if resolved != "index.html" {
			t.Fatalf("expected index.html fallback for dir, got %s", resolved)
		}
	})
}

func TestWithHeaders(t *testing.T) {
	srv := &Server{}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.withHeaders(inner)

	r, _ := http.NewRequest("GET", "/", nil)
	w := &headerRecorder{header: http.Header{}}
	handler.ServeHTTP(w, r)

	if w.header.Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatal("expected COOP header")
	}
	if w.header.Get("Cross-Origin-Embedder-Policy") != "require-corp" {
		t.Fatal("expected COEP header")
	}
}

func TestWithCORSLocalOrigin(t *testing.T) {
	srv := &Server{}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.withCORS(inner)

	// Local origin should get CORS headers
	r, _ := http.NewRequest("GET", "/api/test", nil)
	r.Header.Set("Origin", "http://localhost:3200")
	w := &headerRecorder{header: http.Header{}}
	handler.ServeHTTP(w, r)

	if w.header.Get("Access-Control-Allow-Origin") != "http://localhost:3200" {
		t.Fatalf("expected CORS origin header, got %s", w.header.Get("Access-Control-Allow-Origin"))
	}
}

func TestWithCORSPreflight(t *testing.T) {
	srv := &Server{}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := srv.withCORS(inner)

	r, _ := http.NewRequest("OPTIONS", "/api/test", nil)
	r.Header.Set("Origin", "http://localhost:3200")
	w := &headerRecorder{header: http.Header{}, statusCode: 0}
	handler.ServeHTTP(w, r)

	if called {
		t.Fatal("inner handler should not be called for OPTIONS")
	}
	if w.statusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.statusCode)
	}
}

func TestWithCORSRemoteOrigin(t *testing.T) {
	srv := &Server{}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.withCORS(inner)

	r, _ := http.NewRequest("GET", "/api/test", nil)
	r.Header.Set("Origin", "https://evil.com")
	w := &headerRecorder{header: http.Header{}}
	handler.ServeHTTP(w, r)

	if w.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("remote origin should not get CORS headers")
	}
}

func TestWithCORSNoOrigin(t *testing.T) {
	srv := &Server{}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.withCORS(inner)

	r, _ := http.NewRequest("GET", "/api/test", nil)
	// No Origin header
	w := &headerRecorder{header: http.Header{}}
	handler.ServeHTTP(w, r)

	// Should default to localhost:3200
	if w.header.Get("Access-Control-Allow-Origin") != "http://localhost:3200" {
		t.Fatalf("expected default CORS origin, got %s", w.header.Get("Access-Control-Allow-Origin"))
	}
}

// headerRecorder is a minimal ResponseWriter for testing headers.
type headerRecorder struct {
	header     http.Header
	statusCode int
}

func (h *headerRecorder) Header() http.Header        { return h.header }
func (h *headerRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (h *headerRecorder) WriteHeader(code int)        { h.statusCode = code }

// Ensure it satisfies the fs.FS interface check
var _ fs.FS = fstest.MapFS{}
