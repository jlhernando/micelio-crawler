package crawler

import (
	"context"
	"math"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenBucket enforces a minimum interval between requests.
// Unlike per-worker sleep, it guarantees even spacing across all concurrent workers.
// It can also be paused (e.g. after a 429 with Retry-After) so every worker
// sharing the bucket waits, not just the one that saw the error.
type tokenBucket struct {
	mu          sync.Mutex
	interval    time.Duration
	next        time.Time
	pausedUntil time.Time
}

func newTokenBucket(interval time.Duration) *tokenBucket {
	return &tokenBucket{interval: interval, next: time.Now()}
}

// Wait blocks until the next request slot is available.
// Callers are serialized: each gets a slot spaced by interval.
func (tb *tokenBucket) Wait(ctx context.Context) {
	tb.mu.Lock()
	now := time.Now()
	start := now
	if tb.next.After(start) {
		start = tb.next
	}
	if tb.pausedUntil.After(start) {
		start = tb.pausedUntil
	}
	tb.next = start.Add(tb.interval)
	tb.mu.Unlock()

	if wait := time.Until(start); wait > 0 {
		sleepCtx(ctx, wait)
	}
	// A pause may have been set while we slept on a slot reserved earlier.
	// Honor it so no request slips out during a server-requested backoff.
	for ctx.Err() == nil {
		tb.mu.Lock()
		paused := tb.pausedUntil
		tb.mu.Unlock()
		wait := time.Until(paused)
		if wait <= 0 {
			return
		}
		sleepCtx(ctx, wait)
	}
}

// SetInterval updates the gap between tokens dynamically.
func (tb *tokenBucket) SetInterval(d time.Duration) {
	tb.mu.Lock()
	tb.interval = d
	tb.mu.Unlock()
}

// Interval returns the current gap between tokens.
func (tb *tokenBucket) Interval() time.Duration {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.interval
}

// PauseFor blocks every caller of Wait for at least d from now.
// A shorter pause never shortens an existing longer one.
func (tb *tokenBucket) PauseFor(d time.Duration) {
	if d <= 0 {
		return
	}
	until := time.Now().Add(d)
	tb.mu.Lock()
	if until.After(tb.pausedUntil) {
		tb.pausedUntil = until
	}
	tb.mu.Unlock()
}

// hostRateLimiter keeps one token bucket per host so politeness (delay,
// robots.txt Crawl-delay, Retry-After pauses) is enforced per server and
// shared by all workers hitting that server.
type hostRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	floors   map[string]time.Duration // per-host floor (robots.txt Crawl-delay)
	base     time.Duration            // configured delay (global floor)
	adaptive *AdaptiveRateLimiter     // optional
}

func newHostRateLimiter(base time.Duration, adaptive *AdaptiveRateLimiter) *hostRateLimiter {
	return &hostRateLimiter{
		buckets:  make(map[string]*tokenBucket),
		floors:   make(map[string]time.Duration),
		base:     base,
		adaptive: adaptive,
	}
}

// floorLocked returns the minimum interval for host. Caller holds h.mu.
func (h *hostRateLimiter) floorLocked(host string) time.Duration {
	f := h.base
	if hf := h.floors[host]; hf > f {
		f = hf
	}
	return f
}

func (h *hostRateLimiter) bucket(host string) *tokenBucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.buckets[host]
	if !ok {
		interval := h.floorLocked(host)
		if h.adaptive != nil {
			if d := h.adaptive.currentDelayForDomain(host); d > interval {
				interval = d
			}
		}
		b = newTokenBucket(interval)
		h.buckets[host] = b
	}
	return b
}

// Wait blocks until a request to rawURL's host may be sent.
func (h *hostRateLimiter) Wait(ctx context.Context, rawURL string) {
	h.bucket(extractDomain(rawURL)).Wait(ctx)
}

