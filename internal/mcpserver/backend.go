package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Backend answers dashboard API calls. HTTPBackend talks to a running
// dashboard; an in-process backend can be added later.
type Backend interface {
	// Do sends one API request. body (if not nil) is sent as JSON and the
	// JSON answer is decoded into out (if not nil). A non-2xx answer is
	// returned as an *APIError.
	Do(ctx context.Context, method, path string, query url.Values, body, out any) error
}

// APIError is a dashboard answer that is not 2xx.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("dashboard answered %d: %s", e.Status, e.Message)
}

// statusOf returns the HTTP status of a dashboard error, or 0.
func statusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// maxResponse caps how much of one dashboard answer is read.
const maxResponse = 32 << 20

// HTTPBackend forwards calls to a dashboard over HTTP.
type HTTPBackend struct {
	Base   string       // e.g. http://127.0.0.1:3100
	Token  string       // bearer token, if the dashboard needs one
	Client *http.Client // nil = a client with a 60 s timeout

	// Ready, if set, runs before the first request (for example to start
	// the dashboard). It runs again on the next request if it fails.
	Ready func(ctx context.Context) error

	mu    sync.Mutex
	ready bool
}

// NewHTTPBackend returns a backend for the dashboard at base.
func NewHTTPBackend(base, token string) *HTTPBackend {
	return &HTTPBackend{Base: strings.TrimRight(base, "/"), Token: token}
}

func (b *HTTPBackend) ensureReady(ctx context.Context) error {
	if b.Ready == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ready {
		return nil
	}
	if err := b.Ready(ctx); err != nil {
		return err
	}
	b.ready = true
	return nil
}

// Do implements Backend.
func (b *HTTPBackend) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if err := b.ensureReady(ctx); err != nil {
		return err
	}
	err := b.do(ctx, method, path, query, body, out)
	if b.Ready != nil && refused(err) {
		// The dashboard was closed or crashed since it was checked: run
		// Ready again (it starts a new one) and retry once.
		b.mu.Lock()
		b.ready = false
		b.mu.Unlock()
		if rerr := b.ensureReady(ctx); rerr != nil {
			return rerr
		}
		err = b.do(ctx, method, path, query, body, out)
	}
	return err
}

// refused reports whether err means nothing listens at the dashboard
// address. Timeouts and DNS errors do not count: a slow dashboard is still
// running, and starting a second one could not bind its port.
func refused(err error) bool {
	var errno syscall.Errno
	return errors.Is(err, syscall.ECONNREFUSED) ||
		(errors.As(err, &errno) && errno == wsaECONNREFUSED)
}

// wsaECONNREFUSED is Windows' "connection refused" (WSAECONNREFUSED), which
// syscall.ECONNREFUSED does not match there.
const wsaECONNREFUSED = syscall.Errno(10061)

func (b *HTTPBackend) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := b.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the Micelio dashboard at %s: %w", b.Base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Message: errorMessage(resp.StatusCode, data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unexpected answer from the dashboard: %w", err)
	}
	return nil
}

// errorMessage pulls {"error": "..."} out of an error answer.
func errorMessage(status int, data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	if status == http.StatusUnauthorized {
		return "unauthorized (set --auth-token or MICELIO_AUTH_TOKEN to the dashboard's token)"
	}
	msg := strings.TrimSpace(string(data))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return msg
}
