package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats.go"
)

// TelemetryWorker processes telemetry messages and maintains global entity state
type TelemetryWorker struct {
	*BaseWorker
	db             *sql.DB
	kv             nats.KeyValue
	registry       *EntityRegistry
	entityCache    map[string]*shared.EntityState // Cache of entity states
	cacheMutex     sync.RWMutex
	staleThreshold time.Duration
}

const (
	maxUint8  = 1<<8 - 1
	maxUint16 = 1<<16 - 1
	maxUint32 = 1<<32 - 1
	minInt16  = -1 << 15
	maxInt16  = 1<<15 - 1
	minInt32  = -1 << 31
	maxInt32  = 1<<31 - 1
)

// NewTelemetryWorker creates a new telemetry worker with database and KV store access
func NewTelemetryWorker(nc *nats.Conn, js nats.JetStreamContext, db *sql.DB, kv nats.KeyValue, registry *EntityRegistry) *TelemetryWorker {
	return &TelemetryWorker{
		BaseWorker: NewBaseWorker(
			"TelemetryWorker",
			nc,
			js,
			shared.StreamTelemetry,
			shared.ConsumerTelemetryProcessor,
			shared.SubjectTelemetryAll,
		),
		db:             db,
		kv:             kv,
		registry:       registry,
		entityCache:    make(map[string]*shared.EntityState),
		staleThreshold: 5 * time.Second,
	}
}

func (w *TelemetryWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) error {
		return w.handleTelemetryMessageContext(ctx, msg)
	})
}

// handleTelemetryMessage processes a single telemetry message
func (w *TelemetryWorker) handleTelemetryMessage(msg *nats.Msg) error {
	return w.handleTelemetryMessageContext(context.Background(), msg)
}

func (w *TelemetryWorker) handleTelemetryMessageContext(ctx context.Context, msg *nats.Msg) error {
	// Parse subject: constellation.telemetry.{org_id}.{entity_id}
	entityID, orgID, err := w.parseSubject(msg.Subject)
	if err != nil {
		logger.Errorw("Failed to parse subject", "worker", w.Name(), "subject", msg.Subject, "error", err)
		return fmt.Errorf("failed to parse subject: %w", err)
	}

	// Parse MAVLink telemetry
	telemetry, err := protocol.DecodeTelemetry(msg.Data)
	if err != nil {
		logger.Warnw("Rejected malformed telemetry envelope", "worker", w.Name(), "subject", msg.Subject, "error", err)
		return err
	}
	if telemetry.EntityID != entityID || telemetry.OrgID != orgID {
		return fmt.Errorf("telemetry subject identity %s/%s does not match envelope %s/%s", orgID, entityID, telemetry.OrgID, telemetry.EntityID)
	}

	// Reject unknown entity IDs instead of growing registry/cache from arbitrary telemetry.
	if !w.registry.IsRegistered(entityID) {
		logger.Warnw("Rejected telemetry for unregistered entity", "worker", w.Name(), "entity_id", entityID, "org_id", orgID)
		return fmt.Errorf("telemetry entity %s is not registered", entityID)
	}

	// Get or create entity state
	state, err := w.getOrCreateEntityState(entityID, orgID)
	if err != nil {
		logger.Errorw("Failed to get entity state", "worker", w.Name(), "entity_id", entityID, "error", err)
		return fmt.Errorf("failed to get entity state: %w", err)
	}
	if state.OrgID != orgID {
		return fmt.Errorf("telemetry organization %s does not match entity organization %s", orgID, state.OrgID)
	}

	// Update state based on message type
	if state.TelemetryCursors == nil {
		state.TelemetryCursors = make(map[string]shared.TelemetryCursor)
	}
	if cursor, ok := state.TelemetryCursors[telemetry.MessageType]; ok && !telemetry.Timestamp.After(cursor.Timestamp) {
		logger.Debugw("Ignored duplicate or stale telemetry", "worker", w.Name(), "entity_id", entityID, "message_type", telemetry.MessageType, "message_uid", telemetry.MessageUID)
		return nil
	}

	updated := w.updateEntityState(state, &telemetry)
	if !updated {
		return fmt.Errorf("unsupported telemetry message_type %q", telemetry.MessageType)
	}

	state.TelemetryCursors[telemetry.MessageType] = shared.TelemetryCursor{MessageUID: telemetry.MessageUID, Timestamp: telemetry.Timestamp}
	state.SystemID = telemetry.SystemID
	state.ComponentID = telemetry.ComponentID
	state.LastSeen = laterTime(state.LastSeen, telemetry.Timestamp)
	state.UpdatedAt = laterTime(state.UpdatedAt, telemetry.Timestamp)
	state.IsLive = true

	if err := w.saveEntityState(ctx, state); err != nil {
		return fmt.Errorf("persist telemetry state: %w", err)
	}

	logger.Debugw("Processed telemetry", "worker", w.Name(), "entity_id", entityID, "entity_type", state.EntityType, "message_type", telemetry.MessageType)
	return nil
}

