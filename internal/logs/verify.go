package logs

import (
	"context"
	"net"
	"sort"
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
	TotalIPs   int `json:"totalIps"`
	Verified   int `json:"verified"`
	Spoofed    int `json:"spoofed"`
	Unverified int `json:"unverified"`
	// Skipped counts IPs not checked: past MICELIO_MAX_VERIFY_IPS for the
	// whole job, or left when the verification ran out of time.
	Skipped int `json:"skipped,omitempty"`
}

// verificationDomains maps bot names to valid reverse DNS domain suffixes.
//
// Google (developers.google.com/search/docs/crawling-indexing/verifying-googlebot):
// common crawlers such as Googlebot resolve to crawl-*.googlebot.com or
// geo-crawl-*.geo.googlebot.com, special-case crawlers to
// rate-limited-proxy-*.google.com. *.googleusercontent.com is not accepted:
// Google Cloud customers get reverse names there too, so it only shows the
// address is in Google Cloud, not that the request came from Googlebot.
var verificationDomains = map[string][]string{
	"Googlebot":             {".googlebot.com", ".google.com"},
	"Googlebot-Image":       {".googlebot.com", ".google.com"},
	"Googlebot-Video":       {".googlebot.com", ".google.com"},
	"Googlebot-News":        {".googlebot.com", ".google.com"},
	"StoreBot-Google":       {".googlebot.com", ".google.com"},
	"Google-InspectionTool": {".googlebot.com", ".google.com"},
	"GoogleOther":           {".googlebot.com", ".google.com"},
	"Google-Extended":       {".googlebot.com", ".google.com"},
	"Bingbot":               {".search.msn.com"},
	"YandexBot":             {".yandex.com", ".yandex.ru", ".yandex.net"},
	"Baiduspider":           {".baidu.com", ".baidu.jp"},
	"Applebot":              {".apple.com"},
	"DuckDuckBot":           {".duckduckgo.com"},
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
	return &dnsCache{entries: make(map[string]*dnsCacheEntry), maxSize: maxSize}
}

// cacheKey: a result is only valid for the bot it was checked against (an
// IP that is a real Googlebot is a fake Bingbot).
func cacheKey(ip, bot string) string { return bot + "\x00" + ip }

func (c *dnsCache) get(key string) (*VerificationResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.result, true
}

func (c *dnsCache) set(key string, result *VerificationResult, ttl time.Duration) {
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
	c.entries[key] = &dnsCacheEntry{result: result, expiresAt: time.Now().Add(ttl)}
}

// dnsResolver is the part of *net.Resolver the verifier uses (tests fake it).
type dnsResolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// BotVerifier performs FCrDNS verification of bot IP addresses.
type BotVerifier struct {
	cache        *dnsCache
	resolver     dnsResolver
	concurrency  int           // DNS checks in flight, shared by all batches
	timeout      time.Duration // per DNS lookup
	batchTimeout time.Duration // per VerifyBotIPs call
	cacheTTL     time.Duration
	maxIPs       int // IPs checked per batch (MICELIO_MAX_VERIFY_IPS)

	sem chan struct{} // DNS checks in flight across batches

	jobsMu sync.Mutex
	jobs   map[string]*jobRun // VerifyJob results, by job key
}

// jobRun is one VerifyJob run; waiters block on done.
type jobRun struct {
	done     chan struct{}
	stats    map[string]*VerificationStats
	spoofed  []*VerificationResult
	finished time.Time
}

// NewBotVerifier creates a verifier with default settings.
func NewBotVerifier() *BotVerifier {
	const concurrency = 50
	return &BotVerifier{
		cache:        newDNSCache(50_000),
		resolver:     net.DefaultResolver,
		concurrency:  concurrency,
		timeout:      5 * time.Second,
		batchTimeout: 3 * time.Minute,
		cacheTTL:     time.Hour,
		maxIPs:       limits().MaxVerifyIPs,
		sem:          make(chan struct{}, concurrency),
		jobs:         make(map[string]*jobRun),
	}
}

// SharedBotVerifier returns the process-wide verifier. Sharing it means one
// DNS cache, one bound on DNS checks in flight and one result per job no
// matter how many verification requests arrive.
var SharedBotVerifier = sync.OnceValue(NewBotVerifier)

// VerifyBotIP checks if an IP truly belongs to the claimed bot via FCrDNS.
// 1. Reverse DNS: IP -> hostname
// 2. Check hostname suffix matches known verification domains
// 3. Forward DNS: hostname -> IPs, confirm original IP is in the list
func (v *BotVerifier) VerifyBotIP(ip, claimedBot string) *VerificationResult {
	return v.verifyIP(context.Background(), ip, claimedBot)
}

