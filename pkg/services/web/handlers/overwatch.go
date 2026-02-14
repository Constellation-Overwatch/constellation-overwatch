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

	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	common_components "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/common/components"
	overwatch "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/overwatch/components"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/signals"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats.go"
)

// getMediaMTXConfigOverwatch returns MediaMTX configuration from environment
func getMediaMTXConfigOverwatch() common_components.MediaMTXConfig {
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

type OverwatchHandler struct {
	natsEmbedded *embeddednats.EmbeddedNATS
	orgSvc       *services.OrganizationService
}

func NewOverwatchHandler(natsEmbedded *embeddednats.EmbeddedNATS, orgSvc *services.OrganizationService) *OverwatchHandler {
	return &OverwatchHandler{
		natsEmbedded: natsEmbedded,
		orgSvc:       orgSvc,
	}
}

// API handler for Overwatch KV store
func (h *OverwatchHandler) HandleAPIOverwatchKV(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all keys from the KV store
	kv := h.natsEmbedded.KeyValue()
	if kv == nil {
		http.Error(w, "KV store not initialized", http.StatusInternalServerError)
		return
	}

	// Get all keys using Keys() method
	keys, err := kv.Keys()
	if err != nil {
		logger.Infow("Error fetching KV keys: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch all entries
	var kvEntries []overwatch.KVEntry
	for _, key := range keys {
		entry, err := kv.Get(key)
		if err != nil {
			logger.Infow("Error getting key %s: %v", key, err)
			continue
		}

		kvEntries = append(kvEntries, overwatch.KVEntry{
			Key:      key,
			Value:    string(entry.Value()),
			Revision: fmt.Sprintf("%d", entry.Revision()),
			Updated:  entry.Created().Format("15:04:05"),
		})
	}

	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewServerSentEventGenerator(w, r)
		component := overwatch.KVStateTable(kvEntries)
		err := sse.PatchComponent(r.Context(), component,
			datastar.WithSelector("#kv-content"),
			datastar.WithMode(datastar.ElementPatchModeInner))
		if err != nil {
			logger.Infow("Error patching KV content: %v", err)
		}
		return
	}

	// Otherwise return JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": kvEntries,
	})
}

