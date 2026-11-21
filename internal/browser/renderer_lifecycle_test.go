package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func rendererBrowser(t *testing.T, r *Renderer) *chromedp.Browser {
	t.Helper()
	r.lifecycleMu.Lock()
	process := r.rendererProcess
	r.lifecycleMu.Unlock()
	select {
	case <-process.startReady:
	case <-time.After(5 * time.Second):
		t.Fatal("shared browser was not initialized")
	}
	b := chromedp.FromContext(process.browserCtx).Browser
	if b == nil || b.Process() == nil {
		t.Fatal("renderer has no shared Chrome process")
	}
	return b
}

func TestRendererRecoversOneSharedProcessAfterCrash(t *testing.T) {
	r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second, GuardNetwork: true})
	oldBrowser := rendererBrowser(t, r)
	oldProcess := r.rendererProcess
	guard := r.guard
	makeProcess := r.newProcess
	var replacements atomic.Int32
	r.newProcess = func() *rendererProcess {
		replacements.Add(1)
		return makeProcess()
	}
	if guard == nil {
		t.Fatal("network guard was not created")
	}
	if err := oldBrowser.Process().Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldProcess.browserCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Chrome crash did not cancel its process context")
	}
	const callers = 3
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := r.RunSnippet(context.Background(), "http://public.example/", "", "document.title", &Prefetched{HTML: isolationPage})
			if err != nil || value != "isolated" {
				t.Errorf("render after crash: value=%v err=%v", value, err)
			}
		}()
	}
	wg.Wait()
	newBrowser := rendererBrowser(t, r)
	if replacements.Load() != 1 || newBrowser.Process() == oldBrowser.Process() || r.guard != guard {
		t.Fatal("browser was not replaced or its network guard changed")
	}
	if _, err := r.RunSnippet(context.Background(), "about:blank", "", "'still rendering'", nil); err != nil {
		t.Fatal(err)
	}
	if rendererBrowser(t, r).Process() != newBrowser.Process() {
		t.Fatal("subsequent operation did not reuse the replacement")
	}
	assertNoOperationContexts(t, r)
	r.Close()
	if _, err := r.RunSnippet(context.Background(), "about:blank", "", "1", nil); err == nil {
		t.Fatal("Close allowed another process restart")
	}
}

func assertNoOperationContexts(t *testing.T, r *Renderer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	contexts, err := cdp.Call(ctx, rendererBrowser(t, r), target.GetBrowserContexts, cdp.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts.BrowserContextIDs) != 0 {
		t.Fatalf("operation contexts leaked: %v", contexts.BrowserContextIDs)
	}
	targets, err := cdp.Call(ctx, rendererBrowser(t, r), target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	for _, info := range targets.TargetInfos {
		if info.Type == "page" {
			pages++
		}
	}
	if pages != 1 {
		t.Fatalf("operation tabs leaked: %d pages, want only the lifetime tab", pages)
	}
}

const isolationPage = `<html><title>waiting</title><body><h1>Test</h1><script>
const previous=document.cookie || localStorage.getItem('state') || sessionStorage.getItem('state');
document.title=previous?'leaked':'isolated';
document.cookie='session=test;path=/';localStorage.setItem('state','test');sessionStorage.setItem('state','test');
</script></body></html>`

func TestRendererReusesChromeAndIsolatesOperations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, isolationPage)
	}))
	t.Cleanup(srv.Close)
	r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second})
	process := rendererBrowser(t, r).Process()
	for i := 0; i < 2; i++ {
		rendered, err := r.RenderPage(context.Background(), srv.URL, "TestBot", nil)
		if err != nil || !strings.Contains(rendered.HTML, "<title>isolated</title>") {
			t.Fatalf("render reused site state: %v %+v", err, rendered)
		}
		result, err := r.RunSnippet(context.Background(), srv.URL, "TestBot", "document.title", nil)
		if err != nil || result != "isolated" {
			t.Fatalf("snippet reused site state: %v %v", result, err)
		}
		assertNoOperationContexts(t, r)
		if rendererBrowser(t, r).Process() != process {
			t.Fatal("Chrome process changed between operations")
		}
	}
	raw, err := r.PreviewPage(context.Background(), srv.URL+"/", "TestBot", &Prefetched{HTML: isolationPage}, false)
	if err != nil || raw.Preview.Title != "waiting" {
		t.Fatalf("raw preview executed JavaScript: %v", err)
	}
	preview, err := r.PreviewPage(context.Background(), srv.URL, "TestBot", nil, true)
	if err != nil || preview.Preview.Title != "isolated" {
		t.Fatalf("preview reused site state: %v", err)
	}
	shot, err := r.Screenshot(context.Background(), srv.URL, 640, 480, 1, 50)
	if err != nil || len(shot) == 0 {
		t.Fatalf("screenshot failed: %v", err)
	}
	if rendererBrowser(t, r).Process() != process {
		t.Fatal("preview or screenshot restarted Chrome")
	}
	assertNoOperationContexts(t, r)
	r.Close()
	select {
	case <-rendererBrowser(t, r).LostConnection:
	default:
		t.Fatal("Close did not disconnect Chrome")
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("Chrome process remained alive after Close")
	}
	if _, err := r.RenderPage(context.Background(), srv.URL, "", nil); err == nil {
		t.Fatal("renderer started new work after Close")
	}
	r.Close() // idempotent, including after work has used the shared browser
}

