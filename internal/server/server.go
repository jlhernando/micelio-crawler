package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	_ "net/http/pprof" // register pprof handlers on default mux
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/micelio/micelio/internal/updater"
)

// UpdaterRepo is the GitHub "owner/repo" auto-update polls. Hardcoded to the
// upstream so the macOS .app and any user-installed binary always points at
// the canonical release stream.
const UpdaterRepo = "jlhernando/micelio-crawler"

const wsReadTimeout = 5 * time.Minute
const wsPingInterval = 2 * time.Minute

var upgrader = websocket.Upgrader{
	CheckOrigin: isLocalOrigin,
}

// wsClient wraps a websocket connection with a write mutex to prevent concurrent writes.
type wsClient struct {
	conn *websocket.Conn
	wmu  sync.Mutex
}

// Server is the Micelio web UI server (HTTP + WebSocket + static dashboard).
type Server struct {
	api     http.Handler
	store   *UiStore
	manager *CrawlManager
	clients map[*wsClient]struct{}
	mu      sync.RWMutex
}

// newUpdater wires an Updater with the running binary's path. Returns a
// configured Updater that defaults to "dev build" if version or executable
// path are unavailable.
func newUpdater(version string) *updater.Updater {
	binPath, err := os.Executable()
	if err != nil {
		binPath = ""
	}
	stateDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		stateDir = home + "/.micelio/ui"
	}
	return updater.New(UpdaterRepo, version, binPath, stateDir)
}

// ServerOptions configures the web server.
type ServerOptions struct {
	Port      int
	Host      string // bind address (default "127.0.0.1")
	AuthToken string // bearer token for API auth (optional)
	Version   string // application version, surfaced via /api/update/status
}

// StartUIServer initializes and starts the web server on the given port.
func StartUIServer(port int, dashboardFS fs.FS) error {
	return StartUIServerWithOptions(ServerOptions{Port: port, Host: "127.0.0.1"}, dashboardFS)
}

