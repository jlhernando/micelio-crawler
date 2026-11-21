package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"

	"github.com/micelio/micelio/internal/netguard"
	"github.com/micelio/micelio/internal/types"
)

// NativeConfig never comes directly from an API request. The crawler owns
// the profile, debugging endpoint and lifetime; callers cannot attach to a
// personal browser or select an arbitrary file on this computer.
type NativeConfig struct {
	InitialURL   string
	ProfileSite  string
	Proxy        string
	GuardNetwork bool
	BlockImages  bool
	Timeout      time.Duration
	Warmup       time.Duration
	Pace         func(context.Context, string) func()
}

// Native starts installed Chrome with its own profile and minimal flags,
// then attaches after its initial robots.txt navigation. The same tab and
// cookies are kept for this crawl. No credentials leave Chrome's cookie jar.
type Native struct {
	ctx            context.Context
	cancel         context.CancelFunc
	allocStop      context.CancelFunc
	tabStop        context.CancelFunc
	cmd            *exec.Cmd
	exited         chan struct{}
	profile        string
	temporary      bool
	releaseProfile func()
	guard          *netguard.Proxy
	config         NativeConfig
	userAgent      string
	auxError       string
	mu             sync.Mutex
	once           sync.Once
}

func nativeChromePath() (string, error) {
	for _, path := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome",
		filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if found, err := exec.LookPath(path); err == nil {
			return found, nil
		}
	}
	return "", errors.New("native Chrome requires an installed Chrome or Chromium and a desktop session")
}

