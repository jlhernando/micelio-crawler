package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Settings → Images → Download images is saved, read back, and fills crawl
// configs that do not set downloadImages themselves.
func TestDownloadImagesSetting(t *testing.T) {
	api, store, _ := newTestAPI(t)

	get := func() map[string]interface{} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings", nil))
		var s map[string]interface{}
		json.NewDecoder(rec.Body).Decode(&s)
		return s
	}
	put := func(v bool) {
		body, _ := json.Marshal(map[string]interface{}{"downloadImages": v})
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT settings: %d %s", rec.Code, rec.Body)
		}
		var resp map[string]interface{}
		json.NewDecoder(rec.Body).Decode(&resp)
		if r, _ := resp["rejected"].([]interface{}); len(r) != 0 {
			t.Fatalf("downloadImages rejected: %v", r)
		}
	}

	// Unset: crawls keep the off default.
	if _, ok := get()["downloadImages"]; ok {
		t.Fatal("downloadImages set before anyone saved it")
	}
	cfg := map[string]interface{}{"seedUrl": "https://example.com/"}
	applyDownloadImagesDefault(store, cfg)
	if _, ok := cfg["downloadImages"]; ok || configFromMap(cfg).DownloadImages {
		t.Fatalf("download on without the setting: %v", cfg)
	}

	put(true)
	if get()["downloadImages"] != true {
		t.Fatalf("setting not saved: %v", get()["downloadImages"])
	}
	cfg = map[string]interface{}{"seedUrl": "https://example.com/"}
	applyDownloadImagesDefault(store, cfg)
	if !configFromMap(cfg).DownloadImages {
		t.Fatalf("setting on but crawl config off: %v", cfg)
	}
	// A crawl that says no wins over the setting.
	cfg = map[string]interface{}{"seedUrl": "https://example.com/", "downloadImages": false}
	applyDownloadImagesDefault(store, cfg)
	if configFromMap(cfg).DownloadImages {
		t.Fatal("explicit downloadImages=false overridden by the setting")
	}

	put(false)
	if get()["downloadImages"] != false {
		t.Fatalf("setting not cleared: %v", get()["downloadImages"])
	}
}
