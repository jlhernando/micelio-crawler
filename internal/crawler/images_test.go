package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// imageSite serves two pages with <img> tags and a linked image, and records
// every request as "METHOD /path".
func imageSite(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		switch {
		case r.URL.Path == "/" || r.URL.Path == "/b":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>t</title><script src="/app.js"></script></head><body>
<img src="/img/a.png" alt="A" width="10" height="10"><img src="/img/b.jpg">
<a href="/b">b</a> <a href="/img/linked.jpg">full size</a></body></html>`)
		case strings.HasPrefix(r.URL.Path, "/img/"):
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "1000")
			if r.Method != http.MethodHead {
				w.Write(make([]byte, 1000))
			}
		case r.URL.Path == "/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.Header().Set("Content-Length", "20")
			if r.Method != http.MethodHead {
				fmt.Fprint(w, "console.log('hello')")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]int, len(hits))
		for k, v := range hits {
			out[k] = v
		}
		return out
	}
}

// crawlWithPageWeight runs a crawl and the post-crawl page weight step the
// way the dashboard and CLI do.
func crawlWithPageWeight(t *testing.T, seed string, download bool) *CrawlResult {
	t.Helper()
	cfg := types.CrawlConfig{
		SeedURL: seed, Mode: types.ModeSpider, MaxDepth: 3, MaxPages: 10,
		Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true,
		NoSoft404Probe: true, NoAgentCheck: true,
		PageWeight: true, DownloadImages: download,
	}
	res, err := Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	analysis.ComputePageWeight(context.Background(), res.Pages, res.ResourceRefs, res.TransferSizes, analysis.PageWeightOptions{Concurrency: cfg.Concurrency, Sizer: res.ResourceSizer})
	return res
}

func imageGETs(hits map[string]int) int {
	n := 0
	for k, v := range hits {
		if strings.HasPrefix(k, "GET /img/") {
			n += v
		}
	}
	return n
}

// checkImageWeight: page weight always sizes images (one HEAD each), whatever
// DownloadImages says, so totals stay comparable with Lighthouse.
func checkImageWeight(t *testing.T, res *CrawlResult, h map[string]int) {
	t.Helper()
	if h["HEAD /img/a.png"] != 1 || h["HEAD /img/b.jpg"] != 1 {
		t.Errorf("want one HEAD per unique <img>, got %v", h)
	}
	for _, p := range res.Pages {
		if p.PageWeight == nil {
			continue // the linked image row, if fetched
		}
		if img := p.PageWeight.ByType["image"]; img.Count != 2 || img.Bytes != 2000 {
			t.Errorf("%s: image weight %+v, want 2 images, 2000 bytes", p.URL, img)
		}
	}
}

// Default (DownloadImages off): page weight still sizes images with HEAD,
// no image is ever downloaded, and a linked image is never requested.
func TestCrawlImagesOffSkipsLinkedImages(t *testing.T) {
	srv, hits := imageSite(t)
	res := crawlWithPageWeight(t, srv.URL+"/", false)

	h := hits()
	if n := imageGETs(h); n != 0 {
		t.Errorf("%d image GETs with DownloadImages off: %v", n, h)
	}
	if n := h["HEAD /img/linked.jpg"]; n != 0 {
		t.Errorf("linked image requested %d times with DownloadImages off", n)
	}
	if len(res.Pages) != 2 {
		t.Fatalf("crawled %d pages, want 2 (no row for the linked image)", len(res.Pages))
	}
	checkImageWeight(t, res, h)

	var excl *types.ExclusionReason
	for i, r := range res.Coverage.Reasons {
		if r.Reason == "linked_image" {
			excl = &res.Coverage.Reasons[i]
		}
	}
	if excl == nil || excl.Count != 1 || len(excl.Examples) != 1 || !strings.HasSuffix(excl.Examples[0], "/img/linked.jpg") {
		t.Errorf("linked image exclusion = %+v, want one, recorded once", excl)
	}
}

// DownloadImages on: the linked image is checked with one HEAD, never a GET.
func TestCrawlImagesOnHeadsLinkedImage(t *testing.T) {
	srv, hits := imageSite(t)
	res := crawlWithPageWeight(t, srv.URL+"/", true)

	h := hits()
	if n := imageGETs(h); n != 0 {
		t.Errorf("%d image GETs, want 0 (HEAD only): %v", n, h)
	}
	if h["HEAD /img/linked.jpg"] != 1 {
		t.Errorf("want one HEAD for the linked image, got %v", h)
	}
	var linked *types.PageData
	for _, p := range res.Pages {
		if strings.HasSuffix(p.URL, "/img/linked.jpg") {
			linked = p
		}
	}
	if linked == nil || linked.StatusCode != 200 {
		t.Fatalf("linked image row = %+v", linked)
	}
	checkImageWeight(t, res, h)
}

// FetchPage on an image URL: HEAD, then a one-byte Range GET when HEAD is
// refused, then a plain GET when Range is refused; an image-looking URL that
// serves HTML is fetched as a page.
func TestFetchPageImageProbe(t *testing.T) {
	var mu sync.Mutex
	var log []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.Method+" "+r.URL.Path+" "+r.Header.Get("Range"))
		mu.Unlock()
		switch r.URL.Path {
		case "/head.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "5000")
		case "/nohead.png", "/norange.png":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			if r.Header.Get("Range") != "" {
				if r.URL.Path == "/norange.png" {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				w.Header().Set("Content-Range", "bytes 0-0/7000")
				w.WriteHeader(http.StatusPartialContent)
				w.Write([]byte{0x89})
				return
			}
			w.Write(make([]byte, 7000))
		case "/page.jpg":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><title>not an image</title></html>")
		}
	}))
	defer srv.Close()

	cases := []struct {
		path      string
		wantLog   []string
		wantSize  int64
		wantHTML  bool
		wantCType string
	}{
		{"/head.jpg", []string{"HEAD /head.jpg "}, 5000, false, "image/jpeg"},
		{"/nohead.png", []string{"HEAD /nohead.png ", "GET /nohead.png bytes=0-0"}, 7000, false, "image/png"},
		{"/norange.png", []string{"HEAD /norange.png ", "GET /norange.png bytes=0-0", "GET /norange.png "}, 0, false, "image/png"},
		{"/page.jpg", []string{"HEAD /page.jpg ", "GET /page.jpg "}, 0, true, "text/html"},
	}
	for _, c := range cases {
		mu.Lock()
		log = nil
		mu.Unlock()
		r := FetchPage(context.Background(), srv.URL+c.path, FetchOptions{SkipSSRF: true})
		mu.Lock()
		got := strings.Join(log, " | ")
		mu.Unlock()
		if got != strings.Join(c.wantLog, " | ") {
			t.Errorf("%s: requests %q, want %q", c.path, got, c.wantLog)
		}
		if r.StatusCode != 200 || !strings.HasPrefix(r.ContentType, c.wantCType) {
			t.Errorf("%s: status %d type %q", c.path, r.StatusCode, r.ContentType)
		}
		if c.wantSize > 0 && r.TransferSize != c.wantSize {
			t.Errorf("%s: size %d, want %d", c.path, r.TransferSize, c.wantSize)
		}
		if (r.HTML != "") != c.wantHTML {
			t.Errorf("%s: html %q", c.path, r.HTML)
		}
	}
}