// SetHostFloor raises the minimum interval for a host (robots.txt Crawl-delay).
func (h *hostRateLimiter) SetHostFloor(host string, d time.Duration) {
	host = strings.ToLower(host)
	h.mu.Lock()
	if d > h.floors[host] {
		h.floors[host] = d
	}
	floor := h.floorLocked(host)
	b := h.buckets[host]
	h.mu.Unlock()
	if h.adaptive != nil {
		h.adaptive.SetDomainMinDelay(host, floor)
	}
	if b != nil && b.Interval() < floor {
		b.SetInterval(floor)
	}
}

// SetInterval sets the bucket interval for rawURL's host, never below its floor.
func (h *hostRateLimiter) SetInterval(rawURL string, d time.Duration) {
	host := extractDomain(rawURL)
	b := h.bucket(host)
	h.mu.Lock()
	if f := h.floorLocked(host); d < f {
		d = f
	}
	h.mu.Unlock()
	b.SetInterval(d)
}

// SetBase changes the configured delay for every host (live tuning). Each
// host restarts at its new floor, never below its robots.txt Crawl-delay;
// the adaptive limiter raises it again if the server slows down.
func (h *hostRateLimiter) SetBase(d time.Duration) {
	h.mu.Lock()
	h.base = d
	type hostBucket struct {
		host  string
		b     *tokenBucket
		floor time.Duration
	}
	var all []hostBucket
	for host, b := range h.buckets {
		all = append(all, hostBucket{host, b, h.floorLocked(host)})
	}
	h.mu.Unlock()
	for _, hb := range all {
		if h.adaptive != nil {
			h.adaptive.ResetDomainFloor(hb.host, hb.floor)
		}
		hb.b.SetInterval(hb.floor)
	}
}

// Interval returns the current interval for rawURL's host.
func (h *hostRateLimiter) Interval(rawURL string) time.Duration {
	return h.bucket(extractDomain(rawURL)).Interval()
}

// Pause makes every worker wait at least d before the next request to rawURL's host.
func (h *hostRateLimiter) Pause(rawURL string, d time.Duration) {
	h.bucket(extractDomain(rawURL)).PauseFor(d)
}

// AdaptiveRateLimiter dynamically adjusts request rate based on server feedback.
// It tracks per-domain response times, error rates, and 429/503 signals.
// The delay never drops below max(adaptiveMinDelay, base delay, per-domain floor).
type AdaptiveRateLimiter struct {
	mu          sync.Mutex
	domains     map[string]*domainState
	baseRate    time.Duration // initial delay between requests (also the floor)
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
	adaptiveMinDelay    = 50 * time.Millisecond
	adaptiveMaxDelay    = 30 * time.Second
	adaptiveEMA         = 0.3 // weight for new samples in EMA
	adaptiveSlowdownRT  = 2.0 // response time multiplier that triggers slowdown
	adaptiveSpeedupRT   = 0.8 // response time multiplier that allows speedup
	adaptiveBackoffMult = 2.0 // multiply delay on error
	adaptiveSpeedupMult = 0.9 // multiply delay when conditions improve
	adaptiveErrorWindow = 30 * time.Second
)

// NewAdaptiveRateLimiter creates a rate limiter with the given base delay.
// The base delay is also the floor: adaptive speedups never go below it
// (nor below adaptiveMinDelay).
// delayFactor controls Heritrix-style delay: delay = max(floor, factor * avgResponseTime).
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
		return strings.ToLower(rawURL)
	}
	return strings.ToLower(parsed.Host)
}

// domainStateLocked returns (creating if needed) the state for domain. Caller holds rl.mu.
func (rl *AdaptiveRateLimiter) domainStateLocked(domain string) *domainState {
	ds, ok := rl.domains[domain]
	if !ok {
		ds = &domainState{
			currentDelay: rl.baseRate,
			minDelay:     rl.baseRate,
			maxDelay:     adaptiveMaxDelay,
		}
		rl.domains[domain] = ds
	}
	return ds
}

// SetDomainMinDelay raises the floor for a domain (e.g. robots.txt Crawl-delay).
func (rl *AdaptiveRateLimiter) SetDomainMinDelay(domain string, d time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	ds := rl.domainStateLocked(strings.ToLower(domain))
	if d > ds.minDelay {
		ds.minDelay = d
	}
	if ds.maxDelay < ds.minDelay {
		ds.maxDelay = ds.minDelay
	}
	if ds.currentDelay < ds.minDelay {
		ds.currentDelay = ds.minDelay
	}
}

