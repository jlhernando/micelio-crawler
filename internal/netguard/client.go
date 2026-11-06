package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxRedirects caps how many redirects HTTPClient follows.
const maxRedirects = 10

// HTTPClient returns a client for outbound requests to addresses a user
// typed in (webhooks, alert channels, self-hosted integrations). It:
//   - refuses to connect to a blocked address, checked after DNS (Dialer);
//   - uses HTTP_PROXY/HTTPS_PROXY, but checks and resolves the target
//     itself and gives the proxy only the checked IP (ProxyTransport),
//     because the dialer only sees the proxy's address;
//   - checks every redirect: http(s) only, never to a blocked host.
func HTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dial := Dialer(30 * time.Second).DialContext
	transport.DialContext = dial
	return &http.Client{
		Timeout:       timeout,
		Transport:     NewProxyTransport(transport, http.ProxyFromEnvironment, dial, nil),
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
