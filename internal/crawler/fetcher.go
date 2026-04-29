package crawler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/types"
	"golang.org/x/net/publicsuffix"
)

// bodyBufPool reuses buffers for reading HTTP response bodies.
// Avoids repeated allocation of 20-100KB buffers per page fetch.
// Buffers that grow beyond maxPoolBufSize are discarded to prevent retention of
// large (up to 10MB) buffers from oversized pages.
var bodyBufPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 64*1024))
	},
}

const maxPoolBufSize = 256 * 1024 // don't return oversized buffers to pool

const (
	maxRetries          = 3
	maxRedirects        = 10
	backoffBaseMs       = 1000
	externalTimeout     = 10 * time.Second
	maxRetryAfterMs     = 60000
	defaultFetchTimeout = 30 * time.Second
	maxBodySize         = 10 * 1024 * 1024 // 10 MB body size cap
)

// FetchOptions configures HTTP fetch behavior.
type FetchOptions struct {
	CustomHeaders   map[string]string
	Client          *http.Client
	UserAgent       string
	Cookies         string
	Referrer        string // Page that linked to this URL (sent as Referer header)
	IfNoneMatch     string // ETag for conditional request (sends If-None-Match header)
	IfModifiedSince string // Last-Modified for conditional request (sends If-Modified-Since header)
	Timeout         time.Duration
	SkipSSRF        bool
	Stealth         bool // Use Chrome TLS/header fingerprint
}

// ssrfSafeDialer returns a net.Dialer wrapped with DNS rebinding protection.
// It checks that resolved IP addresses are not private/loopback after DNS resolution.
// If cache is non-nil, DNS results are cached to avoid redundant lookups.
func ssrfSafeDialer(base *net.Dialer, cache *dnsCache) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var ips []net.IPAddr
		if cache != nil {
			ips, err = cache.LookupIPAddr(ctx, host)
		} else {
			ips, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		}
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
				return nil, fmt.Errorf("DNS rebinding blocked: %s resolved to private IP %s", host, ip.IP)
			}
		}
		// Connect to the first resolved IP
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// DefaultClient returns an HTTP client configured for crawling.
// It does NOT follow redirects (we track them manually).
// Includes DNS rebinding protection to prevent SSRF via DNS.
func DefaultClient(timeout time.Duration, proxy *url.URL) *http.Client {
	return newClient(timeout, proxy, true, nil)
}

// UnsafeClient returns an HTTP client without SSRF DNS protection (for testing).
func UnsafeClient(timeout time.Duration, proxy *url.URL) *http.Client {
	return newClient(timeout, proxy, false, nil)
}

func newClient(timeout time.Duration, proxy *url.URL, ssrfProtect bool, cache *dnsCache) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.MaxConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	transport.ForceAttemptHTTP2 = true
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	if ssrfProtect {
		transport.DialContext = ssrfSafeDialer(&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}, cache)
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &http.Client{
		Jar:     jar,
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
		Transport: transport,
	}
}

// SafeClientFollowRedirects returns an SSRF-protected client that follows redirects (max 10).
// Use for webhook dispatch or other cases where redirect following is needed.
func SafeClientFollowRedirects(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = ssrfSafeDialer(&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}, nil)
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
		Transport: transport,
	}
}

