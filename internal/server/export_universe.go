package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/report"
)

// The PDF report shows the crawl's Universe map. Headless Chrome opens this
// dashboard's own bare capture page (#/capture/universe/{id}) on loopback
// and screenshots it in a few views.

var self struct {
	sync.RWMutex
	base  string // http://127.0.0.1:3100, empty until the UI server starts
	token string
}

// setSelfURL records where this dashboard answers on loopback.
func setSelfURL(host string, port int, token string) {
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	self.Lock()
	self.base = "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	self.token = token
	self.Unlock()
}

// captureApp is swapped in tests so they do not need Chrome.
var captureApp = browser.CaptureApp

type pdfUniverseView struct {
	key, query, title, caption string
}

// ticketViews are the maps a backlog ticket can carry, each one lit up to
// show where that problem sits.
var ticketViews = map[string]pdfUniverseView{
	"errors":    {"errors", "layout=links&all=1&highlight=2&zoom=0.2&chips=0", "Errors on the map", "Red marks the 4xx/5xx URLs and the links that lead to them."},
	"redirects": {"redirects", "layout=links&all=1&highlight=1&zoom=0.2&chips=0", "Redirects on the map", "Highlighted lines are internal links that still go through a redirect."},
	"depth":     {"depth", "layout=links&color=depth&zoom=0.4&chips=0", "Click depth", "Colour shows clicks from the start page: blue is close, warm colours are 4+ clicks deep."},
	"pagerank":  {"pagerank", "layout=links&color=pagerank&zoom=0.4&chips=0", "Internal PageRank", "Bright pages receive the most internal link weight; dim pages at the edges receive almost none."},
}

// universeShots captures the main Universe map and the maps the backlog
// tickets need (views: "errors", "redirects", "depth", "pagerank").
func universeShots(ctx context.Context, crawlID string, ticketKeys []string) []report.UniverseShot {
	self.RLock()
	base, token := self.base, self.token
	self.RUnlock()
	if base == "" {
		return nil
	}
	views := []pdfUniverseView{{"structure", "layout=links&zoom=0.6", "Link structure", "Each dot is a page; lines follow the links a crawler takes from the start page."}}
	for _, k := range ticketKeys {
		if v, ok := ticketViews[k]; ok {
			views = append(views, v)
		}
	}
	// The cover backdrop: the link map again, closer and without labels.
	views = append(views, pdfUniverseView{key: "cover", query: "layout=links&zoom=0.45&chips=0"})
	// The main map and cover fill a page width; ticket maps are wider still.
	targets := make([]browser.AppTarget, len(views))
	for i, v := range views {
		targets[i] = browser.AppTarget{URL: base + "/#/capture/universe/" + url.PathEscape(crawlID) + "?" + v.query, Width: 1600, Height: 1000}
		if v.key != "structure" && v.key != "cover" {
			targets[i].Width, targets[i].Height = 1600, 820
		}
	}
	headers := map[string]any{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	start := time.Now()
	shots := captureApp(ctx, targets, headers, 1.5)
	var out []report.UniverseShot
	var cover []byte
	for i, s := range shots {
		if s.Err != nil {
			log.Printf("pdf export %s: universe %s: %v", crawlID, views[i].title, s.Err)
			continue
		}
		if views[i].key == "cover" {
			cover = s.JPEG
			continue
		}
		shot := report.UniverseShot{Key: views[i].key, Title: views[i].title, Caption: views[i].caption, JPEG: s.JPEG}
		if s.Meta != "" {
			var m report.UniverseMeta
			if json.Unmarshal([]byte(s.Meta), &m) == nil {
				shot.Meta = &m
			}
		}
		out = append(out, shot)
	}
	for i := range out {
		if out[i].Key == "structure" {
			out[i].Cover = cover
		}
	}
	ok := 0
	for _, s := range shots {
		if s.Err == nil {
			ok++
		}
	}
	log.Printf("[timing] pdf universe %s: %d/%d captures in %s", crawlID, ok, len(shots), time.Since(start).Round(time.Millisecond))
	return out
}
