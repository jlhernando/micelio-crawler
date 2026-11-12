package crawler

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
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

	"github.com/micelio/micelio/internal/netguard"
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
	maxBodySize         = types.HTMLBodyReadCap // 10 MB body size cap
	// maxDrainBytes caps how much of an unused body (redirects, non-HTML,
	// errors) is read before closing. Past this, the connection is dropped
	// instead of downloading e.g. a multi-GB PDF just to reuse the socket.
	maxDrainBytes = 64 * 1024
	// MaxServerRetryAfter caps how long a Retry-After header can pause a host.
	MaxServerRetryAfter = 10 * time.Minute
)

// drainAndClose reads at most maxDrainBytes of body and closes it.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	io.CopyN(io.Discard, body, maxDrainBytes)
	body.Close()
}

// ssrfBlockedError is returned by the SSRF-safe dialer when a hostname
// resolves to a private address. Such requests must never be retried.
type ssrfBlockedError struct{ msg string }

func (e *ssrfBlockedError) Error() string { return e.msg }

// isRetryableFetchErr reports whether a transport error is worth retrying:
// not caused by context cancellation and not an SSRF block.
func isRetryableFetchErr(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	return !isBlockedErr(err)
}

// isBlockedErr reports whether err is a refused connection to a private
// address. Some HTTP stacks (stealth) keep only the message, so the
// messages are matched too.
func isBlockedErr(err error) bool {
	var sb *ssrfBlockedError
	var nb *netguard.BlockedError
	return errors.As(err, &sb) || errors.As(err, &nb) ||
		strings.Contains(err.Error(), "DNS rebinding blocked") ||
		strings.Contains(err.Error(), "blocked connection to private address")
}

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
	// NoRetry disables in-function retries for network errors and 429s.
	// The crawler sets it so retries are re-queued through the shared per-host
	// rate limiter (and so the limiter sees every 429/503).
	NoRetry bool
	// Pace, if set, is called before every request FetchPage/FetchHead send
	// (redirect hops and retries included). It blocks until rawURL's host may
	// be hit and returns a func that is called once the request is on the
	// wire. The crawler passes its per-host rate limiter.
	Pace func(ctx context.Context, rawURL string) (sent func())
	// FollowRedirect, if set, decides whether FetchPage follows a redirect to
	// target. When it returns false FetchPage stops and returns the 3xx with
	// FinalURL set to target. The crawler uses it to keep off-site pages (an
	// app store, a partner site) out of the crawl.
	FollowRedirect func(target string) bool
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
			ips, err = lookupHost(ctx, host)
		}
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				msg := fmt.Sprintf("DNS rebinding blocked: %s resolved to private IP %s", host, ip.IP)
				if hint := netguard.FakeIPHint(ip.IP.String()); hint != "" {
					msg += ": " + hint
				}
				return nil, &ssrfBlockedError{msg: msg}
			}
		}
		// Connect to the first resolved IP
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// proxyDial connects to a proxy the local user configured (--proxy, Tor,
// HTTP(S)_PROXY, the dashboard's --trusted-proxy). It accepts loopback and
// private addresses; targets reached through the proxy are still checked
// (netguard.ProxyTransport).
var proxyDial netguard.DialFunc = netguard.ProxyDialer(30 * time.Second).DialContext

// crawlTLSConfig, when set, is the TLS config of crawl clients (tests only:
// trusts a test server's certificate).
var crawlTLSConfig *tls.Config

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
	var rt http.RoundTripper = transport
	if ssrfProtect {
		dial := ssrfSafeDialer(&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}, cache)
		transport.DialContext = dial
		// A proxy (--proxy, Tor, or HTTP(S)_PROXY) would resolve the target
		// itself, past the dialer's check: give it the checked IP instead.
		// The proxy itself is the local user's choice (the dashboard only
		// accepts its --trusted-proxy), so it is reached even on loopback
		// or a private address: Tor listens on 127.0.0.1:9050.
		lookup := netguard.LookupFunc(lookupHost)
		if cache != nil {
			lookup = cache.LookupIPAddr
		}
		rt = netguard.NewProxyTransport(transport, transport.Proxy, proxyDial, lookup)
	}
	if crawlTLSConfig != nil {
		transport.TLSClientConfig = crawlTLSConfig.Clone()
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &http.Client{
		Jar:     jar,
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
		Transport: rt,
	}
}

