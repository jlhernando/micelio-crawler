package crawler

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/micelio/micelio/internal/types"
)

// ProbeCDN finds which CDN serves a site and how it caches: the page twice
// (a second request shows whether the edge kept it) and one static asset.
// Four requests at most, with a browser User-Agent.
func ProbeCDN(ctx context.Context, client *http.Client, pageURL string) *types.CDNReport {
	rep := &types.CDNReport{URL: pageURL}
	first, body, h, err := cdnGet(ctx, client, pageURL, 512<<10)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	rep.HTML = append(rep.HTML, first.cache)
	rep.Provider, rep.Evidence = detectCDN(h)
	rep.Compression = strings.ToLower(strings.TrimSpace(h.Get("Content-Encoding")))
	rep.Protocol = first.proto
	rep.HTTP3 = strings.Contains(h.Get("Alt-Svc"), "h3")
	rep.EarlyHints = first.earlyHints
	rep.HSTS = h.Get("Strict-Transport-Security") != ""
	if second, _, _, err := cdnGet(ctx, client, pageURL, 0); err == nil {
		rep.HTML = append(rep.HTML, second.cache)
		// Cloudflare sends Early Hints once it has learnt them for a page.
		rep.EarlyHints = rep.EarlyHints || second.earlyHints
	}
	if asset := firstAsset(pageURL, body); asset != "" {
		if a, _, _, err := cdnGet(ctx, client, asset, 0); err == nil {
			// Ask twice so a cold edge does not read as "not cached".
			if a.cache.Status == "MISS" {
				if b, _, _, err := cdnGet(ctx, client, asset, 0); err == nil {
					a = b
				}
			}
			rep.Asset = &a.cache
		}
	}
	return rep
}

type cdnResp struct {
	cache      types.CDNCache
	proto      string
	earlyHints bool
}

func cdnGet(ctx context.Context, client *http.Client, u string, bodyMax int64) (cdnResp, []byte, http.Header, error) {
	var out cdnResp
	start := time.Now()
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
			if code == http.StatusEarlyHints {
				out.earlyHints = true
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", u, nil)
	if err != nil {
		return out, nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Micelio")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Encoding", "br, zstd, gzip")
	resp, err := client.Do(req)
	if err != nil {
		return out, nil, nil, err
	}
	defer resp.Body.Close()
	// Headers of the final answer: the first byte may be a 103 Early Hints.
	ttfb := time.Since(start).Milliseconds()
	var body []byte
	if bodyMax > 0 {
		// We asked for br/gzip ourselves, so the transport leaves the body
		// encoded: decode it here (only the page; assets are discarded).
		body, _ = io.ReadAll(io.LimitReader(decodeBody(resp), bodyMax))
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	out.proto = resp.Proto
	out.cache = types.CDNCache{
		URL: u, Code: resp.StatusCode, TTFBMs: ttfb,
		Status:       cacheStatus(resp.Header),
		CacheControl: resp.Header.Get("Cache-Control"),
	}
	out.cache.Age, _ = strconv.Atoi(resp.Header.Get("Age"))
	return out, body, resp.Header, nil
}

func decodeBody(resp *http.Response) io.Reader {
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "br":
		return brotli.NewReader(resp.Body)
	case "gzip":
		if zr, err := gzip.NewReader(resp.Body); err == nil {
			return zr
		}
	case "zstd":
		if zr, err := zstd.NewReader(resp.Body); err == nil {
			return zr.IOReadCloser()
		}
	}
	return resp.Body
}

// cacheStatus reads the edge cache result from the headers CDNs use.
func cacheStatus(h http.Header) string {
	for _, k := range []string{"Cf-Cache-Status", "X-Vercel-Cache", "Akamai-Cache-Status", "Cdn-Cache", "X-Cache-Status", "X-Cache", "Cache-Status", "X-Nf-Cache-Status"} {
		v := h.Get(k)
		if v == "" {
			continue
		}
		up := strings.ToUpper(v)
		for _, s := range []string{"DYNAMIC", "BYPASS", "REVALIDATED", "EXPIRED", "STALE", "UPDATING", "HIT", "MISS", "PASS"} {
			if strings.Contains(up, s) {
				if s == "PASS" {
					return "BYPASS"
				}
				return s
			}
		}
		if k == "Cache-Status" && strings.Contains(strings.ToLower(v), "fwd=") {
			return "MISS"
		}
	}
	return ""
}

// detectCDN names the CDN from response headers.
func detectCDN(h http.Header) (provider, evidence string) {
	has := func(k string) bool { return h.Get(k) != "" }
	server := strings.ToLower(h.Get("Server"))
	via := strings.ToLower(h.Get("Via"))
	switch {
	case has("Cf-Ray") || server == "cloudflare":
		return "Cloudflare", "cf-ray header"
	case has("X-Amz-Cf-Id") || strings.Contains(via, "cloudfront"):
		return "Amazon CloudFront", "x-amz-cf-id header"
	case has("X-Vercel-Id") || server == "vercel":
		return "Vercel", "x-vercel-id header"
	case has("X-Nf-Request-Id") || server == "netlify":
		return "Netlify", "x-nf-request-id header"
	case has("Akamai-Grn") || has("Akamai-Cache-Status") || strings.Contains(server, "akamai"):
		return "Akamai", "akamai headers"
	case has("X-Azure-Ref"):
		return "Azure Front Door", "x-azure-ref header"
	case has("Fastly-Debug-Digest") || (has("X-Served-By") && strings.Contains(strings.ToLower(h.Get("X-Served-By")), "cache-")) || strings.Contains(via, "fastly"):
		return "Fastly", "x-served-by header"
	case strings.Contains(server, "bunnycdn") || has("Cdn-Pullzone"):
		return "Bunny CDN", "server header"
	case strings.Contains(server, "keycdn"):
		return "KeyCDN", "server header"
	case has("X-Iinfo") || strings.Contains(strings.ToLower(h.Get("X-Cdn")), "imperva"):
		return "Imperva", "x-iinfo header"
	case has("X-Sucuri-Id"):
		return "Sucuri", "x-sucuri-id header"
	case strings.Contains(via, "google") || server == "google frontend":
		return "Google Cloud", "via header"
	case has("X-Varnish") || strings.Contains(via, "varnish"):
		return "Varnish (origin cache)", "x-varnish header"
	}
	return "", ""
}

var assetRe = regexp.MustCompile(`(?i)<(?:script[^>]+src|link[^>]+href)=["']([^"']+\.(?:js|css)(?:\?[^"']*)?)["']`)

// firstAsset finds a script or stylesheet of the page on the same site.
func firstAsset(pageURL string, body []byte) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	for _, m := range assetRe.FindAllSubmatch(body, 20) {
		u, err := base.Parse(string(m[1]))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if strings.TrimPrefix(u.Hostname(), "www.") == strings.TrimPrefix(base.Hostname(), "www.") {
			return u.String()
		}
	}
	return ""
}
