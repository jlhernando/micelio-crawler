package crawler

import (
	"context"
	"math"
	"math/rand/v2"
	"net/url"
	"sync"
	"time"
)

// tokenBucket enforces a global minimum interval between requests.
// Unlike per-worker sleep, it guarantees even spacing across all concurrent workers.
type tokenBucket struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newTokenBucket(interval time.Duration) *tokenBucket {
	return &tokenBucket{interval: interval, next: time.Now()}
}

// Wait blocks until the next request slot is available.
// Callers are serialized: each gets a slot spaced by interval.
func (tb *tokenBucket) Wait(ctx context.Context) {
	tb.mu.Lock()
	now := time.Now()
	if now.Before(tb.next) {
		wait := time.Until(tb.next)
		tb.next = tb.next.Add(tb.interval)
		tb.mu.Unlock()
		sleepCtx(ctx, wait)
		return
	}
	tb.next = now.Add(tb.interval)
	tb.mu.Unlock()
}

// SetInterval updates the gap between tokens dynamically.
func (tb *tokenBucket) SetInterval(d time.Duration) {
	tb.mu.Lock()
	tb.interval = d
	tb.mu.Unlock()
}

// AdaptiveRateLimiter dynamically adjusts request rate based on server feedback.
// It tracks per-domain response times, error rates, and 429/503 signals.
type AdaptiveRateLimiter struct {
	mu          sync.Mutex
	domains     map[string]*domainState
	baseRate    time.Duration // initial delay between requests
	delayFactor float64       // Heritrix-style: delay = factor * avgResponseTime (0 = disabled)
}

type domainState struct {
	avgResponseMs float64 // exponential moving average
	samples       int
	errorStreak   int
	last429       time.Time
	currentDelay  time.Duration
	minDelay      time.Duration // floor (don't go below this)
	maxDelay      time.Duration // ceiling
}

const (
	adaptiveMinDelay     = 50 * time.Millisecond
	adaptiveMaxDelay     = 30 * time.Second
	adaptiveEMA          = 0.3  // weight for new samples in EMA
	adaptiveSlowdownRT   = 2.0  // response time multiplier that triggers slowdown
	adaptiveSpeedupRT    = 0.8  // response time multiplier that allows speedup
	adaptiveBackoffMult  = 2.0  // multiply delay on error
	adaptiveSpeedupMult  = 0.9  // multiply delay when conditions improve
	adaptiveErrorWindow  = 30 * time.Second
)

// NewAdaptiveRateLimiter creates a rate limiter with the given base delay.
// delayFactor controls Heritrix-style delay: delay = max(minDelay, factor * avgResponseTime).
// Set delayFactor to 0 to use the original backoff/speedup logic.
func NewAdaptiveRateLimiter(baseDelay time.Duration, delayFactor float64) *AdaptiveRateLimiter {
	if baseDelay < adaptiveMinDelay {
		baseDelay = adaptiveMinDelay
	}
	return &AdaptiveRateLimiter{
		domains:     make(map[string]*domainState),
		baseRate:    baseDelay,
		delayFactor: delayFactor,
	}
}

func extractDomain(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsed.Host
}

func (rl *AdaptiveRateLimiter) getDomainState(domain string) *domainState {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	ds, ok := rl.domains[domain]
	if !ok {
		ds = &domainState{
			currentDelay: rl.baseRate,
			minDelay:     adaptiveMinDelay,
			maxDelay:     adaptiveMaxDelay,
		}
		rl.domains[domain] = ds
	}
	return ds
}

// Wait blocks until it's safe to send the next request to the given URL's domain.
// Adds random jitter (0-50%) to avoid uniform request patterns that trigger bot detection.
func (rl *AdaptiveRateLimiter) Wait(ctx context.Context, rawURL string) {
	domain := extractDomain(rawURL)
	rl.mu.Lock()
	ds, ok := rl.domains[domain]
	if !ok {
		ds = &domainState{
			currentDelay: rl.baseRate,
			minDelay:     adaptiveMinDelay,
			maxDelay:     adaptiveMaxDelay,
		}
		rl.domains[domain] = ds
	}
	delay := ds.currentDelay
	rl.mu.Unlock()

	if delay > 0 {
		sleepCtx(ctx, jitter(delay))
	}
}

