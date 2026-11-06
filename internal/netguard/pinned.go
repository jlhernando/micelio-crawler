package netguard

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// LookupFunc resolves a host name to its addresses.
type LookupFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// lookupIPAddr is the default LookupFunc (a variable so tests can stub it).
var lookupIPAddr LookupFunc = net.DefaultResolver.LookupIPAddr

// CheckedIP resolves host (or parses it if it is an address) and returns
// the address to connect to. It refuses blocked hosts and names with any
// blocked address, so the answer can be sent to a proxy as is: the proxy
// never resolves the name again, and DNS that changes answers after the
// check cannot move the connection.
func CheckedIP(ctx context.Context, host string, lookup LookupFunc) (net.IP, error) {
	if BlockedHost(host) {
		return nil, &BlockedError{Addr: host}
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip, nil
	}
	if lookup == nil {
		lookup = lookupIPAddr
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s before using proxy: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s before using proxy: no addresses", host)
	}
	for _, ip := range ips {
		if BlockedIP(ip.IP) {
			return nil, &BlockedError{Addr: ip.IP.String()}
		}
	}
	return ips[0].IP, nil
}

// ProxyRefusedError is returned when a proxy does not open a connection to
// the checked address (some proxies only accept host names; they cannot be
// used with SSRF protection on).
type ProxyRefusedError struct {
	Target string // the IP:port asked for
	Reason string
}

func (e *ProxyRefusedError) Error() string {
	return fmt.Sprintf("proxy refused connection to %s (%s); with SSRF protection the proxy is sent IP addresses, never host names, and must accept them", e.Target, e.Reason)
}

// DialThrough returns a DialFunc that opens a connection to addr
// (host:port) through proxyURL. The host is checked and resolved here
// (CheckedIP) and the proxy only gets the checked IP: CONNECT ip:port for
// http/https proxies, an IP address for socks5. dialProxy connects to the
// proxy itself (nil: Dialer(30 s), which refuses private addresses).
// Credentials in proxyURL are sent (Proxy-Authorization, socks5 user/pass).
// TLS to the target, if any, is the caller's job, with the original name.
func DialThrough(proxyURL *url.URL, dialProxy DialFunc, lookup LookupFunc) (DialFunc, error) {
	if proxyURL == nil || proxyURL.Host == "" {
		return nil, errors.New("proxy URL has no host")
	}
	if dialProxy == nil {
		dialProxy = Dialer(30 * time.Second).DialContext
	}
	scheme := strings.ToLower(proxyURL.Scheme)
	proxyAddr := proxyHostPort(proxyURL)
	var socks xproxy.ContextDialer
	switch scheme {
	case "http", "https":
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if u := proxyURL.User; u != nil {
			pw, _ := u.Password()
			auth = &xproxy.Auth{User: u.Username(), Password: pw}
		}
		d, err := xproxy.SOCKS5("tcp", proxyAddr, auth, dialFuncDialer(dialProxy))
		if err != nil {
			return nil, err
		}
		socks = d.(xproxy.ContextDialer)
	default:
		return nil, fmt.Errorf("proxy scheme %q is not supported with SSRF protection (use http, https or socks5)", proxyURL.Scheme)
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := CheckedIP(ctx, host, lookup)
		if err != nil {
			return nil, err
		}
		target := net.JoinHostPort(ip.String(), port)
		if socks != nil {
			c, err := socks.DialContext(ctx, "tcp", target)
			if err != nil {
				return nil, &ProxyRefusedError{Target: target, Reason: err.Error()}
			}
			return c, nil
		}
		return connectTunnel(ctx, proxyURL, scheme, proxyAddr, dialProxy, target)
	}, nil
}

// connectTunnel asks an http(s) proxy for a CONNECT tunnel to target.
func connectTunnel(ctx context.Context, proxyURL *url.URL, scheme, proxyAddr string, dialProxy DialFunc, target string) (net.Conn, error) {
	conn, err := dialProxy(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy TLS: %w", err)
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if auth := proxyAuth(proxyURL); auth != "" {
		req += "Proxy-Authorization: " + auth + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %w", target, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, &ProxyRefusedError{Target: target, Reason: resp.Status}
	}
	conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func proxyAuth(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pw))
}

func proxyHostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	switch strings.ToLower(u.Scheme) {
	case "https":
		port = "443"
	case "socks5", "socks5h":
		port = "1080"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type dialFuncDialer DialFunc

func (d dialFuncDialer) Dial(network, addr string) (net.Conn, error) {
	return d(context.Background(), network, addr)
}

func (d dialFuncDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d(ctx, network, addr)
}

// ProxyTransport is an http.RoundTripper for SSRF-protected clients that may
// use a proxy. A proxy resolves names itself, out of reach of the dialer's
// private-address check, so the target is checked and resolved here and
// the proxy only gets the checked IP: every proxied request, http and
// https, goes through a tunnel to ip:port (DialThrough: CONNECT for
// http(s) proxies, an IP address for socks5). Inside the tunnel the request
// keeps the original Host header and TLS uses the original name (SNI and
// certificate check unchanged).
//
// Plain http is tunnelled too, not sent to the proxy as
// "GET http://ip/ HTTP/1.1": a proxy must replace the Host header with the
// request-target's host (RFC 9112 3.2.2), so the site would get its IP as
// Host. A proxy that only allows CONNECT to port 443 therefore fails http
// requests (ProxyRefusedError), and never falls back to the name.
//
// Requests with no proxy go straight out on the base transport, whose
// dialer must refuse private addresses.
type ProxyTransport struct {
	base      *http.Transport // template; Proxy is nil
	proxy     func(*http.Request) (*url.URL, error)
	dialProxy DialFunc
	lookup    LookupFunc

	mu      sync.Mutex
	tunnels map[string]*http.Transport // by proxy URL
}

// NewProxyTransport wraps base (its Proxy is ignored and cleared). proxy
// picks the proxy per request (nil URL = direct). dialProxy connects to the
// proxy (nil: Dialer(30 s)); lookup resolves targets (nil: system DNS).
func NewProxyTransport(base *http.Transport, proxy func(*http.Request) (*url.URL, error), dialProxy DialFunc, lookup LookupFunc) *ProxyTransport {
	base.Proxy = nil
	if dialProxy == nil {
		dialProxy = Dialer(30 * time.Second).DialContext
	}
	return &ProxyTransport{base: base, proxy: proxy, dialProxy: dialProxy, lookup: lookup,
		tunnels: map[string]*http.Transport{}}
}

// Base is the direct transport and the template for proxied ones. Change
// its settings (pool sizes) before the first request only.
func (t *ProxyTransport) Base() *http.Transport { return t.base }

// CloseIdleConnections closes idle connections of every inner transport.
func (t *ProxyTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, tr := range t.tunnels {
		tr.CloseIdleConnections()
	}
}

// RoundTrip implements http.RoundTripper.
func (t *ProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var p *url.URL
	if t.proxy != nil {
		var err error
		if p, err = t.proxy(req); err != nil {
			return nil, err
		}
	}
	if p == nil {
		return t.base.RoundTrip(req)
	}
	if host := req.URL.Hostname(); BlockedHost(host) {
		return nil, &BlockedError{Addr: host}
	}
	tr, err := t.tunnel(p)
	if err != nil {
		return nil, err
	}
	return tr.RoundTrip(req)
}

func (t *ProxyTransport) tunnel(p *url.URL) (*http.Transport, error) {
	key := p.String()
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr, ok := t.tunnels[key]; ok {
		return tr, nil
	}
	dial, err := DialThrough(p, t.dialProxy, t.lookup)
	if err != nil {
		return nil, err
	}
	tr := t.base.Clone()
	tr.DialContext = dial // the target address is checked and pinned here
	tr.DialTLSContext = nil
	tr.DialTLS = nil //nolint:staticcheck // cleared so DialContext is always used
	t.tunnels[key] = tr
	return tr, nil
}
