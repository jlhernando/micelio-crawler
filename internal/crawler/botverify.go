package crawler

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Google IP range JSON endpoints (new location announced March 2026).
// Falls back to old /search/apis/ipranges/ if new location fails.
var googleIPRangeURLs = map[string][]string{
	"googlebot": {
		"https://developers.google.com/static/crawling/ipranges/googlebot.json",
		"https://developers.google.com/static/search/apis/ipranges/googlebot.json",
	},
	"google-special": {
		"https://developers.google.com/static/crawling/ipranges/special-crawlers.json",
		"https://developers.google.com/static/search/apis/ipranges/special-crawlers.json",
	},
	"google-user-triggered": {
		"https://developers.google.com/static/crawling/ipranges/user-triggered-fetchers.json",
		"https://developers.google.com/static/search/apis/ipranges/user-triggered-fetchers.json",
	},
}

// ipRangeEntry represents a single prefix entry from Google's JSON.
type ipRangeEntry struct {
	IPv4Prefix string `json:"ipv4Prefix,omitempty"`
	IPv6Prefix string `json:"ipv6Prefix,omitempty"`
}

// ipRangeFile represents Google's IP range JSON format.
type ipRangeFile struct {
	Prefixes []ipRangeEntry `json:"prefixes"`
}

// BotVerifyResult holds the result of a bot IP verification.
type BotVerifyResult struct {
	IP          string `json:"ip"`
	IsVerified  bool   `json:"isVerified"`
	BotCategory string `json:"botCategory,omitempty"` // e.g., "googlebot", "google-special", "google-user-triggered"
	MatchedCIDR string `json:"matchedCidr,omitempty"`
	Error       string `json:"error,omitempty"`
}

// botVerifier caches parsed CIDR networks.
type botVerifier struct {
	mu       sync.Mutex
	networks map[string][]cachedNet // category -> networks
	fetched  time.Time
}

type cachedNet struct {
	Network *net.IPNet
	CIDR    string
}

var defaultVerifier = &botVerifier{
	networks: make(map[string][]cachedNet),
}

const ipCacheTTL = 24 * time.Hour

// cacheDir returns the directory for cached IP range files.
func cacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".micelio", "ip-ranges")
	}
	return filepath.Join(home, ".micelio", "ip-ranges")
}

// fetchIPRanges downloads and parses Google's IP range JSON files.
func fetchIPRanges(category string, urls []string) ([]cachedNet, error) {
	dir := cacheDir()
	cacheFile := filepath.Join(dir, category+".json")

	// Try to use cached file first
	if info, err := os.Stat(cacheFile); err == nil && time.Since(info.ModTime()) < ipCacheTTL {
		data, err := os.ReadFile(cacheFile)
		if err == nil {
			if nets, err := parseIPRangeJSON(data); err == nil {
				return nets, nil
			}
		}
	}

	// Fetch from network (try URLs in order)
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for _, u := range urls {
		nets, data, err := fetchOneIPRange(client, u)
		if err != nil {
			lastErr = err
			continue
		}
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(cacheFile, data, 0o644)
		return nets, nil
	}
	return nil, fmt.Errorf("failed to fetch IP ranges for %s: %v", category, lastErr)
}

// fetchOneIPRange fetches and parses a single IP range URL. Body is always closed.
func fetchOneIPRange(client *http.Client, u string) ([]cachedNet, []byte, error) {
	resp, err := client.Get(u)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, nil, err
	}
	nets, err := parseIPRangeJSON(data)
	if err != nil {
		return nil, nil, err
	}
	return nets, data, nil
}

// parseIPRangeJSON parses the Google IP range JSON format into CIDR networks.
func parseIPRangeJSON(data []byte) ([]cachedNet, error) {
	var file ipRangeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	var nets []cachedNet
	for _, p := range file.Prefixes {
		cidr := p.IPv4Prefix
		if cidr == "" {
			cidr = p.IPv6Prefix
		}
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		nets = append(nets, cachedNet{Network: ipNet, CIDR: cidr})
	}
	return nets, nil
}

// VerifyGoogleBot checks if an IP belongs to any of Google's crawler IP ranges.
func VerifyGoogleBot(ipStr string) BotVerifyResult {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return BotVerifyResult{IP: ipStr, Error: "invalid IP address"}
	}

	defaultVerifier.mu.Lock()
	needsRefresh := time.Since(defaultVerifier.fetched) > ipCacheTTL || len(defaultVerifier.networks) == 0
	defaultVerifier.mu.Unlock()

	if needsRefresh {
		// Fetch outside the lock to avoid blocking concurrent callers
		newNets := make(map[string][]cachedNet)
		allOK := true
		for category, urls := range googleIPRangeURLs {
			nets, err := fetchIPRanges(category, urls)
			if err != nil {
				allOK = false
				continue
			}
			newNets[category] = nets
		}
		defaultVerifier.mu.Lock()
		// Double-check after acquiring lock
		if time.Since(defaultVerifier.fetched) > ipCacheTTL || len(defaultVerifier.networks) == 0 {
			for k, v := range newNets {
				defaultVerifier.networks[k] = v
			}
			if allOK {
				defaultVerifier.fetched = time.Now()
			}
		}
		defaultVerifier.mu.Unlock()
	}

	defaultVerifier.mu.Lock()
	defer defaultVerifier.mu.Unlock()

	for category, nets := range defaultVerifier.networks {
		for _, cn := range nets {
			if cn.Network.Contains(ip) {
				return BotVerifyResult{
					IP:          ipStr,
					IsVerified:  true,
					BotCategory: category,
					MatchedCIDR: cn.CIDR,
				}
			}
		}
	}

	return BotVerifyResult{IP: ipStr, IsVerified: false}
}
