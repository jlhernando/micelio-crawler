package crawler

import (
	"context"
	"net"
	"sync"
	"time"
)

const defaultDNSTTL = 5 * time.Minute

// dnsCache is an in-memory DNS cache with TTL expiry.
// Eliminates redundant DNS lookups for single-domain crawls.
type dnsCache struct {
	mu      sync.RWMutex
	entries map[string]dnsCacheEntry
	ttl     time.Duration
}

type dnsCacheEntry struct {
	ips    []net.IPAddr
	expiry time.Time
}

func newDNSCache(ttl time.Duration) *dnsCache {
	if ttl <= 0 {
		ttl = defaultDNSTTL
	}
	return &dnsCache{entries: make(map[string]dnsCacheEntry), ttl: ttl}
}

// LookupIPAddr resolves a hostname, returning cached results if still valid.
func (c *dnsCache) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	c.mu.RLock()
	if e, ok := c.entries[host]; ok && time.Now().Before(e.expiry) {
		c.mu.RUnlock()
		return e.ips, nil
	}
	c.mu.RUnlock()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[host] = dnsCacheEntry{ips: ips, expiry: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return ips, nil
}
