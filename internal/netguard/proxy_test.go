package netguard

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func localServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "secret")
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func proxyClient(p *Proxy) *http.Client {
	u, _ := url.Parse("http://" + p.Addr())
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

// The proxy resolves names itself and refuses private addresses, for plain
// HTTP and for CONNECT tunnels (HTTPS, WebSockets).
func TestProxyBlocksPrivateAddresses(t *testing.T) {
	srv, hits := localServer(t)
	p, err := StartProxy(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	target := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	resp, err := proxyClient(p).Get(target + "/")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("plain HTTP through the proxy reached %s", target)
		}
	}

	conn, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host := strings.TrimPrefix(target, "http://")
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	cr, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil && cr.StatusCode == http.StatusOK {
		t.Errorf("CONNECT through the proxy reached %s", host)
	}
	if hits.Load() != 0 {
		t.Errorf("private server got %d requests", hits.Load())
	}
}

// With an allowing dialer the proxy forwards plain HTTP and tunnels CONNECT.
func TestProxyForwards(t *testing.T) {
	srv, hits := localServer(t)
	d := &net.Dialer{Timeout: 5 * time.Second}
	p, err := StartProxy(d.DialContext)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	resp, err := proxyClient(p).Get(srv.URL + "/a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("plain HTTP: status %d", resp.StatusCode)
	}

	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer tls.Close()
	u, _ := url.Parse("http://" + p.Addr())
	tr := tls.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(u)
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	resp, err = c.Get(tls.URL + "/b")
	if err != nil {
		t.Fatalf("CONNECT tunnel: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2", hits.Load())
	}
	_ = context.Background
}
