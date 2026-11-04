package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// AppShot is one screenshot of a page of the Micelio dashboard itself.
type AppShot struct {
	JPEG []byte
	// Meta is the page's data-capture-meta attribute (JSON), if any.
	Meta string
	Err  error
}

// AppTarget is one page to capture, at its own viewport size (CSS px).
type AppTarget struct {
	URL           string
	Width, Height int64
}

// CaptureApp opens each URL of this computer's own dashboard in headless
// Chrome and screenshots it once the page sets <body data-capture="ready">
// ("error" skips it). Only loopback is reachable: every other host goes to
// a proxy that does not exist. WebGL runs on SwiftShader, so it works
// without a GPU. headers go with every request (the dashboard token).
func CaptureApp(ctx context.Context, targets []AppTarget, headers map[string]any, scale float64) []AppShot {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("use-angle", "swiftshader"),
		chromedp.Flag("enable-unsafe-swiftshader", true),
		chromedp.Flag("ignore-gpu-blocklist", true),
		chromedp.ProxyServer("http://127.0.0.1:9"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	shots := make([]AppShot, len(targets))
	if err := chromedp.Do(browserCtx); err != nil {
		for i := range shots {
			shots[i].Err = err
		}
		return shots
	}
	for i, t := range targets {
		shots[i] = captureOne(browserCtx, t.URL, headers, t.Width, t.Height, scale)
	}
	return shots
}

func captureOne(browserCtx context.Context, u string, headers map[string]any, width, height int64, scale float64) AppShot {
	tabCtx, cancel := chromedp.NewContext(browserCtx)
	defer cancel()
	ctx, timeout := context.WithTimeout(tabCtx, 90*time.Second)
	defer timeout()

	err := chromedp.Do(ctx,
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if _, err := cdp.Call(ctx, t, network.Enable, network.EnableParams{}); err != nil {
				return err
			}
			_, err := cdp.Call(ctx, t, network.SetExtraHTTPHeaders, network.SetExtraHTTPHeadersParams{Headers: network.Headers(headers)})
			return err
		}),
		chromedp.EmulateViewport(width, height, chromedp.EmulateScale(scale)),
		chromedp.Navigate(u),
		chromedp.WaitVisible(`body[data-capture]`),
	)
	if err != nil {
		return AppShot{Err: fmt.Errorf("capture %s: %w", u, err)}
	}
	attr := func(name string) string {
		r, err := chromedp.Run(ctx, chromedp.AttributeValue(`body`, name))
		if err != nil {
			return ""
		}
		return r.Value
	}
	state, meta, msg := attr("data-capture"), attr("data-capture-meta"), attr("data-capture-error")
	if state != "ready" {
		return AppShot{Err: fmt.Errorf("capture %s: page reported %q %s", u, state, msg)}
	}
	quality := int64(86)
	shot, err := chromedp.Run(ctx, chromedp.Action[[]byte](func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
		res, err := cdp.Call(ctx, t, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatJpeg, Quality: &quality})
		if err != nil {
			return nil, err
		}
		return res.Data, nil
	}))
	if err != nil {
		return AppShot{Err: fmt.Errorf("capture %s: %w", u, err)}
	}
	return AppShot{JPEG: shot, Meta: meta}
}
