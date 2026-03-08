package ontology

import (
	"time"
)

// Valid entity types — must match the CHECK constraint in db/schema.sql.
const (
	EntityTypeAircraftFixedWing  = "aircraft_fixed_wing"
	EntityTypeAircraftMultirotor = "aircraft_multirotor"
	EntityTypeAircraftVTOL       = "aircraft_vtol"
	EntityTypeAircraftHelicopter = "aircraft_helicopter"
	EntityTypeAircraftAirship    = "aircraft_airship"
	EntityTypeGroundWheeled      = "ground_vehicle_wheeled"
	EntityTypeGroundTracked      = "ground_vehicle_tracked"
	EntityTypeSurfaceVesselUSV   = "surface_vessel_usv"
	EntityTypeUnderwaterVehicle  = "underwater_vehicle"
	EntityTypeSensorPlatform     = "sensor_platform"
	EntityTypePayloadSystem      = "payload_system"
	EntityTypeOperatorStation    = "operator_station"
	EntityTypeWaypoint           = "waypoint"
	EntityTypeNoFlyZone          = "no_fly_zone"
	EntityTypeGeofence           = "geofence"
)

// ValidEntityTypes is the canonical set used for validation.
var ValidEntityTypes = map[string]bool{
	EntityTypeAircraftFixedWing:  true,
	EntityTypeAircraftMultirotor: true,
	EntityTypeAircraftVTOL:       true,
	EntityTypeAircraftHelicopter: true,
	EntityTypeAircraftAirship:    true,
	EntityTypeGroundWheeled:      true,
	EntityTypeGroundTracked:      true,
	EntityTypeSurfaceVesselUSV:   true,
	EntityTypeUnderwaterVehicle:  true,
	EntityTypeSensorPlatform:     true,
	EntityTypePayloadSystem:      true,
	EntityTypeOperatorStation:    true,
	EntityTypeWaypoint:           true,
	EntityTypeNoFlyZone:          true,
	EntityTypeGeofence:           true,
}

// IsValidEntityType checks whether a given entity type string is valid.
func IsValidEntityType(t string) bool {
	return ValidEntityTypes[t]
}

type Entity struct {
	EntityID    string    `json:"entity_id" db:"entity_id"`
	OrgID       string    `json:"org_id" db:"org_id"`
	Name        string    `json:"name" db:"name"`
	EntityType  string    `json:"entity_type" db:"entity_type"`
	Status      string    `json:"status" db:"status"`
	Priority    string    `json:"priority" db:"priority"`
	IsLive      bool      `json:"is_live" db:"is_live"`
	Latitude    *float64  `json:"latitude,omitempty" db:"latitude"`
	Longitude   *float64  `json:"longitude,omitempty" db:"longitude"`
	Altitude    *float64  `json:"altitude,omitempty" db:"altitude"`
	Heading     *float64  `json:"heading,omitempty" db:"heading"`
	Velocity    *float64  `json:"velocity,omitempty" db:"velocity"`
	Components  string    `json:"components,omitempty" db:"components"`
	Tags        string    `json:"tags,omitempty" db:"tags"`
	Metadata    string    `json:"metadata,omitempty" db:"metadata"`
	VideoConfig string    `json:"video_config,omitempty" db:"video_config"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

type CreateEntityRequest struct {
	Name        string                 `json:"name,omitempty" maxLength:"255" doc:"Entity display name"`
	EntityType  string                 `json:"entity_type" enum:"aircraft_fixed_wing,aircraft_multirotor,aircraft_vtol,aircraft_helicopter,aircraft_airship,ground_vehicle_wheeled,ground_vehicle_tracked,surface_vessel_usv,underwater_vehicle,sensor_platform,payload_system,operator_station,waypoint,no_fly_zone,geofence" doc:"Entity type from ontology"`
	Status      string                 `json:"status,omitempty" enum:"active,inactive,unknown" doc:"Entity status"`
	Priority    string                 `json:"priority,omitempty" enum:"low,normal,high,critical" doc:"Priority level"`
	Position    *Position              `json:"position,omitempty" doc:"Geographic position"`
	Metadata    map[string]interface{} `json:"metadata,omitempty" doc:"Arbitrary metadata"`
	VideoConfig *VideoConfig           `json:"video_config,omitempty" doc:"Video stream configuration"`
}

type UpdateEntityRequest struct {
	Name        string                 `json:"name,omitempty" maxLength:"255" doc:"Entity display name"`
	Status      string                 `json:"status,omitempty" enum:"active,inactive,unknown" doc:"Entity status"`
	Priority    string                 `json:"priority,omitempty" enum:"low,normal,high,critical" doc:"Priority level"`
	IsLive      *bool                  `json:"is_live,omitempty" doc:"Whether entity is live"`
	Position    *Position              `json:"position,omitempty" doc:"Geographic position"`
	Metadata    map[string]interface{} `json:"metadata,omitempty" doc:"Arbitrary metadata"`
	VideoConfig *VideoConfig           `json:"video_config,omitempty" doc:"Video stream configuration"`
}

type Position struct {
	Latitude  float64 `json:"latitude" minimum:"-90" maximum:"90" doc:"Latitude in degrees"`
	Longitude float64 `json:"longitude" minimum:"-180" maximum:"180" doc:"Longitude in degrees"`
	Altitude  float64 `json:"altitude,omitempty" doc:"Altitude in meters"`
}

// VideoConfig defines the video stream endpoints for an entity.
type VideoConfig struct {
	Protocol         string `json:"protocol,omitempty" enum:"rtsp,rtmp,srt,hls" doc:"Stream source protocol"`
	Port             int    `json:"port,omitempty" doc:"Stream source port"`
	StreamURL        string `json:"stream_url,omitempty" doc:"Primary RTSP/RTMP stream URL"`
	OverlayURL       string `json:"overlay_url,omitempty" doc:"Detection overlay RTSP/RTMP stream URL (e.g. rtsp://host:8554/entity_id/pulsar)"`
	WebRTCURL        string `json:"webrtc_url,omitempty" doc:"WebRTC playback base URL for raw stream (WHEP = webrtc_url + /whep)"`
	OverlayWebRTCURL string `json:"overlay_webrtc_url,omitempty" doc:"WebRTC playback URL for detection overlay stream (WHEP = overlay_webrtc_url + /whep). Preferred by video player when set."`
	HLSURL           string `json:"hls_url,omitempty" doc:"HLS playback URL"`
}

// PreferredWebRTCURL returns the best WebRTC URL for video playback.
// Prefers the overlay (bounding box) stream when available.
func (vc *VideoConfig) PreferredWebRTCURL() string {
	if vc.OverlayWebRTCURL != "" {
		return vc.OverlayWebRTCURL
	}
	return vc.WebRTCURL
}