// parseSubject extracts entity_id and org_id from NATS subject
func (w *TelemetryWorker) parseSubject(subject string) (entityID, orgID string, err error) {
	orgID, entityID, err = protocol.ParseTelemetrySubject(subject)
	return entityID, orgID, err
}

// getOrCreateEntityState retrieves entity state from cache or creates new one
func (w *TelemetryWorker) getOrCreateEntityState(entityID, orgID string) (*shared.EntityState, error) {
	// Check cache first
	w.cacheMutex.RLock()
	if state, exists := w.entityCache[entityID]; exists {
		w.cacheMutex.RUnlock()
		return state, nil
	}
	w.cacheMutex.RUnlock()

	// Try to load from KV store
	state, err := w.loadEntityState(entityID)
	if err == nil {
		// Found in KV, add to cache
		w.cacheMutex.Lock()
		w.entityCache[entityID] = state
		w.cacheMutex.Unlock()
		return state, nil
	}
	if !errors.Is(err, nats.ErrKeyNotFound) {
		return nil, fmt.Errorf("load entity state: %w", err)
	}

	// Not in KV, fetch from database and initialize
	state, err = w.initializeEntityFromDB(entityID, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize entity from DB: %w", err)
	}

	// Add to cache
	w.cacheMutex.Lock()
	w.entityCache[entityID] = state
	w.cacheMutex.Unlock()

	return state, nil
}

// initializeEntityFromDB fetches entity from database and creates initial state
func (w *TelemetryWorker) initializeEntityFromDB(entityID, orgID string) (*shared.EntityState, error) {
	query := `
		SELECT e.entity_id, e.org_id, o.name as org_name, COALESCE(e.name, '') as entity_name,
		       e.entity_type, e.status, e.priority,
		       e.is_live, e.expiry_time, e.latitude, e.longitude, e.altitude,
		       e.components, e.aliases, e.tags, e.source, e.created_by, e.classification,
		       e.metadata, e.created_at, e.updated_at
		FROM entities e
		LEFT JOIN organizations o ON e.org_id = o.org_id
		WHERE e.entity_id = ?`

	var state shared.EntityState
	var isLive int
	var expiryTime, lat, lon, alt, source, createdBy, classification sql.NullString
	var createdAt, updatedAt string
	var components, aliases, tags, metadata sql.NullString

	err := w.db.QueryRow(query, entityID).Scan(
		&state.EntityID, &state.OrgID, &state.OrgName, &state.Name,
		&state.EntityType, &state.Status, &state.Priority,
		&isLive, &expiryTime, &lat, &lon, &alt,
		&components, &aliases, &tags, &source, &createdBy, &classification,
		&metadata, &createdAt, &updatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("entity %s: %w", entityID, shared.ErrNotFound)
	}

	if err != nil {
		return nil, fmt.Errorf("database query failed: %w", err)
	}

	// Parse fields
	state.IsLive = isLive == 1
	if expiryTime.Valid {
		if t, err := time.Parse(time.RFC3339, expiryTime.String); err != nil {
			logger.Debugw("Failed to parse timestamp", "value", expiryTime.String, "error", err)
		} else {
			state.ExpiryTime = &t
		}
	}
	if source.Valid {
		state.Source = source.String
	}
	if createdBy.Valid {
		state.CreatedBy = createdBy.String
	}
	if classification.Valid {
		state.Classification = classification.String
	}

	// Initialize position if lat/lon exist
	if lat.Valid && lon.Valid {
		state.Position = &shared.PositionState{
			Global: &shared.GlobalPosition{
				Latitude:  parseFloat(lat.String),
				Longitude: parseFloat(lon.String),
				Timestamp: time.Now(),
			},
		}
		if alt.Valid {
			state.Position.Global.AltitudeMSL = parseFloat(alt.String)
		}
	}

	// Parse JSON fields
	state.Components = make(map[string]any)
	if components.Valid && components.String != "" {
		if err := json.Unmarshal([]byte(components.String), &state.Components); err != nil {
			logger.Debugw("Failed to unmarshal components", "entity_id", entityID, "error", err)
		}
	}

	state.Aliases = make(map[string]string)
	if aliases.Valid && aliases.String != "" {
		if err := json.Unmarshal([]byte(aliases.String), &state.Aliases); err != nil {
			logger.Debugw("Failed to unmarshal aliases", "entity_id", entityID, "error", err)
		}
	}

	state.Tags = make([]string, 0)
	if tags.Valid && tags.String != "" {
		if err := json.Unmarshal([]byte(tags.String), &state.Tags); err != nil {
			logger.Debugw("Failed to unmarshal tags", "entity_id", entityID, "error", err)
		}
	}

	state.Metadata = make(map[string]any)
	if metadata.Valid && metadata.String != "" {
		if err := json.Unmarshal([]byte(metadata.String), &state.Metadata); err != nil {
			logger.Debugw("Failed to unmarshal metadata", "entity_id", entityID, "error", err)
		}
	}

	if t, err := time.Parse(time.RFC3339, createdAt); err != nil {
		logger.Debugw("Failed to parse timestamp", "value", createdAt, "error", err)
	} else {
		state.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		logger.Debugw("Failed to parse timestamp", "value", updatedAt, "error", err)
	} else {
		state.UpdatedAt = t
	}

	logger.Infow("Initialized entity from database", "component", "TelemetryWorker", "entity_id", entityID, "entity_type", state.EntityType)
	return &state, nil
}

