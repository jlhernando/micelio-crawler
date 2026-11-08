package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestReadCappedBodyFlagsOversize(t *testing.T) {
	if b, err := readCappedBody(bytes.NewReader(make([]byte, 10)), 10); err != nil || len(b) != 10 {
		t.Fatalf("exact-size body: %d bytes, err %v", len(b), err)
	}
	if _, err := readCappedBody(bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("oversize body: want errResponseTooLarge, got %v", err)
	}
}

// longPath gives every row a ~400 byte path so a full default page is over
// the 4 MB cap while half a page fits.
func longPath(i int) string {
	return fmt.Sprintf("/p/%06d/%s", i, strings.Repeat("x", 400))
}

func tokenTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "tok", "expires_in": 3600})
	}))
	t.Cleanup(s.Close)
	useTokenServer(t, s.URL)
	return s
}

// A GA4 page bigger than the 4 MB cap is asked again with fewer rows instead
// of failing the whole enrichment on truncated JSON.
func TestGA4OversizedPageShrinks(t *testing.T) {
	const total = 12000
	tokenServer := tokenTestServer(t)
	var mu sync.Mutex
	var limits []int
	ga4Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		mu.Lock()
		limits = append(limits, q.Limit)
		mu.Unlock()
		var rows []map[string]interface{}
		for i := q.Offset; i < total && i < q.Offset+q.Limit; i++ {
			rows = append(rows, map[string]interface{}{
				"dimensionValues": []map[string]string{{"value": longPath(i)}},
				"metricValues": []map[string]string{
					{"value": "5"}, {"value": "7"}, {"value": "0.5"}, {"value": "1"},
					{"value": "4"}, {"value": "0.6"}, {"value": "30.5"},
				},
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"rows": rows, "rowCount": total})
	}))
	defer ga4Server.Close()
	orig := ga4Endpoint
	defer setGa4Endpoint(orig)
	setGa4Endpoint(ga4Server.URL)

	client, err := NewGA4Client(generateTestKeyJSON(t, tokenServer.URL), "1", 30)
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://example.com" + longPath(0), "https://example.com" + longPath(total-1)}
	results, err := client.FetchBatch(context.Background(), urls)
	if err != nil {
		t.Fatalf("FetchBatch: %v (limits %v)", err, limits)
	}
	for _, u := range urls {
		if results[u] == nil || results[u].Sessions != 5 {
			t.Fatalf("missing GA4 data for %s", u[:30])
		}
	}
	if limits[0] != ga4BatchSize || limits[1] >= ga4BatchSize {
		t.Fatalf("expected a smaller retry after the oversized page, limits %v", limits)
	}
}

// A GSC page bigger than the 4 MB cap is asked again with fewer rows and
// pagination continues from the right row.
func TestGscOversizedPageShrinks(t *testing.T) {
	const total = 12000
	tokenServer := tokenTestServer(t)
	var mu sync.Mutex
	var limits []int
	gscServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			RowLimit int `json:"rowLimit"`
			StartRow int `json:"startRow"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		mu.Lock()
		limits = append(limits, q.RowLimit)
		mu.Unlock()
		var rows []map[string]interface{}
		for i := q.StartRow; i < total && i < q.StartRow+q.RowLimit; i++ {
			rows = append(rows, map[string]interface{}{
				"keys": []string{"https://example.com" + longPath(i)}, "impressions": 10.0,
				"clicks": 1.0, "ctr": 0.1, "position": 3.0,
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"rows": rows})
	}))
	defer gscServer.Close()
	orig := gscEndpoint
	defer setGscEndpoint(orig)
	setGscEndpoint(gscServer.URL)

	client, err := NewGscClient(generateTestKeyJSON(t, tokenServer.URL), "https://example.com/", 30)
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://example.com" + longPath(0), "https://example.com" + longPath(total-1)}
	results, err := client.FetchBatch(context.Background(), urls)
	if err != nil {
		t.Fatalf("FetchBatch: %v (limits %v)", err, limits)
	}
	for _, u := range urls {
		if results[u] == nil || results[u].Impressions != 10 {
			t.Fatalf("missing GSC data for %s", u[:30])
		}
	}
	if limits[0] != gscBatchSize || limits[1] >= gscBatchSize {
		t.Fatalf("expected a smaller retry after the oversized page, limits %v", limits)
	}
}