// FetchPage fetches a URL, following redirects manually to track the chain.
// Supports retry with exponential backoff and 429/Retry-After handling.
func FetchPage(ctx context.Context, rawURL string, opts FetchOptions) types.FetchResult {
	if !opts.SkipSSRF && IsPrivateURL(rawURL) {
		return types.FetchResult{
			URL:   rawURL,
			Error: "Blocked: private/loopback address",
		}
	}

	client := opts.Client
	if client == nil {
		timeout := opts.Timeout
		if timeout == 0 {
			timeout = defaultFetchTimeout
		}
		if opts.Stealth {
			var err error
			client, err = StealthClient(timeout, nil)
			if err != nil {
				return types.FetchResult{URL: rawURL, Error: fmt.Sprintf("stealth client: %v", err)}
			}
		} else if opts.SkipSSRF {
			client = UnsafeClient(timeout, nil)
		} else {
			client = DefaultClient(timeout, nil)
		}
	}

	start := time.Now()
	redirectChain := make([]types.RedirectHop, 0)
	currentURL := rawURL
	retries := 0

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: time.Since(start).Milliseconds(),
				Error:          fmt.Sprintf("request creation failed: %v", err),
			}
		}

		setHeaders(req, opts)

		resp, err := client.Do(req)
		if err != nil {
			if retries < maxRetries {
				delay := backoffDelay(retries)
				retries++
				sleepCtx(ctx, delay)
				continue
			}
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: time.Since(start).Milliseconds(),
				Error:          FormatError(err),
			}
		}

		// Handle redirects manually
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if location != "" {
				if len(redirectChain) >= maxRedirects {
					return types.FetchResult{
						URL:            rawURL,
						FinalURL:       currentURL,
						StatusCode:     resp.StatusCode,
						RedirectChain:  redirectChain,
						ResponseTimeMs: time.Since(start).Milliseconds(),
						Error:          fmt.Sprintf("Max redirects (%d) exceeded", maxRedirects),
					}
				}

				redirectChain = append(redirectChain, types.RedirectHop{
					URL:        currentURL,
					StatusCode: resp.StatusCode,
				})

				resolved := resolveURL(currentURL, location)
				if !opts.SkipSSRF && IsPrivateURL(resolved) {
					return types.FetchResult{
						URL:            rawURL,
						FinalURL:       currentURL,
						StatusCode:     resp.StatusCode,
						RedirectChain:  redirectChain,
						ResponseTimeMs: time.Since(start).Milliseconds(),
						Error:          fmt.Sprintf("Redirect to private/loopback address blocked: %s", resolved),
					}
				}

				currentURL = resolved
				continue
			}
		}

		// Handle 304 Not Modified (conditional request)
		if resp.StatusCode == 304 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				StatusCode:     304,
				RedirectChain:  redirectChain,
				ResponseTimeMs: time.Since(start).Milliseconds(),
				NotModified:    true,
			}
		}

		// Handle 429 Too Many Requests
		if resp.StatusCode == 429 && retries < maxRetries {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), backoffDelay(retries))
			retries++
			sleepCtx(ctx, delay)
			continue
		}

		// Read body
		contentType := resp.Header.Get("Content-Type")
		isHTML := strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")

		var html string
		var transferSize int64
		if isHTML {
			buf := bodyBufPool.Get().(*bytes.Buffer)
			buf.Reset()
			_, readErr := buf.ReadFrom(io.LimitReader(resp.Body, maxBodySize))
			resp.Body.Close()
			if readErr != nil {
				if buf.Cap() <= maxPoolBufSize {
					bodyBufPool.Put(buf)
				}
				return types.FetchResult{
					URL:            rawURL,
					FinalURL:       currentURL,
					StatusCode:     resp.StatusCode,
					RedirectChain:  redirectChain,
					ResponseTimeMs: time.Since(start).Milliseconds(),
					Error:          fmt.Sprintf("body read failed: %v", readErr),
				}
			}
			html = buf.String()
			bodyLen := buf.Len()
			if buf.Cap() <= maxPoolBufSize {
				bodyBufPool.Put(buf)
			}
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
					transferSize = n
				}
			}
			if transferSize == 0 {
				transferSize = int64(bodyLen)
			}
		} else {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
					transferSize = n
				}
			}
		}

		// Flatten headers
		headers := flattenHeaders(resp.Header)

		return types.FetchResult{
			URL:            rawURL,
			FinalURL:       currentURL,
			StatusCode:     resp.StatusCode,
			RedirectChain:  redirectChain,
			Headers:        headers,
			HTML:           html,
			ContentType:    contentType,
			ResponseTimeMs: time.Since(start).Milliseconds(),
			TransferSize:   transferSize,
			BodySize:       int64(len(html)), // uncompressed HTML body size
		}
	}
}

