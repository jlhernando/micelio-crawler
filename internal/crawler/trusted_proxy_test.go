package crawler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// connectProxy is an httptest CONNECT proxy on 127.0.0.1 that records the
// tunnel targets it is asked for and connects every tunnel to origin.
type connectProxy struct {
	*httptest.Server
	origin  string
	mu      sync.Mutex
	targets []string
}

func startConnectProxy(t *testing.T, origin string) *connectProxy {
	t.Helper()
	p := &connectProxy{origin: origin}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.targets = append(p.targets, r.Method+" "+r.Host)
		p.mu.Unlock()
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		up, err := net.Dial("tcp", p.origin)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		c, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if n := buf.Reader.Buffered(); n > 0 {
			b, _ := buf.Reader.Peek(n)
			up.Write(b)
		}
		go func() { io.Copy(up, c); up.Close() }()
		io.Copy(c, up)
		c.Close()
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *connectProxy) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

// A crawl with a proxy on 127.0.0.1 (CLI --proxy, Tor, the dashboard's
// --trusted-proxy) returns pages with SSRF protection on. Before, the
// guarded dialer refused the proxy's own loopback address: robots.txt was
// "unreachable" and the crawl got 0 pages. The proxy is only ever asked
// for the checked IP of the site, never its name.
func TestCrawlThroughLocalProxy(t *testing.T) {
	const publicIP = "93.184.216.34"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			io.WriteString(w, "User-agent: *\nAllow: /\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><head><title>Home</title></head><body><a href="/a">A</a> <a href="/b">B</a></body></html>`)
		case "/a", "/b":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><head><title>%s</title></head><body><a href="/">home</a></body></html>`, r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	proxy := startConnectProxy(t, origin.Listener.Addr().String())

	oldLookup, oldTLS := lookupHost, crawlTLSConfig
	defer func() { lookupHost, crawlTLSConfig = oldLookup, oldTLS }()
	lookupHost = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "example.com" {
			return []net.IPAddr{{IP: net.ParseIP(publicIP)}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	crawlTLSConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig

	cfg := types.CrawlConfig{
		SeedURL:        "https://example.com/",
		Mode:           types.ModeSpider,
		MaxDepth:       2,
		MaxPages:       5,
		Concurrency:    2,
		UserAgent:      "TestBot/1.0",
		RespectRobots:  true,
		Proxy:          proxy.URL, // http://127.0.0.1:port
		NoSoft404Probe: true,
		NoAgentCheck:   true,
		TimeoutSeconds: 5,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := Crawl(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	ok := 0
	for _, p := range res.Pages {
		if p.StatusCode == 200 {
			ok++
		}
	}
	if ok < 3 {
		t.Fatalf("crawl through local proxy: %d pages with 200 (of %d), want 3", ok, len(res.Pages))
	}
	targets := proxy.seen()
	if len(targets) == 0 {
		t.Fatal("proxy never used")
	}
	for _, tg := range targets {
		if tg != "CONNECT "+publicIP+":443" {
			t.Errorf("proxy asked for %q, want only the checked IP", tg)
		}
		if strings.Contains(tg, "example.com") {
			t.Errorf("proxy got the host name: %q", tg)
		}
	}
}
