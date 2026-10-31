package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestRenderer returns a renderer, or skips when no Chrome is installed.
func newTestRenderer(t *testing.T, cfg RenderConfig) *Renderer {
	t.Helper()
	r := NewRenderer("", cfg)
	t.Cleanup(r.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := r.RenderPage(ctx, "about:blank", "", nil); err != nil && strings.Contains(err.Error(), "executable file not found") {
		t.Skip("Chrome not installed")
	}
	return r
}

// The page the crawler already fetched is served to the browser, so the
// server sees no second request for it, and the page's JS still runs.
func TestRenderUsesPrefetchedPage(t *testing.T) {
	var pageHits, frameHits atomic.Int32
	var mu sync.Mutex
	var paced []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			pageHits.Add(1)
		case "/frame":
			frameHits.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>server copy</body></html>")
	}))
	defer srv.Close()

	r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second, Pace: func(ctx context.Context, u string) func() {
		mu.Lock()
		paced = append(paced, u)
		mu.Unlock()
		return func() {}
	}})
	pre := &Prefetched{
		HTML: `<html><body><p>crawled copy</p><iframe src="/frame"></iframe>
<script>document.body.insertAdjacentHTML('beforeend','<p id="js">rendered by js</p>')</script></body></html>`,
		Headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Content-Encoding": "gzip"},
	}
	res, err := r.RenderPage(context.Background(), srv.URL+"/", "Micelio/1.0", pre)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, "rendered by js") || !strings.Contains(res.HTML, "crawled copy") {
		t.Fatalf("rendered HTML is not the prefetched page after JS:\n%s", res.HTML)
	}
	if n := pageHits.Load(); n != 0 {
		t.Errorf("browser fetched the page itself %d times; want 0 (served from the crawl)", n)
	}
	if frameHits.Load() != 1 {
		t.Errorf("iframe fetched %d times, want 1", frameHits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paced) != 1 || !strings.HasSuffix(paced[0], "/frame") {
		t.Errorf("paced = %v, want the iframe document only", paced)
	}
}
