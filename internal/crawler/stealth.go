package crawler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/micelio/micelio/internal/netguard"
	xproxy "golang.org/x/net/proxy"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	srt "github.com/juzeon/spoofed-round-tripper"
)

// headerOrderKey and pHeaderOrderKey are magic keys recognized by fhttp
// for controlling HTTP header and HTTP/2 pseudo-header ordering.
// Defined here to avoid importing fhttp directly.
const (
	headerOrderKey  = "Header-Order:"
	pHeaderOrderKey = "PHeader-Order:"
)

// chromeHeaderOrder is the exact order Chrome sends headers on navigation requests.
var chromeHeaderOrder = []string{
	"host",
	"cache-control",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"upgrade-insecure-requests",
	"user-agent",
	"accept",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-user",
	"sec-fetch-dest",
	"referer",
	"accept-encoding",
	"accept-language",
	"cookie",
	"if-none-match",
	"if-modified-since",
}

// chromePHeaderOrder is the HTTP/2 pseudo-header order Chrome uses.
var chromePHeaderOrder = []string{":method", ":authority", ":scheme", ":path"}

// StealthClient returns an http.Client that mimics Chrome's TLS fingerprint,
// HTTP header ordering, and HTTP/2 SETTINGS. Uses bogdanfinn/tls-client under the hood.
func StealthClient(timeout time.Duration, proxy *url.URL) (*http.Client, error) {
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_133),
		tls_client.WithRandomTLSExtensionOrder(),
		tls_client.WithNotFollowRedirects(),
	}
	if proxy == nil {
		// Without a proxy the client connects to the site itself: refuse
		// private addresses at connect time, like DefaultClient.
		opts = append(opts, tls_client.WithDialer(*SafeDialer(30 * time.Second)))
	}
	if proxy != nil {
		// tls-client tunnels every request through the proxy. Its own
		// dialer would send the host name, and the proxy would resolve it
		// past any check (DNS rebinding into the proxy's network): open the
		// tunnel to the checked IP instead. TLS inside still uses the name.
		dial, err := netguard.DialThrough(proxy, stealthProxyDial, stealthLookup)
		if err != nil {
			return nil, err
		}
		opts = append(opts, tls_client.WithProxyDialerFactory(func(string, time.Duration, *net.TCPAddr, fhttp.Header, tls_client.Logger) (xproxy.ContextDialer, error) {
			return contextDialer(dial), nil
		}))
	}
	rt, err := srt.NewSpoofedRoundTripper(opts...)
	if err != nil {
		return nil, err
	}
	// Timeout and redirect behavior are handled by tls-client internally
	// (WithTimeoutSeconds, WithNotFollowRedirects). The outer http.Client
	// only needs the transport for the spoofed TLS/header fingerprint.
	return &http.Client{Transport: rt}, nil
}

// stealthProxyDial connects to the proxy and stealthLookup resolves
// targets for StealthClient (variables so tests can reach a loopback proxy
// and stub DNS). Like the crawl client, the proxy itself must not be a
// private address.
var (
	stealthProxyDial netguard.DialFunc = SafeDialer(30 * time.Second).DialContext
	stealthLookup    netguard.LookupFunc
)

// contextDialer adapts a netguard.DialFunc to a proxy.ContextDialer.
type contextDialer netguard.DialFunc

func (d contextDialer) Dial(network, addr string) (net.Conn, error) {
	return d(context.Background(), network, addr)
}

func (d contextDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d(ctx, network, addr)
}

// setStealthHeaders adds header ordering keys to the request so fhttp
// sends headers in Chrome's exact order. Must be called after all headers are set.
func setStealthHeaders(req *http.Request) {
	req.Header[headerOrderKey] = chromeHeaderOrder
	req.Header[pHeaderOrderKey] = chromePHeaderOrder
}