// FetchHead performs a HEAD request, tracking redirects and extracting SEO headers.
func FetchHead(ctx context.Context, rawURL string, opts FetchOptions) types.HeadResult {
	if !opts.SkipSSRF && IsPrivateURL(rawURL) {
		return types.HeadResult{
			URL:      rawURL,
			FinalURL: rawURL,
			Error:    "Blocked: private/loopback address",
		}
	}

	client := opts.Client
	if client == nil {
		if opts.SkipSSRF {
			client = UnsafeClient(externalTimeout, nil)
		} else {
			client = DefaultClient(externalTimeout, nil)
		}
	}

	start := time.Now()
	redirectChain := make([]types.RedirectHop, 0)
	currentURL := rawURL
	retries := 0

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, currentURL, nil)
		if err != nil {
			return types.HeadResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: time.Since(start).Milliseconds(),
				Error:          FormatError(err),
			}
		}

		setHeaders(req, opts)

		resp, err := client.Do(req)
		if err != nil {
			if retries < maxRetries {
				retries++
				sleepCtx(ctx, backoffDelay(retries-1))
				continue
			}
			return types.HeadResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: time.Since(start).Milliseconds(),
				Error:          FormatError(err),
			}
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Manual redirect tracking
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			if location != "" {
				if len(redirectChain) >= maxRedirects {
					return types.HeadResult{
						URL:            rawURL,
						FinalURL:       currentURL,
						StatusCode:     resp.StatusCode,
						RedirectChain:  redirectChain,
						ResponseTimeMs: time.Since(start).Milliseconds(),
						Error:          fmt.Sprintf("Max redirects (%d) exceeded", maxRedirects),
					}
				}
				redirectChain = append(redirectChain, types.RedirectHop{URL: currentURL, StatusCode: resp.StatusCode})
				resolved := resolveURL(currentURL, location)
				if !opts.SkipSSRF && IsPrivateURL(resolved) {
					return types.HeadResult{
						URL:            rawURL,
						FinalURL:       currentURL,
						StatusCode:     resp.StatusCode,
						RedirectChain:  redirectChain,
						ResponseTimeMs: time.Since(start).Milliseconds(),
						Error:          fmt.Sprintf("Redirect to private address blocked: %s", resolved),
					}
				}
				currentURL = resolved
				continue
			}
		}

		// 429 retry
		if resp.StatusCode == 429 && retries < maxRetries {
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), backoffDelay(retries))
			retries++
			sleepCtx(ctx, delay)
			continue
		}

		headers := flattenHeaders(resp.Header)
		contentType := headers["content-type"]

		var contentLength *int64
		if cl := headers["content-length"]; cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				contentLength = &n
			}
		}

		var xRobotsTag *string
		if v := headers["x-robots-tag"]; v != "" {
			xRobotsTag = &v
		}

		// Parse Link header for canonical
		var linkCanonical *string
		if linkHeader := headers["link"]; linkHeader != "" {
			if idx := strings.Index(linkHeader, `rel="canonical"`); idx != -1 {
				// Extract URL from <url>; rel="canonical"
				start := strings.LastIndex(linkHeader[:idx], "<")
				end := strings.LastIndex(linkHeader[:idx], ">")
				if start != -1 && end > start {
					val := linkHeader[start+1 : end]
					linkCanonical = &val
				}
			}
		}

		var xFrameOptions, referrerPolicy, cacheControl *string
		if v := headers["x-frame-options"]; v != "" {
			xFrameOptions = &v
		}
		if v := headers["referrer-policy"]; v != "" {
			referrerPolicy = &v
		}
		if v := headers["cache-control"]; v != "" {
			cacheControl = &v
		}

		return types.HeadResult{
			URL:            rawURL,
			FinalURL:       currentURL,
			StatusCode:     resp.StatusCode,
			RedirectChain:  redirectChain,
			ResponseTimeMs: time.Since(start).Milliseconds(),
			ContentType:    contentType,
			ContentLength:  contentLength,
			Server:         headers["server"],
			XRobotsTag:     xRobotsTag,
			LinkCanonical:  linkCanonical,
			HSTS:           headers["strict-transport-security"] != "",
			CSP:            headers["content-security-policy"] != "",
			XFrameOptions:  xFrameOptions,
			ReferrerPolicy: referrerPolicy,
			CacheControl:   cacheControl,
			Headers:        headers,
		}
	}
}

// CheckExternalURL performs a HEAD check on an external URL.
// Uses a no-redirect client to prevent SSRF via redirect to private IPs.
func CheckExternalURL(ctx context.Context, rawURL string, userAgent string, skipSSRF bool) (int, error) {
	if !skipSSRF && IsPrivateURL(rawURL) {
		return 0, fmt.Errorf("blocked: private/loopback address")
	}

	var client *http.Client
	if skipSSRF {
		client = UnsafeClient(externalTimeout, nil)
	} else {
		client = DefaultClient(externalTimeout, nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)

	// Follow redirects manually with SSRF checks
	for redirects := 0; redirects < 5; redirects++ {
		resp, doErr := client.Do(req)
		if doErr != nil {
			return 0, doErr
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			if location == "" {
				return resp.StatusCode, nil
			}
			resolved := resolveURL(rawURL, location)
			if !skipSSRF && IsPrivateURL(resolved) {
				return 0, fmt.Errorf("redirect to private address blocked: %s", resolved)
			}
			req, err = http.NewRequestWithContext(ctx, http.MethodHead, resolved, nil)
			if err != nil {
				return 0, err
			}
			req.Header.Set("User-Agent", userAgent)
			rawURL = resolved
			continue
		}

		return resp.StatusCode, nil
	}

	return 0, fmt.Errorf("max redirects exceeded")
}

// --- Helpers ---

// blockedHeaders are headers that custom headers must not override.
// Accept-Encoding is blocked because Go's http.Transport auto-handles gzip
// only when the header is NOT manually set; overriding it breaks decompression.
var blockedHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"accept-encoding":   true,
}

