package server

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/browser"
)

// Page thumbnails for the Universe view. Captured on demand with headless
// Chrome, only for the pages the dashboard actually shows as cards, and
// cached on disk so each page is captured once. Chrome starts on the first
// request and shuts down after a short idle period to free its memory.

const (
	thumbConcurrency = 2
	thumbIdleTimeout = 2 * time.Minute
	thumbRetryAfter  = 10 * time.Minute
)

type thumbService struct {
	mu          sync.Mutex
	renderer    *browser.Renderer
	idle        *time.Timer
	active      int
	inflight    map[string]chan struct{}
	unavailable time.Time // Chrome failed to start; don't retry until thumbRetryAfter
	sem         chan struct{}
	dir         string
}

var thumbs = &thumbService{inflight: map[string]chan struct{}{}, sem: make(chan struct{}, thumbConcurrency)}

func thumbDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "micelio-thumbs")
	}
	return filepath.Join(home, ".micelio", "ui", "thumbs")
}

func thumbPath(crawlID, pageURL string) string {
	h := sha1.Sum([]byte(pageURL))
	return filepath.Join(thumbDir(), filepath.Base(crawlID), hex.EncodeToString(h[:10])+".jpg")
}

// removeCrawlThumbs deletes the cached thumbnails of a deleted crawl.
func removeCrawlThumbs(crawlID string) {
	id := filepath.Base(crawlID)
	if id == "" || id == "." || id == ".." || id == string(filepath.Separator) {
		return
	}
	if err := os.RemoveAll(filepath.Join(thumbDir(), id)); err != nil {
		log.Printf("universe thumbs: remove %s: %v", id, err)
	}
}

func (s *thumbService) acquireRenderer() *browser.Renderer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.renderer == nil {
		s.renderer = browser.NewRenderer("", browser.RenderConfig{Timeout: 20 * time.Second, BlockResources: false})
	}
	s.active++
	return s.renderer
}

func (s *thumbService) releaseRenderer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.active > 0 {
		return
	}
	s.idle = time.AfterFunc(thumbIdleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active == 0 && s.renderer != nil {
			s.renderer.Close()
			s.renderer = nil
		}
	})
}

// capture returns the cached thumbnail path, capturing it first if needed.
// Concurrent requests for the same page share one capture.
func (s *thumbService) capture(r *http.Request, crawlID, pageURL string) (string, bool) {
	path := thumbPath(crawlID, pageURL)
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	s.mu.Lock()
	if time.Since(s.unavailable) < thumbRetryAfter {
		s.mu.Unlock()
		return "", false
	}
	if ch, ok := s.inflight[path]; ok {
		s.mu.Unlock()
		select {
		case <-ch:
		case <-r.Context().Done():
			return "", false
		}
		_, err := os.Stat(path)
		return path, err == nil
	}
	done := make(chan struct{})
	s.inflight[path] = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, path)
		s.mu.Unlock()
		close(done)
	}()

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-r.Context().Done():
		return "", false
	}
	rd := s.acquireRenderer()
	defer s.releaseRenderer()
	img, err := rd.Screenshot(r.Context(), pageURL, 1280, 800, 0.25, 62)
	if err != nil {
		log.Printf("universe thumb %s: %v", pageURL, err)
		if r.Context().Err() == nil && isChromeMissing(err) {
			s.mu.Lock()
			s.unavailable = time.Now()
			s.mu.Unlock()
		}
		return "", false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, img, 0o644); err != nil {
		return "", false
	}
	return path, os.Rename(tmp, path) == nil
}

func isChromeMissing(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") || strings.Contains(msg, "exec:")
}

// registerUniverseThumbRoute serves GET /api/crawl/{id}/universe/thumb?i=N.
// Pages are addressed by their index in the layout, so only crawled URLs can
// ever be fetched.
func registerUniverseThumbRoute(mux *http.ServeMux, _ *UiStore, manager *CrawlManager) {
	mux.HandleFunc("GET /api/crawl/{id}/universe/thumb", func(w http.ResponseWriter, r *http.Request) {
		var pageURL string
		withUniverse(manager, w, r, func(results *CrawlResults, l *universeLayout) {
			i := intParam(r, "i", -1, -1, len(l.order)-1)
			if i >= 0 {
				pageURL = results.Pages[l.order[i]].URL
			}
		})
		if pageURL == "" {
			if w.Header().Get("Content-Type") == "" {
				jsonError(w, "invalid index", http.StatusBadRequest)
			}
			return
		}
		path, ok := thumbs.capture(r, r.PathValue("id"), pageURL)
		if !ok {
			jsonError(w, "thumbnail unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, path)
	})
}
