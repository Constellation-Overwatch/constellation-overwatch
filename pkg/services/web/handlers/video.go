package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/mediamtx"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	common_components "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/common/components"
	video_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/video/pages"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type VideoHandler struct {
	natsEmbedded *embeddednats.EmbeddedNATS
	mtxClient    *mediamtx.Client
}

func NewVideoHandler(natsEmbedded *embeddednats.EmbeddedNATS) *VideoHandler {
	mtxConfig := getMediaMTXConfig()
	return &VideoHandler{
		natsEmbedded: natsEmbedded,
		mtxClient:    mediamtx.NewClient(mtxConfig.APIURL),
	}
}

// getMediaMTXConfig returns MediaMTX configuration from environment
func getMediaMTXConfig() common_components.MediaMTXConfig {
	webrtcURL := os.Getenv("MEDIAMTX_WEBRTC_URL")
	if webrtcURL == "" {
		webrtcURL = "http://localhost:8889"
	}
	apiURL := os.Getenv("MEDIAMTX_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:9997"
	}
	return common_components.MediaMTXConfig{
		WebRTCURL:  webrtcURL,
		APIURL:     apiURL,
		ViewerUser: os.Getenv("MEDIAMTX_VIEWER_USER"),
		ViewerPass: os.Getenv("MEDIAMTX_VIEWER_PASS"),
	}
}

// StreamInfo combines MediaMTX status with entity metadata
type StreamInfo struct {
	EntityID    string
	StationID   string
	Name        string
	EntityType  string
	IsOnline    bool
	Viewers     int
	LastSeen    time.Time
	FromNATS    bool // True if detected via NATS frames
	FromMediaMTX bool // True if detected via MediaMTX API
}

