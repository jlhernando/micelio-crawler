package browser

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The document prints to a PDF and cannot fetch anything while it does.
func TestPrintPDF(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	// "<-loopback>" sends loopback through the dead proxy too.
	html := `<!DOCTYPE html><style>@page{size:A4;margin:0}</style><h1>Report</h1>` +
		`<img src="http://example.com/x.png"><img src="` + srv.URL + `/x.png">`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pdf, err := PrintPDF(ctx, html)
	if err != nil && strings.Contains(err.Error(), "executable file not found") {
		t.Skip("Chrome not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("not a PDF: %q", pdf[:min(len(pdf), 20)])
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the document reached a local server %d times", n)
	}
}

func isNoChrome(err error) bool { return strings.Contains(err.Error(), "executable file not found") }
