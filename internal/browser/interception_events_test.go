package browser

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/fetch"
)

type interceptionSession struct {
	events    chan jsonv2.Value
	cancelled atomic.Bool
}

func (s *interceptionSession) Call(context.Context, string, any, any) error { panic("unused") }
func (s *interceptionSession) Subscribe(method string) (<-chan jsonv2.Value, func()) {
	if method != fetch.RequestPaused.Method {
		panic("wrong event")
	}
	return s.events, func() { s.cancelled.Store(true) }
}
func TestInterceptionIgnoresBinaryPostDataAndKeepsReading(t *testing.T) {
	s := &interceptionSession{events: make(chan jsonv2.Value, 2)}
	s.events <- jsonv2.Value(`{"requestId":"binary","resourceType":"Fetch","networkId":"n1","request":{"url":"https://example.org/analytics","postData":"\udcf1XC\\*o","postDataEntries":[{"bytes":"irrelevant binary metadata"}]}}`)
	s.events <- jsonv2.Value(`{"requestId":"next","resourceType":"Document","frameId":"f2","request":{"url":"https://example.org/next"}}`)
	close(s.events)
	var ids []string
	for event, err := range interceptionEvents(context.Background(), s) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Request == nil || !strings.HasPrefix(event.Request.URL, "https://example.org/") {
			t.Fatal("routing metadata lost")
		}
		if len(event.Request.PostDataEntries) != 0 {
			t.Fatal("irrelevant POST data was copied")
		}
		if event.RequestID == "binary" && event.NetworkID != "n1" {
			t.Fatal("network ID lost")
		}
		if event.RequestID == "next" && (event.FrameID != "f2" || event.ResourceType != "Document") {
			t.Fatal("native redirect/frame routing fields lost")
		}
		ids = append(ids, string(event.RequestID))
	}
	if strings.Join(ids, ",") != "binary,next" || !s.cancelled.Load() {
		t.Fatalf("reader stopped early or leaked subscription: %v", ids)
	}
}
func TestInterceptionSubscriptionCancels(t *testing.T) {
	s := &interceptionSession{events: make(chan jsonv2.Value)}
	ctx, cancel := context.WithCancel(context.Background())
	events := interceptionEvents(ctx, s)
	cancel()
	for _, err := range events {
		if err != context.Canceled {
			t.Fatalf("cancel=%v", err)
		}
	}
	if !s.cancelled.Load() {
		t.Fatal("subscription leaked")
	}
}