// HandleAPIVideoList handles the Datastar SSE stream for the video list
// Uses hybrid detection: MediaMTX API for online status + NATS for frame activity
func (h *VideoHandler) HandleAPIVideoList(w http.ResponseWriter, r *http.Request) {
	// Verify we have a flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sse := datastar.NewServerSentEventGenerator(w, r)

	// Send initial connection signal
	fmt.Fprintf(w, ": SSE connection established\n\n")
	sse.PatchSignals(map[string]interface{}{
		"_isConnected":   true,
		"mediamtxOnline": false,
	})

	// Reset the grid to loading state
	loadingState := `<div class="empty-state" style="color: #888; padding: 40px; text-align: center; grid-column: 1 / -1;">
						<div style="font-size: 48px; margin-bottom: 10px;">📹</div>
						<p>Connecting to MediaMTX server...</p>
						<p style="font-size: 12px; margin-top: 10px;">Checking for active video streams...</p>
					</div>`
	sse.PatchElements(loadingState,
		datastar.WithSelector("#video-grid"),
		datastar.WithMode(datastar.ElementPatchModeInner))

	flusher.Flush()

	// Track streams from both sources
	streamInfo := make(map[string]*StreamInfo)
	knownStreams := make(map[string]bool)
	var streamsMutex sync.Mutex
	var mediamtxAvailable bool

	nc := h.natsEmbedded.Connection()
	if nc == nil {
		logger.Errorw("NATS not connected", "component", "VideoHandler")
		return
	}

	// Subscribe to NATS video subjects for frame activity detection
	sub, err := nc.Subscribe(shared.SubjectVideoAll, func(msg *nats.Msg) {
		parts := strings.Split(msg.Subject, ".")
		if len(parts) < 3 {
			return
		}
		entityID := parts[len(parts)-1]

		streamsMutex.Lock()
		if info, exists := streamInfo[entityID]; exists {
			info.LastSeen = time.Now()
			info.FromNATS = true
		} else {
			streamInfo[entityID] = &StreamInfo{
				EntityID: entityID,
				LastSeen: time.Now(),
				FromNATS: true,
				IsOnline: true, // Active NATS frames = online
			}
		}
		streamsMutex.Unlock()
	})

	if err != nil {
		logger.Errorw("Failed to subscribe to video subjects", "component", "VideoHandler", "error", err)
		return
	}
	defer sub.Unsubscribe()

	ctx := r.Context()
	mtxConfig := getMediaMTXConfig()

	// MediaMTX polling ticker (every 2 seconds)
	mtxTicker := time.NewTicker(2 * time.Second)
	defer mtxTicker.Stop()

	// UI update ticker (every 1 second)
	uiTicker := time.NewTicker(1 * time.Second)
	defer uiTicker.Stop()

	// Heartbeat ticker
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// Initial MediaMTX poll
	h.pollMediaMTX(ctx, streamInfo, &streamsMutex, &mediamtxAvailable)

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-mtxTicker.C:
			// Poll MediaMTX API for stream status
			h.pollMediaMTX(ctx, streamInfo, &streamsMutex, &mediamtxAvailable)

		case <-uiTicker.C:
			streamsMutex.Lock()
			cutoff := time.Now().Add(-10 * time.Second)

			// Build current active list and update UI
			var currentActive []string
			var onlineCount, offlineCount int

			for entityID, info := range streamInfo {
				// Stream is considered active if:
				// 1. MediaMTX says it's online, OR
				// 2. We've received NATS frames recently
				isActive := info.IsOnline || (info.FromNATS && info.LastSeen.After(cutoff))

				if isActive {
					currentActive = append(currentActive, entityID)
					if info.IsOnline {
						onlineCount++
					}
				}

				// Create or update card
				if !knownStreams[entityID] && isActive {
					// Fetch entity details from KV store
					h.enrichStreamInfo(info)

					var cardHTML strings.Builder
					entityState := shared.EntityState{
						EntityID:   entityID,
						Name:       info.Name,
						EntityType: info.EntityType,
						OrgID:      info.StationID,
						Status:     h.getStreamStatusLabel(info),
					}

					if err := video_pages.VideoCard(entityState, mtxConfig).Render(context.Background(), &cardHTML); err == nil {
						// Remove empty state if first stream
						if len(knownStreams) == 0 {
							sse.PatchElements("", datastar.WithSelector("#video-grid .empty-state"), datastar.WithMode(datastar.ElementPatchModeRemove))
						}

						sse.PatchElements(cardHTML.String(),
							datastar.WithSelector("#video-grid"),
							datastar.WithMode(datastar.ElementPatchModeAppend))

						knownStreams[entityID] = true
						logger.Infow("Added video card", "entity_id", entityID, "online", info.IsOnline, "viewers", info.Viewers)
					}
				} else if knownStreams[entityID] {
					// Update existing card status
					statusClass := "online"
					statusText := "LIVE"
					if !info.IsOnline {
						statusClass = "offline"
						statusText = "OFFLINE"
						offlineCount++
					}

					// Update status indicator via SSE
					statusHTML := fmt.Sprintf(`<span class="stream-status %s">%s</span>`, statusClass, statusText)
					sse.PatchElements(statusHTML,
						datastar.WithSelector(fmt.Sprintf("#video-card-%s .stream-status", entityID)),
						datastar.WithMode(datastar.ElementPatchModeMorph))

					// Update viewer count if available
					if info.Viewers > 0 {
						viewersHTML := fmt.Sprintf(`<span class="viewer-count">👁 %d</span>`, info.Viewers)
						sse.PatchElements(viewersHTML,
							datastar.WithSelector(fmt.Sprintf("#video-card-%s .viewer-count", entityID)),
							datastar.WithMode(datastar.ElementPatchModeMorph))
					}
				}

				// Remove stale streams (not seen in NATS and offline in MediaMTX)
				if !isActive && knownStreams[entityID] {
					sse.PatchElements("",
						datastar.WithSelector(fmt.Sprintf("#video-card-%s", entityID)),
						datastar.WithMode(datastar.ElementPatchModeRemove))
					delete(knownStreams, entityID)
					delete(streamInfo, entityID)
					logger.Infow("Removed stale video card", "entity_id", entityID)
				}
			}
			streamsMutex.Unlock()

			// Show empty state if no streams
			if len(knownStreams) == 0 {
				emptyMsg := "No active video streams detected."
				if !mediamtxAvailable {
					emptyMsg = "MediaMTX server unavailable. Waiting for NATS video frames..."
				}
				emptyState := fmt.Sprintf(`<div class="empty-state" style="color: #888; padding: 40px; text-align: center; grid-column: 1 / -1;">
						<div style="font-size: 48px; margin-bottom: 10px;">📹</div>
						<p>%s</p>
						<p style="font-size: 12px; margin-top: 10px;">Polling MediaMTX at %s</p>
					</div>`, emptyMsg, mtxConfig.APIURL)
				sse.PatchElements(emptyState,
					datastar.WithSelector("#video-grid"),
					datastar.WithMode(datastar.ElementPatchModeInner))
			}

			// Update signals
			sse.PatchSignals(map[string]interface{}{
				"activeStreams":   currentActive,
				"streamCount":     len(currentActive),
				"onlineCount":     onlineCount,
				"offlineCount":    offlineCount,
				"mediamtxOnline":  mediamtxAvailable,
				"lastUpdate":      time.Now().Format("15:04:05"),
			})
			flusher.Flush()
		}
	}
}