// SetBase changes the delay new domains start from (live tuning).
func (rl *AdaptiveRateLimiter) SetBase(d time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.baseRate = max(d, adaptiveMinDelay)
}

// ResetDomainFloor sets a domain's floor, up or down, and restarts its delay
// there (live tuning).
func (rl *AdaptiveRateLimiter) ResetDomainFloor(domain string, d time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	ds := rl.domainStateLocked(strings.ToLower(domain))
	ds.minDelay = max(d, adaptiveMinDelay)
	ds.currentDelay = ds.minDelay
	if ds.maxDelay < ds.minDelay {
		ds.maxDelay = ds.minDelay
	}
}

func (rl *AdaptiveRateLimiter) currentDelayForDomain(domain string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.domainStateLocked(domain).currentDelay
}

// Wait blocks until it's safe to send the next request to the given URL's domain.
// Adds random jitter (0-50%) to avoid uniform request patterns that trigger bot detection.
func (rl *AdaptiveRateLimiter) Wait(ctx context.Context, rawURL string) {
	delay := rl.currentDelayForDomain(extractDomain(rawURL))
	if delay > 0 {
		sleepCtx(ctx, jitter(delay))
	}
}

// jitter adds random 0-50% variation to a duration to avoid uniform request patterns.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Float64()*0.5*float64(d))
}

// RecordResponse updates the rate limiter with the result of a request.
// statusCode 0 means a network error or timeout and is treated as a server
// overload signal (same as 503).
func (rl *AdaptiveRateLimiter) RecordResponse(rawURL string, statusCode int, responseTimeMs int64, rateLimitRemaining int, rateLimitResetSecs int) {
	domain := extractDomain(rawURL)

	rl.mu.Lock()
	defer rl.mu.Unlock()
	ds := rl.domainStateLocked(domain)
	defer ds.clamp()

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
		}

	case statusCode == 503 || statusCode == 504 || statusCode == 0:
		// Server overload / timeout / network error: back off with smaller factor
		ds.errorStreak++
		factor := 1.5 + 0.5*float64(ds.errorStreak)
		if factor > adaptiveBackoffMult {
			factor = adaptiveBackoffMult
		}
		ds.currentDelay = time.Duration(float64(ds.currentDelay) * factor)
		if statusCode == 503 {
			// Treat 503 like a soft 429 for the speedup window.
			ds.last429 = time.Now()
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
				return
			}
			speedup := false
			if time.Since(ds.last429) > adaptiveErrorWindow {
				if ds.avgResponseMs > 0 && rtMs < ds.avgResponseMs*adaptiveSpeedupRT {
					ds.currentDelay = time.Duration(float64(ds.currentDelay) * adaptiveSpeedupMult)
					speedup = true
				}
				if !speedup && rateLimitRemaining > 10 {
					ds.currentDelay = time.Duration(float64(ds.currentDelay) * adaptiveSpeedupMult)
				}
			}
		}
	}
}

// clamp keeps currentDelay within [minDelay, maxDelay].
func (ds *domainState) clamp() {
	if ds.currentDelay < ds.minDelay {
		ds.currentDelay = ds.minDelay
	}
	if ds.currentDelay > ds.maxDelay {
		ds.currentDelay = ds.maxDelay
	}
}

// CurrentDelay returns the current delay for a domain (for logging/monitoring).
func (rl *AdaptiveRateLimiter) CurrentDelay(rawURL string) time.Duration {
	return rl.currentDelayForDomain(extractDomain(rawURL))
}

// AvgResponseTime returns the current EMA response time for a domain.
func (rl *AdaptiveRateLimiter) AvgResponseTime(rawURL string) float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	ds := rl.domainStateLocked(extractDomain(rawURL))
	return math.Round(ds.avgResponseMs*100) / 100
}
