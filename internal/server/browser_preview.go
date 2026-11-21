package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/netguard"
	"github.com/micelio/micelio/internal/types"
)

// One bounded, read-only capture at a time. No browser session or action API
// survives the request; the dashboard receives only an image and metadata.
type previewService struct {
	parent  context.Context
	busy    atomic.Bool
	capture func(context.Context, types.CrawlConfig) (*browser.PreviewFrame, error)
	proxy   func(map[string]any) error
}

func registerBrowserPreview(mux *http.ServeMux, manager *CrawlManager) *previewService {
	s := &previewService{parent: manager.ctx, capture: crawler.PreviewHomepage, proxy: manager.applyTrustedProxy}
	s.register(mux)
	return s
}

func (s *previewService) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/browser-preview", s.open)
}

// Only connection and rendering settings belong here. In particular, this
// endpoint cannot read local snippet/key files or accept arbitrary CDP input.
type previewConfig struct {
	SeedURL              string               `json:"seedUrl"`
	NativeChrome         bool                 `json:"nativeChrome"`
	JSRendering          bool                 `json:"jsRendering"`
	UserAgent            string               `json:"userAgent"`
	Device               *types.DeviceProfile `json:"device"`
	Proxy                string               `json:"proxy"`
	Cookies              string               `json:"cookies"`
	CustomHeaders        map[string]string    `json:"customHeaders"`
	Stealth              bool                 `json:"stealth"`
	RenderBlockResources bool                 `json:"renderBlockResources"`
	RenderTimeoutSec     int                  `json:"renderTimeoutSec"`
	DownloadImages       bool                 `json:"downloadImages"`
	RespectRobots        bool                 `json:"respectRobots"`
	Delay                int                  `json:"delay"`
}

func readPreviewJSON(w http.ResponseWriter, r *http.Request, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid preview settings")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func (s *previewService) open(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		Config *previewConfig `json:"config"`
	}
	if err := readPreviewJSON(w, r, &in); err != nil || in.Config == nil {
		jsonError(w, "invalid preview settings", http.StatusBadRequest)
		return
	}
	in.Config.SeedURL = strings.TrimSpace(in.Config.SeedURL)
	u, err := url.Parse(in.Config.SeedURL)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || netguard.BlockedHost(u.Hostname()) {
		jsonError(w, "a public http or https URL without credentials is required", http.StatusBadRequest)
		return
	}
	data, _ := json.Marshal(in.Config)
	var m map[string]any
	json.Unmarshal(data, &m)
	m["mode"] = "spider"
	if s.proxy != nil {
		if err := s.proxy(m); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := sanitizeCrawlConfig(m); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.busy.CompareAndSwap(false, true) {
		jsonError(w, "a crawler preview is already being generated", http.StatusConflict)
		return
	}
	defer s.busy.Store(false)
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	stop := context.AfterFunc(s.parent, cancel)
	defer func() { stop(); cancel() }()
	frame, err := s.capture(ctx, configFromMap(m))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	jsonOK(w, map[string]any{"frame": frame})
}
