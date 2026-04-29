package logs

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// VerificationResult holds the result of bot identity verification via FCrDNS.
type VerificationResult struct {
	IP            string `json:"ip"`
	Hostname      string `json:"hostname,omitempty"`
	Verified      bool   `json:"verified"`
	ClaimedBot    string `json:"claimedBot"`
	SpoofDetected bool   `json:"spoofDetected"`
	Method        string `json:"method"` // "fcrdns" or "ua_only"
}

// VerificationStats aggregates verification results per bot.
type VerificationStats struct {
	TotalIPs  int `json:"totalIps"`
	Verified  int `json:"verified"`
	Spoofed   int `json:"spoofed"`
	Unverified int `json:"unverified"`
}

// verificationDomains maps bot names to valid reverse DNS domain suffixes.
var verificationDomains = map[string][]string{
	"Googlebot":              {".googlebot.com", ".google.com", ".googleusercontent.com"},
	"Googlebot-Image":        {".googlebot.com", ".google.com"},
	"Googlebot-Video":        {".googlebot.com", ".google.com"},
	"Googlebot-News":         {".googlebot.com", ".google.com"},
	"StoreBot-Google":        {".googlebot.com", ".google.com"},
	"Google-InspectionTool":  {".googlebot.com", ".google.com"},
	"GoogleOther":            {".googlebot.com", ".google.com"},
	"Google-Extended":        {".googlebot.com", ".google.com"},
	"Bingbot":                {".search.msn.com"},
	"YandexBot":              {".yandex.com", ".yandex.ru", ".yandex.net"},
	"Baiduspider":            {".baidu.com", ".baidu.jp"},
	"Applebot":               {".apple.com"},
	"DuckDuckBot":            {".duckduckgo.com"},
}

type dnsCache struct {
	mu      sync.RWMutex
	entries map[string]*dnsCacheEntry
	maxSize int
}

type dnsCacheEntry struct {
	result    *VerificationResult
	expiresAt time.Time
}

func newDNSCache(maxSize int) *dnsCache {
	return &dnsCache{entries: make(map[string]*dnsCacheEntry, maxSize), maxSize: maxSize}
}

func (c *dnsCache) get(ip string) (*VerificationResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[ip]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.result, true
}

func (c *dnsCache) set(ip string, result *VerificationResult, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Simple eviction: clear half when full
	if len(c.entries) >= c.maxSize {
		i := 0
		for k := range c.entries {
			delete(c.entries, k)
			i++
			if i >= c.maxSize/2 {
				break
			}
		}
	}
	c.entries[ip] = &dnsCacheEntry{result: result, expiresAt: time.Now().Add(ttl)}
}

// BotVerifier performs FCrDNS verification of bot IP addresses.
type BotVerifier struct {
	cache       *dnsCache
	resolver    *net.Resolver
	concurrency int
	timeout     time.Duration
	cacheTTL    time.Duration
}

// NewBotVerifier creates a verifier with default settings.
func NewBotVerifier() *BotVerifier {
	return &BotVerifier{
		cache:       newDNSCache(10000),
		resolver:    net.DefaultResolver,
		concurrency: 50,
		timeout:     5 * time.Second,
		cacheTTL:    time.Hour,
	}
}

// VerifyBotIP checks if an IP truly belongs to the claimed bot via FCrDNS.
// 1. Reverse DNS: IP -> hostname
// 2. Check hostname suffix matches known verification domains
// 3. Forward DNS: hostname -> IPs, confirm original IP is in the list
func (v *BotVerifier) VerifyBotIP(ip, claimedBot string) *VerificationResult {
	if cached, ok := v.cache.get(ip); ok {
		return cached
	}
	result := &VerificationResult{IP: ip, ClaimedBot: claimedBot, Method: "ua_only"}

	domains, hasDomains := verificationDomains[claimedBot]
	if !hasDomains {
		v.cache.set(ip, result, v.cacheTTL)
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
	defer cancel()

	// Step 1: Reverse DNS
	names, err := v.resolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		result.SpoofDetected = true
		result.Method = "fcrdns"
		v.cache.set(ip, result, v.cacheTTL)
		return result
	}

	hostname := strings.TrimSuffix(names[0], ".")
	result.Hostname = hostname
	result.Method = "fcrdns"

	// Step 2: Check hostname matches verification domains
	matched := false
	lower := strings.ToLower(hostname)
	for _, domain := range domains {
		if strings.HasSuffix(lower, domain) {
			matched = true
			break
		}
	}
	if !matched {
		result.SpoofDetected = true
		v.cache.set(ip, result, v.cacheTTL)
		return result
	}

	// Step 3: Forward DNS confirmation
	ctx2, cancel2 := context.WithTimeout(context.Background(), v.timeout)
	defer cancel2()
	addrs, err := v.resolver.LookupHost(ctx2, hostname)
	if err != nil {
		result.SpoofDetected = true
		v.cache.set(ip, result, v.cacheTTL)
		return result
	}

	for _, addr := range addrs {
		if addr == ip {
			result.Verified = true
			result.SpoofDetected = false
			v.cache.set(ip, result, v.cacheTTL)
			return result
		}
	}

	result.SpoofDetected = true
	v.cache.set(ip, result, v.cacheTTL)
	return result
}

// VerifyBotIPs verifies a batch of bot IPs concurrently.
// Returns per-bot verification stats and individual results for spoofed IPs.
func (v *BotVerifier) VerifyBotIPs(botIPs map[string]map[string]struct{}) (map[string]*VerificationStats, []*VerificationResult) {
	type work struct {
		ip  string
		bot string
	}

	var tasks []work
	for bot, ips := range botIPs {
		// Only verify bots we have domains for
		if _, ok := verificationDomains[bot]; !ok {
			continue
		}
		for ip := range ips {
			tasks = append(tasks, work{ip, bot})
		}
	}

	results := make([]*VerificationResult, len(tasks))
	sem := make(chan struct{}, v.concurrency)
	var wg sync.WaitGroup

	for i, t := range tasks {
		wg.Add(1)
		go func(idx int, w work) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = v.VerifyBotIP(w.ip, w.bot)
		}(i, t)
	}
	wg.Wait()

	// Aggregate per-bot stats
	statsMap := make(map[string]*VerificationStats)
	var spoofed []*VerificationResult
	for _, r := range results {
		if r == nil {
			continue
		}
		s, ok := statsMap[r.ClaimedBot]
		if !ok {
			s = &VerificationStats{}
			statsMap[r.ClaimedBot] = s
		}
		s.TotalIPs++
		if r.Verified {
			s.Verified++
		} else if r.SpoofDetected {
			s.Spoofed++
			spoofed = append(spoofed, r)
		} else {
			s.Unverified++
		}
	}
	return statsMap, spoofed
}
