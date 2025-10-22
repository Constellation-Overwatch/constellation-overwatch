package services

import (
	"constellation-api/pkg/ontology"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type OrganizationService struct {
	db *sql.DB
}

func (s *OrganizationService) DB() *sql.DB {
	return s.db
}

func NewOrganizationService(db *sql.DB) *OrganizationService {
	return &OrganizationService{db: db}
}

func (s *OrganizationService) CreateOrganization(req *ontology.CreateOrganizationRequest) (*ontology.Organization, error) {
	orgID := uuid.New().String()
	now := time.Now()

	metadataJSON := "{}"
	if req.Metadata != nil {
		bytes, _ := json.Marshal(req.Metadata)
		metadataJSON = string(bytes)
	}

	_, err := s.db.Exec(
		`INSERT INTO organizations (org_id, name, org_type, metadata, created_at, updated_at) 
		 VALUES (?, ?, ?, ?, ?, ?)`,
		orgID, req.Name, req.OrgType, metadataJSON, now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create organization: %w", err)
	}

	return &ontology.Organization{
		OrgID:     orgID,
		Name:      req.Name,
		OrgType:   req.OrgType,
		Metadata:  metadataJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *OrganizationService) ListOrganizations() ([]ontology.Organization, error) {
	rows, err := s.db.Query(
		`SELECT org_id, name, org_type, metadata, created_at, updated_at FROM organizations`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query organizations: %w", err)
	}
	defer rows.Close()

	var orgs []ontology.Organization
	for rows.Next() {
		var org ontology.Organization
		var createdAt, updatedAt string
		
		err := rows.Scan(&org.OrgID, &org.Name, &org.OrgType, &org.Metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan organization: %w", err)
		}
		
		org.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		org.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		orgs = append(orgs, org)
	}

	return orgs, nil
}

func (s *OrganizationService) GetOrganization(orgID string) (*ontology.Organization, error) {
	var org ontology.Organization
	var createdAt, updatedAt string

	err := s.db.QueryRow(
		`SELECT org_id, name, org_type, metadata, created_at, updated_at 
		 FROM organizations WHERE org_id = ?`,
		orgID,
	).Scan(&org.OrgID, &org.Name, &org.OrgType, &org.Metadata, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("organization not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query organization: %w", err)
	}

	org.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	org.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &org, nil
}

func (s *OrganizationService) UpdateOrganization(orgID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	// Build dynamic update query
	query := "UPDATE organizations SET updated_at = ? "
	args := []interface{}{time.Now().Format(time.RFC3339)}

	for key, value := range updates {
		switch key {
		case "name", "org_type":
			query += fmt.Sprintf(", %s = ?", key)
			args = append(args, value)
		case "metadata":
			bytes, _ := json.Marshal(value)
			query += ", metadata = ?"
			args = append(args, string(bytes))
		}
	}

	query += " WHERE org_id = ?"
	args = append(args, orgID)

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update organization: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("organization not found")
	}

	return nil
}

func (s *OrganizationService) DeleteOrganization(orgID string) error {
	result, err := s.db.Exec("DELETE FROM organizations WHERE org_id = ?", orgID)
	if err != nil {
		return fmt.Errorf("failed to delete organization: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("organization not found")
	}

	return nil
}