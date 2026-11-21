//go:build !jsbench

package browser

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRendererContinuesAfterBinaryAnalyticsPost(t *testing.T) {
	payload := []byte{0xed, 0xb3, 0xb1, 0x58, 0x43, 0x5c, 0x2a, 0x6f}
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><body><output>loading</output><script>
   fetch('/binary',{method:'POST',body:new Uint8Array([237,179,177,88,67,92,42,111])})
   .then(()=>fetch('/flag')).then(r=>r.json()).then(data=>document.querySelector('output').textContent=data.value);
   </script></body></html>`)
		case "/binary":
			b, _ := io.ReadAll(r.Body)
			received <- b
			w.WriteHeader(204)
		case "/flag":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"value":"complete"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	r := newTestRenderer(t, RenderConfig{BlockImages: true, Timeout: 30 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := r.RenderPage(ctx, srv.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.HTML, ">complete</output>") {
		t.Fatalf("following fetch was left paused: %s", got.HTML)
	}
	select {
	case body := <-received:
		if !bytes.Equal(body, payload) {
			t.Fatalf("POST body changed: %x != %x", body, payload)
		}
	default:
		t.Fatal("POST was not sent")
	}
}