func NewNative(parent context.Context, cfg NativeConfig) (_ *Native, err error) {
	path, err := nativeChromePath()
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.Warmup <= 0 {
		cfg.Warmup = 15 * time.Second
	}
	var profile string
	if cfg.ProfileSite != "" {
		profile, err = NativeProfileDirectory(cfg.ProfileSite)
	} else {
		profile, err = os.MkdirTemp("", "micelio-native-")
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	n := &Native{ctx: ctx, cancel: cancel, profile: profile, config: cfg, temporary: cfg.ProfileSite == ""}
	ok := false
	defer func() {
		if !ok {
			n.Close()
		}
	}()
	if !n.temporary {
		n.releaseProfile, err = lockNativeProfile(profile)
		if err != nil {
			return nil, err
		}
	}
	// A nonzero port is intentional: Chrome's port=0 startup mode changes
	// navigator.webdriver. Bind only loopback, with a private, fresh profile.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	args := []string{"--user-data-dir=" + profile, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + strconv.Itoa(port), "--no-first-run", "--no-default-browser-check"}
	proxy := cfg.Proxy
	if cfg.GuardNetwork {
		if proxy == "" {
			n.guard, err = netguard.StartProxy(nil)
		} else {
			var upstream *url.URL
			upstream, err = url.Parse(proxy)
			if err == nil {
				n.guard, err = netguard.StartProxyVia(upstream, nil)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("native Chrome network guard: %w", err)
		}
		proxy = "http://" + n.guard.Addr()
	}
	if proxy != "" {
		args = append(args, "--proxy-server="+proxy, "--proxy-bypass-list=<-loopback>", "--disable-quic", "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	}
	initial := cfg.InitialURL
	if initial == "" {
		initial = "about:blank"
	}
	args = append(args, initial)
	n.cmd = exec.CommandContext(ctx, path, args...)
	n.cmd.Cancel = func() error { return n.cmd.Process.Signal(os.Interrupt) }
	n.cmd.WaitDelay = 2 * time.Second
	n.cmd.Stdout, n.cmd.Stderr = io.Discard, io.Discard
	if err := n.cmd.Start(); err != nil {
		n.cmd = nil
		return nil, fmt.Errorf("start native Chrome: %w", err)
	}
	n.exited = make(chan struct{})
	go func() { n.cmd.Wait(); close(n.exited) }()
	// Let Chrome load its first robots document before attaching a client.
	// This is ordinary browser JS, never a simulated click on a challenge.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-n.exited:
		return nil, errors.New("native Chrome exited during startup; a desktop session is required")
	case <-time.After(cfg.Warmup):
	}
	debugURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	var ws string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, debugURL, nil)
		resp, callErr := client.Do(req)
		if callErr == nil {
			var version struct {
				WebSocket string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&version)
			resp.Body.Close()
			u, parseErr := url.Parse(version.WebSocket)
			if parseErr == nil && u.Scheme == "ws" && u.Host == fmt.Sprintf("127.0.0.1:%d", port) {
				ws = version.WebSocket
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-n.exited:
			return nil, errors.New("native Chrome exited")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if ws == "" {
		return nil, errors.New("native Chrome debugging endpoint did not become ready")
	}
	// Attach to the very tab that completed the warmup. Creating a fresh
	// target here changes the browser environment between verification and
	// crawling, even though both targets share a profile.
	listReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), nil)
	listResp, listErr := client.Do(listReq)
	if listErr != nil {
		return nil, fmt.Errorf("native Chrome initial tab unavailable: %w", listErr)
	}
	var tabs []struct {
		ID   target.ID `json:"id"`
		Type string    `json:"type"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(listResp.Body, 1<<20)).Decode(&tabs)
	listResp.Body.Close()
	if decodeErr != nil {
		return nil, decodeErr
	}
	var tabID target.ID
	for _, tab := range tabs {
		if tab.Type == "page" {
			tabID = tab.ID
			break
		}
	}
	if tabID == "" {
		return nil, errors.New("native Chrome initial tab not found")
	}
	alloc, allocStop := remote.NewAllocator(ctx, ws, remote.NoModifyURL)
	n.allocStop = allocStop
	n.ctx, n.tabStop = chromedp.NewContext(alloc, chromedp.WithTargetID(tabID))
	initialized := make(chan error, 1)
	go func() { initialized <- chromedp.Do(n.ctx) }()
	select {
	case err := <-initialized:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		return nil, errors.New("native Chrome attachment timed out")
	}
	if err := chromedp.Do(n.ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, cdpbrowser.SetDownloadBehavior, cdpbrowser.SetDownloadBehaviorParams{Behavior: cdpbrowser.SetDownloadBehaviorBehaviorDeny})
		return err
	})); err != nil {
		return nil, err
	}
	uaCtx, uaCancel := context.WithTimeout(n.ctx, 5*time.Second)
	version, versionErr := cdp.Call(uaCtx, chromedp.FromContext(n.ctx).Browser, cdpbrowser.GetVersion, cdp.Empty{})
	uaCancel()
	if versionErr != nil {
		return nil, versionErr
	}
	n.userAgent = version.UserAgent
	context.AfterFunc(ctx, n.Close)
	ok = true
	return n, nil
}

func (n *Native) UserAgent() string { return n.userAgent }

func (n *Native) AuxiliaryError() string { n.mu.Lock(); defer n.mu.Unlock(); return n.auxError }

func (n *Native) Close() {
	n.once.Do(func() {
		if n.cancel != nil {
			n.cancel()
		}
		if n.cmd != nil && n.cmd.Process != nil {
			n.cmd.Process.Signal(os.Interrupt)
			select {
			case <-n.exited:
			case <-time.After(2 * time.Second):
				n.cmd.Process.Kill()
				<-n.exited
			}
		}
		// End the owned process before waiting for remote CDP cleanup. An
		// unresponsive page must not keep a crawl's Close blocked forever.
		if n.tabStop != nil {
			n.tabStop()
		}
		if n.allocStop != nil {
			n.allocStop()
		}
		if n.guard != nil {
			n.guard.Close()
		}
		if n.temporary {
			os.RemoveAll(n.profile)
		}
		if n.releaseProfile != nil {
			n.releaseProfile()
		}
	})
}

// Fetch preserves the final HTTP response URL. BrowserURL may differ after
// history.replaceState (e.g. ?range=20); it must not decide canonical status.
func (n *Native) Fetch(request context.Context, target string, render bool, follow func(string) bool) types.FetchResult {
	n.mu.Lock()
	defer n.mu.Unlock()
	result := types.FetchResult{URL: target, FinalURL: target, Headers: map[string]string{}}
	ctx, cancel := context.WithTimeout(n.ctx, n.config.Timeout)
	stop := context.AfterFunc(request, cancel)
	defer func() { stop(); cancel() }()
	defer func() {
		cleanup, finish := context.WithTimeout(n.ctx, 2*time.Second)
		defer finish()
		chromedp.Do(cleanup, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, fetch.Disable, cdp.Empty{})
			return err
		}))
	}()
	start := time.Now()
	var stateMu sync.Mutex
	var response *network.Response
	var redirectResponse *network.Response
	var requestID network.RequestID
	responseReady, redirectReady := make(chan struct{}), make(chan struct{})
	var responseOnce, redirectOnce sync.Once
	finished := make(map[network.RequestID]bool)
	finishedNotice := make(chan struct{}, 1)
	var chain []types.RedirectHop
	var blocked bool
	var aborted error
	var navigationAborted bool
	err := chromedp.Do(ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		// Frame ID stays stable for the main tab, unlike the URL shown by JS.
		tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
		if err != nil {
			return err
		}
		mainID := tree.FrameTree.Frame.ID
		responses := cdp.Events(ctx, t, network.ResponseReceived)
		redirects := cdp.Events(ctx, t, network.RequestWillBeSent)
		paused := interceptionEvents(ctx, t)
		loaded := cdp.Events(ctx, t, network.LoadingFinished)
		go func() {
			for event, err := range loaded {
				if err != nil {
					return
				}
				stateMu.Lock()
				finished[event.RequestID] = true
				stateMu.Unlock()
				select {
				case finishedNotice <- struct{}{}:
				default:
				}
			}
		}()
		go func() {
			for e, err := range responses {
				if err != nil {
					return
				}
				if e.FrameID == mainID && e.Type == network.ResourceTypeDocument {
					stateMu.Lock()
					response, requestID = e.Response, e.RequestID
					stateMu.Unlock()
					responseOnce.Do(func() { close(responseReady) })
				}
			}
		}()
		go func() {
			for e, err := range redirects {
				if err != nil {
					return
				}
				if e.FrameID == mainID && e.Type == network.ResourceTypeDocument && e.RedirectResponse != nil {
					stateMu.Lock()
					chain = append(chain, types.RedirectHop{URL: e.RedirectResponse.URL, StatusCode: int(e.RedirectResponse.Status)})
					redirectResponse = e.RedirectResponse
					stateMu.Unlock()
					redirectOnce.Do(func() { close(redirectReady) })
				}
			}
		}()
		go func() {
			for e, eventErr := range paused {
				if eventErr != nil {
					return
				}
				deny := n.config.BlockImages && e.ResourceType == network.ResourceTypeImage
				if e.ResourceType == network.ResourceTypeDocument {
					u, parseErr := url.Parse(e.Request.URL)
					if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") {
						deny = true
					}
					if e.FrameID == mainID && e.Request.URL != target && follow != nil && !follow(e.Request.URL) {
						deny = true
						stateMu.Lock()
						blocked = true
						stateMu.Unlock()
					}
					if !deny && n.config.Pace != nil {
						sent := n.config.Pace(ctx, e.Request.URL)
						if sent != nil {
							deferSent := sent
							_, err := cdp.Call(ctx, t, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
							deferSent()
							if err != nil {
								stateMu.Lock()
								aborted = err
								stateMu.Unlock()
							}
							continue
						}
					}
				}
				if deny {
					cdp.Call(ctx, t, fetch.FailRequest, fetch.FailRequestParams{RequestID: e.RequestID, ErrorReason: network.ErrorReasonBlockedByClient})
				} else {
					cdp.Call(ctx, t, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
				}
			}
		}()
		if _, err := cdp.Call(ctx, t, network.Enable, network.EnableParams{}); err != nil {
			return err
		}
		if _, err := cdp.Call(ctx, t, network.SetCacheDisabled, network.SetCacheDisabledParams{CacheDisabled: true}); err != nil {
			return err
		}
		_, err = cdp.Call(ctx, t, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: "*", RequestStage: fetch.RequestStageRequest}}})
		return err
	}), chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		if _, err := cdp.Call(ctx, t, page.BringToFront, cdp.Empty{}); err != nil {
			return err
		}
		res, err := cdp.Call(ctx, t, page.Navigate, page.NavigateParams{URL: target})
		if err != nil {
			return err
		}
		if res.ErrorText != "" {
			navigationAborted = res.ErrorText == "net::ERR_ABORTED"
			return fmt.Errorf("%s", res.ErrorText)
		}
		return nil
	}))
	// Page.load and Network events have separate consumers. Wait for the
	// response consumer rather than racing a fast cached/plaintext document.
	stateMu.Lock()
	alreadyBlocked := blocked
	stateMu.Unlock()
	if (err == nil || navigationAborted) && !alreadyBlocked {
		select {
		case <-responseReady:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	stateMu.Lock()
	r, id, hops, outOfScope, interceptErr := response, requestID, append([]types.RedirectHop(nil), chain...), blocked, aborted
	stateMu.Unlock()
	if outOfScope {
		select {
		case <-redirectReady:
		case <-ctx.Done():
		}
		stateMu.Lock()
		r = redirectResponse
		hops = append([]types.RedirectHop(nil), chain...)
		stateMu.Unlock()
	}
	waitDocument := func(doc network.RequestID) error {
		for {
			stateMu.Lock()
			done := finished[doc]
			stateMu.Unlock()
			if done {
				return nil
			}
			select {
			case <-finishedNotice:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if outOfScope && r != nil && r.Status >= 300 && r.Status < 400 {
		err = nil
	}
	// Chrome does not commit a document for successful no-content responses.
	// It reports ERR_ABORTED and no body/LoadingFinished event. Wait for the
	// HTTP metadata above, then preserve that success instead of the old tab.
	noContent := r != nil && (r.Status == http.StatusNoContent || r.Status == http.StatusResetContent)
	if noContent && navigationAborted && ctx.Err() == nil {
		err = nil
	}
	if err == nil && interceptErr != nil {
		err = interceptErr
	}
	if r != nil {
		result.FinalURL, result.StatusCode = r.URL, int(r.Status)
		result.ContentType = r.MimeType
		for k, v := range r.Headers {
			result.Headers[strings.ToLower(k)] = fmt.Sprint(v)
		}
		if value := result.Headers["content-type"]; value != "" {
			result.ContentType = value
		}
	}
	result.RedirectChain = hops
	if err != nil {
		result.Error = "native Chrome navigation: " + err.Error()
		result.Retryable = result.StatusCode == 0 && request.Err() == nil && n.ctx.Err() == nil
		result.ResponseTimeMs = time.Since(start).Milliseconds()
		return result
	}
	if outOfScope || noContent {
		result.ResponseTimeMs = time.Since(start).Milliseconds()
		return result
	}
	if err := waitDocument(id); err != nil {
		result.Error = "native Chrome document: " + err.Error()
		result.ResponseTimeMs = time.Since(start).Milliseconds()
		return result
	}
	// Read the original response before waiting for page JS; a client-side
	// navigation can replace the main document and its request ID later.
	body, bodyErr := chromedp.Run(ctx, chromedp.Action[string](func(ctx context.Context, t *chromedp.Target) (string, error) {
		res, err := cdp.Call(ctx, t, network.GetResponseBody, network.GetResponseBodyParams{RequestID: id})
		if err != nil {
			return "", err
		}
		return string(res.Body), nil
	}))
	if bodyErr != nil {
		result.Error = "native Chrome response body unavailable: " + bodyErr.Error()
		result.ResponseTimeMs = time.Since(start).Milliseconds()
		return result
	}
	if len(body) > types.HTMLBodyReadCap {
		result.Error = "native Chrome document exceeds HTML size limit"
		return result
	}
	result.RawHTML, result.HTML = body, body
	result.BodySize = int64(len(body))
	result.ResponseTimeMs = time.Since(start).Milliseconds()
	if render && strings.Contains(strings.ToLower(result.ContentType), "html") {
		if err := chromedp.Do(ctx, waitNativeDOM()); err != nil {
			result.Error = "native Chrome rendering: " + err.Error()
			return result
		}
		result.HTML, err = chromedp.Run(ctx, chromedp.OuterHTML("html"))
		if err != nil {
			result.Error = "native Chrome rendered DOM unavailable"
		}
		result.BrowserURL, _ = chromedp.Run(ctx, chromedp.Location())
		// A real JavaScript navigation loads another HTTP document. Unlike
		// replaceState, that new response must replace status/headers/source.
		stateMu.Lock()
		latest, latestID := response, requestID
		// JavaScript may have navigated through HTTP redirects while the
		// rendered document settled. Keep those hops with its final response.
		result.RedirectChain = append([]types.RedirectHop(nil), chain...)
		stateMu.Unlock()
		if latest != nil && latestID != id && latestID != "" {
			if err := waitDocument(latestID); err != nil {
				result.Error = "native Chrome navigated document: " + err.Error()
				return result
			}
			result.FinalURL, result.StatusCode = latest.URL, int(latest.Status)
			result.Headers = make(map[string]string)
			for key, value := range latest.Headers {
				result.Headers[strings.ToLower(key)] = fmt.Sprint(value)
			}
			result.ContentType = result.Headers["content-type"]
			source, sourceErr := chromedp.Run(ctx, chromedp.Action[string](func(ctx context.Context, t *chromedp.Target) (string, error) {
				r, err := cdp.Call(ctx, t, network.GetResponseBody, network.GetResponseBodyParams{RequestID: latestID})
				return string(r.Body), err
			}))
			if sourceErr != nil || len(source) > types.HTMLBodyReadCap {
				result.Error = "native Chrome navigated document body unavailable"
			} else {
				result.RawHTML = source
				result.BodySize = int64(len(source))
			}
		}
	}
	return result
}

// Wait for an interactive, stable DOM, not window.load: ad frames and
// tracking pixels can keep load pending even after the page is usable.
func waitNativeDOM() chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		started := time.Now()
		type dom struct {
			Ready bool `json:"ready"`
			Size  int  `json:"size"`
		}
		previous, stable := -1, 0
		for i := 0; i < 20; i++ {
			state, err := chromedp.Evaluate[dom](`({ready:document.readyState!=='loading',size:document.body?document.body.innerHTML.length:0})`)(ctx, t)
			if err == nil && state.Ready {
				if state.Size == previous {
					stable++
				} else {
					stable = 0
				}
				previous = state.Size
				if stable >= 2 && time.Since(started) >= 1100*time.Millisecond {
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		return nil
	})
}

// RoundTrip keeps auxiliary GETs (notably robots.txt) in the same native
// browser. It does not turn unsupported methods into anonymous HTTP calls.
func (n *Native) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return nil, errors.New("native Chrome only supports GET document requests")
	}
	r := n.Fetch(req.Context(), req.URL.String(), false, nil)
	if r.Error != "" {
		n.mu.Lock()
		n.auxError = r.Error
		n.mu.Unlock()
		return nil, errors.New(r.Error)
	}
	if r.StatusCode == 403 || r.StatusCode == 429 {
		n.mu.Lock()
		n.auxError = fmt.Sprintf("HTTP %d", r.StatusCode)
		n.mu.Unlock()
		return nil, fmt.Errorf("native Chrome blocked while loading auxiliary document (HTTP %d)", r.StatusCode)
	}
	headers := make(http.Header)
	for k, v := range r.Headers {
		if k != "set-cookie" && k != "content-encoding" && k != "content-length" {
			headers.Set(k, v)
		}
	}
	return &http.Response{StatusCode: r.StatusCode, Header: headers, Body: io.NopCloser(bytes.NewBufferString(r.RawHTML)), ContentLength: int64(len(r.RawHTML)), Request: req}, nil
}
