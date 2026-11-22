package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

type pacingSession struct {
	events  map[string]chan jsonv2.Value
	calls   chan string
	callErr error
	stopped atomic.Int32
}

func newPacingSession() *pacingSession {
	s := &pacingSession{events: map[string]chan jsonv2.Value{}, calls: make(chan string, 1)}
	for _, method := range []string{network.ResponseReceived.Method, network.LoadingFailed.Method, network.RequestWillBeSent.Method} {
		s.events[method] = make(chan jsonv2.Value, 4)
	}
	return s
}

func (s *pacingSession) Call(_ context.Context, method string, _, _ any) error {
	s.calls <- method
	return s.callErr
}

func (s *pacingSession) Subscribe(method string) (<-chan jsonv2.Value, func()) {
	return s.events[method], func() { s.stopped.Add(1) }
}

func TestDocumentPacingWaitsBeyondChromeAcknowledgement(t *testing.T) {
	for _, tc := range []struct{ name, method, event string }{
		{"headers", network.ResponseReceived.Method, `{"requestId":"n1"}`},
		{"redirect", network.RequestWillBeSent.Method, `{"requestId":"n1","redirectResponse":{"status":302},"request":{"postData":"\udcf1"}}`},
		{"failure", network.LoadingFailed.Method, `{"requestId":"n1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := newPacingSession()
			var released atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- continuePacedDocument(ctx, s, fetch.EventRequestPaused{RequestID: "f1", NetworkID: "n1", Request: &network.Request{URL: "https://example.com/frame"}}, func(context.Context, string) func() { return func() { released.Add(1) } })
			}()
			select {
			case method := <-s.calls:
				if method != fetch.ContinueRequest.Method {
					t.Fatal(method)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Neither another request's response nor the initial request event
			// proves that this paused document reached the server.
			s.events[network.ResponseReceived.Method] <- jsonv2.Value(`{"requestId":"other"}`)
			s.events[network.RequestWillBeSent.Method] <- jsonv2.Value(`{"requestId":"n1"}`)
			select {
			case err := <-done:
				t.Fatalf("released on Chrome acknowledgement: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			if released.Load() != 0 {
				t.Fatal("host turn released before network progress")
			}
			s.events[tc.method] <- jsonv2.Value(tc.event)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if released.Load() != 1 || s.stopped.Load() != 3 {
				t.Fatalf("released=%d subscriptions stopped=%d", released.Load(), s.stopped.Load())
			}
		})
	}
}

func TestDocumentPacingReleasesOnCancellationOrContinueFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "continue error"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := newPacingSession()
			failure := errors.New("Chrome disconnected")
			if fail {
				s.callErr = failure
			}
			var released atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- continuePacedDocument(ctx, s, fetch.EventRequestPaused{RequestID: "f1", NetworkID: "n1", Request: &network.Request{URL: "https://example.com/"}}, func(context.Context, string) func() { return func() { released.Add(1) } })
			}()
			select {
			case <-s.calls:
			case <-time.After(2 * time.Second):
				t.Fatal("continue was not called")
			}
			if !fail {
				cancel()
			}
			select {
			case err := <-done:
				want := failure
				if !fail {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("host turn leaked")
			}
			if released.Load() != 1 || s.stopped.Load() != 3 {
				t.Fatalf("released=%d subscriptions stopped=%d", released.Load(), s.stopped.Load())
			}
		})
	}
}

func TestDocumentPacingRejectsUnobservableRequest(t *testing.T) {
	s := newPacingSession()
	var released int
	err := continuePacedDocument(context.Background(), s, fetch.EventRequestPaused{RequestID: "f1", Request: &network.Request{URL: "https://example.com/"}}, func(context.Context, string) func() { return func() { released++ } })
	if err == nil || released != 1 {
		t.Fatalf("err=%v released=%d", err, released)
	}
	if method := <-s.calls; method != fetch.FailRequest.Method {
		t.Fatal(method)
	}
}
