package crawler

import (
	"context"
	"testing"
	"time"
)

func TestAdaptiveRateLimiterBasicDelay(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)

	start := time.Now()
	rl.Wait(context.Background(), "https://example.com/page1")
	elapsed := time.Since(start)

	if elapsed < 90*time.Millisecond {
		t.Errorf("expected at least ~100ms delay, got %v", elapsed)
	}
}

func TestAdaptiveRateLimiterMinDelay(t *testing.T) {
	// Delay below minimum should be capped
	rl := NewAdaptiveRateLimiter(10 * time.Millisecond, 0)

	start := time.Now()
	rl.Wait(context.Background(), "https://example.com/page1")
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("expected at least ~50ms (min delay), got %v", elapsed)
	}
}

func TestAdaptiveRateLimiterBackoffOn429(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)

	// Record a 429 response
	rl.RecordResponse("https://example.com/page1", 429, 50, 0, 0)

	delay := rl.CurrentDelay("https://example.com/page1")
	expected := time.Duration(float64(100*time.Millisecond) * adaptiveBackoffMult)
	if delay != expected {
		t.Errorf("expected delay %v after 429, got %v", expected, delay)
	}
}

func TestAdaptiveRateLimiterBackoffOn503(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)

	rl.RecordResponse("https://example.com/page1", 503, 200, 0, 0)

	delay := rl.CurrentDelay("https://example.com/page1")
	if delay <= 100*time.Millisecond {
		t.Errorf("expected delay to increase after 503, got %v", delay)
	}
}

func TestAdaptiveRateLimiterSpeedupOnSuccess(t *testing.T) {
	rl := NewAdaptiveRateLimiter(200 * time.Millisecond, 0)
	url := "https://example.com/page"

	// Build up EMA with moderate response times
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 100, 0, 0)
	}
	// Send faster responses to trigger speedup (below 80% of avg)
	for i := 0; i < 10; i++ {
		rl.RecordResponse(url, 200, 30, 0, 0)
	}

	delay := rl.CurrentDelay(url)
	if delay >= 200*time.Millisecond {
		t.Errorf("expected delay to decrease after consistent fast responses, got %v", delay)
	}
}

func TestAdaptiveRateLimiterPerDomain(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)

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
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)
	url := "https://example.com/page"

	// Record responses with high remaining capacity
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 50, 100, 0)
	}

	delay := rl.CurrentDelay(url)
	if delay >= 100*time.Millisecond {
		t.Errorf("expected delay to decrease with high rate limit remaining, got %v", delay)
	}
}

func TestAdaptiveRateLimiterResetCappedAtMaxDelay(t *testing.T) {
	rl := NewAdaptiveRateLimiter(100 * time.Millisecond, 0)

	// Server sends X-RateLimit-Reset: 600 (10 minutes) — should be capped at maxDelay (30s)
	rl.RecordResponse("https://example.com/page1", 429, 50, 0, 600)

	delay := rl.CurrentDelay("https://example.com/page1")
	if delay > adaptiveMaxDelay {
		t.Errorf("delay %v should not exceed maxDelay %v", delay, adaptiveMaxDelay)
	}
}

func TestAdaptiveRateLimiterNoDoubleSpeedup(t *testing.T) {
	rl := NewAdaptiveRateLimiter(200 * time.Millisecond, 0)
	url := "https://example.com/page"

	// Build up EMA
	for i := 0; i < 5; i++ {
		rl.RecordResponse(url, 200, 100, 0, 0)
	}

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
	rl := NewAdaptiveRateLimiter(5 * time.Second, 0)

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