// StartUIServerWithOptions initializes and starts the web server with full options.
func StartUIServerWithOptions(opts ServerOptions, dashboardFS fs.FS) error {
	port := opts.Port
	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	authToken := opts.AuthToken

	// Warn if binding to non-localhost without auth
	if host != "127.0.0.1" && host != "localhost" && host != "::1" && authToken == "" {
		log.Println("WARNING: Binding to non-localhost without --auth-token. API endpoints are unauthenticated!")
	}
	store, err := NewUiStore()
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	manager := NewCrawlManager(store)

	upd := newUpdater(opts.Version)
	srv := &Server{
		store:   store,
		manager: manager,
		api:     CreateAPIHandler(store, manager, upd),
		clients: make(map[*wsClient]struct{}),
	}

	// Wire up WebSocket broadcasting
	manager.SetProgressCallback(func(event ProgressEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		srv.broadcast(data)
	})

	mux := http.NewServeMux()

	// WebSocket endpoint — with optional auth
	if authToken != "" {
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			// Allow token via query param for WebSocket connections
			if q := r.URL.Query().Get("token"); q == authToken {
				srv.handleWebSocket(w, r)
				return
			}
			if checkBearerToken(r, authToken) {
				srv.handleWebSocket(w, r)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	} else {
		mux.HandleFunc("/ws", srv.handleWebSocket)
	}

	// API routes — apply auth middleware if token is set
	apiHandler := srv.withCORS(srv.api)
	if authToken != "" {
		apiHandler = srv.withAuth(authToken, apiHandler)
	}
	mux.Handle("/api/", apiHandler)

	// pprof endpoints for profiling (PGO, CPU/memory analysis).
	// Access via: curl http://localhost:3100/debug/pprof/profile?seconds=30 > cpu.pprof
	mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)

	// Static dashboard files
	if dashboardFS != nil {
		srv.serveDashboard(mux, dashboardFS)
	}

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: srv.withHeaders(mux),
	}

	// Graceful shutdown via context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("Shutting down...")
		manager.StopAllSchedulers()
		srv.closeAllClients()
		httpServer.Close()
		store.Close()
	}()

	displayHost := host
	if host == "0.0.0.0" || host == "" {
		displayHost = "localhost"
	}
	log.Printf("Micelio UI: http://%s:%d\n", displayHost, port)
	if authToken != "" {
		log.Printf("Auth: Bearer token required on /api/* and /ws\n")
	}
	return httpServer.ListenAndServe()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &wsClient{conn: conn}

	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()

	// Keep connection alive, clean up on close
	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
		conn.Close()
	}()

	// Set read deadline to detect dead connections
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	// Ping ticker keeps idle connections alive and detects dead ones
	pingTicker := time.NewTicker(wsPingInterval)
	go func() {
		defer pingTicker.Stop()
		for range pingTicker.C {
			client.wmu.Lock()
			err := conn.WriteMessage(websocket.PingMessage, nil)
			client.wmu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	pingTicker.Stop()
}

var clientSnapshotPool = sync.Pool{
	New: func() interface{} {
		s := make([]*wsClient, 0, 8)
		return &s
	},
}

func (s *Server) broadcast(data []byte) {
	s.mu.RLock()
	sp := clientSnapshotPool.Get().(*[]*wsClient)
	snapshot := (*sp)[:0]
	for client := range s.clients {
		snapshot = append(snapshot, client)
	}
	s.mu.RUnlock()

	var failed []*wsClient
	for _, client := range snapshot {
		client.wmu.Lock()
		err := client.conn.WriteMessage(websocket.TextMessage, data)
		client.wmu.Unlock()
		if err != nil {
			failed = append(failed, client)
		}
	}

	// Return snapshot to pool
	*sp = snapshot[:0]
	clientSnapshotPool.Put(sp)

	// Clean up failed clients outside the hot path
	if len(failed) > 0 {
		s.mu.Lock()
		for _, client := range failed {
			delete(s.clients, client)
		}
		s.mu.Unlock()
		for _, client := range failed {
			client.conn.Close()
		}
	}
}

func (s *Server) closeAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.clients {
		client.conn.Close()
		delete(s.clients, client)
	}
}

// withHeaders adds COOP/COEP and common headers.
func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		next.ServeHTTP(w, r)
	})
}

// withCORS adds CORS headers for API routes, restricted to localhost origins.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isLocalOrigin(r) || origin == "" {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3200")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalOrigin checks if the request originates from localhost.
func isLocalOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "https://localhost:") ||
		strings.HasPrefix(origin, "https://127.0.0.1:")
}

// withAuth rejects requests without a valid bearer token.
func (s *Server) withAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Preflight requests don't carry auth
		if r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		if !checkBearerToken(r, token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func checkBearerToken(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	return auth == "Bearer "+token
}

// serveDashboard serves the Svelte SPA from an embedded filesystem.
func (s *Server) serveDashboard(mux *http.ServeMux, dashFS fs.FS) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Skip API and WebSocket routes
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			http.NotFound(w, r)
			return
		}

		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}

		// openFile tries to open the path; falls back to index.html for SPA routing.
		f, stat, resolved := s.openDashboardFile(dashFS, p)
		if f == nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()

		ext := path.Ext(resolved)
		ct := mime.TypeByExtension(ext)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)

		if ext == ".html" || ext == "" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		if rs, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(w, r, resolved, stat.ModTime(), rs)
		} else {
			io.Copy(w, f)
		}
	})
}

// openDashboardFile opens a file from the dashboard FS, falling back to index.html for SPA routes.
func (s *Server) openDashboardFile(dashFS fs.FS, p string) (fs.File, fs.FileInfo, string) {
	f, err := dashFS.Open(p)
	if err == nil {
		stat, err := f.Stat()
		if err == nil && !stat.IsDir() {
			return f, stat, p
		}
		f.Close()
	}
	// SPA fallback
	f, err = dashFS.Open("index.html")
	if err != nil {
		return nil, nil, ""
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, ""
	}
	return f, stat, "index.html"
}
