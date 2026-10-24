package crawler

import (
	"context"
	"testing"
	"time"
)

func TestAdaptiveRateLimiterBasicDelay(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)

	start := time.Now()
	rl.Wait(context.Background(), "https://example.com/page1")
	elapsed := time.Since(start)

	if elapsed < 90*time.Millisecond {
		t.Errorf("expected at least ~100ms delay, got %v", elapsed)
	}
}

func TestAdaptiveRateLimiterMinDelay(t *testing.T) {
	// Delay below minimum should be capped
	rl := NewAdaptiveRateLimiter(10*time.Millisecond, 0)

	start := time.Now()
	rl.Wait(context.Background(), "https://example.com/page1")
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("expected at least ~50ms (min delay), got %v", elapsed)
	}
}

func TestAdaptiveRateLimiterBackoffOn429(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)

	// Record a 429 response
	rl.RecordResponse("https://example.com/page1", 429, 50, 0, 0)

	delay := rl.CurrentDelay("https://example.com/page1")
	expected := time.Duration(float64(100*time.Millisecond) * adaptiveBackoffMult)
	if delay != expected {
		t.Errorf("expected delay %v after 429, got %v", expected, delay)
	}
}

func TestAdaptiveRateLimiterBackoffOn503(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)

	rl.RecordResponse("https://example.com/page1", 503, 200, 0, 0)

	delay := rl.CurrentDelay("https://example.com/page1")
	if delay <= 100*time.Millisecond {
		t.Errorf("expected delay to increase after 503, got %v", delay)
	}
}

// raiseDelay pushes a domain's delay above its floor via the slow-response path
// (no 429/503, so the speedup window stays open).
func raiseDelay(rl *AdaptiveRateLimiter, url string) {
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 100, 0, 0)
	}
	rl.RecordResponse(url, 200, 1000, 0, 0) // > 2x avg: delay *= 1.3
}

func TestAdaptiveRateLimiterSpeedupOnSuccess(t *testing.T) {
	rl := NewAdaptiveRateLimiter(200*time.Millisecond, 0)
	url := "https://example.com/page"
	raiseDelay(rl, url)
	raised := rl.CurrentDelay(url)
	if raised <= 200*time.Millisecond {
		t.Fatalf("expected raised delay, got %v", raised)
	}
	// Fast responses (below 80% of avg) speed up, but never below the base delay.
	for i := 0; i < 30; i++ {
		rl.RecordResponse(url, 200, 10, 0, 0)
	}
	delay := rl.CurrentDelay(url)
	if delay >= raised {
		t.Errorf("expected delay to decrease after fast responses, got %v (was %v)", delay, raised)
	}
	if delay < 200*time.Millisecond {
		t.Errorf("delay %v dropped below configured base delay 200ms", delay)
	}
}

func TestAdaptiveRateLimiterNeverBelowBase(t *testing.T) {
	rl := NewAdaptiveRateLimiter(300*time.Millisecond, 0)
	url := "https://example.com/page"
	for i := 0; i < 100; i++ {
		rl.RecordResponse(url, 200, int64(100-i%90), 100, 0)
	}
	if d := rl.CurrentDelay(url); d < 300*time.Millisecond {
		t.Fatalf("delay %v below configured 300ms", d)
	}
	// Heritrix factor with fast responses must also respect the floor.
	hr := NewAdaptiveRateLimiter(300*time.Millisecond, 1.0)
	for i := 0; i < 10; i++ {
		hr.RecordResponse(url, 200, 5, 0, 0)
	}
	if d := hr.CurrentDelay(url); d < 300*time.Millisecond {
		t.Fatalf("heritrix delay %v below configured 300ms", d)
	}
}

func TestAdaptiveRateLimiterDomainFloor(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)
	rl.SetDomainMinDelay("example.com", 2*time.Second)
	url := "https://example.com/page"
	for i := 0; i < 20; i++ {
		rl.RecordResponse(url, 200, 5, 100, 0)
	}
	if d := rl.CurrentDelay(url); d < 2*time.Second {
		t.Fatalf("delay %v below robots crawl-delay floor 2s", d)
	}
	if d := rl.CurrentDelay("https://other.com/"); d != 100*time.Millisecond {
		t.Fatalf("other domain should keep base delay, got %v", d)
	}
}

func TestAdaptiveRateLimiterNetworkErrorBacksOff(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)
	rl.RecordResponse("https://example.com/", 0, 30000, 0, 0)
	if d := rl.CurrentDelay("https://example.com/"); d <= 100*time.Millisecond {
		t.Fatalf("expected backoff after network error, got %v", d)
	}
}

func TestAdaptiveRateLimiterPerDomain(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)

	// 429 on domain A
	rl.RecordResponse("https://a.com/page1", 429, 50, 0, 0)
	// Success on domain B
	for i := 0; i < 5; i++ {
		rl.RecordResponse("https://b.com/page1", 200, 30, 0, 0)
	}

	delayA := rl.CurrentDelay("https://a.com/page1")
	delayB := rl.CurrentDelay("https://b.com/page1")

	if delayA <= delayB {
		t.Errorf("domain A (429'd) should have higher delay than B, got A=%v B=%v", delayA, delayB)
	}
}

