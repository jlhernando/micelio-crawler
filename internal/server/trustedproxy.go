package server

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// proxyNotTrustedHint says how to use a proxy with dashboard crawls.
const proxyNotTrustedHint = "dashboard crawls can only use the proxy the local user set when starting the dashboard: " +
	"micelio ui --trusted-proxy <url> (or MICELIO_TRUSTED_PROXY, also read from ~/.micelio/micelio.env); " +
	"send that URL or leave the proxy empty"

// proxyNotTrustedError reports a proxy that is not the trusted one. The
// proxy URL is not echoed back (it may hold a password).
type proxyNotTrustedError struct{ configured bool }

func (e *proxyNotTrustedError) Error() string {
	if !e.configured {
		return "proxy refused: no trusted proxy is configured; " + proxyNotTrustedHint
	}
	return "proxy refused: it is not the trusted proxy; " + proxyNotTrustedHint
}

// ValidateTrustedProxy checks a --trusted-proxy value: "" (none), "tor", or
// an http, https, socks5 or socks5h URL with a host.
func ValidateTrustedProxy(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "tor") {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("--trusted-proxy: %q is not a proxy URL (http://host:port, socks5://host:port or tor)", redactURL(s))
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return nil
	}
	return fmt.Errorf("--trusted-proxy: scheme %q is not supported (use http, https, socks5 or tor)", u.Scheme)
}

// normalizeProxyURL puts a proxy URL in one form for comparison: lower-case
// scheme and host, the scheme's default port written out, no trailing
// slash. "tor" stays "tor". Strings that do not parse are only trimmed.
func normalizeProxyURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "tor") {
		return "tor"
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Scheme = strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	u.Host = net.JoinHostPort(strings.ToLower(u.Hostname()), port)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

// trustsProxy reports whether a proxy from a crawl config is the trusted
// proxy: the same URL after normalisation, or its masked form (a config
// read back from the API has the password masked).
func (m *CrawlManager) trustsProxy(p string) bool {
	trusted := strings.TrimSpace(m.TrustedProxy)
	if trusted == "" || strings.TrimSpace(p) == "" {
		return false
	}
	np := normalizeProxyURL(p)
	if np == normalizeProxyURL(trusted) {
		return true
	}
	if !strings.Contains(p, secretMask) {
		return false
	}
	masked := redactURL(trusted)
	if masked != trusted && (strings.TrimSpace(p) == masked || np == normalizeProxyURL(masked)) {
		return true
	}
	// A user name kept visible with only the password masked: put the
	// stored password back (same scheme, host and user only) and compare.
	restored, ok := restoreURL(strings.TrimSpace(p), trusted)
	return ok && normalizeProxyURL(restored) == normalizeProxyURL(trusted)
}

// applyTrustedProxy checks the "proxy" of a crawl config that came over
// HTTP. A proxy resolves names and connects to targets itself, and the
// crawler reaches it even on loopback or a private address, so API clients
// must not choose one: only the trusted proxy (micelio ui --trusted-proxy)
// is accepted, and is written back as the real URL (its masked form
// included). An empty proxy is removed. Any other proxy is an error.
func (m *CrawlManager) applyTrustedProxy(cfg map[string]any) error {
	raw, ok := cfg["proxy"]
	if !ok || raw == nil {
		delete(cfg, "proxy")
		return nil
	}
	s, isStr := raw.(string)
	if !isStr {
		return fmt.Errorf("proxy must be a string")
	}
	if strings.TrimSpace(s) == "" {
		delete(cfg, "proxy")
		return nil
	}
	if !m.trustsProxy(s) {
		return &proxyNotTrustedError{configured: strings.TrimSpace(m.TrustedProxy) != ""}
	}
	cfg["proxy"] = strings.TrimSpace(m.TrustedProxy)
	return nil
}

// checkRunProxy is the last check before a dashboard crawl runs (History
// resumes, saved schedules, configs stored before the trusted-proxy rule):
// a proxy that is not the trusted one fails the crawl.
func (m *CrawlManager) checkRunProxy(p string) error {
	if strings.TrimSpace(p) == "" || m.trustsProxy(p) {
		return nil
	}
	return &proxyNotTrustedError{configured: strings.TrimSpace(m.TrustedProxy) != ""}
}
