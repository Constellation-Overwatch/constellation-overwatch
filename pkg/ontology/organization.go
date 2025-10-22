package ontology

import (
	"time"
)

type Organization struct {
	OrgID     string    `json:"org_id" db:"org_id"`
	Name      string    `json:"name" db:"name"`
	OrgType   string    `json:"org_type" db:"org_type"`
	Metadata  string    `json:"metadata,omitempty" db:"metadata"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type CreateOrganizationRequest struct {
	Name     string                 `json:"name" validate:"required,min=1,max=255"`
	OrgType  string                 `json:"org_type" validate:"required,oneof=company agency individual"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}