func TestRendererConcurrentStartupAndCancellation(t *testing.T) {
	const count = 4
	entered := make(chan string, count)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/page" {
			http.NotFound(w, r)
			return
		}
		entered <- r.URL.Query().Get("i")
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, isolationPage)
	}))
	t.Cleanup(srv.Close)
	r := NewRenderer("", RenderConfig{Timeout: 20 * time.Second})
	t.Cleanup(r.Close)
	results := make([]chan error, count)
	cancels := make([]context.CancelFunc, count)
	for i := range count {
		request, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		t.Cleanup(cancel)
		results[i] = make(chan error, 1)
		go func(i int, request context.Context) {
			rendered, err := r.RenderPage(request, fmt.Sprintf("%s/page?i=%d", srv.URL, i), "TestBot", nil)
			if err == nil && !strings.Contains(rendered.HTML, "<title>isolated</title>") {
				err = fmt.Errorf("site state crossed concurrent contexts")
			}
			results[i] <- err
		}(i, request)
	}
	for range count {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			unblock()
			t.Fatal("concurrent operation did not load")
		}
	}
	browser := rendererBrowser(t, r)
	process := browser.Process()
	contexts, err := cdp.Call(context.Background(), browser, target.GetBrowserContexts, cdp.Empty{})
	if err != nil || len(contexts.BrowserContextIDs) != count {
		unblock()
		t.Fatalf("operations did not share one Chrome with isolated contexts: %v %+v", err, contexts)
	}
	targets, err := cdp.Call(context.Background(), browser, target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		unblock()
		t.Fatal(err)
	}
	active := 0
	for _, info := range targets.TargetInfos {
		if strings.HasPrefix(info.URL, srv.URL+"/page") {
			active++
		}
	}
	// During navigation Chrome may still list about:blank, but every operation
	// must have a target in its own context in this same browser.
	ids := map[cdp.BrowserContextID]bool{}
	operationIDs := map[cdp.BrowserContextID]bool{}
	for _, id := range contexts.BrowserContextIDs {
		operationIDs[id] = true
	}
	for _, info := range targets.TargetInfos {
		if operationIDs[info.BrowserContextID] {
			ids[info.BrowserContextID] = true
		}
	}
	if len(ids) != count {
		unblock()
		t.Fatalf("tabs belong to other browsers: %d shared contexts (loaded URLs %d)", len(ids), active)
	}
	cancels[0]()
	select {
	case err := <-results[0]:
		if err == nil {
			unblock()
			t.Fatal("cancelled page succeeded")
		}
	case <-time.After(5 * time.Second):
		unblock()
		t.Fatal("page ignored cancellation")
	}
	select {
	case <-browser.LostConnection:
		unblock()
		t.Fatal("page cancellation killed the shared browser")
	default:
	}
	unblock()
	for i := 1; i < count; i++ {
		select {
		case err := <-results[i]:
			if err != nil {
				t.Errorf("another page was cancelled: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("another page stalled")
		}
	}
	if rendererBrowser(t, r).Process() != process {
		t.Fatal("concurrent calls restarted the browser")
	}
	assertNoOperationContexts(t, r)
	if _, err := r.RenderPage(context.Background(), "about:blank", "", nil); err != nil {
		t.Fatalf("browser not usable after page cancellation: %v", err)
	}
}

func TestRendererFirstRequestDoesNotOwnBrowserLifetime(t *testing.T) {
	r := NewRenderer("", RenderConfig{Timeout: 15 * time.Second})
	t.Cleanup(r.Close)
	request, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.RenderPage(request, "about:blank", "", nil); err == nil {
		t.Fatal("short request did not expire")
	}
	if _, err := r.RenderPage(context.Background(), "about:blank", "", nil); err != nil {
		t.Fatalf("first caller killed the shared startup: %v", err)
	}
	assertNoOperationContexts(t, r)
}

func TestRendererCloseCancelsActiveOperations(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	r := NewRenderer("", RenderConfig{Timeout: 20 * time.Second})
	t.Cleanup(r.Close)
	result := make(chan error, 1)
	go func() { _, err := r.RenderPage(context.Background(), srv.URL, "", nil); result <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not start")
	}
	closed := make(chan struct{})
	go func() { r.Close(); r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close stalled on active work")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed operation succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left active work")
	}
	if _, err := r.RunSnippet(context.Background(), "about:blank", "", "1", nil); err == nil {
		t.Fatal("snippet restarted Chrome after Close")
	}
}

