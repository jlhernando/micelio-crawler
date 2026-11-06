package crawler

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/netguard"
)

// Every SSRF-protected client that can use a proxy (--proxy, Tor,
// HTTP(S)_PROXY) gives the proxy the checked IP, never the name.
func TestProtectedClientsPinProxyTargets(t *testing.T) {
	proxy, _ := url.Parse("http://proxy.example:8080")
	for name, c := range map[string]*http.Client{
		"DefaultClient with --proxy":      DefaultClient(time.Second, proxy),
		"DefaultClient with env proxy":    DefaultClient(time.Second, nil),
		"crawl client with DNS cache":     newClient(time.Second, proxy, true, newDNSCache(time.Minute)),
		"SafeClientFollowRedirects (env)": SafeClientFollowRedirects(time.Second),
	} {
		if _, ok := c.Transport.(*netguard.ProxyTransport); !ok {
			t.Errorf("%s: transport %T does not pin proxy targets", name, c.Transport)
		}
	}
	if _, ok := UnsafeClient(time.Second, proxy).Transport.(*http.Transport); !ok {
		t.Error("UnsafeClient (tests only) changed")
	}
}

// connectRecorder is a proxy that records CONNECT requests and refuses
// them (the stealth client's TLS fingerprint needs a real certificate, so
// the test stops at the proxy).
type connectRecorder struct {
	ln    net.Listener
	mu    sync.Mutex
	lines []string
	auths []string
}

func startConnectRecorder(t *testing.T) *connectRecorder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &connectRecorder{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				hdr, _ := textproto.NewReader(br).ReadMIMEHeader()
				r.mu.Lock()
				r.lines = append(r.lines, strings.TrimSpace(line))
				r.auths = append(r.auths, hdr.Get("Proxy-Authorization"))
				r.mu.Unlock()
				io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return r
}

func (r *connectRecorder) seen() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...), append([]string(nil), r.auths...)
}

// Stealth mode with a proxy opens the tunnel to the checked IP with the
// proxy credentials, and never hands the proxy a name that resolves to a
// private address.
func TestStealthClientProxyGetsCheckedIP(t *testing.T) {
	rec := startConnectRecorder(t)
	oldDial, oldLookup := stealthProxyDial, stealthLookup
	defer func() { stealthProxyDial, stealthLookup = oldDial, oldLookup }()
	stealthProxyDial = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	stealthLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "shop.example":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		case "internal.example":
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	proxy := &url.URL{Scheme: "http", Host: rec.ln.Addr().String(), User: url.UserPassword("u", "p")}
	client, err := StealthClient(5*time.Second, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Get("https://shop.example/"); err == nil {
		resp.Body.Close()
		t.Fatal("request succeeded through a refusing proxy")
	} else if !strings.Contains(err.Error(), "IP addresses") {
		t.Errorf("err = %v, want a clear proxy-refused error", err)
	}
	if resp, err := client.Get("http://internal.example/"); err == nil {
		resp.Body.Close()
		t.Fatal("private target allowed")
	}
	lines, auths := rec.seen()
	if len(lines) == 0 {
		t.Fatal("proxy never reached")
	}
	for i, l := range lines {
		if l != "CONNECT 93.184.216.34:443 HTTP/1.1" {
			t.Errorf("proxy got %q, want CONNECT to the checked IP only", l)
		}
		if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p")); auths[i] != want {
			t.Errorf("Proxy-Authorization = %q", auths[i])
		}
	}
}
