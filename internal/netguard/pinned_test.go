package netguard

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// publicIP is what the stubbed DNS answers for test names on the first
// lookup. Later lookups answer 127.0.0.1 (DNS rebinding).
const publicIP = "93.184.216.34"

// rebindingDNS answers publicIP once per name, then a loopback address.
type rebindingDNS struct {
	mu    sync.Mutex
	calls map[string]int
}

func (d *rebindingDNS) lookup(_ context.Context, host string) ([]net.IPAddr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.calls == nil {
		d.calls = map[string]int{}
	}
	switch host {
	case "example.com", "public.example":
	case "internal.example":
		return []net.IPAddr{{IP: net.ParseIP(publicIP)}, {IP: net.ParseIP("10.0.0.5")}}, nil
	default:
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	d.calls[host]++
	if d.calls[host] > 1 {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP(publicIP)}}, nil
}

func (d *rebindingDNS) count(host string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[host]
}

// plainDial reaches the loopback test proxies (the real proxy dialer
// refuses private addresses).
var plainDial DialFunc = (&net.Dialer{Timeout: 5 * time.Second}).DialContext

// fakeProxy is an HTTP proxy that records what it is asked for (request
// line, Host and Proxy-Authorization headers, SOCKS address) and connects
// every request to origin, whatever the address asked for.
type fakeProxy struct {
	ln       net.Listener
	origin   string // host:port really dialled
	refuseIP bool   // answer 403 to CONNECT ip:port
	socks    bool   // speak SOCKS5 instead of HTTP

	mu    sync.Mutex
	lines []string // HTTP request lines, or "SOCKS atyp=N addr" entries
	hosts []string // Host headers
	auths []string // Proxy-Authorization headers or socks5 user:pass
	conns atomic.Int32
}

func startFakeProxy(t *testing.T, origin string, opts ...func(*fakeProxy)) *fakeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProxy{ln: ln, origin: origin}
	for _, o := range opts {
		o(p)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.conns.Add(1)
			go p.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return p
}

func (p *fakeProxy) url(user string) *url.URL {
	scheme := "http"
	if p.socks {
		scheme = "socks5"
	}
	u := &url.URL{Scheme: scheme, Host: p.ln.Addr().String()}
	if user != "" {
		name, pw, _ := strings.Cut(user, ":")
		u.User = url.UserPassword(name, pw)
	}
	return u
}

func (p *fakeProxy) record(line, host, auth string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = append(p.lines, line)
	p.hosts = append(p.hosts, host)
	p.auths = append(p.auths, auth)
}

func (p *fakeProxy) seen() (lines, hosts, auths []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.lines...), append([]string(nil), p.hosts...), append([]string(nil), p.auths...)
}

func (p *fakeProxy) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	if p.socks {
		p.serveSOCKS(c, br)
		return
	}
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimRight(line, "\r\n")
	hdr, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		return
	}
	p.record(line, hdr.Get("Host"), hdr.Get("Proxy-Authorization"))
	parts := strings.Fields(line)
	if len(parts) != 3 {
		return
	}
	if parts[0] == "CONNECT" {
		host, _, _ := net.SplitHostPort(parts[1])
		if p.refuseIP && net.ParseIP(host) != nil {
			io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
			return
		}
		up, err := net.Dial("tcp", p.origin)
		if err != nil {
			return
		}
		defer up.Close()
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		pipe(c, br, up)
		return
	}
	u, err := url.Parse(parts[1])
	if err != nil {
		return
	}
	up, err := net.Dial("tcp", p.origin)
	if err != nil {
		return
	}
	defer up.Close()
	fmt.Fprintf(up, "%s %s %s\r\n", parts[0], u.RequestURI(), parts[2])
	for k, vs := range hdr {
		if strings.HasPrefix(k, "Proxy-") {
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(up, "%s: %s\r\n", k, v)
		}
	}
	io.WriteString(up, "Connection: close\r\n\r\n")
	pipe(c, br, up)
}

