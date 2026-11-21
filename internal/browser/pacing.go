package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

// Chrome acknowledges ContinueRequest before the request reaches the network.
// Keep the host's turn until response headers, a redirect response or failure
// confirms that navigation progressed. This conservatively includes TTFB in
// the host's spacing, without waiting for the response body or page load.
func continuePacedDocument(ctx context.Context, session cdp.Session, req fetch.EventRequestPaused, pace func(context.Context, string) func()) error {
	sent := pace(ctx, req.Request.URL)
	defer sent()
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.NetworkID == "" {
		// No observable network request: fail closed instead of releasing the
		// turn on an acknowledgement that does not prove a request was sent.
		_, _ = cdp.Call(ctx, session, fetch.FailRequest, fetch.FailRequestParams{RequestID: req.RequestID, ErrorReason: network.ErrorReasonBlockedByClient})
		return fmt.Errorf("paced document has no network request ID")
	}
	responses, stopResponses := session.Subscribe(network.ResponseReceived.Method)
	defer stopResponses()
	failures, stopFailures := session.Subscribe(network.LoadingFailed.Method)
	defer stopFailures()
	redirects, stopRedirects := session.Subscribe(network.RequestWillBeSent.Method)
	defer stopRedirects()
	if _, err := cdp.Call(ctx, session, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: req.RequestID}); err != nil {
		return err
	}
	for {
		var raw []byte
		var redirect bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-responses:
			if !ok {
				return fmt.Errorf("Chrome response subscription closed")
			}
			raw = event
		case event, ok := <-failures:
			if !ok {
				return fmt.Errorf("Chrome failure subscription closed")
			}
			raw = event
		case event, ok := <-redirects:
			if !ok {
				return fmt.Errorf("Chrome redirect subscription closed")
			}
			raw, redirect = event, true
		}
		// Decode only correlation metadata. RequestWillBeSent can contain
		// the same binary POST strings that interceptionEvents must ignore.
		var event struct {
			RequestID        network.RequestID `json:"requestId"`
			RedirectResponse json.RawMessage   `json:"redirectResponse"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("decode document progress: %w", err)
		}
		if event.RequestID == req.NetworkID && (!redirect || (len(event.RedirectResponse) > 0 && string(event.RedirectResponse) != "null")) {
			return nil
		}
	}
}
