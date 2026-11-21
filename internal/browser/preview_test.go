package browser

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func TestPreviewDocumentModes(t *testing.T) {
	var pageHits, scriptHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/effect" {
			scriptHits.Add(1)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			return
		}
		pageHits.Add(1)
		fmt.Fprint(w, "<html><title>unfetched</title><body>network copy</body></html>")
	}))
	t.Cleanup(srv.Close)
	pre := &Prefetched{HTML: `<html><head><title>HTML source</title></head><body><h1>Raw heading</h1><noscript><p>Without JavaScript</p></noscript><script>document.title='JavaScript document';document.querySelector('h1').textContent='Rendered heading';fetch('/effect')</script></body></html>`}
	for _, js := range []bool{false, true} {
		t.Run(fmt.Sprint(js), func(t *testing.T) {
			r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second, Device: &types.DeviceProfile{ID: "custom", Width: 360, Height: 640, Scale: 1}})
			result, err := r.PreviewPage(context.Background(), srv.URL+"/", "Micelio/1.0", pre, js)
			if err != nil {
				t.Fatal(err)
			}
			want := "HTML source"
			heading := "Raw heading"
			if js {
				want = "JavaScript document"
				heading = "Rendered heading"
			}
			if result.Preview.Title != want || !strings.Contains(result.HTML, "<h1>"+heading+"</h1>") {
				t.Fatalf("preview does not match mode: title=%q html=%s", result.Preview.Title, result.HTML)
			}
			image, err := jpeg.DecodeConfig(bytes.NewReader(result.Preview.JPEG))
			if err != nil || image.Width != 360 || image.Height != 640 {
				t.Fatalf("wrong device capture: %+v %v", image, err)
			}
			if !js && scriptHits.Load() != 0 {
				t.Fatal("scripts executed in HTML-only mode")
			}
		})
	}
	if pageHits.Load() != 0 || scriptHits.Load() != 1 {
		t.Fatalf("page refetched or wrong JS execution: pages=%d scripts=%d", pageHits.Load(), scriptHits.Load())
	}
}

func TestPreviewHTMLDoesNotNavigateOrLoadFrames(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			return
		}
		hits.Add(1)
		fmt.Fprint(w, "<html><title>Wrong document</title><body>Wrong</body></html>")
	}))
	t.Cleanup(srv.Close)
	r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second})
	pre := &Prefetched{Headers: map[string]string{"Refresh": "0;url=/header-destination"}, HTML: `<html><head><title>Original</title><meta http-equiv="refresh" content="0;url=/destination"></head><body><iframe src="/frame"></iframe><h1>Source</h1></body></html>`}
	result, err := r.PreviewPage(context.Background(), srv.URL+"/", "Micelio/1.0", pre, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview.Title != "Original" || hits.Load() != 0 {
		t.Fatalf("HTML preview navigated: title=%q hits=%d", result.Preview.Title, hits.Load())
	}
}

func TestPreviewGuardBlocksPrivateNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second, GuardNetwork: true})
	pre := &Prefetched{HTML: `<html><title>Public page</title><body><img src="` + srv.URL + `/image"><script>fetch('` + srv.URL + `/effect').catch(()=>{})</script></body></html>`}
	if _, err := r.PreviewPage(context.Background(), "http://public.example/", "Micelio/1.0", pre, true); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("preview reached a private address")
	}
}

func TestPreviewCancellationStopsBrowserNavigation(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	}))
	t.Cleanup(srv.Close)
	r := newTestRenderer(t, RenderConfig{Timeout: 30 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.PreviewPage(ctx, srv.URL+"/", "Micelio/1.0", nil, true); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("navigation did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preview succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preview ignored cancellation")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled browser request remained open")
	}
}

func TestPreviewRefreshPreservesSource(t *testing.T) {
	source := `<!doctype html><html><head><META HTTP-EQUIV="Refresh" content="0;url=/next"><title>Raw</title></head><body><noscript><p>HTML only</p></noscript><script>const sample='<meta http-equiv="refresh" content="1">';</script></body></html>`
	want := strings.Replace(source, `<META HTTP-EQUIV="Refresh" content="0;url=/next">`, "", 1)
	if got := withoutPreviewRefresh(source); got != want {
		t.Fatalf("visible/source content changed: %s", got)
	}
}
