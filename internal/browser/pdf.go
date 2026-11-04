package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// PrintPDF turns a self-contained HTML document into a PDF with headless
// Chrome. The document sets its own page size with CSS (@page). Chrome gets
// a proxy that does not exist, so a document can never fetch anything: the
// report must inline all its styles, fonts and images.
func PrintPDF(ctx context.Context, html string) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.ProxyServer("http://127.0.0.1:9"),
		chromedp.Flag("proxy-bypass-list", "<-loopback>"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()
	taskCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	timeoutCtx, timeoutCancel := context.WithTimeout(taskCtx, 60*time.Second)
	defer timeoutCancel()

	var pdf []byte
	yes, zero := true, 0.0
	err := chromedp.Do(timeoutCtx,
		chromedp.Navigate("about:blank"),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
			if err != nil {
				return err
			}
			_, err = cdp.Call(ctx, t, page.SetDocumentContent, page.SetDocumentContentParams{FrameID: tree.FrameTree.Frame.ID, HTML: html})
			return err
		}),
		chromedp.WaitReady("body"),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			res, err := cdp.Call(ctx, t, page.PrintToPDF, page.PrintToPDFParams{
				PrintBackground: &yes, PreferCSSPageSize: &yes,
				MarginTop: &zero, MarginBottom: &zero, MarginLeft: &zero, MarginRight: &zero,
			})
			if err != nil {
				return err
			}
			pdf = res.Data
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("print pdf: %w", err)
	}
	return pdf, nil
}
