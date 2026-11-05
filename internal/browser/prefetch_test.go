package browser

import (
	"context"
	"fmt"
	"net"
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

// With GuardNetwork, Chrome reaches the network only through Micelio's
// guarded proxy: WebSockets, navigations and host names that resolve to
// this computer are all refused, and Chrome never resolves names itself.
func TestRenderGuardNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "secret")
	}))
	defer srv.Close()
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	ws := strings.Replace(srv.URL, "http://", "ws://", 1)
	r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second, GuardNetwork: true})
	pre := &Prefetched{
		HTML: `<html><body><img src="` + byName + `/img.png">
<script>try { new WebSocket("` + ws + `/ws") } catch (e) {}
fetch("` + byName + `/fetch").catch(function(){});
setTimeout(function(){ location.href = "` + byName + `/nav" }, 100)</script></body></html>`,
		Headers: map[string]string{"Content-Type": "text/html"},
	}
	if _, err := r.RenderPage(context.Background(), "http://public.example/", "Micelio/1.0", pre); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the browser sent %d requests to a private address", n)
	}
}

// The page served from the crawl comes from a request made with the crawl's
// cookies and headers: its Set-Cookie must not enter Chrome's cookie jar.
func TestPrefetchedHeadersDropSetCookie(t *testing.T) {
	for _, h := range prefetchedHeaders(map[string]string{"Set-Cookie": "session=s3cret", "Content-Type": "text/html"}) {
		if strings.EqualFold(h.Name, "set-cookie") {
			t.Fatalf("Set-Cookie of the crawled response reaches Chrome: %q", h.Value)
		}
	}
}

// Crawl credentials (cookies, Authorization, X-Api-Key) stay in the crawler's
// HTTP fetcher: Chrome is never given them, so no request it makes carries
// them, to the crawled site or to another one (scripts, images, iframes,
// fetch, redirects).
func TestRenderSendsNoCrawlCredentials(t *testing.T) {
	type hit struct{ path, auth, key, cookie string }
	var mu sync.Mutex
	var hitsA, hitsB []hit
	record := func(list *[]hit, r *http.Request) {
		mu.Lock()
		*list = append(*list, hit{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("Cookie")})
		mu.Unlock()
	}
	page := func(list *[]hit) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			record(list, r)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><body>ok</body></html>")
		}
	}
	// The other host is [::1]: a different host name than 127.0.0.1.
	lnB, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	srvB := &httptest.Server{Listener: lnB, Config: &http.Server{Handler: page(&hitsB)}}
	srvB.Start()
	defer srvB.Close()
	hostB := srvB.URL
	muxA := http.NewServeMux()
	muxA.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) {
		record(&hitsA, r)
		http.Redirect(w, r, hostB+"/redirected", http.StatusFound)
	})
	// Served by host A itself, not as Prefetched: Chrome counts a page it was
	// handed as public and then refuses its requests to a loopback host.
	muxA.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		record(&hitsA, r)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><p>crawled</p>
<script src="/own.js"></script><img src="/own.png">
<script src="`+hostB+`/b.js"></script><img src="`+hostB+`/b.png">
<iframe src="`+hostB+`/frame"></iframe><iframe src="/go"></iframe>
<script>fetch("/api").catch(function(){}); fetch("`+hostB+`/api", {credentials: "include"}).catch(function(){})</script>
</body></html>`)
	})
	muxA.HandleFunc("/", page(&hitsA))
	srvA := httptest.NewServer(muxA)
	defer srvA.Close()

	r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second, BlockResources: false})
	if _, err := r.RenderPage(context.Background(), srvA.URL+"/", "Micelio/1.0", nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hitsA) == 0 || len(hitsB) == 0 {
		t.Fatalf("test pages not loaded: site %v, other host %v", hitsA, hitsB)
	}
	sawRedirect := false
	for _, list := range [][]hit{hitsA, hitsB} {
		for _, h := range list {
			if h.auth != "" || h.key != "" || h.cookie != "" {
				t.Errorf("%s got credentials: Authorization=%q X-Api-Key=%q Cookie=%q", h.path, h.auth, h.key, h.cookie)
			}
			sawRedirect = sawRedirect || h.path == "/redirected"
		}
	}
	if !sawRedirect {
		t.Errorf("cross-host redirect not followed; other host saw %v", hitsB)
	}
}
