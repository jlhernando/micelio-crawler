// Package browser provides headless Chrome rendering via chromedp.
package browser

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/micelio/micelio/internal/types"
)

const (
	defaultTimeout = 30 * time.Second
	snippetTimeout = 15 * time.Second
)

// blockedResourceExts are file extensions blocked when resource blocking is enabled.
var blockedResourceExts = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp", ".avif",
	".woff", ".woff2", ".ttf", ".eot", ".otf",
	".mp4", ".webm", ".mp3", ".ogg", ".avi", ".mov",
}

// blockedDomains are analytics/tracking domains blocked when resource blocking is enabled.
var blockedDomains = []string{
	"google-analytics.com", "googletagmanager.com", "doubleclick.net",
	"facebook.net", "connect.facebook.com", "hotjar.com",
	"mc.yandex.ru", "analytics.tiktok.com", "snap.licdn.com",
}

// RenderResult holds the output of a browser render.
type RenderResult struct {
	HTML     string
	JSErrors []string
}

// RenderConfig holds per-renderer configuration.
type RenderConfig struct {
	Timeout        time.Duration
	BlockResources bool
}

// Renderer manages a headless Chrome instance.
// Fresh tab contexts are created per render to avoid listener accumulation.
// The Chrome process is shared via the allocator for efficiency.
type Renderer struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	proxy       string
	config      RenderConfig
}

// NewRenderer creates a new browser renderer.
// Call Close() when done to release Chrome resources.
func NewRenderer(proxy string, cfg ...RenderConfig) *Renderer {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	if proxy != "" {
		opts = append(opts, chromedp.ProxyServer(proxy))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	rc := RenderConfig{Timeout: defaultTimeout, BlockResources: true}
	if len(cfg) > 0 {
		c := cfg[0]
		if c.Timeout > 0 {
			rc.Timeout = c.Timeout
		}
		rc.BlockResources = c.BlockResources
	}

	return &Renderer{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		proxy:       proxy,
		config:      rc,
	}
}

// waitForDOMStability polls document.body.innerHTML.length until stable.
// Requires 2 consecutive stable intervals (3 matching measurements) to handle
// phased SPA rendering where the DOM briefly stabilizes between render phases.
func waitForDOMStability() chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		// Wait for document.readyState === "complete"
		deadline := time.After(5 * time.Second)
		for {
			readyState, err := chromedp.Evaluate[string](`document.readyState`)(ctx, t)
			if err != nil {
				return nil // best-effort
			}
			if readyState == "complete" {
				break
			}
			select {
			case <-deadline:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		// Poll DOM stability: require 2 consecutive stable intervals
		var prevLen int64
		stableCount := 0
		for i := 0; i < 6; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
			curLen, err := chromedp.Evaluate[int64](`document.body ? document.body.innerHTML.length : 0`)(ctx, t)
			if err != nil {
				return nil
			}
			if i > 0 && curLen == prevLen {
				stableCount++
				if stableCount >= 2 {
					return nil // DOM stable for 2 consecutive intervals
				}
			} else {
				stableCount = 0
			}
			prevLen = curLen
		}
		return nil // max iterations, proceed
	})
}

// setupResourceBlocking enables fetch domain interception to block images/fonts/analytics.
func setupResourceBlocking() chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		// Subscribe before enabling so no paused request is missed.
		events := cdp.Events(ctx, t, fetch.RequestPaused)
		if _, err := cdp.Call(ctx, t, fetch.Enable, fetch.EnableParams{
			Patterns: []*fetch.RequestPattern{{RequestStage: fetch.RequestStageRequest}},
		}); err != nil {
			return nil // non-fatal
		}
		go func() {
			for req, err := range events {
				if err != nil {
					return
				}
				go func() {
					defer func() { recover() }()
					blocked := false
					rt := req.ResourceType
					if rt == network.ResourceTypeImage || rt == network.ResourceTypeFont || rt == network.ResourceTypeMedia {
						blocked = true
					}
					if !blocked {
						lower := strings.ToLower(req.Request.URL)
						pathEnd := lower
						if idx := strings.IndexByte(pathEnd, '?'); idx >= 0 {
							pathEnd = pathEnd[:idx]
						}
						for _, ext := range blockedResourceExts {
							if strings.HasSuffix(pathEnd, ext) {
								blocked = true
								break
							}
						}
						if !blocked {
							for _, domain := range blockedDomains {
								if strings.Contains(lower, domain) {
									blocked = true
									break
								}
							}
						}
					}
					if blocked {
						_, _ = cdp.Call(ctx, t, fetch.FailRequest, fetch.FailRequestParams{
							RequestID: req.RequestID, ErrorReason: network.ErrorReasonBlockedByClient,
						})
					} else {
						_, _ = cdp.Call(ctx, t, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: req.RequestID})
					}
				}()
			}
		}()
		return nil
	})
}

