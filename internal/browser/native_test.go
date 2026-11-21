package browser

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeNoContentResponsesKeepHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/204":
			w.WriteHeader(http.StatusNoContent)
		case "/205":
			w.WriteHeader(http.StatusResetContent)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><body>previous document</body></html>")
		}
	}))
	t.Cleanup(srv.Close)
	n := testNative(t, NativeConfig{Timeout: 5 * time.Second})
	if result := n.Fetch(context.Background(), srv.URL, true, nil); result.Error != "" {
		t.Fatal(result.Error)
	}
	for _, status := range []int{http.StatusNoContent, http.StatusResetContent} {
		target := fmt.Sprintf("%s/%d", srv.URL, status)
		result := n.Fetch(context.Background(), target, true, nil)
		if result.Error != "" || result.StatusCode != status || result.FinalURL != target || result.HTML != "" || result.RawHTML != "" || result.BodySize != 0 {
			t.Fatalf("no-content response reused a page or became an error: %+v", result)
		}
		response, err := n.RoundTrip(httptest.NewRequest(http.MethodGet, target, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != status || len(body) != 0 {
			t.Fatalf("round trip: status=%d body=%q err=%v", response.StatusCode, body, readErr)
		}
	}
	if result := n.Fetch(context.Background(), srv.URL, true, nil); result.Error != "" || result.StatusCode != http.StatusOK {
		t.Fatalf("navigation after no-content response failed: %+v", result)
	}
}

func testNative(t *testing.T, cfg NativeConfig) *Native {
	t.Helper()
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		t.Skip("native Chrome requires a display")
	}
	if _, err := nativeChromePath(); err != nil {
		t.Skip(err)
	}
	cfg.Warmup = time.Millisecond
	n, err := NewNative(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n
}

func TestNativeHTTPURLAndSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("User-agent: *\nDisallow: /private\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/never-finishes" {
			fmt.Fprint(w, "<html><body>pending")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/ads" {
			fmt.Fprint(w, `<html><title>Usable page</title><body><h1>Loaded</h1><iframe src="/never-finishes"></iframe></body></html>`)
			return
		}
		if r.URL.Path == "/jsredirect" {
			w.Write([]byte(`<html><title>Moving</title><body><script>setTimeout(()=>location.replace('/destination'),100)</script></body></html>`))
			return
		}
		if r.URL.Path == "/destination" {
			w.Write([]byte(`<html><title>Destination</title><body><h1>Destination</h1></body></html>`))
			return
		}
		w.Write([]byte(`<html><head><title>source</title><link rel="canonical" href="/page"></head><body><h1>Native</h1><script>document.title=navigator.webdriver?'automated':'native';document.cookie='native_test=present';history.replaceState(null,'','?range=20')</script></body></html>`))
	}))
	t.Cleanup(srv.Close)
	n := testNative(t, NativeConfig{InitialURL: srv.URL + "/robots.txt"})
	r := n.Fetch(context.Background(), srv.URL+"/page", true, nil)
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if r.StatusCode != 200 || r.FinalURL != srv.URL+"/page" || r.BrowserURL != srv.URL+"/page?range=20" {
		t.Fatalf("wrong URLs/status: %+v", r)
	}
	if !strings.Contains(r.HTML, "<title>native</title>") || !strings.Contains(r.RawHTML, "<title>source</title>") {
		t.Fatalf("raw/rendered content not distinguished")
	}
	frame, err := n.Snapshot(context.Background())
	if err != nil || frame.Title != "native" || frame.BrowserURL != r.BrowserURL || len(frame.JPEG) == 0 {
		t.Fatalf("native snapshot: %+v %v", frame, err)
	}
	resp, err := n.RoundTrip(httptest.NewRequest("GET", srv.URL+"/robots.txt", nil))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("native robots: %v", err)
	}
	resp.Body.Close()
	// A second operation must not be left paused by the first one's listeners.
	r = n.Fetch(context.Background(), srv.URL+"/page", true, nil)
	if r.Error != "" || r.StatusCode != 200 {
		t.Fatalf("second fetch: %s %d", r.Error, r.StatusCode)
	}
	r = n.Fetch(context.Background(), srv.URL+"/jsredirect", true, nil)
	if r.Error != "" || r.FinalURL != srv.URL+"/destination" || !strings.Contains(r.RawHTML, "<title>Destination</title>") {
		t.Fatalf("JavaScript HTTP navigation metadata/source: %+v", r)
	}
	start := time.Now()
	r = n.Fetch(context.Background(), srv.URL+"/ads", true, nil)
	if r.Error != "" || r.StatusCode != 200 || time.Since(start) > 5*time.Second {
		t.Fatalf("native capture waited for an ad frame/window.load: status=%d error=%s elapsed=%v", r.StatusCode, r.Error, time.Since(start))
	}
	profile := n.profile
	n.Close()
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("native profile not removed: %v", err)
	}
	select {
	case <-n.exited:
	default:
		t.Fatal("native Chrome still running after Close")
	}
}

