package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterDuration(t *testing.T) {
	if d, ok := RetryAfterDuration("5", time.Minute); !ok || d != 5*time.Second {
		t.Errorf("seconds: got %v %v", d, ok)
	}
	if d, ok := RetryAfterDuration("99999", 10*time.Minute); !ok || d != 10*time.Minute {
		t.Errorf("cap: got %v %v", d, ok)
	}
	date := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := RetryAfterDuration(date, time.Minute); !ok || d < 25*time.Second || d > 31*time.Second {
		t.Errorf("http-date: got %v %v", d, ok)
	}
	if _, ok := RetryAfterDuration("", time.Minute); ok {
		t.Error("empty header should be invalid")
	}
	if _, ok := RetryAfterDuration("soon", time.Minute); ok {
		t.Error("garbage header should be invalid")
	}
}

func TestFetchPageNonHTMLBodyNotDownloaded(t *testing.T) {
	const total = 64 << 20 // 64 MB
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		chunk := make([]byte, 32<<10)
		for written.Load() < total {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	result := FetchPage(context.Background(), srv.URL+"/big.pdf", FetchOptions{
		UserAgent: "test", SkipSSRF: true, NoRetry: true,
	})
	if result.StatusCode != 200 {
		t.Fatalf("status = %d (%s)", result.StatusCode, result.Error)
	}
	time.Sleep(200 * time.Millisecond) // let the server notice the closed connection
	if w := written.Load(); w >= total {
		t.Fatalf("server wrote the full %d-byte body; non-HTML body must not be fully downloaded", w)
	}
}

func TestFetchPageNoRetryOnSSRFBlock(t *testing.T) {
	// A hostname resolving to loopback is blocked by the dialer: no retries.
	start := time.Now()
	result := FetchPage(context.Background(), "http://localhost.:1/x", FetchOptions{UserAgent: "test"})
	if result.Error == "" {
		t.Fatal("expected an error")
	}
	if result.Retryable {
		t.Error("SSRF-blocked request must not be retryable")
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Errorf("SSRF-blocked fetch took %v: it was retried", d)
	}
}

func TestNormalizeURLLowercasesHost(t *testing.T) {
	if got := NormalizeURL("https://Example.COM/Path"); got != "https://example.com/Path" {
		t.Errorf("NormalizeURL = %q", got)
	}
}

func TestQueueMarkSeenDoesNotCount(t *testing.T) {
	q := NewCrawlQueue("https://example.com/", 5, 10, nil)
	before := q.TotalSeen()
	q.MarkSeen("https://example.com/redirect-target")
	if q.TotalSeen() != before {
		t.Errorf("MarkSeen changed TotalSeen: %d -> %d", before, q.TotalSeen())
	}
	if q.Enqueue("https://example.com/redirect-target", 1, nil) {
		t.Error("redirect target should not be enqueued again")
	}
}
