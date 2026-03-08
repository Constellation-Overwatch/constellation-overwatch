// Package dev provides development-only features like hot reload.
// These features should only be enabled in development mode.
package dev

import (
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
)

// HotReload manages development hot reload functionality.
// It maintains SSE connections to all connected browsers and can trigger
// page reloads when files change.
type HotReload struct {
	reloadChan    chan struct{}
	hotReloadOnce sync.Once
	mu            sync.Mutex
}

// NewHotReload creates a new hot reload manager.
func NewHotReload() *HotReload {
	return &HotReload{
		reloadChan: make(chan struct{}, 1),
	}
}

// HandleReloadSSE handles the SSE connection that triggers page reload.
// Browsers connect to this endpoint and wait for reload signals.
// On initial connection (or reconnection after server restart), it triggers an immediate reload.
func (h *HotReload) HandleReloadSSE(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)

	// Send a comment to establish the connection
	fmt.Fprintf(w, ": hot reload connection established\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Reload function that sends JavaScript to reload the page
	reload := func() {
		sse.ExecuteScript("window.location.reload()")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	// Reload once on initial connection (for reconnects after server restart)
	h.hotReloadOnce.Do(reload)

	// Wait for reload signal or context cancellation
	select {
	case <-h.reloadChan:
		reload()
	case <-r.Context().Done():
		return
	}
}

// HandleTriggerReload triggers a reload for all connected clients.
// Call this endpoint (e.g., via curl) when files change.
func (h *HotReload) HandleTriggerReload(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Reset the once so next connection triggers reload
	h.hotReloadOnce = sync.Once{}

	// Send reload signal (non-blocking)
	select {
	case h.reloadChan <- struct{}{}:
	default:
		// Channel full, reload already pending
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// SetupRoutes registers hot reload routes on the given mux.
// Only call this in development mode.
func (h *HotReload) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dev/reload", h.HandleReloadSSE)
	mux.HandleFunc("/dev/trigger-reload", h.HandleTriggerReload)
}

// IsDev returns true if the application is running in development mode.
// Checks GO_ENV environment variable. Requires explicit "development" or "dev" value.
func IsDev() bool {
	env := os.Getenv("GO_ENV")
	return env == "development" || env == "dev"
}

// DevReloadScript returns the Datastar attribute for hot reload.
// Use this in templates to enable hot reload in dev mode.
func DevReloadScript() string {
	if !IsDev() {
		return ""
	}
	return "@get('/dev/reload', {retryMaxCount: 1000, retryInterval: 100, retryMaxWaitMs: 500})"
}