// pollMediaMTX queries MediaMTX API and updates stream info
func (h *VideoHandler) pollMediaMTX(ctx context.Context, streamInfo map[string]*StreamInfo, mutex *sync.Mutex, available *bool) {
	statuses, err := h.mtxClient.GetStreamStatuses(ctx)
	if err != nil {
		logger.Debugw("MediaMTX API unavailable", "error", err)
		*available = false
		return
	}

	*available = true
	mutex.Lock()
	defer mutex.Unlock()

	// Mark all existing streams as potentially offline
	for _, info := range streamInfo {
		info.FromMediaMTX = false
	}

	// Update with MediaMTX data
	for entityID, status := range statuses {
		if info, exists := streamInfo[entityID]; exists {
			info.IsOnline = status.IsOnline
			info.Viewers = status.Readers
			info.StationID = status.StationID
			info.FromMediaMTX = true
		} else {
			streamInfo[entityID] = &StreamInfo{
				EntityID:     entityID,
				StationID:    status.StationID,
				IsOnline:     status.IsOnline,
				Viewers:      status.Readers,
				LastSeen:     time.Now(),
				FromMediaMTX: true,
			}
		}
	}

	// Update online status for streams not in MediaMTX response
	for _, info := range streamInfo {
		if !info.FromMediaMTX {
			info.IsOnline = false
		}
	}

	logger.Debugw("MediaMTX poll complete", "streams", len(statuses))
}

// enrichStreamInfo fetches additional entity details from KV store
func (h *VideoHandler) enrichStreamInfo(info *StreamInfo) {
	if h.natsEmbedded == nil {
		return
	}

	kv := h.natsEmbedded.KeyValue()
	if kv == nil {
		return
	}

	entry, err := kv.Get(info.EntityID)
	if err != nil {
		return
	}

	var state shared.EntityState
	if json.Unmarshal(entry.Value(), &state) == nil {
		info.Name = state.Name
		info.EntityType = state.EntityType
		if info.StationID == "" {
			info.StationID = state.OrgID
		}
	}
}

// getStreamStatusLabel returns a status label for the stream
func (h *VideoHandler) getStreamStatusLabel(info *StreamInfo) string {
	if info.IsOnline {
		if info.Viewers > 0 {
			return fmt.Sprintf("live (%d viewers)", info.Viewers)
		}
		return "live"
	}
	return "offline"
}

// HandleAPIVideoStatus returns JSON status of all streams (for AJAX polling)
func (h *VideoHandler) HandleAPIVideoStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statuses, err := h.mtxClient.GetStreamStatuses(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("MediaMTX unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"streams":   statuses,
		"count":     len(statuses),
		"timestamp": time.Now().Format(time.RFC3339),
		"apiUrl":    h.mtxClient.GetAPIURL(),
	})
}
