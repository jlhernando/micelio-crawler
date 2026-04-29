package crawler

import (
	"net/http"
	"net/url"
	"time"

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
	if proxy != nil {
		opts = append(opts, tls_client.WithProxyUrl(proxy.String()))
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

// setStealthHeaders adds header ordering keys to the request so fhttp
// sends headers in Chrome's exact order. Must be called after all headers are set.
func setStealthHeaders(req *http.Request) {
	req.Header[headerOrderKey] = chromeHeaderOrder
	req.Header[pHeaderOrderKey] = chromePHeaderOrder
}
