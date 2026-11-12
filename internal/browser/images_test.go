package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// BlockImages stops every image load (img, CSS background, preload),
// even with BlockResources off; without it Chrome loads them.
func TestRenderBlockImages(t *testing.T) {
	var images, scripts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><link rel="preload" as="image" href="/img/pre.webp">
<style>body{background:url(/img/bg.jpg)}</style></head><body>
<img src="/img/a.png" width="10" height="10"><img src="/pic?id=1">
<script src="/app.js"></script></body></html>`)
		case strings.HasPrefix(r.URL.Path, "/img/") || r.URL.Path == "/pic":
			images.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG\r\n\x1a\n"))
		case r.URL.Path == "/app.js":
			scripts.Add(1)
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, "document.body.dataset.ok = 1")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second, BlockImages: true})
	if _, err := r.RenderPage(context.Background(), srv.URL+"/", "Micelio/1.0", nil); err != nil {
		t.Fatal(err)
	}
	if n := images.Load(); n != 0 {
		t.Errorf("BlockImages: %d image requests reached the server, want 0", n)
	}
	if scripts.Load() == 0 {
		t.Error("BlockImages blocked the page's script too")
	}

	images.Store(0)
	r2 := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second})
	if _, err := r2.RenderPage(context.Background(), srv.URL+"/", "Micelio/1.0", nil); err != nil {
		t.Fatal(err)
	}
	if images.Load() == 0 {
		t.Error("without BlockImages Chrome loaded no image: test page broken")
	}
}
