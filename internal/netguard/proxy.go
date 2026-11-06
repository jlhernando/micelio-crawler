package netguard

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// DialFunc dials a network address.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Proxy is a local HTTP proxy that connects only through a guarded dialer.
// Headless Chrome resolves host names and opens connections itself (page
// loads, redirects, WebSockets, service workers), so its own requests
// cannot be checked from outside. Sent through this proxy, every one of
// them is resolved and checked here, at the moment of connecting.
type Proxy struct {
	ln   net.Listener
	srv  *http.Server
	dial DialFunc
	tr   interface {
		http.RoundTripper
		CloseIdleConnections()
	}
	once sync.Once
}

// StartProxy starts a proxy on a free loopback port. dial defaults to
// Dialer(30 s), which refuses private addresses.
func StartProxy(dial DialFunc) (*Proxy, error) {
	if dial == nil {
		dial = Dialer(30 * time.Second).DialContext
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{ln: ln, dial: dial, tr: proxyBaseTransport(dial)}
	p.serveOn()
	return p, nil
}

func proxyBaseTransport(dial DialFunc) *http.Transport {
	return &http.Transport{
		Proxy:                 nil, // never chain to an environment proxy
		DialContext:           dial,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
}

// StartProxyVia starts a guarded proxy that chains to upstream (http,
// https or socks5; credentials in the URL are sent). Each target is checked
// and resolved here and upstream only gets the checked IP (DialThrough,
// ProxyTransport), so it cannot be steered by DNS that changes answers.
// dialUpstream connects to upstream itself (nil: Dialer(30 s), which
// refuses private addresses).
func StartProxyVia(upstream *url.URL, dialUpstream DialFunc) (*Proxy, error) {
	if dialUpstream == nil {
		dialUpstream = Dialer(30 * time.Second).DialContext
	}
	dial, err := DialThrough(upstream, dialUpstream, nil)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	base := proxyBaseTransport(dialUpstream)
	p := &Proxy{ln: ln, dial: dial,
		tr: NewProxyTransport(base, http.ProxyURL(upstream), dialUpstream, nil)}
	p.serveOn()
	return p, nil
}

func (p *Proxy) serveOn() {
	p.srv = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 30 * time.Second}
	go p.srv.Serve(p.ln)
}

// Addr is the proxy's host:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Close stops the proxy.
func (p *Proxy) Close() error {
	var err error
	p.once.Do(func() {
		p.tr.CloseIdleConnections()
		err = p.srv.Close()
	})
	return err
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
		return
	}
	if r.URL.Host == "" {
		http.Error(w, "not a proxy request", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Proxy-Authorization")
	resp, err := p.tr.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// tunnel serves CONNECT (HTTPS, WebSockets): the target is dialled through
// the guarded dialer, then bytes are copied both ways.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	target, err := p.dial(ctx, "tcp", r.Host)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		target.Close()
		http.Error(w, "tunnel not supported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		target.Close()
		return
	}
	client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if n := buf.Reader.Buffered(); n > 0 {
		data, _ := buf.Reader.Peek(n)
		target.Write(data)
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(target, client); done <- struct{}{} }()
	go func() { io.Copy(client, target); done <- struct{}{} }()
	<-done
	client.Close()
	target.Close()
}
