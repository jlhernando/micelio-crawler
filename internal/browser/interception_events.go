package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

// interceptionEvents reads only routing metadata. Chrome can serialize binary
// POST data as escaped lone UTF-16 surrogates. The strict CDP JSON decoder
// rejects those even in fields it ignores and ends the entire subscription,
// leaving all later requests paused. The legacy JSON decoder accepts Chrome's
// string representation; it never decodes/copies the irrelevant POST body.
// ContinueRequest still sends only the ID, so Chrome forwards its original
// headers and body unchanged.
func interceptionEvents(ctx context.Context, session cdp.Session) iter.Seq2[fetch.EventRequestPaused, error] {
	events, cancel := session.Subscribe(fetch.RequestPaused.Method)
	return func(yield func(fetch.EventRequestPaused, error) bool) {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				yield(fetch.EventRequestPaused{}, ctx.Err())
				return
			case raw, ok := <-events:
				if !ok {
					return
				}
				var metadata struct {
					ID        fetch.RequestID      `json:"requestId"`
					Type      network.ResourceType `json:"resourceType"`
					FrameID   cdp.FrameID          `json:"frameId"`
					NetworkID network.RequestID    `json:"networkId"`
					Request   struct {
						URL string `json:"url"`
					} `json:"request"`
				}
				if err := json.Unmarshal(raw, &metadata); err != nil {
					yield(fetch.EventRequestPaused{}, fmt.Errorf("decode interception metadata: %w", err))
					return
				}
				if metadata.ID == "" || metadata.Request.URL == "" {
					yield(fetch.EventRequestPaused{}, fmt.Errorf("missing interception request ID or URL"))
					return
				}
				event := fetch.EventRequestPaused{RequestID: metadata.ID, ResourceType: metadata.Type, FrameID: metadata.FrameID, NetworkID: metadata.NetworkID, Request: &network.Request{URL: metadata.Request.URL}}
				if !yield(event, nil) {
					return
				}
			}
		}
	}
}