// serveSOCKS speaks just enough SOCKS5 (RFC 1928/1929) for the tests.
func (p *fakeProxy) serveSOCKS(c net.Conn, br *bufio.Reader) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	io.ReadFull(br, methods)
	auth := ""
	if strings.Contains(string(methods), "\x02") {
		c.Write([]byte{5, 2})
		ver := make([]byte, 2)
		io.ReadFull(br, ver)
		user := make([]byte, ver[1])
		io.ReadFull(br, user)
		pl, _ := br.ReadByte()
		pw := make([]byte, pl)
		io.ReadFull(br, pw)
		auth = string(user) + ":" + string(pw)
		c.Write([]byte{1, 0})
	} else {
		c.Write([]byte{5, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 3:
		n, _ := br.ReadByte()
		b := make([]byte, n)
		io.ReadFull(br, b)
		host = string(b)
	}
	pb := make([]byte, 2)
	io.ReadFull(br, pb)
	addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
	p.record(fmt.Sprintf("SOCKS atyp=%d %s", req[3], addr), "", auth)
	up, err := net.Dial("tcp", p.origin)
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	pipe(c, br, up)
}

func socksProxy(p *fakeProxy) { p.socks = true }
func refuseIPs(p *fakeProxy)  { p.refuseIP = true }

func pipe(c net.Conn, br *bufio.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, br); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

// origin records the Host header and TLS server name of each request.
type origin struct {
	*httptest.Server
	mu    sync.Mutex
	hosts []string
	snis  []string
}

func startOrigin(t *testing.T, tlsOn bool) *origin {
	t.Helper()
	o := &origin{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.hosts = append(o.hosts, r.Host)
		if r.TLS != nil {
			o.snis = append(o.snis, r.TLS.ServerName)
		}
		o.mu.Unlock()
		io.WriteString(w, "ok")
	})
	if tlsOn {
		o.Server = httptest.NewTLSServer(h)
	} else {
		o.Server = httptest.NewServer(h)
	}
	t.Cleanup(o.Close)
	return o
}

func (o *origin) seen() (hosts, snis []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.hosts...), append([]string(nil), o.snis...)
}

// pinnedClient is a client like HTTPClient's, with a fixed proxy, stubbed
// DNS and, for TLS origins, the test certificate trusted.
func pinnedClient(o *origin, proxy *url.URL, dns *rebindingDNS) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = Dialer(5 * time.Second).DialContext
	if o != nil && o.TLS != nil {
		base.TLSClientConfig = o.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	return &http.Client{Timeout: 5 * time.Second,
		Transport: NewProxyTransport(base, http.ProxyURL(proxy), plainDial, dns.lookup)}
}

func get(t *testing.T, c *http.Client, raw string) error {
	t.Helper()
	resp, err := c.Get(raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		return fmt.Errorf("status %d body %q", resp.StatusCode, body)
	}
	if resp.Request.URL.String() != raw {
		return fmt.Errorf("response request URL %s, want the original", resp.Request.URL)
	}
	return nil
}

func basic(user string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user))
}

// Plain HTTP through an HTTP proxy: the proxy is asked for a tunnel to the
// checked IP (an absolute-form "GET http://ip/" would make the proxy send
// the IP as Host), the request inside keeps the original Host, the proxy
// credentials are sent, and DNS answering differently after the check
// cannot move the request.
func TestProxyTransportHTTPSendsCheckedIP(t *testing.T) {
	o := startOrigin(t, false)
	p := startFakeProxy(t, o.Listener.Addr().String())
	dns := &rebindingDNS{}
	c := pinnedClient(nil, p.url("alice:s3cret"), dns)

	if err := get(t, c, "http://public.example/page?q=1"); err != nil {
		t.Fatal(err)
	}
	lines, hosts, auths := p.seen()
	if len(lines) != 1 || lines[0] != "CONNECT "+publicIP+":80 HTTP/1.1" {
		t.Fatalf("proxy request lines = %q, want the checked IP", lines)
	}
	if hosts[0] != publicIP+":80" {
		t.Errorf("CONNECT Host = %q, want the checked IP", hosts[0])
	}
	if auths[0] != basic("alice:s3cret") {
		t.Errorf("Proxy-Authorization = %q", auths[0])
	}
	if oh, _ := o.seen(); len(oh) != 1 || oh[0] != "public.example" {
		t.Errorf("origin Host = %q", oh)
	}
	if n := dns.count("public.example"); n != 1 {
		t.Errorf("resolved %d times, want once (the IP that passed the check is the one sent)", n)
	}
	// The name now resolves to loopback: a new connection is refused
	// before the proxy sees it.
	c.CloseIdleConnections()
	var be *BlockedError
	if err := get(t, c, "http://public.example/again"); !errors.As(err, &be) {
		t.Fatalf("rebound name: err = %v, want BlockedError", err)
	}
	if lines, _, _ := p.seen(); len(lines) != 1 {
		t.Fatalf("rebound request reached the proxy: %q", lines)
	}
}