// loadEntityState loads entity state from KV store
func (w *TelemetryWorker) loadEntityState(entityID string) (*shared.EntityState, error) {
	key := shared.EntityKey(entityID)
	entry, err := w.kv.Get(key)
	if err != nil {
		return nil, err
	}

	var state shared.EntityState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entity state: %w", err)
	}

	return &state, nil
}

// saveEntityState saves entity state to KV store with merge support
// This uses Read-Modify-Write with optimistic locking to preserve data from other publishers
func (w *TelemetryWorker) saveEntityState(ctx context.Context, state *shared.EntityState) error {
	if state.EntityID == "" {
		return fmt.Errorf("entity_id is empty, cannot create KV key")
	}

	key := shared.EntityKey(state.EntityID)
	const maxRetries = 5

	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Try to get existing entry with revision
		existingEntry, err := w.kv.Get(key)

		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				// Key doesn't exist yet, create it
				data, err := json.Marshal(state)
				if err != nil {
					return fmt.Errorf("failed to marshal entity state: %w", err)
				}

				if _, err := w.kv.Create(key, data); err != nil {
					if errors.Is(err, nats.ErrKeyExists) {
						// Race condition: key was created between check and create, retry
						logger.Debugw("Race condition creating key, retrying", "worker", w.Name(), "key", key)
						continue
					}
					return fmt.Errorf("failed to create entity state (key='%s'): %w", key, err)
				}

				logger.Debugw("Created new entity state", "worker", w.Name(), "entity_id", state.EntityID)
				w.updateCache(state)
				return nil
			}
			return fmt.Errorf("failed to get existing state for merge: %w", err)
		}

		// Key exists - merge with existing data
		var existingState shared.EntityState
		if err := json.Unmarshal(existingEntry.Value(), &existingState); err != nil {
			return fmt.Errorf("decode existing entity state: %w", err)
		}
		// Preserve fields owned by other Hub workers while replacing telemetry-owned fields.
		state = w.mergeTelemetryWithDetections(&existingState, state)

		// Marshal merged state
		data, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("failed to marshal merged entity state: %w", err)
		}

		// Try to update with revision check (optimistic locking)
		if _, err := w.kv.Update(key, data, existingEntry.Revision()); err != nil {
			if errors.Is(err, nats.ErrKeyExists) || strings.Contains(err.Error(), "wrong last sequence") {
				// Revision mismatch - someone else updated between our read and write
				logger.Debugw("Revision mismatch, retrying", "worker", w.Name(), "key", key, "attempt", attempt+1, "max_retries", maxRetries)
				if err := waitForRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("failed to update entity state (key='%s'): %w", key, err)
		}

		logger.Debugw("Updated entity state (merged with existing data)", "worker", w.Name(), "entity_id", state.EntityID)
		w.updateCache(state)
		return nil
	}

	return fmt.Errorf("failed to save entity state after %d attempts (revision conflicts)", maxRetries)
}