// setUserAgent overrides the tab's user agent when one is given.
func setUserAgent(userAgent string) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		if userAgent == "" {
			return nil
		}
		_, err := cdp.Call(ctx, t, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: userAgent})
		return err
	})
}

// collectJSErrors enables runtime domain and collects console errors.
func collectJSErrors(errors *[]string, mu *sync.Mutex) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		events := cdp.Events(ctx, t, runtime.ExceptionThrown)
		if _, err := cdp.Call(ctx, t, runtime.Enable, cdp.Empty{}); err != nil {
			return nil // non-fatal
		}
		go func() {
			for e, err := range events {
				if err != nil {
					return
				}
				if e.ExceptionDetails == nil {
					continue
				}
				msg := e.ExceptionDetails.Text
				if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
					msg = e.ExceptionDetails.Exception.Description
				}
				if msg != "" {
					mu.Lock()
					if len(*errors) < 50 {
						*errors = append(*errors, msg)
					}
					mu.Unlock()
				}
			}
		}()
		return nil
	})
}

// RenderPage navigates to a URL and returns the rendered HTML after JS execution.
func (r *Renderer) RenderPage(ctx context.Context, pageURL, userAgent string) (*RenderResult, error) {
	taskCtx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(taskCtx, r.config.Timeout)
	defer timeoutCancel()

	var jsErrors []string
	var errorMu sync.Mutex

	actions := []chromedp.Action[chromedp.Void]{
		chromedp.EmulateViewport(1920, 1080),
		setUserAgent(userAgent),
		collectJSErrors(&jsErrors, &errorMu),
	}
	if r.config.BlockResources {
		actions = append(actions, setupResourceBlocking())
	}

	actions = append(actions,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		waitForDOMStability(),
	)

	if err := chromedp.Do(timeoutCtx, actions...); err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	html, err := chromedp.Run(timeoutCtx, chromedp.OuterHTML("html"))
	if err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}

	errorMu.Lock()
	errs := jsErrors
	errorMu.Unlock()

	return &RenderResult{HTML: html, JSErrors: errs}, nil
}

// RunSnippet executes a JavaScript snippet on a rendered page and returns the result.
func (r *Renderer) RunSnippet(ctx context.Context, pageURL, userAgent, jsCode string) (any, error) {
	taskCtx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(taskCtx, snippetTimeout)
	defer timeoutCancel()

	actions := []chromedp.Action[chromedp.Void]{
		setUserAgent(userAgent),
	}
	if r.config.BlockResources {
		actions = append(actions, setupResourceBlocking())
	}

	actions = append(actions,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		waitForDOMStability(),
	)

	if err := chromedp.Do(timeoutCtx, actions...); err != nil {
		return nil, fmt.Errorf("run snippet: %w", err)
	}
	result, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[any](jsCode))
	if err != nil {
		return nil, fmt.Errorf("run snippet: %w", err)
	}
	return result, nil
}