// API handler for real-time KV watching via SSE
func (h *OverwatchHandler) HandleAPIOverwatchKVWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify we have a flusher (required for SSE)
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Infow("[Overwatch] ERROR: ResponseWriter does not support flushing (SSE won't work)")
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// CRITICAL: Set SSE headers BEFORE creating SSE generator or writing anything
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	logger.Infow("[Overwatch] ✓ SSE headers set, establishing connection", "remote_addr", r.RemoteAddr)

	// Create SSE generator AFTER setting headers
	sse := datastar.NewServerSentEventGenerator(w, r)

	// Determine view mode
	viewMode := r.URL.Query().Get("view")

	// Mutex to synchronize writes to ResponseWriter from multiple goroutines
	var writeMutex sync.Mutex

	// Send an immediate comment to establish the SSE stream in the browser
	writeMutex.Lock()
	fmt.Fprintf(w, ": SSE connection established\n\n")

	// Reset the entities container to empty state to prevent duplicates on reconnection
	emptyState := `<div class="empty-state" style="color: #888; padding: 40px; text-align: center;">
					<p>No entity states in global store. Waiting for telemetry data...</p>
					<p style="font-size: 10px; margin-top: 10px;">Server-side rendering via SSE</p>
				</div>`
	if viewMode == "map" {
		sse.PatchElements(emptyState,
			datastar.WithSelector("#entity-list"),
			datastar.WithMode(datastar.ElementPatchModeInner))
	} else {
		sse.PatchElements(emptyState,
			datastar.WithSelector("#entities-container"),
			datastar.WithMode(datastar.ElementPatchModeInner))
	}

	flusher.Flush()
	writeMutex.Unlock()
	logger.Infow("SSE client connected", "component", "Overwatch", "remote_addr", r.RemoteAddr)

	// Local cache of all KV data: entityID -> key -> data
	// This allows us to reconstruct a single entity's state without fetching everything
	localEntityCache := make(map[string]map[string][]byte)

	// Track known entities (ID -> OrgID) to handle cleanup
	knownEntities := make(map[string]string)
	knownOrgs := make(map[string]bool)

	// Struct to pass data to renderer
	type RenderPayload struct {
		Snapshot      []shared.EntityState
		RemovedIDs    []string
		TotalEntities int
	}

	// Channel to buffer updates from NATS to SSE
	// Increased buffer size to handle initial state dump and high throughput
	updateChan := make(chan nats.KeyValueEntry, 10000)

	// Channel to send snapshots to the renderer
	// Buffer of 1 allows us to have one snapshot pending while the renderer is busy.
	// If the renderer is too slow, we drop intermediate snapshots (conflation).
	renderChan := make(chan RenderPayload, 1)

	// Load initial KV data before starting watcher
	ctx := r.Context()
	go func() {
		defer close(updateChan)

		// STEP 1: Load existing KV data on initial connection
		logger.Infow("[Overwatch] Loading initial KV state...")
		if initialEntries, err := h.natsEmbedded.GetAllKVEntries(); err == nil {
			logger.Infow("[Overwatch] Loaded initial state", "kv_entries", len(initialEntries))

			// Send existing entries through the update channel to populate initial state
			for _, entry := range initialEntries {
				select {
				case updateChan <- entry:
					// Successfully queued initial entry
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					// Skip if channel is backed up during initial load
					logger.Debugw("[Overwatch] Skipped initial entry due to channel backup", "key", entry.Key())
				}
			}
			logger.Infow("[Overwatch] Initial state load completed", "entities_loaded", len(initialEntries))
		} else {
			logger.Warnw("[Overwatch] Failed to load initial KV state", "error", err)
		}

		// STEP 2: Start watching for real-time changes
		// Retry loop for the watcher
		for {
			if ctx.Err() != nil {
				return
			}

			logger.Infow("[Overwatch] KV watcher goroutine started, waiting for changes...")

			watchErr := h.natsEmbedded.WatchKV(ctx, func(key string, entry nats.KeyValueEntry) error {
				select {
				case updateChan <- entry:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})

			if ctx.Err() != nil {
				return
			}

			if watchErr != nil {
				logger.Warnw("[Overwatch] KV watcher stopped unexpectedly, restarting...", "error", watchErr)
			} else {
				logger.Warnw("[Overwatch] KV watcher channel closed unexpectedly, restarting...")
			}

			select {
			case <-time.After(1 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start Renderer Goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Debugw("Renderer goroutine stopping - context canceled")
				return
			case payload, ok := <-renderChan:
				if !ok {
					logger.Debugw("Renderer goroutine stopping - render channel closed")
					return
				}

				// Check if context is still valid before rendering
				if ctx.Err() != nil {
					logger.Debugw("Context canceled, skipping render")
					return
				}

				// Render and Flush
				h.renderAndFlushSnapshot(w, flusher, &writeMutex, sse, payload.Snapshot, payload.RemovedIDs, payload.TotalEntities, knownEntities, knownOrgs, viewMode)
			}
		}
	}()

	// Keep the connection alive with heartbeats
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Flush ticker for batching
	flushTicker := time.NewTicker(50 * time.Millisecond)
	defer flushTicker.Stop()

	dirtyEntities := make(map[string]bool)

	// State Manager Loop (Main Goroutine)
	// This loop MUST be fast to keep up with NATS.
	// It does NO rendering.
	for {
		select {
		case <-ctx.Done():
			logger.Infow("[Overwatch] Client disconnected", "remote_addr", r.RemoteAddr)
			return

		case <-ticker.C:
			// Send heartbeat only if connection is still valid
			if flusher != nil {
				writeMutex.Lock()
				if flusher != nil { // Double-check after acquiring lock
					fmt.Fprintf(w, ": heartbeat\n\n")
					func() {
						defer func() {
							if r := recover(); r != nil {
								logger.Debugw("Recovered from heartbeat flush panic", "panic", r)
							}
						}()
						flusher.Flush()
					}()
				}
				writeMutex.Unlock()
			}

		case <-flushTicker.C:
			// Periodic flush of dirty entities
			if len(dirtyEntities) > 0 {
				// Create snapshot and track removals
				var snapshot []shared.EntityState
				var removedIDs []string

				for entityID := range dirtyEntities {
					entityData, exists := localEntityCache[entityID]
					if !exists {
						removedIDs = append(removedIDs, entityID)
						continue
					}
					// Reconstruct state (fast, just map lookups and struct creation)
					entityState := h.mergeEntityData(entityID, entityData)
					snapshot = append(snapshot, entityState)
				}

				// Try to send to renderer (non-blocking)
				payload := RenderPayload{
					Snapshot:      snapshot,
					RemovedIDs:    removedIDs,
					TotalEntities: len(localEntityCache),
				}

				select {
				case renderChan <- payload:
					// Success, renderer will handle it
					dirtyEntities = make(map[string]bool)
				default:
					// Renderer is busy, skip this frame (conflation)
					// We keep the entities dirty so they are included in the next snapshot
					logger.Debugw("[Overwatch] Renderer busy, skipping frame (conflation)", "pending_entities", len(dirtyEntities))
				}
			}

		case entry, ok := <-updateChan:
			if !ok {
				logger.Infow("[Overwatch] Update channel closed, stopping SSE stream")
				return
			}

			// Process KV entry with enhanced signal handling
			key := entry.Key()
			if key == "" {
				logger.Warnw("[Overwatch] Received entry with empty key, skipping")
				continue
			}

			parts := strings.Split(key, ".")
			if len(parts) == 0 {
				logger.Warnw("[Overwatch] Invalid key format", "key", key)
				continue
			}

			entityID := parts[0]
			if entityID == "" {
				logger.Warnw("[Overwatch] Empty entity ID", "key", key)
				continue
			}

			// Initialize entity cache if needed
			if localEntityCache[entityID] == nil {
				localEntityCache[entityID] = make(map[string][]byte)
				logger.Debugw("[Overwatch] Initialized cache for new entity", "entity_id", entityID)
			}

			// Handle delete operations
			if entry.Operation() == nats.KeyValueDelete || entry.Operation() == nats.KeyValuePurge {
				delete(localEntityCache[entityID], key)
				if len(localEntityCache[entityID]) == 0 {
					delete(localEntityCache, entityID)
					logger.Debugw("[Overwatch] Removed entity from cache", "entity_id", entityID)
				} else {
					logger.Debugw("[Overwatch] Removed signal from entity", "entity_id", entityID, "key", key)
				}
			} else {
				// Handle create/update operations
				value := entry.Value()
				if len(value) == 0 {
					logger.Debugw("[Overwatch] Received empty value", "key", key)
					// Store empty value to indicate signal exists but has no data
					localEntityCache[entityID][key] = []byte("{}")
				} else {
					// Validate JSON before storing
					var testJSON interface{}
					if err := json.Unmarshal(value, &testJSON); err != nil {
						logger.Warnw("[Overwatch] Invalid JSON in KV entry, storing as-is", "key", key, "error", err)
						// Store anyway - merge functions will handle gracefully
					}
					localEntityCache[entityID][key] = value
					logger.Debugw("[Overwatch] Updated entity signal", "entity_id", entityID, "key", key, "size", len(value))

					// Log mavlink data for debugging
					if strings.HasSuffix(key, ".mavlink") {
						previewLen := 200
						if len(value) < previewLen {
							previewLen = len(value)
						}
						logger.Infow("[Overwatch] NEW MAVLink data received", "entity_id", entityID, "key", key, "data_preview", string(value)[:previewLen])
					}
				}
			}

			// Mark entity as dirty for rendering
			dirtyEntities[entityID] = true
		}
	}
}

// Helper to render and flush a snapshot
func (h *OverwatchHandler) renderAndFlushSnapshot(w http.ResponseWriter, flusher http.Flusher, writeMutex *sync.Mutex, sse *datastar.ServerSentEventGenerator, snapshot []shared.EntityState, removedIDs []string, totalEntities int, knownEntities map[string]string, knownOrgs map[string]bool, viewMode string) {
	// Check if flusher is nil to prevent panic
	if flusher == nil {
		logger.Debugw("Flusher is nil, connection likely closed, skipping render")
		return
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

	// Double-check flusher after acquiring mutex
	if flusher == nil {
		logger.Debugw("Flusher became nil while waiting for mutex, connection closed")
		return
	}

	updatesSent := 0

	for _, entityState := range snapshot {
		// Render card based on view mode
		var cardHTML strings.Builder
		if viewMode == "map" {
			// Map View: Use C4EntityCard (Simple Mode initially)
			// Selector: #c4-entity-{id}
			// Container: #entity-list
			if err := common_components.C4EntityCard(entityState, false).Render(context.Background(), &cardHTML); err != nil {
				logger.Errorw("Error rendering C4 entity card", "error", err)
				continue
			}
		} else {
			// Default Overwatch View
			if err := overwatch.EntityCard(entityState).Render(context.Background(), &cardHTML); err != nil {
				logger.Errorw("Error rendering entity card", "error", err)
				continue
			}
		}

		// Determine patch mode
		var patchMode datastar.PatchElementMode
		var selector string
		var containerSelector string
		entityID := entityState.EntityID

		if viewMode == "map" {
			containerSelector = "#entity-list"
		} else {
			containerSelector = "#entities-container"
		}

		if _, exists := knownEntities[entityID]; !exists {
			// New entity
			// Handle Org Headers only for Overwatch Dashboard
			if viewMode != "map" && !knownOrgs[entityState.OrgID] {
				// Create Org Container
				if len(knownOrgs) == 0 {
					// Use specific selector to only target empty state within our container
					if err := sse.PatchElements("", datastar.WithSelector(containerSelector+" .empty-state"), datastar.WithMode(datastar.ElementPatchModeRemove)); err != nil {
						logger.Debugw("Failed to patch empty state, connection may be closed", "error", err)
						return
					}
				}

				var orgHTML strings.Builder
				orgName := entityState.OrgID
				if entityState.OrgName != "" {
					orgName = entityState.OrgName
				}
				orgHTML.WriteString(fmt.Sprintf(`<div class="org-section"><div class="org-header">Organization: %s</div></div>`, orgName))

				if err := sse.PatchElements(orgHTML.String(), datastar.WithSelector(containerSelector), datastar.WithMode(datastar.ElementPatchModeAppend)); err != nil {
					logger.Debugw("Failed to patch org container, connection may be closed", "error", err)
					return
				}
				knownOrgs[entityState.OrgID] = true

				// Initialize signal (Same signal for both views)
				if err := sse.PatchSignals(map[string]interface{}{
					fmt.Sprintf("entityStatesByOrg.%s", entityState.OrgID): map[string]interface{}{},
				}); err != nil {
					logger.Debugw("Failed to patch org signals, connection may be closed", "error", err)
					return
				}
			} else if viewMode == "map" {
				// For map, remove empty state if first entity
				if len(knownEntities) == 0 {
					if err := sse.PatchElements("", datastar.WithSelector(containerSelector+" .empty-state"), datastar.WithMode(datastar.ElementPatchModeRemove)); err != nil {
						// Ignore error as empty state might not exist
					}
				}
			}

			patchMode = datastar.ElementPatchModeAppend
			selector = containerSelector
			knownEntities[entityID] = entityState.OrgID
		} else {
			patchMode = datastar.ElementPatchModeMorph
			if viewMode == "map" {
				selector = fmt.Sprintf("#c4-entity-%s", entityID)
			} else {
				selector = fmt.Sprintf("#entity-%s", entityID)
			}
			// Update known org just in case it changed (unlikely but possible)
			knownEntities[entityID] = entityState.OrgID
		}

		// Patch Element
		if err := sse.PatchElements(cardHTML.String(), datastar.WithSelector(selector), datastar.WithMode(patchMode)); err != nil {
			logger.Debugw("Failed to patch entity, connection may be closed", "entity_id", entityID, "error", err)
			return
		}

		// For new entities (append mode), also append the video player component separately
		// This ensures video is only added once and never morphed, preventing connection duplication
		if patchMode == datastar.ElementPatchModeAppend && viewMode == "map" {
			var videoHTML strings.Builder
			mtxConfig := getMediaMTXConfigOverwatch()
			// Pass stationID (OrgID), entityID, and config for path: /{station_id}/{entity_id} with optional auth
			if err := common_components.C4VideoPlayer(entityState.OrgID, entityID, mtxConfig).Render(context.Background(), &videoHTML); err == nil {
				videoSelector := fmt.Sprintf("#video-section-%s", entityID)
				if err := sse.PatchElements(videoHTML.String(), datastar.WithSelector(videoSelector), datastar.WithMode(datastar.ElementPatchModeInner)); err != nil {
					logger.Debugw("Failed to append video player", "entity_id", entityID, "error", err)
				}
			}
		}

		// Patch Signal with typed entity metadata (not the full state - that's too large!)
		// The full entity data is already rendered server-side in the card HTML
		entitySignal := buildEntitySignal(entityID, entityState)

		if err := sse.PatchSignals(map[string]interface{}{
			fmt.Sprintf("entityStatesByOrg.%s.%s", entityState.OrgID, entityID): entitySignal,
		}); err != nil {
			logger.Debugw("Failed to patch entity signals, connection may be closed", "entity_id", entityID, "error", err)
			return
		}

		updatesSent++
	}

	// Process Removed IDs
	for _, entityID := range removedIDs {
		// Only remove if we knew about it
		if orgID, known := knownEntities[entityID]; known {
			logger.Infow("[Overwatch] Removing entity", "entity_id", entityID)

			// Remove from DOM
			var selector string
			if viewMode == "map" {
				selector = fmt.Sprintf("#c4-entity-%s", entityID)
			} else {
				selector = fmt.Sprintf("#entity-%s", entityID)
			}

			if err := sse.PatchElements("", datastar.WithSelector(selector), datastar.WithMode(datastar.ElementPatchModeRemove)); err != nil {
				logger.Debugw("Failed to remove entity element", "error", err)
			}

			// Remove from Signal (set to null)
			if err := sse.PatchSignals(map[string]interface{}{
				fmt.Sprintf("entityStatesByOrg.%s.%s", orgID, entityID): nil,
			}); err != nil {
				logger.Debugw("Failed to update signal for removed entity", "error", err)
			}

			delete(knownEntities, entityID)
			updatesSent++
		}
	}

	if updatesSent > 0 {
		// Get total orgs from DB for accurate count
		orgs, _ := h.orgSvc.ListOrganizations()
		totalOrgs := len(orgs)

		// Compute analytics from the current snapshot
		analytics := h.computeAnalyticsTyped(snapshot)

		// Use typed dashboard signals
		dashboardSig := signals.DashboardSignals{
			LastUpdate:    time.Now().Format("15:04:05"),
			TotalEntities: totalEntities,
			TotalOrgs:     totalOrgs,
			IsConnected:   true,
			Analytics:     analytics,
		}

		if err := datastar.MarshalAndPatchSignals(sse, dashboardSig); err != nil {
			logger.Debugw("Failed to patch final signals, connection may be closed", "error", err)
			return
		}

		// Safe flush with recovery
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Debugw("Recovered from flush panic, connection likely closed", "panic", r)
				}
			}()
			if flusher != nil {
				flusher.Flush()
			}
		}()
	}
}