func (v *BotVerifier) verifyIP(ctx context.Context, ip, claimedBot string) *VerificationResult {
	key := cacheKey(ip, claimedBot)
	if cached, ok := v.cache.get(key); ok {
		return cached
	}
	result := &VerificationResult{IP: ip, ClaimedBot: claimedBot, Method: "ua_only"}

	domains, hasDomains := verificationDomains[claimedBot]
	if !hasDomains {
		v.cache.set(key, result, v.cacheTTL)
		return result
	}
	result.Method = "fcrdns"

	// Not an IP address (garbage in the log): no DNS needed to reject it.
	addr := net.ParseIP(ip)
	if addr == nil {
		result.SpoofDetected = true
		v.cache.set(key, result, v.cacheTTL)
		return result
	}

	lctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	// Step 1: Reverse DNS
	names, err := v.resolver.LookupAddr(lctx, ip)
	if err != nil || len(names) == 0 {
		if ctx.Err() != nil {
			return nil // batch cancelled: not a verdict, don't cache it
		}
		result.SpoofDetected = true
		v.cache.set(key, result, v.cacheTTL)
		return result
	}

	hostname := strings.TrimSuffix(names[0], ".")
	result.Hostname = hostname

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
		v.cache.set(key, result, v.cacheTTL)
		return result
	}

	// Step 3: Forward DNS confirmation
	addrs, err := v.resolver.LookupHost(lctx, hostname)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		result.SpoofDetected = true
		v.cache.set(key, result, v.cacheTTL)
		return result
	}

	for _, a := range addrs {
		if fa := net.ParseIP(a); fa != nil && fa.Equal(addr) {
			result.Verified = true
			result.SpoofDetected = false
			v.cache.set(key, result, v.cacheTTL)
			return result
		}
	}

	result.SpoofDetected = true
	v.cache.set(key, result, v.cacheTTL)
	return result
}

// VerifyJob verifies a log job's bot IPs once: a request that arrives while
// the job is being verified waits for that run, and the result is reused for
// the cache TTL. Callers must not modify the returned values.
func (v *BotVerifier) VerifyJob(jobKey string, botIPs map[string]map[string]struct{}) (map[string]*VerificationStats, []*VerificationResult) {
	v.jobsMu.Lock()
	if r, ok := v.jobs[jobKey]; ok {
		select {
		case <-r.done:
			if time.Since(r.finished) < v.cacheTTL {
				v.jobsMu.Unlock()
				return r.stats, r.spoofed
			}
		default: // running: wait for it
			v.jobsMu.Unlock()
			<-r.done
			return r.stats, r.spoofed
		}
	}
	for k, r := range v.jobs { // drop expired results
		select {
		case <-r.done:
			if time.Since(r.finished) >= v.cacheTTL {
				delete(v.jobs, k)
			}
		default:
		}
	}
	r := &jobRun{done: make(chan struct{})}
	v.jobs[jobKey] = r
	v.jobsMu.Unlock()

	defer func() {
		r.finished = time.Now()
		close(r.done)
	}()
	r.stats, r.spoofed = v.VerifyBotIPs(botIPs)
	return r.stats, r.spoofed
}

// VerifyBotIPs verifies a batch of bot IPs with a fixed pool of workers.
// Returns per-bot verification stats and individual results for spoofed IPs.
func (v *BotVerifier) VerifyBotIPs(botIPs map[string]map[string]struct{}) (map[string]*VerificationStats, []*VerificationResult) {
	return v.VerifyBotIPsContext(context.Background(), botIPs)
}

// VerifyBotIPsContext is VerifyBotIPs with a context. At most
// MICELIO_MAX_VERIFY_IPS IPs are checked, shared fairly between bots; the
// rest, and any left when ctx ends or the batch timeout passes, are counted
// as Skipped.
func (v *BotVerifier) VerifyBotIPsContext(ctx context.Context, botIPs map[string]map[string]struct{}) (map[string]*VerificationStats, []*VerificationResult) {
	type work struct {
		ip  string
		bot string
	}

	statsMap := make(map[string]*VerificationStats)
	statsFor := func(bot string) *VerificationStats {
		s, ok := statsMap[bot]
		if !ok {
			s = &VerificationStats{}
			statsMap[bot] = s
		}
		return s
	}

	// Canonical, deduplicated, sorted IPs per verifiable bot.
	var bots []string
	perBot := make(map[string][]string)
	for bot, ips := range botIPs {
		if _, ok := verificationDomains[bot]; !ok {
			continue // only verify bots we have domains for
		}
		seen := make(map[string]struct{}, len(ips))
		list := make([]string, 0, len(ips))
		for ip := range ips {
			if a := net.ParseIP(ip); a != nil {
				ip = a.String()
			}
			if _, dup := seen[ip]; dup {
				continue
			}
			seen[ip] = struct{}{}
			list = append(list, ip)
		}
		sort.Strings(list)
		perBot[bot] = list
		bots = append(bots, bot)
	}
	sort.Strings(bots)

	// Take IPs round-robin across bots until the cap.
	var tasks []work
	maxIPs := v.maxIPs
	if maxIPs <= 0 {
		maxIPs = defaultMaxVerifyIPs
	}
	for i := 0; len(tasks) < maxIPs; i++ {
		added := false
		for _, bot := range bots {
			if i < len(perBot[bot]) && len(tasks) < maxIPs {
				tasks = append(tasks, work{perBot[bot][i], bot})
				added = true
			}
		}
		if !added {
			break
		}
	}
	taken := make(map[string]int)
	for _, t := range tasks {
		taken[t.bot]++
	}
	for _, bot := range bots {
		if n := len(perBot[bot]) - taken[bot]; n > 0 {
			statsFor(bot).Skipped += n
		}
	}

	if v.batchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, v.batchTimeout)
		defer cancel()
	}

	results := make([]*VerificationResult, len(tasks))
	next := make(chan int)
	workers := min(v.concurrency, len(tasks))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range next {
				select {
				case v.sem <- struct{}{}:
				case <-ctx.Done():
					continue
				}
				results[idx] = v.verifyIP(ctx, tasks[idx].ip, tasks[idx].bot)
				<-v.sem
			}
		}()
	}
feed:
	for i := range tasks {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()

	// Aggregate per-bot stats
	var spoofed []*VerificationResult
	for i, r := range results {
		if r == nil {
			statsFor(tasks[i].bot).Skipped++
			continue
		}
		s := statsFor(r.ClaimedBot)
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