// hideOverlaysJS removes cookie and consent banners before a screenshot.
// Only overlays go (fixed/sticky elements and known consent dialogs), never
// the page itself: <body class="cookie-consent-active"> must stay.
const hideOverlaysJS = `(() => {
  const main = document.querySelector('main');
  for (const e of document.querySelectorAll('[id*=cookie i],[class*=cookie i],[id*=consent i],[class*=consent i],#onetrust-banner-sdk,#CybotCookiebotDialog')) {
    if (e === document.body || e === document.documentElement || (main && e.contains(main))) continue;
    const pos = getComputedStyle(e).position;
    if (pos === 'fixed' || pos === 'sticky' || e.id === 'onetrust-banner-sdk' || e.id === 'CybotCookiebotDialog') e.remove();
  }
})()`

// Screenshot loads pageURL in a width x height viewport and returns a JPEG
// scaled by scale (0.25 turns 1280x800 into a 320x200 thumbnail) without any
// image processing in Go. Use a renderer with BlockResources off so images load.
func (r *Renderer) Screenshot(ctx context.Context, pageURL string, width, height int64, scale float64, quality int64) ([]byte, error) {
	taskCtx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()
	timeoutCtx, timeoutCancel := context.WithTimeout(taskCtx, r.config.Timeout)
	defer timeoutCancel()
	stop := context.AfterFunc(ctx, timeoutCancel)
	defer stop()

	var shot []byte
	err := chromedp.Do(timeoutCtx,
		chromedp.EmulateViewport(width, height, chromedp.EmulateScale(scale)),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(700*time.Millisecond),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, _ = chromedp.Evaluate[any](hideOverlaysJS)(ctx, t) // best-effort
			res, err := cdp.Call(ctx, t, page.CaptureScreenshot, page.CaptureScreenshotParams{
				Format: page.CaptureScreenshotFormatJpeg, Quality: &quality,
			})
			if err != nil {
				return err
			}
			shot = res.Data
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return shot, nil
}

// Close shuts down the Chrome instance.
func (r *Renderer) Close() {
	r.allocCancel()
}

// ── Render comparison ──

// CompareRender compares pre-render and post-render HTML for SEO-critical differences.
func CompareRender(rawHTML, renderedHTML string) ([]types.RenderDiff, error) {
	var diffs []types.RenderDiff

	rawDoc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return nil, fmt.Errorf("parse raw HTML: %w", err)
	}
	renderedDoc, err := goquery.NewDocumentFromReader(strings.NewReader(renderedHTML))
	if err != nil {
		return nil, fmt.Errorf("parse rendered HTML: %w", err)
	}

	// 1. Title
	rawTitle := strings.TrimSpace(rawDoc.Find("title").First().Text())
	renderedTitle := strings.TrimSpace(renderedDoc.Find("title").First().Text())
	if rawTitle != renderedTitle {
		diffs = append(diffs, types.RenderDiff{Field: "title", Original: rawTitle, Rendered: renderedTitle})
	}

	// 2. Meta description
	rawDesc, _ := rawDoc.Find(`meta[name="description"]`).Attr("content")
	renderedDesc, _ := renderedDoc.Find(`meta[name="description"]`).Attr("content")
	rawDesc = strings.TrimSpace(rawDesc)
	renderedDesc = strings.TrimSpace(renderedDesc)
	if rawDesc != renderedDesc {
		diffs = append(diffs, types.RenderDiff{Field: "meta_description", Original: rawDesc, Rendered: renderedDesc})
	}

	// 3. Canonical
	rawCanonical, _ := rawDoc.Find(`link[rel="canonical"]`).Attr("href")
	renderedCanonical, _ := renderedDoc.Find(`link[rel="canonical"]`).Attr("href")
	if strings.TrimSpace(rawCanonical) != strings.TrimSpace(renderedCanonical) {
		diffs = append(diffs, types.RenderDiff{Field: "canonical", Original: rawCanonical, Rendered: renderedCanonical})
	}

	// 4. Meta robots
	rawRobots, _ := rawDoc.Find(`meta[name="robots"]`).Attr("content")
	renderedRobots, _ := renderedDoc.Find(`meta[name="robots"]`).Attr("content")
	if strings.TrimSpace(rawRobots) != strings.TrimSpace(renderedRobots) {
		diffs = append(diffs, types.RenderDiff{Field: "meta_robots", Original: rawRobots, Rendered: renderedRobots})
	}

	// 5. H1
	rawH1 := collectText(rawDoc, "h1")
	renderedH1 := collectText(renderedDoc, "h1")
	if rawH1 != renderedH1 {
		diffs = append(diffs, types.RenderDiff{Field: "h1", Original: rawH1, Rendered: renderedH1})
	}

	// 6. Internal links count
	rawLinkCount := countInternalLinks(rawDoc)
	renderedLinkCount := countInternalLinks(renderedDoc)
	if rawLinkCount != renderedLinkCount {
		diffs = append(diffs, types.RenderDiff{
			Field: "internal_links_count",
			Original: fmt.Sprintf("%d", rawLinkCount),
			Rendered: fmt.Sprintf("%d", renderedLinkCount),
		})
	}

	// 7. Word count (only report >20% difference)
	rawWords := countBodyWords(rawDoc)
	renderedWords := countBodyWords(renderedDoc)
	if rawWords > 0 && renderedWords > 0 {
		ratio := math.Abs(float64(renderedWords-rawWords)) / float64(max(rawWords, 1))
		if ratio > 0.2 {
			diffs = append(diffs, types.RenderDiff{
				Field: "word_count",
				Original: fmt.Sprintf("%d", rawWords),
				Rendered: fmt.Sprintf("%d", renderedWords),
			})
		}
	} else if rawWords == 0 && renderedWords > 50 {
		diffs = append(diffs, types.RenderDiff{Field: "word_count", Original: "0", Rendered: fmt.Sprintf("%d", renderedWords)})
	} else if rawWords > 50 && renderedWords == 0 {
		diffs = append(diffs, types.RenderDiff{Field: "word_count", Original: fmt.Sprintf("%d", rawWords), Rendered: "0"})
	}

	// 8. JSON-LD count
	rawJsonLd := rawDoc.Find(`script[type="application/ld+json"]`).Length()
	renderedJsonLd := renderedDoc.Find(`script[type="application/ld+json"]`).Length()
	if rawJsonLd != renderedJsonLd {
		diffs = append(diffs, types.RenderDiff{
			Field: "json_ld_count",
			Original: fmt.Sprintf("%d", rawJsonLd),
			Rendered: fmt.Sprintf("%d", renderedJsonLd),
		})
	}

	return diffs, nil
}