func TestAdaptiveRateLimiterRateLimitHeaders(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)
	url := "https://example.com/page"
	raiseDelay(rl, url)
	raised := rl.CurrentDelay(url)

	// Responses with high remaining capacity speed up (not below base).
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 100, 100, 0)
	}

	delay := rl.CurrentDelay(url)
	if delay >= raised {
		t.Errorf("expected delay to decrease with high rate limit remaining, got %v (was %v)", delay, raised)
	}
	if delay < 100*time.Millisecond {
		t.Errorf("delay %v below base", delay)
	}
}

func TestAdaptiveRateLimiterResetCappedAtMaxDelay(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 0)

	// Server sends X-RateLimit-Reset: 600 (10 minutes) — should be capped at maxDelay (30s)
	rl.RecordResponse("https://example.com/page1", 429, 50, 0, 600)

	delay := rl.CurrentDelay("https://example.com/page1")
	if delay > adaptiveMaxDelay {
		t.Errorf("delay %v should not exceed maxDelay %v", delay, adaptiveMaxDelay)
	}
}

func TestAdaptiveRateLimiterNoDoubleSpeedup(t *testing.T) {
	rl := NewAdaptiveRateLimiter(200*time.Millisecond, 0)
	url := "https://example.com/page"

	raiseDelay(rl, url)

	// A response that's fast (triggers RT speedup) AND has high remaining (triggers header speedup)
	// Should only apply one speedup (0.9x), not double (0.81x)
	before := rl.CurrentDelay(url)
	rl.RecordResponse(url, 200, 30, 50, 0)
	after := rl.CurrentDelay(url)

	expected := time.Duration(float64(before) * adaptiveSpeedupMult)
	tolerance := 2 * time.Millisecond
	if after < expected-tolerance || after > expected+tolerance {
		t.Errorf("expected single speedup from %v to ~%v, got %v", before, expected, after)
	}
}

func TestAdaptiveRateLimiterContextCancellation(t *testing.T) {
	rl := NewAdaptiveRateLimiter(5*time.Second, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	start := time.Now()
	rl.Wait(ctx, "https://example.com/page1")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("expected Wait to return quickly on cancelled context, took %v", elapsed)
	}
}

func TestTokenBucketStaggering(t *testing.T) {
	tb := newTokenBucket(100 * time.Millisecond)
	ctx := context.Background()

	// First call should return immediately
	start := time.Now()
	tb.Wait(ctx)
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Errorf("first Wait took %v, expected immediate", d)
	}

	// Second call should wait ~100ms
	start = time.Now()
	tb.Wait(ctx)
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("second Wait took %v, expected ~100ms", d)
	}
}

func TestTokenBucketSetInterval(t *testing.T) {
	tb := newTokenBucket(50 * time.Millisecond)
	ctx := context.Background()
	tb.Wait(ctx) // consume first token

	// Change interval to 200ms
	tb.SetInterval(200 * time.Millisecond)
	start := time.Now()
	tb.Wait(ctx)
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Errorf("Wait after SetInterval took %v, expected >= 50ms", d)
	}
}

func TestTokenBucketContextCancel(t *testing.T) {
	tb := newTokenBucket(5 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	tb.Wait(ctx) // consume first token

	cancel()
	start := time.Now()
	tb.Wait(ctx)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("Wait on cancelled ctx took %v, expected immediate", d)
	}
}

func TestHeritrixDelayFactor(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100*time.Millisecond, 5.0)
	url := "https://example.com/page"

	// Build up EMA with 200ms avg response time
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 200, 0, 0)
	}

	// Heritrix delay should be ~5 * 200ms = 1000ms
	delay := rl.CurrentDelay(url)
	if delay < 800*time.Millisecond || delay > 1200*time.Millisecond {
		t.Errorf("expected Heritrix delay ~1000ms, got %v", delay)
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/page1", "example.com"},
		{"https://sub.example.com:8080/path", "sub.example.com:8080"},
		{"invalid-url", ""},
	}

	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.expected {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestTokenBucketPause(t *testing.T) {
	tb := newTokenBucket(0)
	ctx := context.Background()
	tb.Wait(ctx)
	tb.PauseFor(150 * time.Millisecond)
	start := time.Now()
	tb.Wait(ctx)
	if d := time.Since(start); d < 130*time.Millisecond {
		t.Fatalf("Wait during pause took %v, expected >= 150ms", d)
	}
}

func TestHostRateLimiterFloorAndPause(t *testing.T) {
	h := newHostRateLimiter(100*time.Millisecond, nil)
	h.SetInterval("https://a.com/x", 10*time.Millisecond)
	if got := h.Interval("https://a.com/y"); got != 100*time.Millisecond {
		t.Fatalf("interval below configured delay: %v", got)
	}
	h.SetHostFloor("a.com", time.Second)
	if got := h.Interval("https://a.com/y"); got != time.Second {
		t.Fatalf("crawl-delay floor not applied: %v", got)
	}
	if got := h.Interval("https://b.com/"); got != 100*time.Millisecond {
		t.Fatalf("other host affected: %v", got)
	}

	// Pause on one host blocks every worker on that host but not others.
	h.Pause("https://b.com/", 200*time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	h.Wait(ctx, "https://c.com/")
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("unpaused host waited %v", d)
	}
	start = time.Now()
	h.Wait(ctx, "https://b.com/z")
	if d := time.Since(start); d < 180*time.Millisecond {
		t.Fatalf("paused host waited only %v", d)
	}
}
