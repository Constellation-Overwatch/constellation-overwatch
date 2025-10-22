package ontology

import (
	"time"
)

type Entity struct {
	EntityID   string    `json:"entity_id" db:"entity_id"`
	OrgID      string    `json:"org_id" db:"org_id"`
	EntityType string    `json:"entity_type" db:"entity_type"`
	Status     string    `json:"status" db:"status"`
	Priority   string    `json:"priority" db:"priority"`
	IsLive     bool      `json:"is_live" db:"is_live"`
	Latitude   *float64  `json:"latitude,omitempty" db:"latitude"`
	Longitude  *float64  `json:"longitude,omitempty" db:"longitude"`
	Altitude   *float64  `json:"altitude,omitempty" db:"altitude"`
	Heading    *float64  `json:"heading,omitempty" db:"heading"`
	Velocity   *float64  `json:"velocity,omitempty" db:"velocity"`
	Components string    `json:"components,omitempty" db:"components"`
	Tags       string    `json:"tags,omitempty" db:"tags"`
	Metadata   string    `json:"metadata,omitempty" db:"metadata"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

type CreateEntityRequest struct {
	EntityType string                 `json:"entity_type" validate:"required"`
	Status     string                 `json:"status,omitempty" validate:"omitempty,oneof=active inactive unknown"`
	Priority   string                 `json:"priority,omitempty" validate:"omitempty,oneof=low normal high critical"`
	Position   *Position              `json:"position,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type Position struct {
	Latitude  float64 `json:"latitude" validate:"required,min=-90,max=90"`
	Longitude float64 `json:"longitude" validate:"required,min=-180,max=180"`
	Altitude  float64 `json:"altitude,omitempty"`
}