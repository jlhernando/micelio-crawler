package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxRedirects caps how many redirects HTTPClient follows.
const maxRedirects = 10

// HTTPClient returns a client for outbound requests to addresses a user
// typed in (webhooks, alert channels, self-hosted integrations). It:
//   - refuses to connect to a blocked address, checked after DNS (Dialer);
//   - uses HTTP_PROXY/HTTPS_PROXY, but checks the target before handing a
//     request to the proxy (GuardProxy), because the dialer only sees the
//     proxy's address;
//   - checks every redirect: http(s) only, never to a blocked host.
func HTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = GuardProxy(http.ProxyFromEnvironment)
	transport.DialContext = Dialer(30 * time.Second).DialContext
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: CheckRedirect,
	}
}

// CheckRedirect is an http.Client CheckRedirect that refuses more than 10
// redirects, non-http(s) targets and targets on a blocked host name or
// address. Names that resolve to a blocked address are refused at dial time.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if s := req.URL.Scheme; s != "http" && s != "https" {
		return fmt.Errorf("redirect to unsupported scheme %q", s)
	}
	if BlockedHost(req.URL.Hostname()) {
		return &BlockedError{Addr: req.URL.Hostname()}
	}
	return nil
}

// BlockedHost reports whether host is localhost or a literal blocked
// address. It does not resolve names: dial-time checks cover those.
func BlockedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && BlockedIP(ip)
}

// lookupIPAddr resolves names for GuardProxy (a variable so tests can stub it).
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// GuardProxy wraps a Transport.Proxy func. When it picks a proxy for a
// request, the proxy resolves and connects to the target itself, so the
// dial-time check never sees the target. The target is checked here
// instead: refused if it is a blocked host, or if its name resolves here to
// any blocked address (or does not resolve). Direct requests (no proxy)
// pass unchanged and are checked by the dialer.
func GuardProxy(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if err != nil || u == nil {
			return u, err
		}
		host := req.URL.Hostname()
		if BlockedHost(host) {
			return nil, &BlockedError{Addr: host}
		}
		if net.ParseIP(host) != nil {
			return u, nil
		}
		ips, err := lookupIPAddr(req.Context(), host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s before using proxy: %w", host, err)
		}
		for _, ip := range ips {
			if BlockedIP(ip.IP) {
				return nil, &BlockedError{Addr: ip.IP.String()}
			}
		}
		return u, nil
	}
}
