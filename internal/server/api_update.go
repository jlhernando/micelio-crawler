package server

import (
	"context"
	"net/http"
	"time"

	"github.com/micelio/micelio/internal/updater"
)

// statusRequestTimeout caps how long the GitHub fetch can take per request,
// independent of the updater's internal client timeout.
const statusRequestTimeout = 12 * time.Second

// installRequestTimeout caps the install request — the actual download has a
// longer internal timeout, but we don't want a hung browser request blocking
// the UI indefinitely.
const installRequestTimeout = 6 * time.Minute

func registerUpdateRoutes(mux *http.ServeMux, upd *updater.Updater) {
	if upd == nil {
		return
	}

	// GET /api/update/status — cached daily check, fast for UI polling.
	mux.HandleFunc("GET /api/update/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), statusRequestTimeout)
		defer cancel()
		st, err := upd.Status(ctx, false)
		if err != nil && st == nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		jsonOK(w, st)
	})

	// POST /api/update/check — bypass cache, force a network refresh.
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), statusRequestTimeout)
		defer cancel()
		st, err := upd.Status(ctx, true)
		if err != nil && st == nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		jsonOK(w, st)
	})

	// POST /api/update/install — download, verify checksum, and atomic-swap the binary.
	mux.HandleFunc("POST /api/update/install", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), installRequestTimeout)
		defer cancel()
		st, err := upd.Install(ctx)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		jsonOK(w, map[string]interface{}{
			"installed":       true,
			"restartRequired": true,
			"status":          st,
		})
	})

	// POST /api/update/rollback — restore the previous binary from .bak file.
	mux.HandleFunc("POST /api/update/rollback", func(w http.ResponseWriter, r *http.Request) {
		if err := upd.Rollback(); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]interface{}{
			"rolledBack":      true,
			"restartRequired": true,
		})
	})
}
