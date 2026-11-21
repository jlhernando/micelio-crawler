package crawler

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A local SOCKS5 stand-in sends every request to the test origin. The browser
// must use the proxy to reach the non-resolving hostname, including its CSS.
// Bind Tor's existing default rather than changing production configuration.
func previewTorProxy(t *testing.T, target string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:9050")
	if err != nil {
		t.Skip("Tor's default port is unavailable:", err)
	}
	var sessions sync.WaitGroup
	var connections sync.Map
	accepted := make(chan struct{})
	t.Cleanup(func() {
		ln.Close()
		<-accepted
		connections.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
		sessions.Wait()
	})
	go func() {
		defer close(accepted)
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			connections.Store(client, true)
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer connections.Delete(client)
				defer client.Close()
				client.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(client)
				var hello [2]byte
				if _, err := io.ReadFull(reader, hello[:]); err != nil || hello[0] != 5 {
					return
				}
				if _, err := io.CopyN(io.Discard, reader, int64(hello[1])); err != nil {
					return
				}
				if _, err := client.Write([]byte{5, 0}); err != nil {
					return
				}
				var request [4]byte
				if _, err := io.ReadFull(reader, request[:]); err != nil || request[0] != 5 || request[1] != 1 {
					return
				}
				var size int64
				switch request[3] {
				case 1:
					size = 4
				case 4:
					size = 16
				case 3:
					length, err := reader.ReadByte()
					if err != nil {
						return
					}
					size = int64(length)
				default:
					return
				}
				if _, err := io.CopyN(io.Discard, reader, size+2); err != nil {
					return
				}
				upstream, err := net.DialTimeout("tcp", target, 3*time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				if _, err := client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				client.SetDeadline(time.Time{})
				written := make(chan struct{})
				go func() {
					io.Copy(upstream, reader)
					upstream.Close()
					close(written)
				}()
				io.Copy(client, upstream)
				client.Close()
				<-written
			}()
		}
	}()
}

func TestHTMLPreviewUsesTorForStyles(t *testing.T) {
	var pages, styles atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			pages.Add(1)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><link rel="stylesheet" href="/style.css"></head><body>Tor preview</body></html>`)
		case "/style.css":
			styles.Add(1)
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "html { background: rgb(34,85,136); }")
		}
	}))
	t.Cleanup(srv.Close)
	previewTorProxy(t, srv.Listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	frame, err := PreviewHomepage(ctx, types.CrawlConfig{SeedURL: "http://preview.invalid/", Proxy: "tor", SkipSSRF: true})
	if err != nil {
		t.Fatal(err)
	}
	if pages.Load() != 1 || styles.Load() == 0 {
		t.Fatalf("preview did not use the proxy for HTML and CSS: pages=%d styles=%d", pages.Load(), styles.Load())
	}
	image, err := jpeg.Decode(bytes.NewReader(frame.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, _ := image.At(100, 100).RGBA()
	if red>>8 < 29 || red>>8 > 39 || green>>8 < 80 || green>>8 > 90 || blue>>8 < 131 || blue>>8 > 141 {
		t.Fatalf("CSS did not reach the captured page: RGB=%d,%d,%d", red>>8, green>>8, blue>>8)
	}
}
