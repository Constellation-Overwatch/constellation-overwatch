package shared

import (
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
)

// API Response types
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Pagination
type PaginationRequest struct {
	Page     int    `json:"page" validate:"min=1"`
	PageSize int    `json:"page_size" validate:"min=1,max=100"`
	OrderBy  string `json:"order_by"`
	Order    string `json:"order" validate:"omitempty,oneof=asc desc"`
}

type PaginationResponse struct {
	Items      interface{} `json:"items"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalItems int64       `json:"total_items"`
	TotalPages int         `json:"total_pages"`
}

// Event types
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Subject   string                 `json:"subject"`
	Data      map[string]interface{} `json:"data"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`
}

// Fleet represents a collection of swarms
type Fleet struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	OrgID       string            `json:"org_id"`
	SwarmIDs    []string          `json:"swarm_ids"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Swarm represents a group of entities working together
type Swarm struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	OrgID       string            `json:"org_id"`
	FleetID     string            `json:"fleet_id,omitempty"`
	EntityIDs   []string          `json:"entity_ids"`
	Status      string            `json:"status"`
	Type        string            `json:"type"` // e.g., "formation", "patrol", "search"
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Health check
type HealthStatus struct {
	Status    string            `json:"status"`
	Service   string            `json:"service"`
	Version   string            `json:"version,omitempty"`
	Uptime    time.Duration     `json:"uptime,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Details   map[string]string `json:"details,omitempty"`
}

// ═══════════════════════════════════════════════════════════
// GLOBAL STATE TYPES - Real-time entity state management
// ═══════════════════════════════════════════════════════════

// EntityState represents the complete real-time state of an entity
type EntityState struct {
	// Core Entity Identity (from entities table)
	EntityID   string     `json:"entity_id"`
	OrgID      string     `json:"org_id"`
	OrgName    string     `json:"org_name,omitempty"`
	Name       string     `json:"name,omitempty"`
	EntityType string     `json:"entity_type"`
	Status     string     `json:"status"`
	Priority   string     `json:"priority"`
	IsLive     bool       `json:"is_live"`
	ExpiryTime *time.Time `json:"expiry_time,omitempty"`

	// Position State
	Position *PositionState `json:"position,omitempty"`

	// Attitude State
	Attitude *AttitudeState `json:"attitude,omitempty"`

	// Vehicle Status
	VehicleStatus *VehicleStatusState `json:"vehicle_status,omitempty"`

	// Power System
	Power *PowerState `json:"power,omitempty"`

	// Flight Performance
	VFR *VFRState `json:"vfr,omitempty"`

	// Mission State
	Mission *MissionState `json:"mission,omitempty"`

	// Control Outputs
	Actuators *ActuatorState `json:"actuators,omitempty"`

	// Environmental
	Environment *EnvironmentState `json:"environment,omitempty"`

	// C4ISR & Analytics
	Analytics   *AnalyticsState   `json:"analytics,omitempty"`
	Detections  *DetectionState   `json:"detections,omitempty"`
	ThreatIntel *ThreatIntelState `json:"threat_intel,omitempty"`

	// Device Identity (MAVLink/Legacy)
	SystemID      uint8                 `json:"system_id,omitempty"`
	ComponentID   uint8                 `json:"component_id,omitempty"`
	DeviceID      string                `json:"device_id,omitempty"`
	StreamPort    string                `json:"stream_port,omitempty"`
	VideoConfig   *ontology.VideoConfig `json:"video_config,omitempty"`
	Subject       string                `json:"subject,omitempty"`
	FirstSeen     time.Time             `json:"first_seen,omitempty"`
	LastSeen      time.Time             `json:"last_seen,omitempty"`
	Fingerprinted bool                  `json:"fingerprinted,omitempty"`

	// Database Fields
	Components     map[string]interface{} `json:"components"`
	Aliases        map[string]string      `json:"aliases"`
	Tags           []string               `json:"tags"`
	Source         string                 `json:"source"`
	CreatedBy      string                 `json:"created_by"`
	Classification string                 `json:"classification,omitempty"`
	Metadata       map[string]interface{} `json:"metadata"`

	// TelemetryCursors make replay/redelivery idempotent across Hub restarts.
	TelemetryCursors map[string]TelemetryCursor `json:"telemetry_cursors,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TelemetryCursor records the last applied message for one MAVLink type.
type TelemetryCursor struct {
	MessageUID string    `json:"message_uid"`
	Timestamp  time.Time `json:"timestamp"`
}

// PositionState contains position data from GPS and local navigation
type PositionState struct {
	Global *GlobalPosition `json:"global,omitempty"`
	Local  *LocalPosition  `json:"local,omitempty"`
}

// GlobalPosition represents GPS-based global position
type GlobalPosition struct {
	Latitude          float64   `json:"latitude"`
	Longitude         float64   `json:"longitude"`
	AltitudeMSL       float64   `json:"altitude_msl"`
	AltitudeRelative  float64   `json:"altitude_relative"`
	AltitudeTerrain   float64   `json:"altitude_terrain,omitempty"`
	AccuracyH         float64   `json:"accuracy_h"`
	AccuracyV         float64   `json:"accuracy_v"`
	SatellitesVisible int       `json:"satellites_visible,omitempty"`
	FixType           int       `json:"fix_type,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
}

// LocalPosition represents NED (North-East-Down) local position
type LocalPosition struct {
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	Z         float64   `json:"z"`
	VX        float64   `json:"vx"`
	VY        float64   `json:"vy"`
	VZ        float64   `json:"vz"`
	Timestamp time.Time `json:"timestamp"`
}

// AttitudeState contains orientation data in multiple representations
type AttitudeState struct {
	Euler      *EulerAttitude      `json:"euler,omitempty"`
	Quaternion *QuaternionAttitude `json:"quaternion,omitempty"`
}

// EulerAttitude represents orientation as Euler angles
type EulerAttitude struct {
	Roll       float64   `json:"roll"`
	Pitch      float64   `json:"pitch"`
	Yaw        float64   `json:"yaw"`
	RollSpeed  float64   `json:"rollspeed"`
	PitchSpeed float64   `json:"pitchspeed"`
	YawSpeed   float64   `json:"yawspeed"`
	Timestamp  time.Time `json:"timestamp"`
}

// QuaternionAttitude represents orientation as quaternion
type QuaternionAttitude struct {
	Q1         float64   `json:"q1"`
	Q2         float64   `json:"q2"`
	Q3         float64   `json:"q3"`
	Q4         float64   `json:"q4"`
	RollSpeed  float64   `json:"rollspeed,omitempty"`
	PitchSpeed float64   `json:"pitchspeed,omitempty"`
	YawSpeed   float64   `json:"yawspeed,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// VehicleStatusState contains vehicle operational status
type VehicleStatusState struct {
	Armed          bool      `json:"armed"`
	Mode           string    `json:"mode"`
	CustomMode     uint32    `json:"custom_mode"`
	Autopilot      uint8     `json:"autopilot"`
	SystemStatus   uint8     `json:"system_status"`
	VehicleType    uint8     `json:"vehicle_type"`
	LandedState    uint8     `json:"landed_state"`
	VTOLState      uint8     `json:"vtol_state,omitempty"`
	Load           uint16    `json:"load"`
	SensorsEnabled uint32    `json:"sensors_enabled"`
	SensorsHealth  uint32    `json:"sensors_health"`
	Timestamp      time.Time `json:"timestamp"`
}

// PowerState contains battery and power system data
type PowerState struct {
	Voltage        float64   `json:"voltage"`
	Current        float64   `json:"current"`
	BatteryRemain  int8      `json:"battery_remaining"`
	Consumed       int32     `json:"consumed"`
	EnergyConsumed int32     `json:"energy_consumed"`
	Cells          []uint16  `json:"cells,omitempty"`
	Temperature    int16     `json:"temperature"`
	Timestamp      time.Time `json:"timestamp"`
}

// VFRState contains VFR (Visual Flight Rules) HUD data
type VFRState struct {
	Airspeed    float64   `json:"airspeed"`
	Groundspeed float64   `json:"groundspeed"`
	Heading     int16     `json:"heading"`
	ClimbRate   float64   `json:"climb_rate"`
	Throttle    uint16    `json:"throttle"`
	Altitude    float64   `json:"altitude"`
	Timestamp   time.Time `json:"timestamp"`
}

// MissionState contains current mission information
type MissionState struct {
	CurrentWaypoint uint16    `json:"current_waypoint"`
	TotalWaypoints  uint16    `json:"total_waypoints,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// ActuatorState contains servo/motor output data
type ActuatorState struct {
	Servos    []uint16  `json:"servos"`
	Timestamp time.Time `json:"timestamp"`
}

// EnvironmentState contains environmental sensor data
type EnvironmentState struct {
	PressureAbs  float64   `json:"pressure_abs"`
	PressureDiff float64   `json:"pressure_diff"`
	Temperature  int16     `json:"temperature"`
	Humidity     float64   `json:"humidity,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// AnalyticsState contains analytics summary data
type AnalyticsState struct {
	TotalUniqueObjects   int            `json:"total_unique_objects"`
	TotalFramesProcessed int            `json:"total_frames_processed"`
	ActiveObjectsCount   int            `json:"active_objects_count"`
	TrackedObjectsCount  int            `json:"tracked_objects_count"`
	LabelDistribution    map[string]int `json:"label_distribution"`
	ThreatDistribution   map[string]int `json:"threat_distribution"`
	ActiveThreatCount    int            `json:"active_threat_count"`
	ActiveTrackIDs       []string       `json:"active_track_ids"`
	ThreatAlerts         []interface{}  `json:"threat_alerts"`
	Timestamp            time.Time      `json:"timestamp"`
}

// DetectionState contains object detection and tracking data.
// Aligned to the new KV shape: detections.objects.{track_id}
type DetectionState struct {
	// Detection-level metadata (from detections root)
	Status     string `json:"status,omitempty"`
	IsLive     bool   `json:"is_live,omitempty"`
	FrameCount int    `json:"frame_count,omitempty"`

	// Tracked objects keyed by track_id
	TrackedObjects map[string]TrackedObject `json:"tracked_objects,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// TrackedObject represents a single tracked object from the detection pipeline.
// New format fields: confidence, bbox{x1,y1,x2,y2}, cx/cy, dx/dy, first_seen, last_seen
type TrackedObject struct {
	TrackID    string  `json:"track_id"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	FrameCount int     `json:"frame_count"`

	// Bounding box (normalized 0-1)
	BBox *BoundingBox `json:"bbox,omitempty"`

	// Centroid position (normalized 0-1)
	CX float64 `json:"cx"`
	CY float64 `json:"cy"`

	// Motion vector (per-frame delta, normalized)
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`

	// Timestamps
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// Legacy fields (kept for backward compat with old publishers)
	AvgConfidence        float64  `json:"avg_confidence,omitempty"`
	IsActive             bool     `json:"is_active,omitempty"`
	ThreatLevel          string   `json:"threat_level,omitempty"`
	SuspiciousIndicators []string `json:"suspicious_indicators,omitempty"`
}

// EffectiveConfidence returns whichever confidence value is populated.
func (t TrackedObject) EffectiveConfidence() float64 {
	if t.Confidence > 0 {
		return t.Confidence
	}
	return t.AvgConfidence
}

// BoundingBox represents object bounding box coordinates (normalized 0-1)
type BoundingBox struct {
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
	X2 float64 `json:"x2"`
	Y2 float64 `json:"y2"`
}

// ThreatIntelState contains threat intelligence data
type ThreatIntelState struct {
	Mission       string          `json:"mission"`
	Analytics     *AnalyticsState `json:"analytics,omitempty"`
	ThreatSummary *ThreatSummary  `json:"threat_summary,omitempty"`
	ThreatAlerts  []interface{}   `json:"threat_alerts"`
	Timestamp     time.Time       `json:"timestamp"`
}

// ThreatSummary contains aggregated threat information
type ThreatSummary struct {
	TotalThreats       int            `json:"total_threats"`
	ThreatDistribution map[string]int `json:"threat_distribution"`
	AlertLevel         string         `json:"alert_level"`
}

// MAVLinkTelemetry is retained as a source-compatible alias for the canonical
// versioned Pulsar-to-Hub protocol envelope.
type MAVLinkTelemetry = protocol.TelemetryEnvelope

// ═══════════════════════════════════════════════════════════
// VIDEO FRAME TYPES - Video stream data from vision2constellation
// ═══════════════════════════════════════════════════════════

// VideoFrame represents a video frame from an entity's camera
type VideoFrame struct {
	// Identity (entity-centric)
	EntityID string `json:"entity_id"`

	// Frame metadata
	FrameID     string    `json:"frame_id"`
	Timestamp   time.Time `json:"timestamp"`
	SequenceNum uint64    `json:"sequence_num"`

	// Source info
	CameraID string `json:"camera_id,omitempty"`
	StreamID string `json:"stream_id,omitempty"`

	// Frame properties
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Format   string `json:"format"`   // "jpeg", "h264", "raw"
	Encoding string `json:"encoding"` // "base64", "binary"

	// Frame data
	Data []byte `json:"data"`

	// Optional: Detection overlay (from vision2constellation)
	Detections []VideoDetectionBBox `json:"detections,omitempty"`

	// Quality/Priority
	Priority int `json:"priority,omitempty"` // 0=low, 1=normal, 2=high
	Quality  int `json:"quality,omitempty"`  // JPEG quality 1-100
}

// VideoDetectionBBox represents a detection bounding box in a video frame
type VideoDetectionBBox struct {
	ClassID    int     `json:"class_id"`
	ClassName  string  `json:"class_name"`
	Confidence float64 `json:"confidence"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	TrackID    string  `json:"track_id,omitempty"`
}

// Constants
const (
	// Entity Types - aligned with db/schema.sql entity_type CHECK constraint
	// Aircraft
	EntityTypeAircraftFixedWing  = "aircraft_fixed_wing"
	EntityTypeAircraftMultirotor = "aircraft_multirotor"
	EntityTypeAircraftVTOL       = "aircraft_vtol"
	EntityTypeAircraftHelicopter = "aircraft_helicopter"
	EntityTypeAircraftAirship    = "aircraft_airship"
	// Ground Vehicles
	EntityTypeGroundVehicleWheeled = "ground_vehicle_wheeled"
	EntityTypeGroundVehicleTracked = "ground_vehicle_tracked"
	// Marine
	EntityTypeSurfaceVesselUSV  = "surface_vessel_usv"
	EntityTypeUnderwaterVehicle = "underwater_vehicle"
	// Platforms & Systems
	EntityTypeSensorPlatform  = "sensor_platform"
	EntityTypePayloadSystem   = "payload_system"
	EntityTypeOperatorStation = "operator_station"
	// Geographic/Mission
	EntityTypeWaypoint  = "waypoint"
	EntityTypeNoFlyZone = "no_fly_zone"
	EntityTypeGeofence  = "geofence"
)

// ValidRoles is the set of allowed user roles.
var ValidRoles = map[string]bool{
	RoleAdmin:    true,
	RoleOperator: true,
	RoleViewer:   true,
}

// IsValidRole reports whether role is a recognized user role.
func IsValidRole(role string) bool {
	return ValidRoles[role]
}

// EntityTypeDisplayNames maps entity types to human-readable display names
var EntityTypeDisplayNames = map[string]string{
	EntityTypeAircraftFixedWing:    "Fixed Wing",
	EntityTypeAircraftMultirotor:   "Multirotor",
	EntityTypeAircraftVTOL:         "VTOL",
	EntityTypeAircraftHelicopter:   "Helicopter",
	EntityTypeAircraftAirship:      "Airship",
	EntityTypeGroundVehicleWheeled: "Wheeled Vehicle",
	EntityTypeGroundVehicleTracked: "Tracked Vehicle",
	EntityTypeSurfaceVesselUSV:     "USV",
	EntityTypeUnderwaterVehicle:    "UUV",
	EntityTypeSensorPlatform:       "Sensor",
	EntityTypePayloadSystem:        "Payload",
	EntityTypeOperatorStation:      "Operator",
	EntityTypeWaypoint:             "Waypoint",
	EntityTypeNoFlyZone:            "No-Fly Zone",
	EntityTypeGeofence:             "Geofence",
}

// EntityTypeCategory groups entity types for UI display
var EntityTypeCategory = map[string]string{
	EntityTypeAircraftFixedWing:    "aircraft",
	EntityTypeAircraftMultirotor:   "aircraft",
	EntityTypeAircraftVTOL:         "aircraft",
	EntityTypeAircraftHelicopter:   "aircraft",
	EntityTypeAircraftAirship:      "aircraft",
	EntityTypeGroundVehicleWheeled: "ground",
	EntityTypeGroundVehicleTracked: "ground",
	EntityTypeSurfaceVesselUSV:     "marine",
	EntityTypeUnderwaterVehicle:    "marine",
	EntityTypeSensorPlatform:       "system",
	EntityTypePayloadSystem:        "system",
	EntityTypeOperatorStation:      "system",
	EntityTypeWaypoint:             "geo",
	EntityTypeNoFlyZone:            "geo",
	EntityTypeGeofence:             "geo",
}

// EntityTypeLucideIcon maps entity types to Lucide icon names
// These icons are used for both UI cards and MapLibre markers
var EntityTypeLucideIcon = map[string]string{
	EntityTypeAircraftFixedWing:    "plane",
	EntityTypeAircraftMultirotor:   "fan",
	EntityTypeAircraftVTOL:         "plane-takeoff",
	EntityTypeAircraftHelicopter:   "fan",
	EntityTypeAircraftAirship:      "cloud",
	EntityTypeGroundVehicleWheeled: "car",
	EntityTypeGroundVehicleTracked: "truck",
	EntityTypeSurfaceVesselUSV:     "ship",
	EntityTypeUnderwaterVehicle:    "anchor",
	EntityTypeSensorPlatform:       "radar",
	EntityTypePayloadSystem:        "box",
	EntityTypeOperatorStation:      "monitor",
	EntityTypeWaypoint:             "map-pin",
	EntityTypeNoFlyZone:            "shield-ban",
	EntityTypeGeofence:             "hexagon",
}

// GetEntityTypeLucideIcon returns the Lucide icon name for the entity type
func GetEntityTypeLucideIcon(entityType string) string {
	if icon, ok := EntityTypeLucideIcon[entityType]; ok {
		return icon
	}
	return "help-circle" // fallback icon
}

// GetEntityTypeDisplayName returns a human-readable name for the entity type
func GetEntityTypeDisplayName(entityType string) string {
	if name, ok := EntityTypeDisplayNames[entityType]; ok {
		return name
	}
	return entityType // fallback to raw value
}

// GetEntityTypeCategory returns the category for the entity type
func GetEntityTypeCategory(entityType string) string {
	if cat, ok := EntityTypeCategory[entityType]; ok {
		return cat
	}
	return "unknown"
}

const (
	// Entity Status
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusUnknown  = "unknown"
	StatusOffline  = "offline"
	StatusOnline   = "online"

	// Priority Levels
	PriorityLow      = "low"
	PriorityNormal   = "normal"
	PriorityHigh     = "high"
	PriorityCritical = "critical"

	// Organization Types
	OrgTypeCompany    = "company"
	OrgTypeAgency     = "agency"
	OrgTypeIndividual = "individual"

	// User Roles
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	// Event Types
	EventTypeCreated = "created"
	EventTypeUpdated = "updated"
	EventTypeDeleted = "deleted"
	EventTypeStatus  = "status_changed"
	EventTypeAlert   = "alert"
)
