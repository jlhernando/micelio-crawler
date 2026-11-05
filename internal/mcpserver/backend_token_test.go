package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckTokenTransport(t *testing.T) {
	tests := []struct {
		base string
		ok   bool
	}{
		{"http://127.0.0.1:3100", true},
		{"http://localhost:3100", true},
		{"http://[::1]:3100", true},
		{"https://micelio.example.com", true},
		{"https://10.0.0.5:3100", true},
		{"http://10.0.0.5:3100", false},
		{"http://micelio.example.com", false},
		{"http://localhost.evil.com:3100", false},
		{"ftp://127.0.0.1", false},
		{"not a url", false},
	}
	for _, tc := range tests {
		err := CheckTokenTransport(tc.base)
		if (err == nil) != tc.ok {
			t.Errorf("CheckTokenTransport(%q) = %v, want ok=%v", tc.base, err, tc.ok)
		}
	}
}

type failTransport struct{ used atomic.Bool }

func (f *failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.used.Store(true)
	return nil, errors.New("must not connect")
}

func TestBackendRefusesTokenOverRemoteHTTP(t *testing.T) {
	ft := &failTransport{}
	b := NewHTTPBackend("http://192.0.2.10:3100", "s3cret")
	b.Client = &http.Client{Transport: ft}
	err := b.Do(context.Background(), "GET", "/api/crawls", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to send the dashboard token") {
		t.Fatalf("want refusal, got %v", err)
	}
	if ft.used.Load() {
		t.Fatal("request was sent with the token over plain http")
	}

	// Without a token there is nothing to leak: the request goes out.
	b = NewHTTPBackend("http://192.0.2.10:3100", "")
	b.Client = &http.Client{Transport: ft}
	_ = b.Do(context.Background(), "GET", "/api/crawls", nil, nil, nil)
	if !ft.used.Load() {
		t.Fatal("tokenless request should be sent")
	}
}

func TestBackendDoesNotFollowRedirectWithTokenToOtherOrigin(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.Write([]byte(`{}`))
	}))
	defer other.Close()
	// Same IP, other port: another origin that Go's own redirect policy
	// would still send the Authorization header to.
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/moved" {
			http.Redirect(w, r, "/api/crawls", http.StatusFound)
			return
		}
		if r.URL.Path == "/api/crawls" {
			w.Write([]byte(`{}`))
			return
		}
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer dash.Close()

	b := NewHTTPBackend(dash.URL, "s3cret")
	err := b.Do(context.Background(), "GET", "/api/elsewhere", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("want redirect refusal, got %v", err)
	}
	if leaked.Load() {
		t.Fatal("token followed a redirect to another origin")
	}

	// A redirect inside the same dashboard still works.
	if err := b.Do(context.Background(), "GET", "/api/moved", nil, nil, nil); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
}
