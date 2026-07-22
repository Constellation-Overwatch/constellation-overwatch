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
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	common_components "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/common/components"
	overwatch "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/overwatch/components"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/signals"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats.go"
)

const overwatchClientQueueSize = 32

type overwatchKVKind int

const (
	overwatchKVKindFullState overwatchKVKind = iota
	overwatchKVKindDetections
	overwatchKVKindAnalytics
	overwatchKVKindThreatIntel
	overwatchKVKindMAVLink
)

type overwatchKVKey struct {
	EntityID string
	Kind     overwatchKVKind
}

type overwatchKVUpdate struct {
	EntityID      string
	State         shared.EntityState
	Removed       bool
	TotalEntities int
	FullSnapshot  bool
}

type overwatchKVBroadcaster struct {
	natsEmbedded *embeddednats.EmbeddedNATS

	mu          sync.RWMutex
	entityData  map[string]map[string][]byte
	entityState map[string]shared.EntityState
	subscribers map[chan overwatchKVUpdate]struct{}

	startMu sync.Mutex
	started bool
}

type OverwatchHandler struct {
	natsEmbedded *embeddednats.EmbeddedNATS
	orgSvc       *services.OrganizationService
	entitySvc    *services.EntityService

	kvBroadcaster      *overwatchKVBroadcaster
	videoConfigMu      sync.Mutex
	videoConfigCache   map[string]*ontology.VideoConfig
	videoConfigCacheAt time.Time
}

func NewOverwatchHandler(natsEmbedded *embeddednats.EmbeddedNATS, orgSvc *services.OrganizationService, entitySvc *services.EntityService) *OverwatchHandler {
	h := &OverwatchHandler{
		natsEmbedded: natsEmbedded,
		orgSvc:       orgSvc,
		entitySvc:    entitySvc,
	}
	h.kvBroadcaster = newOverwatchKVBroadcaster(natsEmbedded)
	return h
}

func newOverwatchKVBroadcaster(natsEmbedded *embeddednats.EmbeddedNATS) *overwatchKVBroadcaster {
	return &overwatchKVBroadcaster{
		natsEmbedded: natsEmbedded,
		entityData:   make(map[string]map[string][]byte),
		entityState:  make(map[string]shared.EntityState),
		subscribers:  make(map[chan overwatchKVUpdate]struct{}),
	}
}

func (b *overwatchKVBroadcaster) Start(ctx context.Context) error {
	b.startMu.Lock()
	defer b.startMu.Unlock()

	if b.started {
		return nil
	}
	if b.natsEmbedded == nil {
		return fmt.Errorf("NATS not available")
	}

	if err := b.loadInitialState(); err != nil {
		return err
	}

	b.started = true
	go b.watch(ctx)
	return nil
}

func (b *overwatchKVBroadcaster) Subscribe(ctx context.Context) (<-chan overwatchKVUpdate, []overwatchKVUpdate, func(), error) {
	if err := b.Start(context.Background()); err != nil {
		return nil, nil, nil, err
	}

	ch := make(chan overwatchKVUpdate, overwatchClientQueueSize)

	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	initial := b.snapshotLocked()
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}

	go func() {
		<-ctx.Done()
		unsubscribe()
	}()

	return ch, initial, unsubscribe, nil
}

func (b *overwatchKVBroadcaster) Snapshot() []overwatchKVUpdate {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshotLocked()
}

func (b *overwatchKVBroadcaster) snapshotLocked() []overwatchKVUpdate {
	updates := make([]overwatchKVUpdate, 0, len(b.entityState))
	total := len(b.entityState)
	for entityID, state := range b.entityState {
		updates = append(updates, overwatchKVUpdate{
			EntityID:      entityID,
			State:         state,
			TotalEntities: total,
		})
	}
	return updates
}