// spaRootIDs are common SPA framework root element IDs.
var spaRootIDs = []string{"root", "app", "__next", "__nuxt"}

// IsSPALikely detects if HTML is likely a single-page application.
func IsSPALikely(htmlStr string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return false
	}

	hasRoot := false
	for _, id := range spaRootIDs {
		if doc.Find(fmt.Sprintf(`div#%s`, id)).Length() > 0 {
			hasRoot = true
			break
		}
	}
	if !hasRoot {
		return false
	}

	body := doc.Find("body").Clone()
	body.Find("script, style, noscript").Remove()
	text := strings.TrimSpace(body.Text())
	text = strings.Join(strings.Fields(text), " ")

	return len(text) < 200
}

// ── helpers ──

func collectText(doc *goquery.Document, selector string) string {
	var parts []string
	doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			parts = append(parts, text)
		}
	})
	return strings.Join(parts, " | ")
}

func countInternalLinks(doc *goquery.Document) int {
	seen := make(map[string]struct{})
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}
		if isLikelyInternalHref(href) {
			seen[href] = struct{}{}
		}
	})
	return len(seen)
}

func isLikelyInternalHref(href string) bool {
	if strings.HasPrefix(href, "//") {
		return false
	}
	if strings.HasPrefix(href, "/") {
		return true
	}
	if strings.HasPrefix(href, "#") {
		return false
	}
	if strings.Contains(href, ":") {
		return false
	}
	return true
}

func countBodyWords(doc *goquery.Document) int {
	body := doc.Find("body").Clone()
	body.Find("script, style, nav, footer, header, noscript").Remove()
	text := strings.TrimSpace(body.Text())
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