// SafeClientFollowRedirects returns an SSRF-protected client that follows redirects (max 10).
// Use for webhook dispatch or other cases where redirect following is needed.
// Environment proxies are used, but a proxy resolves and reaches the target
// itself, past the dialer's private-address check, so the target is checked
// and resolved here and the proxy only gets the checked IP
// (netguard.ProxyTransport). Every redirect hop is checked too
// (netguard.CheckRedirect).
func SafeClientFollowRedirects(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dial := ssrfSafeDialer(&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}, nil)
	transport.DialContext = dial
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: netguard.CheckRedirect,
		Transport:     netguard.NewProxyTransport(transport, http.ProxyFromEnvironment, proxyDial, nil),
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

	clock := newFetchClock()
	redirectChain := make([]types.RedirectHop, 0)
	currentURL := rawURL
	retries := 0
	// Cookies and custom headers go to rawURL's host only, not to another
	// site a redirect leads to.
	credScope, _ := url.Parse(rawURL)
	// An image URL (a linked .jpg) only needs its status and headers: ask
	// with HEAD, then a one-byte Range GET if HEAD is refused. Never the
	// whole file.
	probe := probeNone
	if types.HasImageExt(rawURL) {
		probe = probeHead
	}

	for {
		method := http.MethodGet
		if probe == probeHead {
			method = http.MethodHead
		}
		req, err := newRequest(ctx, method, currentURL)
		if err != nil {
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: clock.ms(),
				Error:          fmt.Sprintf("request creation failed: %v", err),
			}
		}

		setHeadersFor(req, opts, credentialsAllowed(credScope, req.URL))
		if probe == probeRange {
			req.Header.Set("Range", "bytes=0-0")
		}

		resp, err := clock.do(client, req, opts.Pace)
		if err != nil {
			retryable := isRetryableFetchErr(ctx, err)
			if retryable && !opts.NoRetry && retries < maxRetries {
				delay := backoffDelay(retries)
				retries++
				clock.sleep(ctx, delay)
				continue
			}
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: clock.ms(),
				Error:          FormatError(err),
				Retryable:      retryable,
			}
		}

		// HEAD refused: ask for the first byte instead. Range refused: plain
		// GET (the non-HTML branch below reads at most maxDrainBytes).
		if (probe == probeHead && (resp.StatusCode == 405 || resp.StatusCode == 501)) ||
			(probe == probeRange && resp.StatusCode == 416) {
			drainAndClose(resp.Body)
			probe++
			continue
		}
		if probe == probeRange && resp.StatusCode == 206 {
			// One byte of a 200 resource: report it as the 200 it is.
			resp.StatusCode = http.StatusOK
			if total := rangeTotal(resp.Header.Get("Content-Range")); total > 0 {
				resp.Header.Set("Content-Length", strconv.FormatInt(total, 10))
			}
		}

		// Handle redirects manually
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			drainAndClose(resp.Body)

			if location != "" {
				if len(redirectChain) >= maxRedirects {
					return types.FetchResult{
						URL:            rawURL,
						FinalURL:       currentURL,
						StatusCode:     resp.StatusCode,
						RedirectChain:  redirectChain,
						ResponseTimeMs: clock.ms(),
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
						ResponseTimeMs: clock.ms(),
						Error:          fmt.Sprintf("Redirect to private/loopback address blocked: %s", resolved),
					}
				}
				if opts.FollowRedirect != nil && !opts.FollowRedirect(resolved) {
					return types.FetchResult{
						URL:            rawURL,
						FinalURL:       resolved,
						StatusCode:     resp.StatusCode,
						Headers:        flattenHeaders(resp.Header),
						RedirectChain:  redirectChain,
						ResponseTimeMs: clock.ms(),
					}
				}

				currentURL = resolved
				continue
			}
		}

		// Handle 304 Not Modified (conditional request)
		if resp.StatusCode == 304 {
			drainAndClose(resp.Body)
			return types.FetchResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				StatusCode:     304,
				RedirectChain:  redirectChain,
				ResponseTimeMs: clock.ms(),
				NotModified:    true,
			}
		}

		// Handle 429 Too Many Requests
		if resp.StatusCode == 429 && !opts.NoRetry && retries < maxRetries {
			drainAndClose(resp.Body)
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), backoffDelay(retries))
			retries++
			clock.sleep(ctx, delay)
			continue
		}

		// Read body
		contentType := resp.Header.Get("Content-Type")
		isHTML := strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")

		if isHTML && probe != probeNone {
			// An image-looking URL that serves a page: fetch it as a page.
			drainAndClose(resp.Body)
			probe = probeNone
			continue
		}

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
					ResponseTimeMs: clock.ms(),
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
			// Non-HTML (PDF, images, archives...): only the status and headers
			// are needed. Read a small capped amount and close.
			drainAndClose(resp.Body)
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
			ResponseTimeMs: clock.ms(),
			TransferSize:   transferSize,
			BodySize:       int64(len(html)), // uncompressed HTML body size
		}
	}
}

