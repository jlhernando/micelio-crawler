package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A responsive SPA: it writes what it sees (screen width, mobile client
// hint, touch, the Sec-CH-UA-Mobile header) and shows different links on
// small screens, the case a desktop-only render gets wrong. Without the
// viewport meta a mobile browser lays the page out 980 px wide.
const deviceProbePage = `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>probe</title></head><body><div id=out></div><script>
const ua = navigator.userAgentData;
document.getElementById('out').textContent =
  'w=' + window.innerWidth + ' mobile=' + (ua ? ua.mobile : 'none') +
  ' touch=' + (navigator.maxTouchPoints > 0) + ' dpr=' + window.devicePixelRatio + ' hdr=' + document.title;
const a = document.createElement('a');
a.href = innerWidth < 768 ? '/m-only' : '/desktop-only';
a.textContent = 'nav';
document.body.appendChild(a);
</script></body></html>`

func TestRenderEmulatesDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Replace(deviceProbePage, "<title>probe</title>", "<title>"+r.Header.Get("Sec-CH-UA-Mobile")+"</title>", 1)))
	}))
	defer srv.Close()

	mobile := types.ResolveDevice(&types.DeviceProfile{ID: "mobile"})
	tablet := types.ResolveDevice(&types.DeviceProfile{ID: "tablet", Landscape: true})
	for _, tc := range []struct {
		name   string
		device *types.DeviceProfile
		want   []string
	}{
		{"no device keeps 1920 desktop", nil, []string{"w=1920 ", "touch=false", "/desktop-only"}},
		{"mobile", mobile, []string{"w=412 ", "mobile=true", "touch=true", "dpr=2.625", "hdr=?1", "/m-only"}},
		{"tablet landscape", tablet, []string{"w=1180 ", "touch=true", "/desktop-only"}},
		{"googlebot desktop", types.ResolveDevice(&types.DeviceProfile{ID: "googlebot-desktop"}), []string{"w=1024 ", "touch=false", "/desktop-only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRenderer(t, RenderConfig{Timeout: 20 * time.Second, Device: tc.device})
			ua := ""
			if tc.device != nil {
				ua = tc.device.UserAgent
			}
			res, err := r.RenderPage(context.Background(), srv.URL+"/", ua, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.HTML, w) {
					t.Errorf("render lacks %q:\n%s", w, res.HTML)
				}
			}
		})
	}
}

func TestUserAgentMetadata(t *testing.T) {
	mobile := types.ResolveDevice(&types.DeviceProfile{ID: "mobile"})
	md := userAgentMetadata(mobile.UserAgent, mobile)
	if md == nil || !md.Mobile || md.Platform != "Android" || md.Model != "Pixel 8" || md.Brands[1].Version != "136" {
		t.Errorf("mobile metadata = %+v", md)
	}
	desk := types.ResolveDevice(&types.DeviceProfile{ID: "desktop"})
	if md := userAgentMetadata(desk.UserAgent, desk); md == nil || md.Mobile || md.Platform != "Windows" {
		t.Errorf("desktop metadata = %+v", md)
	}
	for _, id := range []string{"iphone", "googlebot-smartphone", "bingbot"} {
		d := types.ResolveDevice(&types.DeviceProfile{ID: id})
		if md := userAgentMetadata(d.UserAgent, d); md != nil {
			t.Errorf("%s: want no client hints, got %+v", id, md)
		}
	}
}
