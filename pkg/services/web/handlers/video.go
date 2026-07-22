package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/mediamtx"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	video_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/video/pages"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

type VideoHandler struct {
	mtxClient *mediamtx.Client
	entitySvc *services.EntityService
}

func NewVideoHandler(mtxClient *mediamtx.Client, entitySvc *services.EntityService) *VideoHandler {
	return &VideoHandler{
		mtxClient: mtxClient,
		entitySvc: entitySvc,
	}
}

// HandleAPIVideoList handles the Datastar SSE stream for the video list
func (h *VideoHandler) HandleAPIVideoList(w http.ResponseWriter, r *http.Request) {
	orgID, err := authorizedOrganizationID(r, "")
	if err != nil {
		writeResourceNotFound(w)
		return
	}

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

	sse := datastar.NewSSE(w, r)

	// Send initial connection signal
	fmt.Fprintf(w, ": SSE connection established\n\n")
	sse.MarshalAndPatchSignals(map[string]interface{}{
		"_isConnected": true,
	})

	// Reset the grid to empty state to prevent duplicates on reconnection
	emptyState := `<div class="empty-state" style="color: #888; padding: 40px; text-align: center; grid-column: 1 / -1;">
						<div style="font-size: 48px; margin-bottom: 10px;">📹</div>
						<p>No active video streams detected.</p>
						<p style="font-size: 12px; margin-top: 10px;">Waiting for entities with video configuration...</p>
					</div>`
	sse.PatchElements(emptyState,
		datastar.WithSelector("#video-grid"),
		datastar.WithModeInner())

	flusher.Flush()

	// Track known streams for add/remove detection
	knownStreams := make(map[string]bool)
	var streamsMutex sync.Mutex

	// Cache entity data from DB to resolve VideoConfig per entity.
	// Refreshed periodically so new entities are picked up.
	entityCache := make(map[string]*ontology.Entity)
	var entityCacheTime time.Time

	refreshEntityCache := func() {
		if time.Since(entityCacheTime) < 30*time.Second {
			return
		}
		var entities []ontology.Entity
		var err error
		if orgID == "" {
			entities, err = h.entitySvc.ListAllEntities()
		} else {
			entities, err = h.entitySvc.ListEntities(orgID)
		}
		if err != nil {
			logger.Warnw("Video handler: failed to refresh entity cache", "error", err)
			return
		}
		next := make(map[string]*ontology.Entity, len(entities))
		for i := range entities {
			next[entities[i].EntityID] = &entities[i]
		}
		entityCache = next
		entityCacheTime = time.Now()
	}

	// buildEntityState constructs an EntityState from the DB entity (if found)
	// with its VideoConfig, falling back to a minimal state using just the stream ID.
	buildEntityState := func(entityID string) shared.EntityState {
		state := shared.EntityState{
			EntityID: entityID,
			Name:     entityID,
		}
		if ent, ok := entityCache[entityID]; ok {
			state.Name = ent.Name
			if state.Name == "" {
				state.Name = entityID
			}
			state.EntityType = ent.EntityType
			state.Status = ent.Status
			state.OrgID = ent.OrgID
			// Parse VideoConfig from DB JSON
			if ent.VideoConfig != "" && ent.VideoConfig != "{}" {
				var vc ontology.VideoConfig
				if json.Unmarshal([]byte(ent.VideoConfig), &vc) == nil {
					state.VideoConfig = &vc
				}
			}
		}
		return state
	}

	ctx := r.Context()

	// Ticker for polling entity video state
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-ticker.C:
			// Refresh entity cache periodically
			refreshEntityCache()

			// Build MediaMTX lookup (may be nil/empty)
			mtxStreams := make(map[string]mediamtx.PathStatus)
			if h.mtxClient != nil {
				for _, s := range h.mtxClient.GetAllStreams() {
					mtxStreams[s.EntityID] = s
				}
			}

			streamsMutex.Lock()

			// Primary source: entities with video_config
			currentActive := make(map[string]bool)
			var activeIDs []string

			for entityID, ent := range entityCache {
				// Parse VideoConfig from DB JSON
				if ent.VideoConfig == "" || ent.VideoConfig == "{}" {
					continue
				}
				var vc ontology.VideoConfig
				if json.Unmarshal([]byte(ent.VideoConfig), &vc) != nil {
					continue
				}
				if vc.PreferredWebRTCURL() == "" {
					continue // no playable URL
				}

				currentActive[entityID] = true
				activeIDs = append(activeIDs, entityID)

				// Use MediaMTX status if available, otherwise synthetic
				status, ok := mtxStreams[entityID]
				if !ok {
					status = mediamtx.PathStatus{
						Name:     entityID,
						EntityID: entityID,
						Ready:    true,
					}
				}

				// New stream: render and append card
				if !knownStreams[entityID] {
					entityState := buildEntityState(entityID)

					var cardHTML strings.Builder
					if err := video_pages.VideoCard(entityState, status).Render(context.Background(), &cardHTML); err == nil {
						if len(knownStreams) == 0 {
							sse.RemoveElement("#video-grid .empty-state")
						}

						sse.PatchElements(cardHTML.String(),
							datastar.WithSelector("#video-grid"),
							datastar.WithModeAppend())

						knownStreams[entityID] = true
					}
				}

				// Update status badge for existing streams
				var badgeHTML strings.Builder
				if err := video_pages.VideoStatusBadge(entityID, status.Ready, status.ReaderCount).Render(context.Background(), &badgeHTML); err == nil {
					sse.PatchElements(badgeHTML.String(),
						datastar.WithSelector(fmt.Sprintf("#video-status-%s", entityID)),
						datastar.WithModeOuter())
				}
			}

			// Remove cards for entities no longer video-capable
			for entityID := range knownStreams {
				if !currentActive[entityID] {
					sse.RemoveElementf("video-card-%s", entityID)
					delete(knownStreams, entityID)
				}
			}

			streamsMutex.Unlock()

			// Update signals
			sse.MarshalAndPatchSignals(map[string]interface{}{
				"activeStreams": activeIDs,
				"streamCount":   len(activeIDs),
				"lastUpdate":    time.Now().Format("15:04:05"),
			})
			flusher.Flush()
		}
	}
}

// HandleAPIVideoStatus returns JSON status of all MediaMTX streams
func (h *VideoHandler) HandleAPIVideoStatus(w http.ResponseWriter, r *http.Request) {
	if h.mtxClient == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"streams":[]}`))
		return
	}

	orgID, err := authorizedOrganizationID(r, "")
	if err != nil {
		writeResourceNotFound(w)
		return
	}

	streams := h.mtxClient.GetAllStreams()
	if orgID != "" {
		entities, listErr := h.entitySvc.ListEntities(orgID)
		if listErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		allowed := make(map[string]struct{}, len(entities))
		for _, entity := range entities {
			allowed[entity.EntityID] = struct{}{}
		}
		filtered := streams[:0]
		for _, stream := range streams {
			if _, ok := allowed[stream.EntityID]; ok {
				filtered = append(filtered, stream)
			}
		}
		streams = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"streams": streams,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		logger.Errorw("Failed to marshal video status", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}