func TestNativeStartupCancellation(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		t.Skip("native Chrome requires a display")
	}
	if _, err := nativeChromePath(); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if n, err := NewNative(ctx, NativeConfig{Warmup: time.Hour}); err == nil {
		n.Close()
		t.Fatal("cancelled startup succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancelled startup did not release Chrome promptly")
	}
}

func TestNativeRedirectAndScope(t *testing.T) {
	var external atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { external.Add(1); w.Write([]byte("outside")) }))
	t.Cleanup(dst.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/outside":
			http.Redirect(w, r, dst.URL, http.StatusFound)
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusMovedPermanently)
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><title>Final</title><body>done</body></html>"))
		}
	}))
	t.Cleanup(srv.Close)
	n := testNative(t, NativeConfig{})
	r := n.Fetch(context.Background(), srv.URL+"/redirect", true, func(u string) bool { return strings.HasPrefix(u, srv.URL) })
	if r.Error != "" || r.StatusCode != 200 || r.FinalURL != srv.URL+"/final" || len(r.RedirectChain) != 1 {
		t.Fatalf("redirect: %+v", r)
	}
	r = n.Fetch(context.Background(), srv.URL+"/outside", true, func(u string) bool { return strings.HasPrefix(u, srv.URL) })
	if r.Error != "" || r.StatusCode != 302 || external.Load() != 0 {
		t.Fatalf("off-site redirect: status=%d err=%s hits=%d", r.StatusCode, r.Error, external.Load())
	}
}

func TestNativeGuardBlocksPrivateNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	n := testNative(t, NativeConfig{GuardNetwork: true})
	n.Fetch(context.Background(), srv.URL, true, nil)
	if hits.Load() != 0 {
		t.Fatal("native Chrome reached a private address")
	}
}

func TestNativeKeepsHTTPRedirectsAfterJavaScriptNavigation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/page", http.StatusMovedPermanently)
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><title>Start</title><body>Start<script>setTimeout(()=>location.replace('/hop'),200)</script></body></html>`)
		case "/hop":
			http.Redirect(w, r, "/middle", http.StatusMovedPermanently)
		case "/middle":
			http.Redirect(w, r, "/final", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><title>Final</title><body>Final</body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	n := testNative(t, NativeConfig{})
	for i := 0; i < 2; i++ {
		r := n.Fetch(context.Background(), srv.URL+"/start", true, func(u string) bool { return strings.HasPrefix(u, srv.URL) })
		if r.Error != "" || r.FinalURL != srv.URL+"/final" || r.StatusCode != http.StatusOK ||
			!strings.Contains(r.RawHTML, "<title>Final</title>") || !strings.Contains(r.HTML, "<title>Final</title>") {
			t.Fatalf("navigation %d returned inconsistent final content: %+v", i, r)
		}
		paths, codes := []string{"/start", "/hop", "/middle"}, []int{301, 301, 302}
		if len(r.RedirectChain) != len(paths) {
			t.Fatalf("navigation %d lost HTTP redirects after JavaScript: %+v", i, r.RedirectChain)
		}
		for j, hop := range r.RedirectChain {
			if hop.URL != srv.URL+paths[j] || hop.StatusCode != codes[j] {
				t.Fatalf("navigation %d hop %d: %+v", i, j, hop)
			}
		}
	}
}
