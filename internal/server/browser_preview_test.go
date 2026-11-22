package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/types"
)

func previewRequest(api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}
func previewTestAPI(t *testing.T, capture func(context.Context, types.CrawlConfig) (*browser.PreviewFrame, error)) (*previewService, *http.ServeMux) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &previewService{parent: ctx, capture: capture}
	mux := http.NewServeMux()
	s.register(mux)
	return s, mux
}

const validPreview = `{"config":{"seedUrl":"https://example.com/category?query=1","jsRendering":false,"respectRobots":true}}`

func TestPreviewUsesCrawlSettingsAndReturnsOnlySnapshot(t *testing.T) {
	var got types.CrawlConfig
	_, api := previewTestAPI(t, func(ctx context.Context, c types.CrawlConfig) (*browser.PreviewFrame, error) {
		got = c
		return &browser.PreviewFrame{Title: "Source", StatusCode: 403, Mode: "html", JPEG: []byte("image")}, nil
	})
	w := previewRequest(api, "POST", "/api/browser-preview", `{"config":{"seedUrl":"https://example.com/","jsRendering":true,"userAgent":"TestBot","cookies":"session=test","customHeaders":{"X-Test":"yes"},"device":{"id":"mobile"},"respectRobots":true,"delay":1200}}`)
	if w.Code != 200 {
		t.Fatalf("capture failed: %d %s", w.Code, w.Body.String())
	}
	if got.UserAgent != "TestBot" || got.Cookies != "session=test" || got.CustomHeaders["X-Test"] != "yes" || !got.JSRendering || !got.RespectRobots || got.DelayMs != 1200 || got.Device.ID != "mobile" {
		t.Fatalf("settings lost: %+v", got)
	}
	var response map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &response)
	if len(response) != 1 || response["frame"] == nil || strings.Contains(w.Body.String(), "session=test") {
		t.Fatalf("session or secrets returned: %s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview may be cached")
	}
	for _, method := range []string{"GET", "DELETE"} {
		if w := previewRequest(api, method, "/api/browser-preview/session", ""); w.Code != 404 {
			t.Fatalf("old session route still active: %d", w.Code)
		}
	}
	if w := previewRequest(api, "POST", "/api/browser-preview/session/action", `{"type":"click","x":1,"y":1}`); w.Code != 404 {
		t.Fatal("old action API still active")
	}
}

func TestPreviewRejectsUnsafeRequests(t *testing.T) {
	_, api := previewTestAPI(t, func(context.Context, types.CrawlConfig) (*browser.PreviewFrame, error) {
		t.Fatal("unsafe capture reached browser")
		return nil, nil
	})
	for _, body := range []string{
		`null`, `{}`, `{"config":null}`, `{"config":{"seedUrl":"file:///etc/passwd"}}`,
		`{"config":{"seedUrl":"http://127.0.0.1"}}`, `{"config":{"seedUrl":"http://[::1]"}}`,
		`{"config":{"seedUrl":"http://169.254.169.254"}}`, `{"config":{"seedUrl":"http://localhost"}}`,
		`{"config":{"seedUrl":"https://user:secret@example.com"}}`, `{"config":{"seedUrl":"https://example.com","skipSSRF":true}}`,
		`{"config":{"seedUrl":"https://example.com","snippetPaths":["/etc/secret"]}}`,
		`{"config":{"seedUrl":"https://example.com","renderTimeoutSec":999999}}`,
		validPreview + ` {}`, `{"url":"https://example.com"}`,
	} {
		if w := previewRequest(api, "POST", "/api/browser-preview", body); w.Code != 400 {
			t.Errorf("accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestPreviewNormalizesNativeAndChecksProxy(t *testing.T) {
	var got types.CrawlConfig
	s, api := previewTestAPI(t, func(_ context.Context, c types.CrawlConfig) (*browser.PreviewFrame, error) {
		got = c
		return &browser.PreviewFrame{}, nil
	})
	manager := &CrawlManager{TrustedProxy: "http://127.0.0.1:9999"}
	s.proxy = manager.applyTrustedProxy
	w := previewRequest(api, "POST", "/api/browser-preview", `{"config":{"seedUrl":"https://example.com/","nativeChrome":true,"respectRobots":true,"delay":200}}`)
	if w.Code != 200 || got.Concurrency != 1 || got.DelayMs != 1000 || !got.NativeChrome || got.JSRendering {
		t.Fatalf("native settings: %d %+v", w.Code, got)
	}
	for _, extra := range []string{`"proxy":"http://127.0.0.1:1234"`, `"nativeChrome":true,"respectRobots":true,"cookies":"secret=test"`} {
		w := previewRequest(api, "POST", "/api/browser-preview", `{"config":{"seedUrl":"https://example.com/",`+extra+`}}`)
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret=test") {
			t.Fatalf("unsafe native/proxy config accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestPreviewFailureReleasesSlot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	_, api := previewTestAPI(t, func(ctx context.Context, c types.CrawlConfig) (*browser.PreviewFrame, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return nil, errors.New("capture failed")
		}
		return &browser.PreviewFrame{}, nil
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- previewRequest(api, "POST", "/api/browser-preview", validPreview) }()
	<-entered
	if w := previewRequest(api, "POST", "/api/browser-preview", validPreview); w.Code != 409 {
		t.Fatalf("concurrent capture: %d", w.Code)
	}
	close(release)
	if w := <-done; w.Code != 502 {
		t.Fatalf("failure status: %d", w.Code)
	}
	if w := previewRequest(api, "POST", "/api/browser-preview", validPreview); w.Code != 200 {
		t.Fatalf("slot leaked: %d", w.Code)
	}
}

func TestPreviewCancellationReachesCapture(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "server"}[parentCancel], func(t *testing.T) {
			entered := make(chan struct{})
			s, api := previewTestAPI(t, func(ctx context.Context, c types.CrawlConfig) (*browser.PreviewFrame, error) {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			s.parent = parent
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", "/api/browser-preview", strings.NewReader(validPreview)).WithContext(ctx)
			done := make(chan struct{})
			go func() { api.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
			<-entered
			if parentCancel {
				cancelParent()
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("capture did not stop on cancellation")
			}
			if s.busy.Load() {
				t.Fatal("cancelled capture leaked slot")
			}
		})
	}
}

func TestPreviewUsesDashboardAccessControls(t *testing.T) {
	_, api := previewTestAPI(t, func(context.Context, types.CrawlConfig) (*browser.PreviewFrame, error) {
		t.Fatal("access control bypassed")
		return nil, nil
	})
	for _, tc := range []struct {
		token, origin string
		status        int
	}{{"a-long-dashboard-token", "", 401}, {"", "https://foreign.example", 403}} {
		srv := &Server{api: api}
		h := srv.buildHandler(ServerOptions{Host: "127.0.0.1", Port: 3100, AuthToken: tc.token}, nil)
		r := httptest.NewRequest("POST", "http://127.0.0.1:3100/api/browser-preview", strings.NewReader(validPreview))
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("access check: %d, want %d", w.Code, tc.status)
		}
	}
}
