package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeCDN(t *testing.T) {
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		w.Header().Set("Cf-Ray", "abc-MAD")
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Alt-Svc", `h3=":443"; ma=86400`)
		w.Header().Set("Strict-Transport-Security", "max-age=1")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Cf-Cache-Status", "DYNAMIC")
			w.Header().Set("Cache-Control", "private, no-store")
			w.Write([]byte(`<html><link rel="stylesheet" href="/s/app.css"><script src="https://other.cdn/x.js"></script></html>`))
		case "/s/app.css":
			st := "MISS"
			if hits[r.URL.Path] > 1 {
				st = "HIT"
			}
			w.Header().Set("Cf-Cache-Status", st)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
	}))
	defer srv.Close()

	rep := ProbeCDN(context.Background(), srv.Client(), srv.URL+"/")
	if rep.Error != "" || rep.Provider != "Cloudflare" || !rep.HTTP3 || !rep.HSTS {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.HTML) != 2 || rep.HTML[0].Status != "DYNAMIC" || rep.HTML[0].CacheControl != "private, no-store" {
		t.Errorf("html %+v", rep.HTML)
	}
	// The same-site stylesheet is checked (twice: the first was a cold MISS).
	if rep.Asset == nil || rep.Asset.Status != "HIT" || hits["/s/app.css"] != 2 {
		t.Errorf("asset %+v hits %v", rep.Asset, hits)
	}
}

func TestDetectCDN(t *testing.T) {
	for _, c := range []struct {
		h    map[string]string
		want string
	}{
		{map[string]string{"X-Amz-Cf-Id": "x"}, "Amazon CloudFront"},
		{map[string]string{"X-Vercel-Id": "x"}, "Vercel"},
		{map[string]string{"X-Served-By": "cache-mad2220-MAD"}, "Fastly"},
		{map[string]string{"Akamai-Grn": "x"}, "Akamai"},
		{map[string]string{"Server": "nginx"}, ""},
	} {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}
		if got, _ := detectCDN(h); got != c.want {
			t.Errorf("%v: got %q, want %q", c.h, got, c.want)
		}
	}
	h := http.Header{}
	h.Set("X-Cache", "Hit from cloudfront")
	if got := cacheStatus(h); got != "HIT" {
		t.Errorf("x-cache: %q", got)
	}
}