// Image-URL probe steps in FetchPage, in fallback order.
const (
	probeHead  = iota + 1 // HEAD
	probeRange            // GET with Range: bytes=0-0
	probeNone             // plain GET
)

// rangeTotal returns the full size from a Content-Range header
// ("bytes 0-0/12345"), or 0 when unknown.
func rangeTotal(cr string) int64 {
	i := strings.LastIndexByte(cr, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
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

	clock := newFetchClock()
	redirectChain := make([]types.RedirectHop, 0)
	currentURL := rawURL
	retries := 0
	credScope, _ := url.Parse(rawURL)

	for {
		req, err := newRequest(ctx, http.MethodHead, currentURL)
		if err != nil {
			return types.HeadResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: clock.ms(),
				Error:          FormatError(err),
			}
		}

		setHeadersFor(req, opts, credentialsAllowed(credScope, req.URL))

		resp, err := clock.do(client, req, opts.Pace)
		if err != nil {
			if isRetryableFetchErr(ctx, err) && !opts.NoRetry && retries < maxRetries {
				retries++
				clock.sleep(ctx, backoffDelay(retries-1))
				continue
			}
			return types.HeadResult{
				URL:            rawURL,
				FinalURL:       currentURL,
				RedirectChain:  redirectChain,
				ResponseTimeMs: clock.ms(),
				Error:          FormatError(err),
			}
		}
		drainAndClose(resp.Body)

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
						ResponseTimeMs: clock.ms(),
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
						ResponseTimeMs: clock.ms(),
						Error:          fmt.Sprintf("Redirect to private address blocked: %s", resolved),
					}
				}
				currentURL = resolved
				continue
			}
		}

		// 429 retry
		if resp.StatusCode == 429 && !opts.NoRetry && retries < maxRetries {
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), backoffDelay(retries))
			retries++
			clock.sleep(ctx, delay)
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
			ResponseTimeMs: clock.ms(),
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

	req, err := newRequest(ctx, http.MethodHead, rawURL)
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
		drainAndClose(resp.Body)

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			if location == "" {
				return resp.StatusCode, nil
			}
			resolved := resolveURL(rawURL, location)
			if !skipSSRF && IsPrivateURL(resolved) {
				return 0, fmt.Errorf("redirect to private address blocked: %s", resolved)
			}
			req, err = newRequest(ctx, http.MethodHead, resolved)
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
	setHeadersFor(req, opts, true)
}

