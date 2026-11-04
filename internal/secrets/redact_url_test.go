package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

// Custom header values are masked unless the header is a known
// non-credential one: names like X-JFrog-Art-Api carry API keys without
// matching any credential word.
func TestRedactCustomHeadersDefaultDeny(t *testing.T) {
	cfg := map[string]any{"customHeaders": map[string]any{
		"X-JFrog-Art-Api":          "JFROG-SECRET",
		"X-Goog-IAP-JWT-Assertion": "IAP-SECRET",
		"X-Custom":                 "CUSTOM-SECRET",
		"Accept-Language":          "es-ES",
		"User-Agent":               "Micelio/1.0",
		"Sec-CH-UA-Mobile":         "?1",
		"Referer":                  "https://example.com/?token=REF-SECRET",
	}}
	red := Redact(cfg, false).(map[string]any)["customHeaders"].(map[string]any)
	for _, k := range []string{"X-JFrog-Art-Api", "X-Goog-IAP-JWT-Assertion", "X-Custom"} {
		if red[k] != Mask {
			t.Errorf("%s = %v, want mask", k, red[k])
		}
	}
	if red["Accept-Language"] != "es-ES" || red["User-Agent"] != "Micelio/1.0" || red["Sec-CH-UA-Mobile"] != "?1" {
		t.Errorf("safe headers changed: %v", red)
	}
	if red["Referer"] != "https://example.com/?token="+Mask {
		t.Errorf("Referer = %v", red["Referer"])
	}
	// Headers in another form are masked whole.
	if got := Redact(map[string]any{"customHeaders": "X-Art: SECRET"}, false).(map[string]any)["customHeaders"]; got != Mask {
		t.Errorf("string headers = %v", got)
	}
	// The crawl database keeps only the safe headers.
	Drop(cfg)
	data, _ := json.Marshal(cfg)
	if strings.Contains(string(data), "SECRET") || !strings.Contains(string(data), "Accept-Language") {
		t.Errorf("Drop = %s", data)
	}
}

// URL user info (user and password) and credential query parameters are
// masked in every URL, also one per line in a URL list, at any depth.
func TestRedactURLCredentials(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a?access_token=T1&page=2":                        "https://example.com/a?access_token=" + Mask + "&page=2",
		"https://example.com/?api_key=K&X-Amz-Signature=S&X-Amz-Credential=C": "https://example.com/?api_key=" + Mask + "&X-Amz-Signature=" + Mask + "&X-Amz-Credential=" + Mask,
		"https://example.com/?sig=S&AWSAccessKeyId=A&q=shoes":                 "https://example.com/?sig=" + Mask + "&AWSAccessKeyId=" + Mask + "&q=shoes",
		"https://example.com/cb#access_token=F&state=x":                       "https://example.com/cb#access_token=" + Mask + "&state=x",
		"https://bob:pw@example.com/?password=P":                              "https://" + Mask + ":" + Mask + "@example.com/?password=" + Mask,
		"https://tok@example.com/":                                            "https://" + Mask + "@example.com/",
		"https://example.com/?q=a%20b&author=me":                              "https://example.com/?q=a%20b&author=me",
		`^/search\?token=`:                                                    `^/search\?token=`,
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
	list := "https://a.example/?token=T1\nhttps://u:P@b.example/\nhttps://c.example/"
	if got := RedactURL(list); strings.Contains(got, "T1") || strings.Contains(got, ":P@") || !strings.HasSuffix(got, "\nhttps://c.example/") {
		t.Errorf("list = %q", got)
	}
	cfg := map[string]any{
		"seedUrl":     "https://example.com/?key=SEED-SECRET",
		"urlList":     "https://example.com/?token=LIST-SECRET\nhttps://u:LISTPW-SECRET@example.com/b",
		"sitemapUrls": []any{"https://example.com/sitemap.xml?signature=SM-SECRET"},
		"nested":      map[string]any{"deep": []any{"https://x:DEEP-SECRET@example.com/"}},
	}
	data, _ := json.Marshal(Redact(cfg, false))
	if strings.Contains(string(data), "SECRET") {
		t.Errorf("Redact leaks: %s", data)
	}
	out, _ := ScrubJSON([]byte(`{"seedUrl":"https://example.com/?access_token=DB-SECRET","urls":["https://example.com/?apikey=DB2-SECRET"]}`))
	if strings.Contains(string(out), "SECRET") {
		t.Errorf("ScrubJSON leaks: %s", out)
	}
}