func (b *overwatchKVBroadcaster) loadInitialState() error {
	initialEntries, err := b.natsEmbedded.GetAllKVEntries()
	if err != nil {
		return fmt.Errorf("loading initial KV state: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for _, entry := range initialEntries {
		if _, ok := b.applyEntryLocked(entry); !ok {
			continue
		}
	}
	logger.Debugw("[Overwatch] Loaded shared KV state", "kv_entries", len(initialEntries), "entities", len(b.entityState))
	return nil
}

func (b *overwatchKVBroadcaster) watch(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		err := b.natsEmbedded.WatchKV(ctx, func(_ string, entry nats.KeyValueEntry) error {
			update, ok := b.applyEntry(entry)
			if ok {
				b.broadcast(update)
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Warnw("[Overwatch] Shared KV watcher stopped unexpectedly, restarting", "error", err)
		} else {
			logger.Warnw("[Overwatch] Shared KV watcher channel closed unexpectedly, restarting")
		}

		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (b *overwatchKVBroadcaster) applyEntry(entry nats.KeyValueEntry) (overwatchKVUpdate, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.applyEntryLocked(entry)
}

func (b *overwatchKVBroadcaster) applyEntryLocked(entry nats.KeyValueEntry) (overwatchKVUpdate, bool) {
	if entry == nil {
		return overwatchKVUpdate{}, false
	}

	parsed, ok := parseOverwatchKVKey(entry.Key())
	if !ok {
		return overwatchKVUpdate{}, false
	}

	entityID := parsed.EntityID
	key := entry.Key()

	if entry.Operation() == nats.KeyValueDelete || entry.Operation() == nats.KeyValuePurge {
		if b.entityData[entityID] == nil {
			return overwatchKVUpdate{}, false
		}
		delete(b.entityData[entityID], key)
		if len(b.entityData[entityID]) == 0 {
			delete(b.entityData, entityID)
			delete(b.entityState, entityID)
			return overwatchKVUpdate{
				EntityID:      entityID,
				Removed:       true,
				TotalEntities: len(b.entityState),
			}, true
		}
		state := mergeOverwatchEntityData(entityID, b.entityData[entityID])
		b.entityState[entityID] = state
		return overwatchKVUpdate{
			EntityID:      entityID,
			State:         state,
			TotalEntities: len(b.entityState),
		}, true
	}

	value := entry.Value()
	if len(value) == 0 {
		value = []byte("{}")
	} else {
		var testJSON interface{}
		if err := json.Unmarshal(value, &testJSON); err != nil {
			logger.Warnw("[Overwatch] Invalid JSON in KV entry, storing as-is", "key", key, "error", err)
		}
	}

	if b.entityData[entityID] == nil {
		b.entityData[entityID] = make(map[string][]byte)
	}
	b.entityData[entityID][key] = value

	state := mergeOverwatchEntityData(entityID, b.entityData[entityID])
	b.entityState[entityID] = state

	if parsed.Kind == overwatchKVKindMAVLink {
		previewLen := 200
		if len(value) < previewLen {
			previewLen = len(value)
		}
		logger.Debugw("[Overwatch] MAVLink data received", "entity_id", entityID, "key", key, "data_preview", string(value)[:previewLen])
	}

	return overwatchKVUpdate{
		EntityID:      entityID,
		State:         state,
		TotalEntities: len(b.entityState),
	}, true
}

func (b *overwatchKVBroadcaster) broadcast(update overwatchKVUpdate) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		sendOverwatchUpdate(ch, update)
	}
}

func sendOverwatchUpdate(ch chan overwatchKVUpdate, update overwatchKVUpdate) {
	select {
	case ch <- update:
		return
	default:
	}

	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}

	select {
	case ch <- overwatchKVUpdate{FullSnapshot: true}:
	default:
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
		logger.Infof("Error fetching KV keys: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Fetch all entries
	var kvEntries []overwatch.KVEntry
	for _, key := range keys {
		entry, err := kv.Get(key)
		if err != nil {
			logger.Infof("Error getting key %s: %v", key, err)
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
		sse := datastar.NewSSE(w, r)
		component := overwatch.KVStateTable(kvEntries)
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("#kv-content"),
			datastar.WithModeInner())
		if err != nil {
			logger.Infof("Error patching KV content: %v", err)
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
	orgID, err := authorizedOrganizationID(r, "")
	if err != nil {
		writeResourceNotFound(w)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Infow("[Overwatch] ERROR: ResponseWriter does not support flushing (SSE won't work)")
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	updates, initialUpdates, unsubscribe, err := h.kvBroadcaster.Subscribe(ctx)
	if err != nil {
		logger.Warnw("[Overwatch] Failed to subscribe to shared KV broadcaster", "error", err)
		http.Error(w, "KV watch unavailable", http.StatusServiceUnavailable)
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	logger.Debugw("[Overwatch] SSE headers set, establishing connection", "remote_addr", r.RemoteAddr)

	sse := datastar.NewSSE(w, r)
	viewMode := r.URL.Query().Get("view")

	var writeMutex sync.Mutex

	writeMutex.Lock()
	fmt.Fprintf(w, ": SSE connection established\n\n")

	emptyState := `<div class="empty-state" style="color: #888; padding: 40px; text-align: center;">
					<p>No entity states in global store. Waiting for telemetry data...</p>
					<p style="font-size: 10px; margin-top: 10px;">Server-side rendering via SSE</p>
				</div>`
	if viewMode == "map" {
		sse.PatchElements(emptyState,
			datastar.WithSelector("#entity-list"),
			datastar.WithModeInner())
	} else {
		sse.PatchElements(emptyState,
			datastar.WithSelector("#entities-container"),
			datastar.WithModeInner())
	}

	flusher.Flush()
	writeMutex.Unlock()
	logger.Debugw("SSE client connected", "component", "Overwatch", "remote_addr", r.RemoteAddr)

	knownEntities := make(map[string]string)
	knownOrgs := make(map[string]bool)
	dirtyStates := make(map[string]shared.EntityState)
	removedEntities := make(map[string]bool)
	totalEntities := 0

	for _, update := range initialUpdates {
		if overwatchUpdateAllowed(update, orgID, knownEntities) {
			queueOverwatchUpdate(update, dirtyStates, removedEntities)
		}
	}
	totalEntities = h.countOverwatchEntities(orgID)
	h.flushOverwatchUpdates(w, flusher, &writeMutex, sse, dirtyStates, removedEntities, totalEntities, knownEntities, knownOrgs, viewMode)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	flushTicker := time.NewTicker(50 * time.Millisecond)
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Debugw("[Overwatch] Client disconnected", "remote_addr", r.RemoteAddr)
			return

		case <-ticker.C:
			h.currentVideoConfigCache()
			writeOverwatchHeartbeat(w, flusher, &writeMutex)

		case <-flushTicker.C:
			h.flushOverwatchUpdates(w, flusher, &writeMutex, sse, dirtyStates, removedEntities, totalEntities, knownEntities, knownOrgs, viewMode)

		case update, ok := <-updates:
			if !ok {
				logger.Debugw("[Overwatch] Update channel closed, stopping SSE stream")
				return
			}
			if update.FullSnapshot {
				totalEntities = h.queueFullOverwatchSnapshot(orgID, knownEntities, dirtyStates, removedEntities)
				continue
			}
			if overwatchUpdateAllowed(update, orgID, knownEntities) {
				queueOverwatchUpdate(update, dirtyStates, removedEntities)
				totalEntities = h.countOverwatchEntities(orgID)
			}
		}
	}
}

func overwatchUpdateAllowed(update overwatchKVUpdate, orgID string, knownEntities map[string]string) bool {
	if orgID == "" {
		return true
	}
	if update.Removed {
		knownOrgID, known := knownEntities[update.EntityID]
		return known && knownOrgID == orgID
	}
	return update.State.OrgID == orgID
}

func queueOverwatchUpdate(update overwatchKVUpdate, dirtyStates map[string]shared.EntityState, removedEntities map[string]bool) int {
	if update.Removed {
		delete(dirtyStates, update.EntityID)
		removedEntities[update.EntityID] = true
		return update.TotalEntities
	}

	dirtyStates[update.EntityID] = update.State
	delete(removedEntities, update.EntityID)
	return update.TotalEntities
}

func (h *OverwatchHandler) queueFullOverwatchSnapshot(orgID string, knownEntities map[string]string, dirtyStates map[string]shared.EntityState, removedEntities map[string]bool) int {
	updates := h.kvBroadcaster.Snapshot()
	seen := make(map[string]bool, len(updates))
	totalEntities := 0

	for _, update := range updates {
		if !overwatchUpdateAllowed(update, orgID, knownEntities) {
			continue
		}
		queueOverwatchUpdate(update, dirtyStates, removedEntities)
		seen[update.EntityID] = true
		if !update.Removed {
			totalEntities++
		}
	}

	for entityID := range knownEntities {
		if !seen[entityID] {
			delete(dirtyStates, entityID)
			removedEntities[entityID] = true
		}
	}

	return totalEntities
}

func (h *OverwatchHandler) countOverwatchEntities(orgID string) int {
	count := 0
	for _, update := range h.kvBroadcaster.Snapshot() {
		if !update.Removed && (orgID == "" || update.State.OrgID == orgID) {
			count++
		}
	}
	return count
}

func (h *OverwatchHandler) flushOverwatchUpdates(w http.ResponseWriter, flusher http.Flusher, writeMutex *sync.Mutex, sse *datastar.ServerSentEventGenerator, dirtyStates map[string]shared.EntityState, removedEntities map[string]bool, totalEntities int, knownEntities map[string]string, knownOrgs map[string]bool, viewMode string) {
	if len(dirtyStates) == 0 && len(removedEntities) == 0 {
		return
	}

	snapshot := make([]shared.EntityState, 0, len(dirtyStates))
	for _, state := range dirtyStates {
		snapshot = append(snapshot, state)
	}

	removedIDs := make([]string, 0, len(removedEntities))
	for entityID := range removedEntities {
		removedIDs = append(removedIDs, entityID)
	}

	videoConfigCache := h.currentVideoConfigCache()
	h.renderAndFlushSnapshot(w, flusher, writeMutex, sse, snapshot, removedIDs, totalEntities, knownEntities, knownOrgs, viewMode, videoConfigCache)

	for entityID := range dirtyStates {
		delete(dirtyStates, entityID)
	}
	for entityID := range removedEntities {
		delete(removedEntities, entityID)
	}
}

func writeOverwatchHeartbeat(w http.ResponseWriter, flusher http.Flusher, writeMutex *sync.Mutex) {
	if flusher == nil {
		return
	}

	writeMutex.Lock()
	defer writeMutex.Unlock()

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

func (h *OverwatchHandler) currentVideoConfigCache() map[string]*ontology.VideoConfig {
	h.videoConfigMu.Lock()
	defer h.videoConfigMu.Unlock()

	if h.videoConfigCache != nil && time.Since(h.videoConfigCacheAt) < 30*time.Second {
		return h.videoConfigCache
	}
	if h.entitySvc == nil {
		if h.videoConfigCache == nil {
			h.videoConfigCache = make(map[string]*ontology.VideoConfig)
		}
		return h.videoConfigCache
	}

	entities, err := h.entitySvc.ListAllEntities()
	if err != nil {
		logger.Warnw("[Overwatch] Failed to refresh video config cache", "error", err)
		if h.videoConfigCache == nil {
			h.videoConfigCache = make(map[string]*ontology.VideoConfig)
		}
		return h.videoConfigCache
	}

	next := make(map[string]*ontology.VideoConfig, len(entities))
	for _, ent := range entities {
		if ent.VideoConfig == "" || ent.VideoConfig == "{}" {
			continue
		}
		var vc ontology.VideoConfig
		if json.Unmarshal([]byte(ent.VideoConfig), &vc) == nil {
			next[ent.EntityID] = &vc
		}
	}

	h.videoConfigCache = next
	h.videoConfigCacheAt = time.Now()
	logger.Debugw("[Overwatch] Refreshed video config cache", "entries", len(next))
	return h.videoConfigCache
}

// Helper to render and flush a snapshot
func (h *OverwatchHandler) renderAndFlushSnapshot(w http.ResponseWriter, flusher http.Flusher, writeMutex *sync.Mutex, sse *datastar.ServerSentEventGenerator, snapshot []shared.EntityState, removedIDs []string, totalEntities int, knownEntities map[string]string, knownOrgs map[string]bool, viewMode string, videoConfigCache map[string]*ontology.VideoConfig) {
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
		isNew := false
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
					if err := sse.RemoveElement(containerSelector + " .empty-state"); err != nil {
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

				if err := sse.PatchElements(orgHTML.String(), datastar.WithSelector(containerSelector), datastar.WithModeAppend()); err != nil {
					logger.Debugw("Failed to patch org container, connection may be closed", "error", err)
					return
				}
				knownOrgs[entityState.OrgID] = true

				// Initialize signal (Same signal for both views)
				if err := sse.MarshalAndPatchSignals(map[string]interface{}{
					fmt.Sprintf("entityStatesByOrg.%s", entityState.OrgID): map[string]interface{}{},
				}); err != nil {
					logger.Debugw("Failed to patch org signals, connection may be closed", "error", err)
					return
				}
			} else if viewMode == "map" {
				// For map, remove empty state if first entity
				if len(knownEntities) == 0 {
					if err := sse.RemoveElement(containerSelector + " .empty-state"); err != nil {
						// Ignore error as empty state might not exist
					}
				}
			}

			isNew = true
			selector = containerSelector
			knownEntities[entityID] = entityState.OrgID
		} else {
			isNew = false
			if viewMode == "map" {
				selector = fmt.Sprintf("#c4-entity-%s", entityID)
			} else {
				selector = fmt.Sprintf("#entity-%s", entityID)
			}
			// Update known org just in case it changed (unlikely but possible)
			knownEntities[entityID] = entityState.OrgID
		}

		// Patch Element
		var patchOpts []datastar.PatchElementOption
		patchOpts = append(patchOpts, datastar.WithSelector(selector))
		if isNew {
			patchOpts = append(patchOpts, datastar.WithModeAppend())
		} else {
			patchOpts = append(patchOpts, datastar.WithModeOuter())
		}
		if err := sse.PatchElements(cardHTML.String(), patchOpts...); err != nil {
			logger.Debugw("Failed to patch entity, connection may be closed", "entity_id", entityID, "error", err)
			return
		}

		// For new entities (append mode), also append the video player component separately
		// This ensures video is only added once and never morphed, preventing connection duplication
		if isNew {
			// VideoConfig lives in the DB, not KV — look it up from the DB cache
			// (same approach as the video handler)
			webrtcURL := ""
			if vc, ok := videoConfigCache[entityID]; ok {
				webrtcURL = vc.PreferredWebRTCURL()
			}
			var videoHTML strings.Builder
			if err := common_components.VideoPlayer(entityID, webrtcURL).Render(context.Background(), &videoHTML); err == nil {
				videoSelector := fmt.Sprintf("#video-section-%s", entityID)
				if err := sse.PatchElements(videoHTML.String(), datastar.WithSelector(videoSelector), datastar.WithModeInner()); err != nil {
					logger.Debugw("Failed to append video player", "entity_id", entityID, "error", err)
				}
			}
		}

		// Patch Signal with typed entity metadata (not the full state - that's too large!)
		// The full entity data is already rendered server-side in the card HTML
		// Enrich with VideoConfig from DB cache (KV doesn't contain video_config)
		if entityState.VideoConfig == nil {
			if vc, ok := videoConfigCache[entityID]; ok {
				entityState.VideoConfig = vc
			}
		}
		entitySignal := buildEntitySignal(entityID, entityState)

		if err := sse.MarshalAndPatchSignals(map[string]interface{}{
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
			logger.Debugw("[Overwatch] Removing entity", "entity_id", entityID)

			// Remove from DOM
			var selector string
			if viewMode == "map" {
				selector = fmt.Sprintf("#c4-entity-%s", entityID)
			} else {
				selector = fmt.Sprintf("#entity-%s", entityID)
			}

			if err := sse.RemoveElement(selector); err != nil {
				logger.Debugw("Failed to remove entity element", "error", err)
			}

			// Remove from Signal (set to null)
			if err := sse.MarshalAndPatchSignals(map[string]interface{}{
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
		orgs, err := h.orgSvc.ListOrganizations()
		if err != nil {
			logger.Warnw("Failed to fetch organizations for analytics", "error", err)
		}
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

		if err := sse.MarshalAndPatchSignals(dashboardSig); err != nil {
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
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// parseKVEntriesToEntityStates parses KV entries and aggregates them by entity_id
func (h *OverwatchHandler) parseKVEntriesToEntityStates(entries []nats.KeyValueEntry) map[string][]shared.EntityState {
	// First, group entries by entity_id
	entitiesByID := make(map[string]map[string][]byte)

	for _, entry := range entries {
		parsed, ok := parseOverwatchKVKey(entry.Key())
		if !ok {
			continue
		}
		entityID := parsed.EntityID

		if entitiesByID[entityID] == nil {
			entitiesByID[entityID] = make(map[string][]byte)
		}

		// Store raw data keyed by full key for later processing
		entitiesByID[entityID][entry.Key()] = entry.Value()
	}

	// Now build consolidated EntityState objects
	entityStatesByOrg := make(map[string][]shared.EntityState)

	logger.Debugw("[Overwatch] Aggregating entities from KV entries", "entity_count", len(entitiesByID), "kv_entry_count", len(entries))

	for entityID, dataMap := range entitiesByID {
		logger.Debugw("[Overwatch] Processing entity", "entity_id", entityID, "kv_entry_count", len(dataMap))
		entityState := mergeOverwatchEntityData(entityID, dataMap)

		// Group by org_id
		orgID := entityState.OrgID
		if orgID == "" {
			orgID = "unknown"
		}

		entityStatesByOrg[orgID] = append(entityStatesByOrg[orgID], entityState)
	}

	logger.Debugw("[Overwatch] Built entities", "total_entities", len(entitiesByID), "org_count", len(entityStatesByOrg))
	return entityStatesByOrg
}

func parseOverwatchKVKey(key string) (overwatchKVKey, bool) {
	if key == "" {
		logger.Warnw("[Overwatch] Received entry with empty key, skipping")
		return overwatchKVKey{}, false
	}

	patterns := []struct {
		marker string
		kind   overwatchKVKind
	}{
		{marker: ".detections.objects", kind: overwatchKVKindDetections},
		{marker: ".analytics.summary", kind: overwatchKVKindAnalytics},
		{marker: ".analytics.c4isr_summary", kind: overwatchKVKindAnalytics},
		{marker: ".c4isr.threat_intelligence", kind: overwatchKVKindThreatIntel},
		{marker: ".mavlink", kind: overwatchKVKindMAVLink},
	}

	for _, pattern := range patterns {
		if idx := strings.LastIndex(key, pattern.marker); idx > 0 {
			return overwatchKVKey{
				EntityID: key[:idx],
				Kind:     pattern.kind,
			}, true
		}
	}

	return overwatchKVKey{
		EntityID: key,
		Kind:     overwatchKVKindFullState,
	}, true
}

// mergeOverwatchEntityData merges separate KV entries into a single EntityState.
func mergeOverwatchEntityData(entityID string, dataMap map[string][]byte) shared.EntityState {
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

		parsed, ok := parseOverwatchKVKey(key)
		if !ok {
			continue
		}

		switch parsed.Kind {
		case overwatchKVKindDetections:
			mergeDetections(&state, rawData)
		case overwatchKVKindAnalytics:
			mergeAnalytics(&state, rawData)
		case overwatchKVKindThreatIntel:
			mergeThreatIntel(&state, rawData)
		case overwatchKVKindMAVLink:
			mergeNewMAVLinkData(&state, rawData)
		case overwatchKVKindFullState:
			mergeFullState(&state, rawData)
		}
	}

	return state
}

// mergeDetections merges detection data into EntityState.
// Supports both old format (tracked_objects with avg_confidence/threat_level)
// and new format (objects with confidence/bbox/cx/cy/dx/dy).
func mergeDetections(state *shared.EntityState, data map[string]interface{}) {
	// Try "objects" (new format) first, then "tracked_objects" (old)
	trackedObjects, ok := data["objects"].(map[string]interface{})
	if !ok {
		trackedObjects, ok = data["tracked_objects"].(map[string]interface{})
	}
	if !ok {
		return
	}

	detectionState := &shared.DetectionState{
		TrackedObjects: make(map[string]shared.TrackedObject),
		Timestamp:      time.Now(),
	}

	// Detection-level metadata (new format)
	if status, ok := data["status"].(string); ok {
		detectionState.Status = status
	}
	if isLive, ok := data["is_live"].(bool); ok {
		detectionState.IsLive = isLive
	}
	if fc, ok := data["frame_count"].(float64); ok {
		detectionState.FrameCount = int(fc)
	}

	for trackID, objData := range trackedObjects {
		objMap, ok := objData.(map[string]interface{})
		if !ok {
			continue
		}

		trackedObj := shared.TrackedObject{
			TrackID:  trackID,
			IsActive: true, // presence in KV = active (1Hz atomic replacement)
		}

		if label, ok := objMap["label"].(string); ok {
			trackedObj.Label = label
		}

		// New format: "confidence"; old format: "avg_confidence"
		if conf, ok := objMap["confidence"].(float64); ok {
			trackedObj.Confidence = conf
		}
		if conf, ok := objMap["avg_confidence"].(float64); ok {
			trackedObj.AvgConfidence = conf
		}

		if frames, ok := objMap["frame_count"].(float64); ok {
			trackedObj.FrameCount = int(frames)
		}

		// New format fields: bbox, centroid, motion vector
		if bboxData, ok := objMap["bbox"].(map[string]interface{}); ok {
			trackedObj.BBox = &shared.BoundingBox{}
			if v, ok := bboxData["x1"].(float64); ok {
				trackedObj.BBox.X1 = v
			}
			if v, ok := bboxData["y1"].(float64); ok {
				trackedObj.BBox.Y1 = v
			}
			if v, ok := bboxData["x2"].(float64); ok {
				trackedObj.BBox.X2 = v
			}
			if v, ok := bboxData["y2"].(float64); ok {
				trackedObj.BBox.Y2 = v
			}
		}
		if v, ok := objMap["cx"].(float64); ok {
			trackedObj.CX = v
		}
		if v, ok := objMap["cy"].(float64); ok {
			trackedObj.CY = v
		}
		if v, ok := objMap["dx"].(float64); ok {
			trackedObj.DX = v
		}
		if v, ok := objMap["dy"].(float64); ok {
			trackedObj.DY = v
		}

		// Timestamps
		if fs, ok := objMap["first_seen"].(string); ok {
			if t, err := time.Parse(time.RFC3339, fs); err == nil {
				trackedObj.FirstSeen = t
			}
		}
		if ls, ok := objMap["last_seen"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ls); err == nil {
				trackedObj.LastSeen = t
			}
		}

		// Legacy fields
		if active, ok := objMap["is_active"].(bool); ok {
			trackedObj.IsActive = active
		}
		if threat, ok := objMap["threat_level"].(string); ok {
			trackedObj.ThreatLevel = threat
		}

		detectionState.TrackedObjects[trackID] = trackedObj
	}

	state.Detections = detectionState
}

// mergeAnalytics merges analytics data into EntityState
func mergeAnalytics(state *shared.EntityState, data map[string]interface{}) {
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
func mergeThreatIntel(state *shared.EntityState, data map[string]interface{}) {
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
func mergeFullState(state *shared.EntityState, data map[string]interface{}) {
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
		logger.Debugw("[Overwatch] mergeFullState: no org_id or organization_id in data")
	}

	// Detection service format: detections.objects.{track_id} (new) or detections.tracked_objects (old)
	if detectionsData, ok := data["detections"].(map[string]interface{}); ok {
		// Pass the whole detections map — mergeDetections handles both
		// "objects" (new) and "tracked_objects" (old), plus detection-level metadata
		mergeDetections(state, detectionsData)
		objectCount := 0
		if state.Detections != nil {
			objectCount = len(state.Detections.TrackedObjects)
		}
		logger.Debugw("[Overwatch] Merged detections", "entity_id", state.EntityID, "objects", objectCount)

		// Check for analytics nested inside detections
		if analyticsData, ok := detectionsData["analytics"].(map[string]interface{}); ok {
			mergeAnalytics(state, analyticsData)
			logger.Debugw("[Overwatch] Merged detections.analytics")
		}
	}

	// Python analytics format (OLD): top-level analytics.summary
	if analyticsData, ok := data["analytics"].(map[string]interface{}); ok {
		if summaryData, ok := analyticsData["summary"].(map[string]interface{}); ok {
			mergeAnalytics(state, summaryData)
			logger.Debugw("[Overwatch] Merged analytics.summary")
		}
	}

	// Python threat intelligence format (NEW): top-level threat_intelligence
	if threatData, ok := data["threat_intelligence"].(map[string]interface{}); ok {
		mergeThreatIntel(state, threatData)
		logger.Debugw("[Overwatch] Merged threat_intelligence")
	}

	// Python C4ISR format (OLD): c4isr.threat_intelligence
	if c4isrData, ok := data["c4isr"].(map[string]interface{}); ok {
		if threatData, ok := c4isrData["threat_intelligence"].(map[string]interface{}); ok {
			mergeThreatIntel(state, threatData)
			logger.Debugw("[Overwatch] Merged c4isr.threat_intelligence")
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
func mergeNewMAVLinkData(state *shared.EntityState, data map[string]interface{}) {
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

	// Add WebRTC URL if available (for per-entity WHEP playback)
	// Prefers overlay (bounding box) stream over raw
	if state.VideoConfig != nil {
		sig.WebRTCURL = state.VideoConfig.PreferredWebRTCURL()
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