// HTTPS through an HTTP proxy: CONNECT goes to the checked IP, TLS inside
// the tunnel uses the original name (SNI and certificate check), the
// origin sees the original Host, and the proxy credentials are sent.
func TestProxyTransportHTTPSTunnelsToCheckedIP(t *testing.T) {
	o := startOrigin(t, true)
	p := startFakeProxy(t, o.Listener.Addr().String())
	dns := &rebindingDNS{}
	c := pinnedClient(o, p.url("alice:s3cret"), dns)

	if err := get(t, c, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	lines, hosts, auths := p.seen()
	if len(lines) != 1 || lines[0] != "CONNECT "+publicIP+":443 HTTP/1.1" {
		t.Fatalf("proxy request lines = %q, want CONNECT to the checked IP", lines)
	}
	if hosts[0] != publicIP+":443" {
		t.Errorf("CONNECT Host = %q", hosts[0])
	}
	if auths[0] != basic("alice:s3cret") {
		t.Errorf("Proxy-Authorization = %q", auths[0])
	}
	oh, snis := o.seen()
	if len(oh) != 1 || oh[0] != "example.com" || snis[0] != "example.com" {
		t.Errorf("origin Host = %q, SNI = %q, want example.com", oh, snis)
	}
	if n := dns.count("example.com"); n != 1 {
		t.Errorf("resolved %d times, want once", n)
	}
}

// SOCKS5: the proxy gets an IPv4 address (address type 1), never a name,
// and the user/password are sent.
func TestProxyTransportSOCKS5SendsCheckedIP(t *testing.T) {
	o := startOrigin(t, true)
	p := startFakeProxy(t, o.Listener.Addr().String(), socksProxy)
	dns := &rebindingDNS{}
	c := pinnedClient(o, p.url("bob:pw"), dns)

	if err := get(t, c, "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	lines, _, auths := p.seen()
	if len(lines) != 1 || lines[0] != "SOCKS atyp=1 "+publicIP+":443" {
		t.Fatalf("socks requests = %q, want the checked IPv4 address", lines)
	}
	if auths[0] != "bob:pw" {
		t.Errorf("socks5 credentials = %q", auths[0])
	}
	if oh, snis := o.seen(); len(oh) != 1 || oh[0] != "example.com" || snis[0] != "example.com" {
		t.Errorf("origin Host = %q, SNI = %q", oh, snis)
	}

	// Plain HTTP goes through the socks tunnel too, with the IP.
	oh := startOrigin(t, false)
	p2 := startFakeProxy(t, oh.Listener.Addr().String(), socksProxy)
	c2 := pinnedClient(nil, p2.url(""), dns)
	if err := get(t, c2, "http://public.example/"); err != nil {
		t.Fatal(err)
	}
	if lines, _, _ := p2.seen(); len(lines) != 1 || lines[0] != "SOCKS atyp=1 "+publicIP+":80" {
		t.Fatalf("socks requests = %q", lines)
	}
	if hosts, _ := oh.seen(); len(hosts) != 1 || hosts[0] != "public.example" {
		t.Errorf("origin Host = %q", hosts)
	}
}

// Blocked targets (literals, localhost, names with any private address,
// names that do not resolve) never reach the proxy, over http or https.
func TestProxyTransportRefusesBlockedTargets(t *testing.T) {
	p := startFakeProxy(t, "127.0.0.1:9")
	sp := startFakeProxy(t, "127.0.0.1:9", socksProxy)
	dns := &rebindingDNS{}
	for _, proxy := range []*url.URL{p.url(""), sp.url("")} {
		c := pinnedClient(nil, proxy, dns)
		for _, raw := range []string{
			"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data", "http://localhost/",
			"http://[::1]/", "http://internal.example/", "http://missing.example/",
			"https://127.0.0.1/", "https://internal.example/", "https://x.localhost/",
		} {
			if err := get(t, c, raw); err == nil {
				t.Errorf("%s via %s: request allowed", raw, proxy.Scheme)
			}
		}
	}
	if n := p.conns.Load() + sp.conns.Load(); n != 0 {
		t.Fatalf("proxy reached %d times for blocked targets", n)
	}
}

// A proxy that will not tunnel to an IP address fails the request with a
// clear error; nothing falls back to sending the name.
func TestProxyTransportFailsClosedWhenProxyRefusesIP(t *testing.T) {
	o := startOrigin(t, true)
	p := startFakeProxy(t, o.Listener.Addr().String(), refuseIPs)
	c := pinnedClient(o, p.url(""), &rebindingDNS{})
	err := get(t, c, "https://example.com/")
	var re *ProxyRefusedError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "IP addresses") {
		t.Fatalf("err = %v, want ProxyRefusedError", err)
	}
	if lines, _, _ := p.seen(); len(lines) != 1 || strings.Contains(lines[0], "example.com") {
		t.Fatalf("proxy saw %q", lines)
	}
}

// The Chrome guard proxy chained to an upstream proxy hands upstream the
// checked IP for CONNECT and for plain HTTP.
func TestStartProxyViaSendsUpstreamCheckedIP(t *testing.T) {
	dns := &rebindingDNS{}
	old := lookupIPAddr
	lookupIPAddr = dns.lookup
	defer func() { lookupIPAddr = old }()

	o := startOrigin(t, true)
	up := startFakeProxy(t, o.Listener.Addr().String())
	g, err := StartProxyVia(up.url("carol:pw"), plainDial)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	gu, _ := url.Parse("http://" + g.Addr())
	tr := &http.Transport{Proxy: http.ProxyURL(gu),
		TLSClientConfig: o.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	if err := get(t, c, "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	lines, _, auths := up.seen()
	if len(lines) != 1 || lines[0] != "CONNECT "+publicIP+":443 HTTP/1.1" || auths[0] != basic("carol:pw") {
		t.Fatalf("upstream saw %q auth %q", lines, auths)
	}
	if oh, snis := o.seen(); oh[0] != "example.com" || snis[0] != "example.com" {
		t.Errorf("origin Host = %q, SNI = %q", oh, snis)
	}

	ho := startOrigin(t, false)
	up2 := startFakeProxy(t, ho.Listener.Addr().String())
	g2, err := StartProxyVia(up2.url(""), plainDial)
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()
	gu2, _ := url.Parse("http://" + g2.Addr())
	c2 := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(gu2)}}
	if err := get(t, c2, "http://public.example/p"); err != nil {
		t.Fatal(err)
	}
	if lines, hosts, _ := up2.seen(); len(lines) != 1 || lines[0] != "CONNECT "+publicIP+":80 HTTP/1.1" || hosts[0] != publicIP+":80" {
		t.Fatalf("upstream saw %q hosts %q", lines, hosts)
	}
	if hosts, _ := ho.seen(); len(hosts) != 1 || hosts[0] != "public.example" {
		t.Errorf("origin Host = %q", hosts)
	}

	// Rebound names are refused by the guard; upstream never sees them.
	tr.CloseIdleConnections()
	if err := get(t, c, "https://example.com/again"); err == nil {
		t.Fatal("rebound name allowed")
	}
	if lines, _, _ := up.seen(); len(lines) != 1 {
		t.Fatalf("upstream reached for a rebound name: %q", lines)
	}
}