func TestRendererCloseBeforeFirstUse(t *testing.T) {
	r := NewRenderer("", RenderConfig{Timeout: time.Second})
	r.Close()
	r.Close()
	if _, err := r.RenderPage(context.Background(), "about:blank", "", nil); err == nil {
		t.Fatal("closed renderer started Chrome")
	}
	if chromedp.FromContext(r.browserCtx).Browser != nil {
		t.Fatal("closed renderer started Chrome")
	}
}

func TestRendererCloseWaitsForStartup(t *testing.T) {
	r := NewRenderer("", RenderConfig{Timeout: 15 * time.Second})
	request, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := r.RenderPage(request, "about:blank", "", nil); err == nil {
		t.Fatal("short startup request succeeded")
	}
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish startup cleanup")
	}
	select {
	case <-r.startReady:
	default:
		t.Fatal("Close returned while shared startup was still running")
	}
	if browser := chromedp.FromContext(r.browserCtx).Browser; browser != nil && browser.Process() != nil {
		if err := browser.Process().Signal(syscall.Signal(0)); err == nil {
			t.Fatal("Chrome remained alive after closing during startup")
		}
	}
}

type failingRendererAllocator struct {
	calls atomic.Int32
	err   error
}

func (a *failingRendererAllocator) Allocate(context.Context, ...chromedp.BrowserOption) (*chromedp.Browser, error) {
	a.calls.Add(1)
	return nil, a.err
}
func (a *failingRendererAllocator) Wait() {}

func TestRendererStartupErrorIsPreserved(t *testing.T) {
	r := NewRenderer("", RenderConfig{Timeout: time.Second})
	defer r.Close()
	want := errors.New("Chrome executable file not found")
	allocator := &failingRendererAllocator{err: want}
	chromedp.FromContext(r.browserCtx).Allocator = allocator
	if _, err := r.RenderPage(context.Background(), "about:blank", "", nil); !errors.Is(err, want) {
		t.Fatalf("startup error was hidden by lifetime cancellation: %v", err)
	}
	if _, err := r.RenderPage(context.Background(), "about:blank", "", nil); err == nil {
		t.Fatal("failed renderer accepted more work")
	}
	if allocator.calls.Load() != 1 {
		t.Fatal("startup failure restarted Chrome")
	}
}