func waitForRetry(ctx context.Context, attempt int) error {
	delay := 10 * time.Millisecond * time.Duration(1<<attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// mergeTelemetryWithDetections merges telemetry data with existing detection/analytics data
// Telemetry fields (from this worker): Position, Attitude, Power, VFR, VehicleStatus, Mission, Actuators, Environment
// Detection fields (from Python): Analytics, Detections, ThreatIntel
func (w *TelemetryWorker) mergeTelemetryWithDetections(existing, telemetry *shared.EntityState) *shared.EntityState {
	// Start with existing state to preserve all fields
	merged := *existing

	// Update telemetry-specific fields from new data
	if telemetry.Position != nil {
		merged.Position = telemetry.Position
	}
	if telemetry.Attitude != nil {
		merged.Attitude = telemetry.Attitude
	}
	if telemetry.Power != nil {
		merged.Power = telemetry.Power
	}
	if telemetry.VFR != nil {
		merged.VFR = telemetry.VFR
	}
	if telemetry.VehicleStatus != nil {
		merged.VehicleStatus = telemetry.VehicleStatus
	}
	if telemetry.Mission != nil {
		merged.Mission = telemetry.Mission
	}
	if telemetry.Actuators != nil {
		merged.Actuators = telemetry.Actuators
	}
	if telemetry.Environment != nil {
		merged.Environment = telemetry.Environment
	}

	// Update core entity metadata
	merged.UpdatedAt = laterTime(existing.UpdatedAt, telemetry.UpdatedAt)
	merged.IsLive = telemetry.IsLive
	merged.SystemID = telemetry.SystemID
	merged.ComponentID = telemetry.ComponentID
	merged.LastSeen = laterTime(existing.LastSeen, telemetry.LastSeen)
	merged.TelemetryCursors = telemetry.TelemetryCursors

	// Preserve detection/analytics fields from existing state (Python service owns these)
	// No need to explicitly copy since we started with existing state

	return &merged
}

// updateCache updates the in-memory cache
func (w *TelemetryWorker) updateCache(state *shared.EntityState) {
	w.cacheMutex.Lock()
	w.entityCache[state.EntityID] = state
	w.cacheMutex.Unlock()
}

// updateEntityState updates entity state based on MAVLink message type
func (w *TelemetryWorker) updateEntityState(state *shared.EntityState, msg *shared.MAVLinkTelemetry) bool {
	switch msg.MessageType {
	case "HEARTBEAT":
		w.updateHeartbeat(state, msg.Data, msg.Timestamp)
	case "SYS_STATUS":
		w.updateSysStatus(state, msg.Data, msg.Timestamp)
	case "GPS_RAW_INT":
		w.updateGPSRaw(state, msg.Data, msg.Timestamp)
	case "GLOBAL_POSITION_INT":
		w.updateGlobalPosition(state, msg.Data, msg.Timestamp)
	case "ATTITUDE":
		w.updateAttitude(state, msg.Data, msg.Timestamp)
	case "ATTITUDE_QUATERNION":
		w.updateAttitudeQuaternion(state, msg.Data, msg.Timestamp)
	case "LOCAL_POSITION_NED":
		w.updateLocalPosition(state, msg.Data, msg.Timestamp)
	case "ALTITUDE":
		w.updateAltitude(state, msg.Data, msg.Timestamp)
	case "VFR_HUD":
		w.updateVFR(state, msg.Data, msg.Timestamp)
	case "MISSION_CURRENT":
		w.updateMission(state, msg.Data, msg.Timestamp)
	case "BATTERY_STATUS":
		w.updateBattery(state, msg.Data, msg.Timestamp)
	case "SERVO_OUTPUT_RAW":
		w.updateServos(state, msg.Data, msg.Timestamp)
	case "SCALED_PRESSURE":
		w.updatePressure(state, msg.Data, msg.Timestamp)
	case "EXTENDED_SYS_STATE":
		w.updateExtendedSysState(state, msg.Data, msg.Timestamp)
	default:
		return false // Unknown message type
	}
	return true
}

// ═══════════════════════════════════════════════════════════
// MAVLink Message Handlers
// ═══════════════════════════════════════════════════════════

func (w *TelemetryWorker) updateHeartbeat(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.VehicleStatus == nil {
		state.VehicleStatus = &shared.VehicleStatusState{}
	}

	if customMode, ok := getUint32(data, "custom_mode"); ok {
		state.VehicleStatus.CustomMode = customMode
		state.VehicleStatus.Mode = decodeArduPilotMode(customMode)
	}
	if baseMode, ok := getUint8(data, "base_mode"); ok {
		state.VehicleStatus.Armed = (baseMode & 128) != 0
	}
	if autopilot, ok := getUint8(data, "autopilot"); ok {
		state.VehicleStatus.Autopilot = autopilot
	}
	if systemStatus, ok := getUint8(data, "system_status"); ok {
		state.VehicleStatus.SystemStatus = systemStatus
	}
	if vehicleType, ok := getUint8(data, "type"); ok {
		state.VehicleStatus.VehicleType = vehicleType
	}

	state.VehicleStatus.Timestamp = ts
}

func (w *TelemetryWorker) updateSysStatus(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.VehicleStatus == nil {
		state.VehicleStatus = &shared.VehicleStatusState{}
	}
	if state.Power == nil {
		state.Power = &shared.PowerState{}
	}

	if load, ok := getUint16(data, "load"); ok {
		state.VehicleStatus.Load = load
	}
	if sensorsEnabled, ok := getUint32(data, "onboard_control_sensors_enabled"); ok {
		state.VehicleStatus.SensorsEnabled = sensorsEnabled
	}
	if sensorsHealth, ok := getUint32(data, "onboard_control_sensors_health"); ok {
		state.VehicleStatus.SensorsHealth = sensorsHealth
	}

	if voltage, ok := getFloat64(data, "voltage_battery"); ok {
		state.Power.Voltage = voltage
	}
	if current, ok := getFloat64(data, "current_battery"); ok {
		state.Power.Current = current
	}
	if remaining, ok := getBatteryRemaining(data, "battery_remaining"); ok {
		state.Power.BatteryRemain = remaining
	}

	state.VehicleStatus.Timestamp = ts
	state.Power.Timestamp = ts
}

func (w *TelemetryWorker) updateGPSRaw(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Position == nil {
		state.Position = &shared.PositionState{}
	}
	if state.Position.Global == nil {
		state.Position.Global = &shared.GlobalPosition{}
	}

	if lat, ok := getFloat64(data, "lat"); ok {
		state.Position.Global.Latitude = lat / 1e7 // MAVLink sends lat*1e7
	}
	if lon, ok := getFloat64(data, "lon"); ok {
		state.Position.Global.Longitude = lon / 1e7
	}
	if alt, ok := getFloat64(data, "alt"); ok {
		state.Position.Global.AltitudeMSL = alt / 1000.0 // MAVLink sends alt in mm
	}
	if eph, ok := getFloat64(data, "eph"); ok {
		state.Position.Global.AccuracyH = eph / 100.0
	}
	if epv, ok := getFloat64(data, "epv"); ok {
		state.Position.Global.AccuracyV = epv / 100.0
	}
	if satsVisible, ok := getUint8(data, "satellites_visible"); ok {
		state.Position.Global.SatellitesVisible = int(satsVisible)
	}
	if fixType, ok := getUint8(data, "fix_type"); ok {
		state.Position.Global.FixType = int(fixType)
	}

	state.Position.Global.Timestamp = ts
}

func (w *TelemetryWorker) updateGlobalPosition(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Position == nil {
		state.Position = &shared.PositionState{}
	}
	if state.Position.Global == nil {
		state.Position.Global = &shared.GlobalPosition{}
	}
	if state.Position.Local == nil {
		state.Position.Local = &shared.LocalPosition{}
	}

	if lat, ok := getFloat64(data, "lat"); ok {
		state.Position.Global.Latitude = lat / 1e7
	}
	if lon, ok := getFloat64(data, "lon"); ok {
		state.Position.Global.Longitude = lon / 1e7
	}
	if alt, ok := getFloat64(data, "alt"); ok {
		state.Position.Global.AltitudeMSL = alt / 1000
	}
	if relativeAlt, ok := getFloat64(data, "relative_alt"); ok {
		state.Position.Global.AltitudeRelative = relativeAlt / 1000
	}
	if vx, ok := getFloat64(data, "vx"); ok {
		state.Position.Local.VX = vx / 100
	}
	if vy, ok := getFloat64(data, "vy"); ok {
		state.Position.Local.VY = vy / 100
	}
	if vz, ok := getFloat64(data, "vz"); ok {
		state.Position.Local.VZ = vz / 100
	}

	state.Position.Global.Timestamp = ts
	state.Position.Local.Timestamp = ts
}

func (w *TelemetryWorker) updateAttitude(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Attitude == nil {
		state.Attitude = &shared.AttitudeState{}
	}
	if state.Attitude.Euler == nil {
		state.Attitude.Euler = &shared.EulerAttitude{}
	}

	if roll, ok := getFloat64(data, "roll"); ok {
		state.Attitude.Euler.Roll = roll
	}
	if pitch, ok := getFloat64(data, "pitch"); ok {
		state.Attitude.Euler.Pitch = pitch
	}
	if yaw, ok := getFloat64(data, "yaw"); ok {
		state.Attitude.Euler.Yaw = yaw
	}
	if rollspeed, ok := getFloat64(data, "rollspeed"); ok {
		state.Attitude.Euler.RollSpeed = rollspeed
	}
	if pitchspeed, ok := getFloat64(data, "pitchspeed"); ok {
		state.Attitude.Euler.PitchSpeed = pitchspeed
	}
	if yawspeed, ok := getFloat64(data, "yawspeed"); ok {
		state.Attitude.Euler.YawSpeed = yawspeed
	}

	state.Attitude.Euler.Timestamp = ts
}

func (w *TelemetryWorker) updateAttitudeQuaternion(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Attitude == nil {
		state.Attitude = &shared.AttitudeState{}
	}
	if state.Attitude.Quaternion == nil {
		state.Attitude.Quaternion = &shared.QuaternionAttitude{}
	}

	if q1, ok := getFloat64(data, "q1"); ok {
		state.Attitude.Quaternion.Q1 = q1
	}
	if q2, ok := getFloat64(data, "q2"); ok {
		state.Attitude.Quaternion.Q2 = q2
	}
	if q3, ok := getFloat64(data, "q3"); ok {
		state.Attitude.Quaternion.Q3 = q3
	}
	if q4, ok := getFloat64(data, "q4"); ok {
		state.Attitude.Quaternion.Q4 = q4
	}
	if rollspeed, ok := getFloat64(data, "rollspeed"); ok {
		state.Attitude.Quaternion.RollSpeed = rollspeed
	}
	if pitchspeed, ok := getFloat64(data, "pitchspeed"); ok {
		state.Attitude.Quaternion.PitchSpeed = pitchspeed
	}
	if yawspeed, ok := getFloat64(data, "yawspeed"); ok {
		state.Attitude.Quaternion.YawSpeed = yawspeed
	}

	state.Attitude.Quaternion.Timestamp = ts
}

func (w *TelemetryWorker) updateLocalPosition(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Position == nil {
		state.Position = &shared.PositionState{}
	}
	if state.Position.Local == nil {
		state.Position.Local = &shared.LocalPosition{}
	}

	if x, ok := getFloat64(data, "x"); ok {
		state.Position.Local.X = x
	}
	if y, ok := getFloat64(data, "y"); ok {
		state.Position.Local.Y = y
	}
	if z, ok := getFloat64(data, "z"); ok {
		state.Position.Local.Z = z
	}
	if vx, ok := getFloat64(data, "vx"); ok {
		state.Position.Local.VX = vx
	}
	if vy, ok := getFloat64(data, "vy"); ok {
		state.Position.Local.VY = vy
	}
	if vz, ok := getFloat64(data, "vz"); ok {
		state.Position.Local.VZ = vz
	}

	state.Position.Local.Timestamp = ts
}

func (w *TelemetryWorker) updateAltitude(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Position == nil {
		state.Position = &shared.PositionState{}
	}
	if state.Position.Global == nil {
		state.Position.Global = &shared.GlobalPosition{}
	}

	if altMSL, ok := getFloat64(data, "altitude_amsl"); ok {
		state.Position.Global.AltitudeMSL = altMSL
	}
	if altRel, ok := getFloat64(data, "altitude_relative"); ok {
		state.Position.Global.AltitudeRelative = altRel
	}
	if altTerrain, ok := getFloat64(data, "altitude_terrain"); ok {
		state.Position.Global.AltitudeTerrain = altTerrain
	}

	state.Position.Global.Timestamp = ts
}

func (w *TelemetryWorker) updateVFR(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.VFR == nil {
		state.VFR = &shared.VFRState{}
	}

	if airspeed, ok := getFloat64(data, "airspeed"); ok {
		state.VFR.Airspeed = airspeed
	}
	if groundspeed, ok := getFloat64(data, "groundspeed"); ok {
		state.VFR.Groundspeed = groundspeed
	}
	if heading, ok := getInt16InRange(data, "heading", 0, 360); ok {
		state.VFR.Heading = heading
	}
	if climb, ok := getFloat64(data, "climb"); ok {
		state.VFR.ClimbRate = climb
	}
	if throttle, ok := getUint16InRange(data, "throttle", 100); ok {
		state.VFR.Throttle = throttle
	}
	if alt, ok := getFloat64(data, "alt"); ok {
		state.VFR.Altitude = alt
	}

	state.VFR.Timestamp = ts
}

func (w *TelemetryWorker) updateMission(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Mission == nil {
		state.Mission = &shared.MissionState{}
	}

	if seq, ok := getUint16(data, "seq"); ok {
		state.Mission.CurrentWaypoint = seq
	}

	state.Mission.Timestamp = ts
}

func (w *TelemetryWorker) updateBattery(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Power == nil {
		state.Power = &shared.PowerState{}
	}

	if remaining, ok := getBatteryRemaining(data, "battery_remaining"); ok {
		state.Power.BatteryRemain = remaining
	}
	if current, ok := getFloat64(data, "current_battery"); ok {
		state.Power.Current = current / 100.0 // MAVLink sends in cA
	}
	if consumed, ok := getInt32(data, "current_consumed"); ok {
		state.Power.Consumed = consumed
	}
	if energy, ok := getInt32(data, "energy_consumed"); ok {
		state.Power.EnergyConsumed = energy
	}
	if temp, ok := getInt16(data, "temperature"); ok {
		state.Power.Temperature = temp
	}

	if voltages, ok := data["voltages"].([]any); ok {
		cells := make([]uint16, len(voltages))
		valid := true
		for i, v := range voltages {
			voltage, ok := v.(float64)
			if !ok {
				valid = false
				break
			}
			cell, ok := checkedUint16(fmt.Sprintf("voltages[%d]", i), voltage)
			if !ok {
				valid = false
				break
			}
			cells[i] = cell
		}
		if valid {
			state.Power.Cells = cells
		}
	}

	state.Power.Timestamp = ts
}

func (w *TelemetryWorker) updateServos(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Actuators == nil {
		state.Actuators = &shared.ActuatorState{}
	}

	state.Actuators.Servos = make([]uint16, 8)
	for i := 1; i <= 8; i++ {
		key := fmt.Sprintf("servo%d_raw", i)
		if val, ok := getUint16(data, key); ok {
			state.Actuators.Servos[i-1] = val
		}
	}

	state.Actuators.Timestamp = ts
}

func (w *TelemetryWorker) updatePressure(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.Environment == nil {
		state.Environment = &shared.EnvironmentState{}
	}

	if pressAbs, ok := getFloat64(data, "press_abs"); ok {
		state.Environment.PressureAbs = pressAbs
	}
	if pressDiff, ok := getFloat64(data, "press_diff"); ok {
		state.Environment.PressureDiff = pressDiff
	}
	if temp, ok := getInt16(data, "temperature"); ok {
		state.Environment.Temperature = temp
	}

	state.Environment.Timestamp = ts
}

func (w *TelemetryWorker) updateExtendedSysState(state *shared.EntityState, data map[string]any, ts time.Time) {
	if state.VehicleStatus == nil {
		state.VehicleStatus = &shared.VehicleStatusState{}
	}

	if landedState, ok := getUint8(data, "landed_state"); ok {
		state.VehicleStatus.LandedState = landedState
	}
	if vtolState, ok := getUint8(data, "vtol_state"); ok {
		state.VehicleStatus.VTOLState = vtolState
	}

	state.VehicleStatus.Timestamp = ts
}

// ═══════════════════════════════════════════════════════════
// Helper Functions
// ═══════════════════════════════════════════════════════════

func getFloat64(data map[string]any, key string) (float64, bool) {
	if val, ok := data[key]; ok {
		if f, ok := val.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func getUint8(data map[string]any, key string) (uint8, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	return checkedUint8(key, value)
}

func getUint16(data map[string]any, key string) (uint16, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	return checkedUint16(key, value)
}

func getUint16InRange(data map[string]any, key string, max uint16) (uint16, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	converted, ok := checkedUintRange(key, value, uint64(max))
	return uint16(converted), ok
}

func getUint32(data map[string]any, key string) (uint32, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	converted, ok := checkedUintRange(key, value, maxUint32)
	return uint32(converted), ok
}

func getInt16(data map[string]any, key string) (int16, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	return checkedInt16(key, value)
}

func getInt16InRange(data map[string]any, key string, min, max int16) (int16, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	converted, ok := checkedIntRange(key, value, int64(min), int64(max))
	return int16(converted), ok
}

func getInt32(data map[string]any, key string) (int32, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	converted, ok := checkedIntRange(key, value, minInt32, maxInt32)
	return int32(converted), ok
}

func getBatteryRemaining(data map[string]any, key string) (int8, bool) {
	value, ok := getFloat64(data, key)
	if !ok {
		return 0, false
	}
	converted, ok := checkedIntRange(key, value, -1, 100)
	return int8(converted), ok
}

func checkedUint8(key string, value float64) (uint8, bool) {
	converted, ok := checkedUintRange(key, value, maxUint8)
	return uint8(converted), ok
}

func checkedUint16(key string, value float64) (uint16, bool) {
	converted, ok := checkedUintRange(key, value, maxUint16)
	return uint16(converted), ok
}

func checkedInt16(key string, value float64) (int16, bool) {
	converted, ok := checkedIntRange(key, value, minInt16, maxInt16)
	return int16(converted), ok
}

func checkedUintRange(key string, value float64, max uint64) (uint64, bool) {
	if !validInteger(value) || value < 0 || value > float64(max) {
		logger.Warnw("Rejected out-of-range MAVLink unsigned integer", "field", key, "value", value, "min", 0, "max", max)
		return 0, false
	}
	return uint64(value), true
}

func checkedIntRange(key string, value float64, min, max int64) (int64, bool) {
	if !validInteger(value) || value < float64(min) || value > float64(max) {
		logger.Warnw("Rejected out-of-range MAVLink integer", "field", key, "value", value, "min", min, "max", max)
		return 0, false
	}
	return int64(value), true
}

func validInteger(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func decodeArduPilotMode(customMode uint32) string {
	modes := map[uint32]string{
		0:  "STABILIZE",
		1:  "ACRO",
		2:  "ALT_HOLD",
		3:  "AUTO",
		4:  "GUIDED",
		5:  "LOITER",
		6:  "RTL",
		7:  "CIRCLE",
		9:  "LAND",
		11: "DRIFT",
		13: "SPORT",
		14: "FLIP",
		15: "AUTOTUNE",
		16: "POSHOLD",
		17: "BRAKE",
		18: "THROW",
		19: "AVOID_ADSB",
		20: "GUIDED_NOGPS",
		21: "SMART_RTL",
		22: "FLOWHOLD",
		23: "FOLLOW",
		24: "ZIGZAG",
		25: "SYSTEMID",
		26: "AUTOROTATE",
		27: "AUTO_RTL",
	}

	if mode, ok := modes[customMode]; ok {
		return mode
	}
	return fmt.Sprintf("UNKNOWN_%d", customMode)
}

// validateEntityID checks if an entity_id is valid for use in NATS KV keys
// NATS KV keys cannot contain: . (dots), * (asterisks), > (greater-than), spaces
// and cannot be empty or start with underscore
func validateEntityID(entityID string) error {
	if entityID == "" {
		return fmt.Errorf("entity_id is empty")
	}

	// Check for invalid characters for NATS KV keys
	invalidChars := map[rune]string{
		'.': "dot",
		'*': "asterisk",
		'>': "greater-than",
		' ': "space",
	}

	for _, char := range entityID {
		if desc, invalid := invalidChars[char]; invalid {
			return fmt.Errorf("contains invalid character '%c' (%s) for NATS KV key", char, desc)
		}
	}

	// Check for leading underscore (reserved in NATS KV)
	if entityID[0] == '_' {
		return fmt.Errorf("cannot start with underscore (reserved)")
	}

	return nil
}
