package analysis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Resource URLs come from crawled pages: the fallback HEAD sizer must not
// reach this computer or its networks.
func TestPlainHeadSizerBlocksPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "1234")
	}))
	defer srv.Close()
	target := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if r := plainHeadSizer("test")(context.Background(), target+"/img.png"); r.OK || r.Size != 0 {
		t.Errorf("sizer reached a loopback address: %+v", r)
	}
	if hits.Load() != 0 {
		t.Errorf("loopback server got %d requests", hits.Load())
	}
}