// isChromeUA returns true if the User-Agent string looks like Chrome.
func isChromeUA(ua string) bool {
	return strings.Contains(ua, "Chrome/") && !strings.Contains(ua, "compatible;")
}

func setHeaders(req *http.Request, opts FetchOptions) {
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// When UA is Chrome-like, send Client Hints and Fetch Metadata headers
	// that real Chrome sends on every navigation request.
	if isChromeUA(opts.UserAgent) {
		ver := extractChromeVersion(opts.UserAgent)
		req.Header.Set("Sec-Ch-Ua", chromeSecChUa(ver))
		req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
		req.Header.Set("Sec-Ch-Ua-Platform", fmt.Sprintf(`"%s"`, platformFromUA(opts.UserAgent)))
		req.Header.Set("Upgrade-Insecure-Requests", "1")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-User", "?1")
		req.Header.Set("Sec-Fetch-Site", secFetchSite(opts.Referrer, req.URL))
	}
	if opts.Referrer != "" {
		req.Header.Set("Referer", opts.Referrer)
	}
	for k, v := range opts.CustomHeaders {
		if blockedHeaders[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
	if opts.Cookies != "" {
		req.Header.Set("Cookie", opts.Cookies)
	}
	if opts.IfNoneMatch != "" {
		req.Header.Set("If-None-Match", opts.IfNoneMatch)
	}
	if opts.IfModifiedSince != "" {
		req.Header.Set("If-Modified-Since", opts.IfModifiedSince)
	}
	if opts.Stealth {
		setStealthHeaders(req)
	}
}

// extractChromeVersion parses the major version from a Chrome UA string.
func extractChromeVersion(ua string) string {
	idx := strings.Index(ua, "Chrome/")
	if idx < 0 {
		return defaultChromeVersion
	}
	rest := ua[idx+7:]
	if dot := strings.IndexByte(rest, '.'); dot > 0 {
		return rest[:dot]
	}
	return defaultChromeVersion
}

const defaultChromeVersion = "136" // keep in sync with latest stable

// chromeSecChUa builds the Sec-Ch-Ua header, rotating the Not-A-Brand token
// the way real Chrome does across major versions.
func chromeSecChUa(ver string) string {
	v, _ := strconv.Atoi(ver)
	// Chrome rotates the brand string per major version (simplified rotation).
	brands := []string{`"Not/A)Brand"`, `"Not_A Brand"`, `"Not?A_Brand"`}
	brand := brands[v%len(brands)]
	return fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", %s;v="99"`, ver, ver, brand)
}

// platformFromUA infers the OS platform from the User-Agent string.
func platformFromUA(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Linux") && !strings.Contains(ua, "Android"):
		return "Linux"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "CrOS"):
		return "Chrome OS"
	default:
		// Default to runtime OS for macOS or unknown UA patterns.
		if runtime.GOOS == "windows" {
			return "Windows"
		}
		if runtime.GOOS == "linux" {
			return "Linux"
		}
		return "macOS"
	}
}

// secFetchSite computes the Sec-Fetch-Site value by comparing referrer and request origins.
func secFetchSite(referrer string, reqURL *url.URL) string {
	if referrer == "" {
		return "none"
	}
	refURL, err := url.Parse(referrer)
	if err != nil {
		return "cross-site"
	}
	if refURL.Scheme == reqURL.Scheme && refURL.Host == reqURL.Host {
		return "same-origin"
	}
	// same-site: same registrable domain (e.g. www.example.com vs api.example.com)
	refDomain, _ := publicsuffix.EffectiveTLDPlusOne(refURL.Hostname())
	reqDomain, _ := publicsuffix.EffectiveTLDPlusOne(reqURL.Hostname())
	if refDomain != "" && refDomain == reqDomain {
		return "same-site"
	}
	return "cross-site"
}

func flattenHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k, v := range h {
		result[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return result
}

func resolveURL(base, ref string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return baseURL.ResolveReference(refURL).String()
}

func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	// Try as seconds
	if secs, err := strconv.Atoi(header); err == nil {
		ms := secs * 1000
		if ms > maxRetryAfterMs {
			ms = maxRetryAfterMs
		}
		return time.Duration(ms) * time.Millisecond
	}
	// Try as HTTP-date
	if t, err := http.ParseTime(header); err == nil {
		delay := time.Until(t)
		if delay < time.Second {
			delay = time.Second
		}
		if delay > time.Duration(maxRetryAfterMs)*time.Millisecond {
			delay = time.Duration(maxRetryAfterMs) * time.Millisecond
		}
		return delay
	}
	return fallback
}

func backoffDelay(retry int) time.Duration {
	ms := float64(backoffBaseMs) * math.Pow(2, float64(retry))
	return time.Duration(ms) * time.Millisecond
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
