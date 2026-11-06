package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func generateTestKeyJSON(t *testing.T, tokenURL string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	kf := GscKeyFile{
		ClientEmail: "test@test.iam.gserviceaccount.com",
		PrivateKey:  string(pemBlock),
		TokenURI:    tokenURL,
	}
	data, _ := json.Marshal(kf)
	return data
}

// useTokenServer points the pinned Google token endpoint at a fake server.
func useTokenServer(t *testing.T, u string) {
	t.Helper()
	old := googleTokenURL
	googleTokenURL = u
	t.Cleanup(func() { googleTokenURL = old })
}

// OAuth token exchange and refresh give up on a token endpoint that never
// answers instead of hanging.
func TestOAuthTokenCallsTimeOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	useTokenServer(t, srv.URL)
	old := oauthHTTPClient
	oauthHTTPClient = &http.Client{Timeout: 100 * time.Millisecond}
	defer func() { oauthHTTPClient = old }()
	if _, err := refreshOAuthToken("id", "secret", "refresh"); err == nil {
		t.Error("refresh against a silent endpoint: no error")
	}
	if _, err := exchangeOAuthCode("id", "secret", "code"); err == nil {
		t.Error("exchange against a silent endpoint: no error")
	}
}

func TestServiceAccountTokenURIPinned(t *testing.T) {
	for _, uri := range []string{"", "https://oauth2.googleapis.com/token", "https://accounts.google.com/o/oauth2/token"} {
		if _, err := NewGscClient(generateTestKeyJSON(t, uri), "https://example.com/", 90); err != nil {
			t.Errorf("gsc token_uri %q rejected: %v", uri, err)
		}
		if _, err := NewGA4Client(generateTestKeyJSON(t, uri), "1", 90); err != nil {
			t.Errorf("ga4 token_uri %q rejected: %v", uri, err)
		}
	}
	for _, uri := range []string{
		"http://127.0.0.1:8080/token",
		"http://169.254.169.254/latest/meta-data",
		"https://evil.example/token",
		"http://oauth2.googleapis.com/token",
		"https://oauth2.googleapis.com.evil.example/token",
	} {
		if _, err := NewGscClient(generateTestKeyJSON(t, uri), "https://example.com/", 90); err == nil {
			t.Errorf("gsc accepted token_uri %q", uri)
		}
		if _, err := NewGA4Client(generateTestKeyJSON(t, uri), "1", 90); err == nil {
			t.Errorf("ga4 accepted token_uri %q", uri)
		}
	}
}

func TestGscFetch(t *testing.T) {
	// Token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "mock-token",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	// GSC API server
	gscServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mock-token" {
			t.Fatalf("expected Bearer mock-token, got %s", auth)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"rows": []map[string]interface{}{
				{
					"keys":        []string{"https://example.com/page-1"},
					"impressions": 1000.0,
					"clicks":      100.0,
					"ctr":         0.1,
					"position":    5.5,
				},
				{
					"keys":        []string{"https://example.com/page-2"},
					"impressions": 500.0,
					"clicks":      25.0,
					"ctr":         0.05,
					"position":    12.3,
				},
			},
		})
	}))
	defer gscServer.Close()

	origEndpoint := gscEndpoint
	defer func() { setGscEndpoint(origEndpoint) }()
	setGscEndpoint(gscServer.URL)

	useTokenServer(t, tokenServer.URL)
	keyJSON := generateTestKeyJSON(t, tokenServer.URL)
	client, err := NewGscClient(keyJSON, "https://example.com/", 90)
	if err != nil {
		t.Fatal(err)
	}

	results, err := client.FetchBatch(context.Background(), []string{
		"https://example.com/page-1",
		"https://example.com/page-2",
		"https://example.com/page-3",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	data := results["https://example.com/page-1"]
	if data == nil {
		t.Fatal("expected data for page-1")
	}
	if data.Impressions != 1000 {
		t.Fatalf("expected 1000 impressions, got %d", data.Impressions)
	}
	if data.Clicks != 100 {
		t.Fatalf("expected 100 clicks, got %d", data.Clicks)
	}
	if data.Position != 5.5 {
		t.Fatalf("expected position 5.5, got %f", data.Position)
	}
}

// GSC rows match crawled URLs exactly first: /Page and /page crawled both
// keep their own data; the loose key (case, trailing slash) only applies
// when it points at one crawled URL.
func TestGscFetchExactBeforeLoose(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "mock-token", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	row := func(u string, clicks float64) map[string]interface{} {
		return map[string]interface{}{"keys": []string{u}, "impressions": 10.0, "clicks": clicks, "ctr": 0.1, "position": 1.0}
	}
	gscServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"rows": []map[string]interface{}{
			row("https://example.com/Page", 1),
			row("https://example.com/page", 2),
			row("https://example.com/solo", 3), // crawled as /solo/
		}})
	}))
	defer gscServer.Close()
	origEndpoint := gscEndpoint
	defer func() { setGscEndpoint(origEndpoint) }()
	setGscEndpoint(gscServer.URL)
	useTokenServer(t, tokenServer.URL)
	client, err := NewGscClient(generateTestKeyJSON(t, tokenServer.URL), "https://example.com/", 90)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.FetchBatch(context.Background(), []string{
		"https://example.com/page", "https://example.com/Page", "https://example.com/solo/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := res["https://example.com/Page"]; d == nil || d.Clicks != 1 {
		t.Errorf("/Page = %+v, want 1 click", d)
	}
	if d := res["https://example.com/page"]; d == nil || d.Clicks != 2 {
		t.Errorf("/page = %+v, want 2 clicks", d)
	}
	if d := res["https://example.com/solo/"]; d == nil || d.Clicks != 3 {
		t.Errorf("/solo/ = %+v, want 3 clicks (loose match)", d)
	}
}

func TestGscNormalizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://Example.Com/Page/", "https://example.com/page"},
		{"https://example.com/", "https://example.com/"},
		{"https://example.com/path?b=2&a=1", "https://example.com/path?b=2&a=1"},
	}

	for _, tc := range tests {
		got := normalizeGscURL(tc.input)
		if got != tc.want {
			t.Errorf("normalizeGscURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestGscInvalidKeyFile(t *testing.T) {
	_, err := NewGscClient([]byte(`{"invalid": true}`), "https://example.com/", 90)
	if err == nil {
		t.Fatal("expected error for invalid key file")
	}
}
