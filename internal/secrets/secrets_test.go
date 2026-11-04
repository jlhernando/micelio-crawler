package secrets

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// TestCrawlConfigSensitiveKeys pins which crawl config fields are dropped
// from crawl databases: a new credential field must match SensitiveKey, and
// a setting the analysis needs must not.
func TestCrawlConfigSensitiveKeys(t *testing.T) {
	var got []string
	rt := reflect.TypeOf(types.CrawlConfig{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" && SensitiveKey(name) {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"aiKey", "cookies", "cruxKey", "embeddingKey", "ga4KeyFile", "gscKeyFile", "plausibleApiKey", "psiKey", "torPassword"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sensitive crawl config keys = %v, want %v", got, want)
	}
}

func TestScrubJSON(t *testing.T) {
	cfg := types.CrawlConfig{
		SeedURL:       "https://user:seedpass@example.com/",
		Cookies:       "sid=cookie-secret",
		CustomHeaders: map[string]string{"Authorization": "Bearer header-secret", "X-Api-Key": "apikey-secret", "Accept-Language": "es"},
		Proxy:         "http://bob:proxy-secret@proxy.example:8080",
		PSIKey:        "psi-secret",
		AIKey:         "ai-secret",
		EmbeddingKey:  "emb-secret",
		TorPassword:   "tor-secret",
		MaxPages:      42,
		UserAgent:     "Micelio/1.0",
	}
	data, _ := json.Marshal(cfg)
	out, err := ScrubJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"seedpass", "cookie-secret", "header-secret", "apikey-secret", "proxy-secret", "psi-secret", "ai-secret", "emb-secret", "tor-secret"} {
		if strings.Contains(string(out), s) {
			t.Errorf("scrubbed config still contains %q: %s", s, out)
		}
	}
	var back types.CrawlConfig
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.MaxPages != 42 || back.UserAgent != "Micelio/1.0" || back.CustomHeaders["Accept-Language"] != "es" {
		t.Errorf("settings lost: %+v", back)
	}
	if back.Proxy != "http://bob:"+Mask+"@proxy.example:8080" {
		t.Errorf("proxy = %q", back.Proxy)
	}
	if !strings.HasPrefix(back.SeedURL, "https://user:") || !strings.HasSuffix(back.SeedURL, "@example.com/") {
		t.Errorf("seed URL = %q", back.SeedURL)
	}
}