// jitter adds random 0-50% variation to a duration to avoid uniform request patterns.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Float64()*0.5*float64(d))
}

// RecordResponse updates the rate limiter with the result of a request.
func (rl *AdaptiveRateLimiter) RecordResponse(rawURL string, statusCode int, responseTimeMs int64, rateLimitRemaining int, rateLimitResetSecs int) {
	domain := extractDomain(rawURL)
	ds := rl.getDomainState(domain)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rtMs := float64(responseTimeMs)

	switch {
	case statusCode == 429:
		// Rate limited: back off significantly
		ds.errorStreak++
		ds.last429 = time.Now()
		ds.currentDelay = time.Duration(float64(ds.currentDelay) * adaptiveBackoffMult)
		if ds.currentDelay > ds.maxDelay {
			ds.currentDelay = ds.maxDelay
		}
		// If rate limit headers provide reset info, use that (capped at maxDelay)
		if rateLimitResetSecs > 0 {
			resetDelay := time.Duration(rateLimitResetSecs) * time.Second
			if resetDelay > ds.currentDelay {
				ds.currentDelay = resetDelay
			}
			if ds.currentDelay > ds.maxDelay {
				ds.currentDelay = ds.maxDelay
			}
		}

	case statusCode == 503 || statusCode == 504:
		// Server overload: back off with smaller factor
		ds.errorStreak++
		factor := 1.5 + 0.5*float64(ds.errorStreak)
		if factor > adaptiveBackoffMult {
			factor = adaptiveBackoffMult
		}
		ds.currentDelay = time.Duration(float64(ds.currentDelay) * factor)
		if ds.currentDelay > ds.maxDelay {
			ds.currentDelay = ds.maxDelay
		}

	case statusCode >= 200 && statusCode < 400:
		// Success: update EMA and potentially speed up
		ds.errorStreak = 0

		if ds.samples == 0 {
			ds.avgResponseMs = rtMs
		} else {
			ds.avgResponseMs = adaptiveEMA*rtMs + (1-adaptiveEMA)*ds.avgResponseMs
		}
		ds.samples++

		// Only adjust after enough samples
		if ds.samples < 3 {
			return
		}

		if rl.delayFactor > 0 {
			// Heritrix-style: delay = factor * avgResponseTime
			ds.currentDelay = time.Duration(rl.delayFactor * ds.avgResponseMs * float64(time.Millisecond))
		} else {
			// Original logic: response time based slowdown/speedup
			if ds.avgResponseMs > 0 && rtMs > ds.avgResponseMs*adaptiveSlowdownRT {
				ds.currentDelay = time.Duration(float64(ds.currentDelay) * 1.3)
				if ds.currentDelay > ds.maxDelay {
					ds.currentDelay = ds.maxDelay
				}
				return
			}
			speedup := false
			if time.Since(ds.last429) > adaptiveErrorWindow {
				if ds.avgResponseMs > 0 && rtMs < ds.avgResponseMs*adaptiveSpeedupRT {
					ds.currentDelay = time.Duration(float64(ds.currentDelay) * adaptiveSpeedupMult)
					speedup = true
				}
			}
			if !speedup && rateLimitRemaining > 10 {
				ds.currentDelay = time.Duration(float64(ds.currentDelay) * adaptiveSpeedupMult)
			}
		}
		if ds.currentDelay < ds.minDelay {
			ds.currentDelay = ds.minDelay
		}
		if ds.currentDelay > ds.maxDelay {
			ds.currentDelay = ds.maxDelay
		}
	}
}

// CurrentDelay returns the current delay for a domain (for logging/monitoring).
func (rl *AdaptiveRateLimiter) CurrentDelay(rawURL string) time.Duration {
	domain := extractDomain(rawURL)
	ds := rl.getDomainState(domain)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return ds.currentDelay
}

// AvgResponseTime returns the current EMA response time for a domain.
func (rl *AdaptiveRateLimiter) AvgResponseTime(rawURL string) float64 {
	domain := extractDomain(rawURL)
	ds := rl.getDomainState(domain)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return math.Round(ds.avgResponseMs*100) / 100
}