// API handler for debugging KV data structure
func (h *OverwatchHandler) HandleAPIOverwatchKVDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all KV entries
	entries, err := h.natsEmbedded.GetAllKVEntries()
	if err != nil {
		logger.Infof("[Overwatch Debug] Error fetching KV entries: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Parse into entity states
	entityStatesByOrg := h.parseKVEntriesToEntityStates(entries)

	// Create the same structure we send via SSE
	response := map[string]interface{}{
		"entityStatesByOrg": entityStatesByOrg,
		"lastUpdate":        time.Now().Format("15:04:05"),
		"_isConnected":      true,
		"totalOrgs":         len(entityStatesByOrg),
		"totalEntities":     0,
	}

	for _, entities := range entityStatesByOrg {
		response["totalEntities"] = response["totalEntities"].(int) + len(entities)
	}

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		logger.Infof("[Overwatch Debug] Error encoding JSON: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// parseKVEntriesToEntityStates parses KV entries and aggregates them by entity_id
func (h *OverwatchHandler) parseKVEntriesToEntityStates(entries []nats.KeyValueEntry) map[string][]shared.EntityState {
	// First, group entries by entity_id
	entitiesByID := make(map[string]map[string][]byte)

	for _, entry := range entries {
		key := entry.Key()

		// Extract entity_id from key (before first dot or entire key if no dot)
		parts := strings.Split(key, ".")
		entityID := parts[0]

		if entitiesByID[entityID] == nil {
			entitiesByID[entityID] = make(map[string][]byte)
		}

		// Store raw data keyed by full key for later processing
		entitiesByID[entityID][key] = entry.Value()
	}

	// Now build consolidated EntityState objects
	entityStatesByOrg := make(map[string][]shared.EntityState)

	logger.Infow("[Overwatch] Aggregating entities from KV entries", "entity_count", len(entitiesByID), "kv_entry_count", len(entries))

	for entityID, dataMap := range entitiesByID {
		logger.Infow("[Overwatch] Processing entity", "entity_id", entityID, "kv_entry_count", len(dataMap))
		entityState := h.mergeEntityData(entityID, dataMap)

		// Group by org_id
		orgID := entityState.OrgID
		if orgID == "" {
			orgID = "unknown"
		}

		entityStatesByOrg[orgID] = append(entityStatesByOrg[orgID], entityState)
	}

	logger.Infow("[Overwatch] Built entities", "total_entities", len(entitiesByID), "org_count", len(entityStatesByOrg))
	return entityStatesByOrg
}

// mergeEntityData merges separate KV entries into a single EntityState
func (h *OverwatchHandler) mergeEntityData(entityID string, dataMap map[string][]byte) shared.EntityState {
	state := shared.EntityState{
		EntityID:   entityID,
		EntityType: "sensor", // Default type for detection entities
		Status:     "active",
		Priority:   "normal",
		IsLive:     true,
		Components: make(map[string]interface{}),
		Aliases:    make(map[string]string),
		Tags:       []string{},
		Metadata:   make(map[string]interface{}),
		UpdatedAt:  time.Now(),
	}

	// Process each key and merge data
	for key, data := range dataMap {
		// Skip empty data
		if len(data) == 0 {
			continue
		}

		var rawData map[string]interface{}
		if err := json.Unmarshal(data, &rawData); err != nil {
			logger.Warnf("[Overwatch] Failed to unmarshal key %s: %v", key, err)
			continue
		}

		// Extract org_id (check both org_id and organization_id)
		if orgID, ok := rawData["org_id"].(string); ok && orgID != "" {
			state.OrgID = orgID
		}
		if orgID, ok := rawData["organization_id"].(string); ok && orgID != "" {
			state.OrgID = orgID
		}

		// Extract device_id if present
		if deviceID, ok := rawData["device_id"].(string); ok && deviceID != "" {
			state.DeviceID = deviceID
		}

		// Extract entity_type if present
		if entityType, ok := rawData["entity_type"].(string); ok && entityType != "" {
			state.EntityType = entityType
		}

		// Extract name if present
		if name, ok := rawData["name"].(string); ok && name != "" {
			state.Name = name
		}

		// Extract status if present
		if status, ok := rawData["status"].(string); ok && status != "" {
			state.Status = status
		}

		// Extract priority if present
		if priority, ok := rawData["priority"].(string); ok && priority != "" {
			state.Priority = priority
		}

		// Extract is_live if present
		if isLive, ok := rawData["is_live"].(bool); ok {
			state.IsLive = isLive
		}

		// Determine data type from key suffix and merge accordingly
		if strings.Contains(key, ".detections.objects") {
			h.mergeDetections(&state, rawData)
		} else if strings.Contains(key, ".analytics.summary") || strings.Contains(key, ".analytics.c4isr_summary") {
			h.mergeAnalytics(&state, rawData)
		} else if strings.Contains(key, ".c4isr.threat_intelligence") {
			h.mergeThreatIntel(&state, rawData)
		} else if strings.HasSuffix(key, ".mavlink") {
			// NEW: Handle flattened mavlink data (entity_id.mavlink)
			h.mergeNewMAVLinkData(&state, rawData)
		} else if !strings.Contains(key, ".") {
			// Single-key entity state (like device.1.1 or full EntityState)
			h.mergeFullState(&state, rawData)
		}
	}

	return state
}

// mergeDetections merges detection data into EntityState
func (h *OverwatchHandler) mergeDetections(state *shared.EntityState, data map[string]interface{}) {
	if trackedObjects, ok := data["tracked_objects"].(map[string]interface{}); ok {
		detectionState := &shared.DetectionState{
			TrackedObjects: make(map[string]shared.TrackedObject),
			Timestamp:      time.Now(),
		}

		for trackID, objData := range trackedObjects {
			if objMap, ok := objData.(map[string]interface{}); ok {
				trackedObj := shared.TrackedObject{
					TrackID:  trackID,
					IsActive: false,
				}

				if label, ok := objMap["label"].(string); ok {
					trackedObj.Label = label
				}
				if conf, ok := objMap["avg_confidence"].(float64); ok {
					trackedObj.AvgConfidence = conf
				}
				if active, ok := objMap["is_active"].(bool); ok {
					trackedObj.IsActive = active
				}
				if threat, ok := objMap["threat_level"].(string); ok {
					trackedObj.ThreatLevel = threat
				}
				if frames, ok := objMap["frame_count"].(float64); ok {
					trackedObj.FrameCount = int(frames)
				}

				detectionState.TrackedObjects[trackID] = trackedObj
			}
		}

		state.Detections = detectionState
	}
}

// mergeAnalytics merges analytics data into EntityState
func (h *OverwatchHandler) mergeAnalytics(state *shared.EntityState, data map[string]interface{}) {
	analyticsState := &shared.AnalyticsState{
		Timestamp: time.Now(),
	}

	if val, ok := data["total_unique_objects"].(float64); ok {
		analyticsState.TotalUniqueObjects = int(val)
	}
	if val, ok := data["total_frames_processed"].(float64); ok {
		analyticsState.TotalFramesProcessed = int(val)
	}
	if val, ok := data["active_objects_count"].(float64); ok {
		analyticsState.ActiveObjectsCount = int(val)
	}
	if val, ok := data["tracked_objects_count"].(float64); ok {
		analyticsState.TrackedObjectsCount = int(val)
	}
	if val, ok := data["active_threat_count"].(float64); ok {
		analyticsState.ActiveThreatCount = int(val)
	}
	if labels, ok := data["label_distribution"].(map[string]interface{}); ok {
		analyticsState.LabelDistribution = make(map[string]int)
		for k, v := range labels {
			if num, ok := v.(float64); ok {
				analyticsState.LabelDistribution[k] = int(num)
			}
		}
	}
	if threats, ok := data["threat_distribution"].(map[string]interface{}); ok {
		analyticsState.ThreatDistribution = make(map[string]int)
		for k, v := range threats {
			if num, ok := v.(float64); ok {
				analyticsState.ThreatDistribution[k] = int(num)
			}
		}
	}
	if ids, ok := data["active_track_ids"].([]interface{}); ok {
		for _, id := range ids {
			if str, ok := id.(string); ok {
				analyticsState.ActiveTrackIDs = append(analyticsState.ActiveTrackIDs, str)
			}
		}
	}

	state.Analytics = analyticsState
}

// mergeThreatIntel merges threat intelligence data into EntityState
func (h *OverwatchHandler) mergeThreatIntel(state *shared.EntityState, data map[string]interface{}) {
	threatIntel := &shared.ThreatIntelState{
		Timestamp: time.Now(),
	}

	if mission, ok := data["mission"].(string); ok {
		threatIntel.Mission = mission
	}

	if summary, ok := data["threat_summary"].(map[string]interface{}); ok {
		threatSummary := &shared.ThreatSummary{}

		if total, ok := summary["total_threats"].(float64); ok {
			threatSummary.TotalThreats = int(total)
		}
		if alert, ok := summary["alert_level"].(string); ok {
			threatSummary.AlertLevel = alert
		}
		if dist, ok := summary["threat_distribution"].(map[string]interface{}); ok {
			threatSummary.ThreatDistribution = make(map[string]int)
			for k, v := range dist {
				if num, ok := v.(float64); ok {
					threatSummary.ThreatDistribution[k] = int(num)
				}
			}
		}

		threatIntel.ThreatSummary = threatSummary
	}

	state.ThreatIntel = threatIntel
}

// mergeFullState merges full entity state data (Python + TelemetryWorker consolidated format)
func (h *OverwatchHandler) mergeFullState(state *shared.EntityState, data map[string]interface{}) {
	// Extract core fields (check both org_id and organization_id)
	if orgID, ok := data["org_id"].(string); ok && orgID != "" {
		state.OrgID = orgID
	}
	if orgID, ok := data["organization_id"].(string); ok && orgID != "" {
		state.OrgID = orgID
	}

	// Extract Name and OrgName if present
	if name, ok := data["name"].(string); ok && name != "" {
		state.Name = name
	}
	if orgName, ok := data["org_name"].(string); ok && orgName != "" {
		state.OrgName = orgName
	}

	// Extract entity_type if present
	if entityType, ok := data["entity_type"].(string); ok && entityType != "" {
		state.EntityType = entityType
	}

	// Extract status if present
	if status, ok := data["status"].(string); ok && status != "" {
		state.Status = status
	}

	// Extract priority if present
	if priority, ok := data["priority"].(string); ok && priority != "" {
		state.Priority = priority
	}

	// Extract is_live if present
	if isLive, ok := data["is_live"].(bool); ok {
		state.IsLive = isLive
	}

	if state.OrgID == "" {
		logger.Infow("[Overwatch] mergeFullState: WARNING - No org_id or organization_id in data")
	}

	// Python detection service format (NEW): detections.tracked_objects
	if detectionsData, ok := data["detections"].(map[string]interface{}); ok {
		// Check for tracked_objects (new format)
		if trackedObjects, ok := detectionsData["tracked_objects"].(map[string]interface{}); ok {
			h.mergeDetections(state, map[string]interface{}{"tracked_objects": trackedObjects})
			logger.Infof("[Overwatch] Merged detections.tracked_objects with %d tracked objects", len(trackedObjects))
		} else if objectsData, ok := detectionsData["objects"].(map[string]interface{}); ok {
			// Fallback to old format: detections.objects
			h.mergeDetections(state, map[string]interface{}{"tracked_objects": objectsData})
			logger.Infof("[Overwatch] Merged detections.objects with %d tracked objects", len(objectsData))
		}

		// Check for analytics nested inside detections (new format)
		if analyticsData, ok := detectionsData["analytics"].(map[string]interface{}); ok {
			h.mergeAnalytics(state, analyticsData)
			logger.Infow("[Overwatch] Merged detections.analytics")
		}
	}

	// Python analytics format (OLD): top-level analytics.summary
	if analyticsData, ok := data["analytics"].(map[string]interface{}); ok {
		if summaryData, ok := analyticsData["summary"].(map[string]interface{}); ok {
			h.mergeAnalytics(state, summaryData)
			logger.Infow("[Overwatch] Merged analytics.summary")
		}
	}

	// Python threat intelligence format (NEW): top-level threat_intelligence
	if threatData, ok := data["threat_intelligence"].(map[string]interface{}); ok {
		h.mergeThreatIntel(state, threatData)
		logger.Infow("[Overwatch] Merged threat_intelligence")
	}

	// Python C4ISR format (OLD): c4isr.threat_intelligence
	if c4isrData, ok := data["c4isr"].(map[string]interface{}); ok {
		if threatData, ok := c4isrData["threat_intelligence"].(map[string]interface{}); ok {
			h.mergeThreatIntel(state, threatData)
			logger.Infow("[Overwatch] Merged c4isr.threat_intelligence")
		}
	}

	// Try to unmarshal entire object for telemetry fields (from TelemetryWorker)
	jsonData, _ := json.Marshal(data)
	var fullState shared.EntityState
	if err := json.Unmarshal(jsonData, &fullState); err == nil {
		// Merge telemetry fields
		if fullState.Position != nil {
			state.Position = fullState.Position
		}
		if fullState.Attitude != nil {
			state.Attitude = fullState.Attitude
		}
		if fullState.Power != nil {
			state.Power = fullState.Power
		}
		if fullState.VFR != nil {
			state.VFR = fullState.VFR
		}
		if fullState.VehicleStatus != nil {
			state.VehicleStatus = fullState.VehicleStatus
		}
		if fullState.Mission != nil {
			state.Mission = fullState.Mission
		}
	}
}

// MAVLink signal merge functions for modular telemetry streams

// mergeNewMAVLinkData merges the new flattened mavlink data format
func (h *OverwatchHandler) mergeNewMAVLinkData(state *shared.EntityState, data map[string]interface{}) {
	logger.Debugw("[Overwatch] Merging new flattened MAVLink data", "entity_id", state.EntityID)

	// Extract SystemID and ComponentID
	if systemID, ok := data["system_id"].(float64); ok {
		state.SystemID = uint8(systemID)
	}
	if componentID, ok := data["component_id"].(float64); ok {
		state.ComponentID = uint8(componentID)
	}

	// Merge Attitude data (pitch, roll, yaw in radians)
	if pitch, hasPitch := data["pitch"].(float64); hasPitch {
		if state.Attitude == nil {
			state.Attitude = &shared.AttitudeState{}
		}
		if state.Attitude.Euler == nil {
			state.Attitude.Euler = &shared.EulerAttitude{}
		}

		state.Attitude.Euler.Pitch = pitch

		if roll, ok := data["roll"].(float64); ok {
			state.Attitude.Euler.Roll = roll
		}
		if yaw, ok := data["yaw"].(float64); ok {
			state.Attitude.Euler.Yaw = yaw
		}
		if pitchSpeed, ok := data["pitch_speed"].(float64); ok {
			state.Attitude.Euler.PitchSpeed = pitchSpeed
		}
		if rollSpeed, ok := data["roll_speed"].(float64); ok {
			state.Attitude.Euler.RollSpeed = rollSpeed
		}
		if yawSpeed, ok := data["yaw_speed"].(float64); ok {
			state.Attitude.Euler.YawSpeed = yawSpeed
		}

		state.Attitude.Euler.Timestamp = time.Now()
		logger.Debugw("[Overwatch] Merged attitude data", "entity_id", state.EntityID, "pitch", pitch, "roll", state.Attitude.Euler.Roll, "yaw", state.Attitude.Euler.Yaw)
	}

	// Merge Power/Battery data
	if batteryRemaining, hasBattery := data["battery_remaining"].(float64); hasBattery {
		if state.Power == nil {
			state.Power = &shared.PowerState{}
		}

		state.Power.BatteryRemain = int8(batteryRemaining)

		// voltage_battery is in mV, convert to volts
		if voltageBattery, ok := data["voltage_battery"].(float64); ok {
			state.Power.Voltage = voltageBattery / 1000.0 // Convert mV to V
		}

		state.Power.Timestamp = time.Now()
		logger.Debugw("[Overwatch] Merged power data", "entity_id", state.EntityID, "battery", batteryRemaining, "voltage", state.Power.Voltage)
	}

	// Merge Position data (GlobalPositionInt)
	if latitude, hasLatitude := data["latitude"].(float64); hasLatitude {
		if state.Position == nil {
			state.Position = &shared.PositionState{}
		}
		if state.Position.Global == nil {
			state.Position.Global = &shared.GlobalPosition{}
		}
		if state.Position.Local == nil {
			state.Position.Local = &shared.LocalPosition{}
		}

		// Convert latitude from degE7 to degrees
		state.Position.Global.Latitude = latitude / 1e7

		// Convert longitude from degE7 to degrees
		if longitude, ok := data["longitude"].(float64); ok {
			state.Position.Global.Longitude = longitude / 1e7
		}

		// Convert altitude from mm to meters
		if altitude, ok := data["altitude"].(float64); ok {
			state.Position.Global.AltitudeMSL = altitude / 1000.0
		}

		// Convert relative altitude from mm to meters
		if relativeAlt, ok := data["relative_alt"].(float64); ok {
			state.Position.Global.AltitudeRelative = relativeAlt / 1000.0
		}

		// Convert velocities from cm/s to m/s
		if vx, ok := data["vx"].(float64); ok {
			state.Position.Local.VX = vx / 100.0
		}
		if vy, ok := data["vy"].(float64); ok {
			state.Position.Local.VY = vy / 100.0
		}
		if vz, ok := data["vz"].(float64); ok {
			state.Position.Local.VZ = vz / 100.0
		}

		state.Position.Global.Timestamp = time.Now()
		state.Position.Local.Timestamp = time.Now()
		logger.Debugw("[Overwatch] Merged position data", "entity_id", state.EntityID, "lat", state.Position.Global.Latitude, "lon", state.Position.Global.Longitude, "alt_msl", state.Position.Global.AltitudeMSL, "alt_rel", state.Position.Global.AltitudeRelative)
	}

	// Merge VFR/Flight data
	if groundSpeed, hasGroundSpeed := data["ground_speed"].(float64); hasGroundSpeed {
		if state.VFR == nil {
			state.VFR = &shared.VFRState{}
		}

		state.VFR.Groundspeed = groundSpeed

		if throttle, ok := data["throttle"].(float64); ok {
			state.VFR.Throttle = uint16(throttle)
		}
		if climbRate, ok := data["climb_rate"].(float64); ok {
			state.VFR.ClimbRate = climbRate
		}
		// Convert heading from centidegrees to degrees
		if heading, ok := data["heading"].(float64); ok {
			state.VFR.Heading = int16(heading / 100.0)
		}

		state.VFR.Timestamp = time.Now()
		logger.Debugw("[Overwatch] Merged VFR data", "entity_id", state.EntityID, "ground_speed", groundSpeed, "throttle", state.VFR.Throttle, "climb_rate", state.VFR.ClimbRate, "heading", state.VFR.Heading)
	}

	// Merge Vehicle Status data
	if load, hasLoad := data["load"].(float64); hasLoad {
		if state.VehicleStatus == nil {
			state.VehicleStatus = &shared.VehicleStatusState{}
		}

		state.VehicleStatus.Load = uint16(load)

		// Extract vehicle type from last_msg_type or vehicle_type fields
		if vehicleType, ok := data["vehicle_type"].(string); ok {
			state.VehicleStatus.Mode = vehicleType // Store vehicle type in mode for display
		}

		state.VehicleStatus.Timestamp = time.Now()
		logger.Debugw("[Overwatch] Merged vehicle status", "entity_id", state.EntityID, "load", load, "vehicle_type", state.VehicleStatus.Mode)
	}

	// Update entity metadata from mavlink data
	if source, ok := data["source"].(string); ok && source != "" {
		state.Name = source
	}
	if lastSeen, ok := data["last_seen"].(string); ok {
		if ts, err := time.Parse(time.RFC3339, lastSeen); err == nil {
			state.UpdatedAt = ts
		}
	}
}

// buildEntitySignal creates a typed EntitySignal from EntityState.
// This extracts minimal metadata for frontend signals (position, status, etc.)
// while the full entity data is rendered server-side in the card HTML.
func buildEntitySignal(entityID string, state shared.EntityState) signals.EntitySignal {
	sig := signals.EntitySignal{
		EntityID:   entityID,
		OrgID:      state.OrgID,
		Name:       state.Name,
		EntityType: state.EntityType,
		Status:     state.Status,
		IsLive:     state.IsLive,
	}

	// Add position if available (for map integration)
	// Using pointers to correctly represent zero values (equator/prime meridian)
	if state.Position != nil && state.Position.Global != nil {
		lat := state.Position.Global.Latitude
		lng := state.Position.Global.Longitude
		alt := state.Position.Global.AltitudeMSL
		sig.Lat = &lat
		sig.Lng = &lng
		sig.Alt = &alt
	}

	// Add heading if available (for map marker rotation)
	// Using pointer to correctly represent heading 0 (north)
	if state.VFR != nil {
		heading := state.VFR.Heading
		sig.Heading = &heading
	}

	return sig
}

// computeAnalyticsTyped computes aggregated analytics from entity states using typed signals.
func (h *OverwatchHandler) computeAnalyticsTyped(entities []shared.EntityState) signals.AnalyticsSignals {
	typeCounts := make(map[string]int)
	statusCounts := map[string]int{
		"active":      0,
		"maintenance": 0,
		"unknown":     0,
	}
	activeThreats := 0
	criticalThreats := 0
	highThreats := 0
	trackedObjects := 0
	activeDetections := 0

	for _, entity := range entities {
		// Count by entity type
		if entity.EntityType != "" {
			typeCounts[entity.EntityType]++
		} else {
			typeCounts["unknown"]++
		}

		// Count by status
		switch entity.Status {
		case "active", "online", "connected":
			statusCounts["active"]++
		case "maintenance", "offline", "disconnected":
			statusCounts["maintenance"]++
		default:
			statusCounts["unknown"]++
		}

		// Aggregate threat data
		if entity.Analytics != nil {
			activeThreats += entity.Analytics.ActiveThreatCount

			if entity.Analytics.ThreatDistribution != nil {
				if count, ok := entity.Analytics.ThreatDistribution["critical"]; ok {
					criticalThreats += count
				}
				if count, ok := entity.Analytics.ThreatDistribution["HIGH_THREAT"]; ok {
					highThreats += count
				}
				if count, ok := entity.Analytics.ThreatDistribution["high"]; ok {
					highThreats += count
				}
			}
		}

		if entity.ThreatIntel != nil && entity.ThreatIntel.ThreatSummary != nil {
			activeThreats += entity.ThreatIntel.ThreatSummary.TotalThreats
		}

		// Aggregate vision/detection data per entity
		// Use Detections if present, otherwise fall back to Analytics counts
		if entity.Detections != nil && len(entity.Detections.TrackedObjects) > 0 {
			for _, obj := range entity.Detections.TrackedObjects {
				trackedObjects++
				if obj.IsActive {
					activeDetections++
				}
			}
		} else if entity.Analytics != nil {
			// Only use Analytics counts if Detections not available for this entity
			trackedObjects += entity.Analytics.TrackedObjectsCount
			activeDetections += entity.Analytics.ActiveObjectsCount
		}
	}

	return signals.AnalyticsSignals{
		TypeCounts:   typeCounts,
		StatusCounts: statusCounts,
		Threats: signals.ThreatSignals{
			Active: activeThreats,
			Priority: signals.ThreatPriorityData{
				Critical: criticalThreats,
				High:     highThreats,
			},
		},
		Vision: signals.VisionSignals{
			Tracked:    trackedObjects,
			Detections: activeDetections,
		},
	}
}
