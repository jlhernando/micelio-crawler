package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The page draws WebGL, reports ready with its meta, and gets the headers.
func TestCaptureApp(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`<body><canvas id=c width=300 height=200></canvas><script>
const gl = document.getElementById('c').getContext('webgl');
if (!gl) { document.body.dataset.capture = 'error'; document.body.dataset.captureError = 'no webgl'; }
else { gl.clearColor(0.2, 0.5, 1, 1); gl.clear(gl.COLOR_BUFFER_BIT);
  document.body.dataset.captureMeta = '{"pages":3}'; document.body.dataset.capture = 'ready'; }
</script></body>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	shots := CaptureApp(ctx, []AppTarget{{URL: srv.URL, Width: 300, Height: 200}}, map[string]any{"Authorization": "Bearer x"}, 1)
	if err := shots[0].Err; err != nil {
		if isNoChrome(err) {
			t.Skip("Chrome not installed")
		}
		t.Fatal(err)
	}
	if len(shots[0].JPEG) < 100 || shots[0].Meta != `{"pages":3}` || auth != "Bearer x" {
		t.Errorf("jpeg %d bytes, meta %q, auth %q", len(shots[0].JPEG), shots[0].Meta, auth)
	}
}