// credentialsAllowed reports whether the crawl's cookies and custom headers
// (Authorization, X-Api-Key...), meant for the site at from, may be sent to
// to: the same host or its www/apex twin (www.example.com <-> example.com, a
// common first redirect), on the same port, and never after a downgrade
// from https to http. Other subdomains are not trusted: on shared hosting
// or SaaS platforms (tenant.example.com) they belong to someone else.
// Requests that start on another in-scope host (an allowed domain) use that
// host as from, so they still get the credentials.
func credentialsAllowed(from, to *url.URL) bool {
	if from == nil || to == nil {
		return false
	}
	if strings.EqualFold(from.Scheme, "https") && !strings.EqualFold(to.Scheme, "https") {
		return false
	}
	fh, th := strings.ToLower(from.Hostname()), strings.ToLower(to.Hostname())
	if fh == "" {
		return false
	}
	if effectivePort(from) != effectivePort(to) && !(from.Port() == "" && to.Port() == "") {
		return false
	}
	return th == fh || strings.TrimPrefix(th, "www.") == strings.TrimPrefix(fh, "www.")
}

// effectivePort is u's port, or the scheme's default (an http -> https
// upgrade moves 80 -> 443 without a real port change).
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// setHeadersFor sets the request headers. With creds false the custom
// headers and cookies are left out: a redirect or a reference led to another
// site, which must not receive the crawl's credentials.
func setHeadersFor(req *http.Request, opts FetchOptions, creds bool) {
	if !creds {
		opts.CustomHeaders, opts.Cookies = nil, ""
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// When UA is Chrome-like, send Client Hints and Fetch Metadata headers
	// that real Chrome sends on every navigation request.
	if isChromeUA(opts.UserAgent) {
		ver := extractChromeVersion(opts.UserAgent)
		req.Header.Set("Sec-Ch-Ua", chromeSecChUa(ver))
		req.Header.Set("Sec-Ch-Ua-Mobile", secChUaMobile(opts.UserAgent))
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

// secChUaMobile is "?1" for a mobile Chrome User-Agent (Android phones put
// "Mobile" in it, tablets do not), else "?0".
func secChUaMobile(ua string) string {
	if strings.Contains(ua, " Mobile") {
		return "?1"
	}
	return "?0"
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
	case strings.Contains(ua, "Macintosh"):
		return "macOS"
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

// RetryAfterDuration parses a Retry-After header (delta-seconds or HTTP-date).
// Returns false when the header is missing or invalid. The result is capped at
// maxWait and is never negative.
func RetryAfterDuration(header string, maxWait time.Duration) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		if secs > int64(maxWait/time.Second) {
			return maxWait, true
		}
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(header); err == nil {
		d = time.Until(t)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	if d > maxWait {
		d = maxWait
	}
	return d, true
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

// fetchClock times a fetch without the time it spends waiting: for its turn
// in the per-host rate limiter and in retry backoff. Counting that wait made
// a busy crawl look like a slow site, and the adaptive limiter then slowed
// the crawl down further.
type fetchClock struct {
	start  time.Time
	waited time.Duration
}

func newFetchClock() *fetchClock { return &fetchClock{start: time.Now()} }

func (c *fetchClock) do(client *http.Client, req *http.Request, pace func(context.Context, string) func()) (*http.Response, error) {
	resp, wait, err := pacedDoWait(client, req, pace)
	c.waited += wait
	return resp, err
}

func (c *fetchClock) sleep(ctx context.Context, d time.Duration) {
	t := time.Now()
	sleepCtx(ctx, d)
	c.waited += time.Since(t)
}

// ms is the time spent fetching so far, in milliseconds.
func (c *fetchClock) ms() int64 {
	return max(time.Since(c.start)-c.waited, 0).Milliseconds()
}
