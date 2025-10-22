package services

import (
	"constellation-api/pkg/ontology"
	"constellation-api/pkg/shared"
	embeddednats "constellation-api/pkg/services/embedded-nats"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

type EntityService struct {
	db   *sql.DB
	nats *embeddednats.EmbeddedNATS
}

func NewEntityService(db *sql.DB, nats *embeddednats.EmbeddedNATS) *EntityService {
	return &EntityService{
		db:   db,
		nats: nats,
	}
}

func (s *EntityService) CreateEntity(orgID string, req *ontology.CreateEntityRequest) (*ontology.Entity, error) {
	entityID := uuid.New().String()
	now := time.Now()

	// Set defaults
	status := shared.StatusUnknown
	if req.Status != "" {
		status = req.Status
	}

	priority := shared.PriorityNormal
	if req.Priority != "" {
		priority = req.Priority
	}

	metadataJSON := "{}"
	if req.Metadata != nil {
		bytes, _ := json.Marshal(req.Metadata)
		metadataJSON = string(bytes)
	}

	var latitude, longitude, altitude interface{}
	if req.Position != nil {
		latitude = req.Position.Latitude
		longitude = req.Position.Longitude
		if req.Position.Altitude != 0 {
			altitude = req.Position.Altitude
		}
	}

	_, err := s.db.Exec(
		`INSERT INTO entities (entity_id, org_id, entity_type, status, priority, latitude, longitude, altitude, metadata, created_at, updated_at) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entityID, orgID, req.EntityType, status, priority, latitude, longitude, altitude, metadataJSON, 
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	entity := &ontology.Entity{
		EntityID:   entityID,
		OrgID:      orgID,
		EntityType: req.EntityType,
		Status:     status,
		Priority:   priority,
		Metadata:   metadataJSON,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if req.Position != nil {
		entity.Latitude = &req.Position.Latitude
		entity.Longitude = &req.Position.Longitude
		if req.Position.Altitude != 0 {
			entity.Altitude = &req.Position.Altitude
		}
	}

	// Publish entity created event
	go s.publishEntityEvent(entity, shared.EventTypeCreated)

	return entity, nil
}

func (s *EntityService) ListEntities(orgID string) ([]ontology.Entity, error) {
	rows, err := s.db.Query(
		`SELECT entity_id, org_id, entity_type, status, priority, is_live, 
		        latitude, longitude, altitude, heading, velocity, 
		        components, tags, metadata, created_at, updated_at 
		 FROM entities WHERE org_id = ?`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities: %w", err)
	}
	defer rows.Close()

	var entities []ontology.Entity
	for rows.Next() {
		entity, err := s.scanEntity(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, *entity)
	}

	return entities, nil
}

func (s *EntityService) GetEntity(orgID, entityID string) (*ontology.Entity, error) {
	row := s.db.QueryRow(
		`SELECT entity_id, org_id, entity_type, status, priority, is_live, 
		        latitude, longitude, altitude, heading, velocity, 
		        components, tags, metadata, created_at, updated_at 
		 FROM entities WHERE org_id = ? AND entity_id = ?`, 
		orgID, entityID,
	)

	entity, err := s.scanEntity(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("entity not found")
	}
	if err != nil {
		return nil, err
	}

	return entity, nil
}

func (s *EntityService) UpdateEntity(orgID, entityID string, updates map[string]interface{}) (*ontology.Entity, error) {
	if len(updates) == 0 {
		return nil, fmt.Errorf("no updates provided")
	}

	// Build dynamic update query
	query := "UPDATE entities SET updated_at = ?"
	args := []interface{}{time.Now().Format(time.RFC3339)}

	for key, value := range updates {
		switch key {
		case "status", "priority", "entity_type":
			query += fmt.Sprintf(", %s = ?", key)
			args = append(args, value)
		case "is_live":
			query += ", is_live = ?"
			if v, ok := value.(bool); ok {
				if v {
					args = append(args, 1)
				} else {
					args = append(args, 0)
				}
			}
		case "latitude", "longitude", "altitude", "heading", "velocity":
			query += fmt.Sprintf(", %s = ?", key)
			args = append(args, value)
		case "metadata", "components", "tags":
			bytes, _ := json.Marshal(value)
			query += fmt.Sprintf(", %s = ?", key)
			args = append(args, string(bytes))
		}
	}

	query += " WHERE org_id = ? AND entity_id = ?"
	args = append(args, orgID, entityID)

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update entity: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("entity not found")
	}

	// Get updated entity
	entity, err := s.GetEntity(orgID, entityID)
	if err != nil {
		return nil, err
	}

	// Publish entity updated event
	go s.publishEntityEvent(entity, shared.EventTypeUpdated)

	return entity, nil
}

func (s *EntityService) DeleteEntity(orgID, entityID string) error {
	// Get entity before deletion for event
	entity, err := s.GetEntity(orgID, entityID)
	if err != nil {
		return err
	}

	result, err := s.db.Exec(
		"DELETE FROM entities WHERE org_id = ? AND entity_id = ?", 
		orgID, entityID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("entity not found")
	}

	// Publish entity deleted event
	go s.publishEntityEvent(entity, shared.EventTypeDeleted)

	return nil
}

func (s *EntityService) UpdateEntityStatus(orgID, entityID, status string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	
	entity, err := s.UpdateEntity(orgID, entityID, updates)
	if err != nil {
		return err
	}

	// Publish status change event
	go s.publishEntityEvent(entity, shared.EventTypeStatus)

	return nil
}

func (s *EntityService) publishEntityEvent(entity *ontology.Entity, eventType string) {
	if s.nats == nil || s.nats.JetStream() == nil {
		log.Printf("NATS not available for publishing event")
		return
	}

	event := shared.Event{
		ID:      uuid.New().String(),
		Type:    eventType,
		Subject: shared.EntityCreatedSubject(entity.OrgID),
		Data: map[string]interface{}{
			"entity_id":   entity.EntityID,
			"org_id":      entity.OrgID,
			"entity_type": entity.EntityType,
			"status":      entity.Status,
			"priority":    entity.Priority,
		},
		Timestamp: time.Now().UTC(),
		Source:    "entity-service",
	}

	// Add full entity data for create/update events
	if eventType == shared.EventTypeCreated || eventType == shared.EventTypeUpdated {
		event.Data["entity"] = entity
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal entity event: %v", err)
		return
	}

	msgID := fmt.Sprintf("%s-%s-%d", entity.EntityID, eventType, time.Now().UnixNano())

	if err := s.nats.PublishWithDedup(event.Subject, data, msgID); err != nil {
		log.Printf("Failed to publish entity event: %v", err)
	} else {
		log.Printf("Published entity event: %s on subject: %s", eventType, event.Subject)
	}
}

func (s *EntityService) scanEntity(scanner interface{ Scan(...interface{}) error }) (*ontology.Entity, error) {
	var entity ontology.Entity
	var createdAt, updatedAt string
	var isLive int
	var lat, lon, alt, heading, velocity sql.NullFloat64

	err := scanner.Scan(
		&entity.EntityID, &entity.OrgID, &entity.EntityType, &entity.Status, &entity.Priority,
		&isLive, &lat, &lon, &alt, &heading, &velocity,
		&entity.Components, &entity.Tags, &entity.Metadata, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entity: %w", err)
	}

	entity.IsLive = isLive == 1
	if lat.Valid {
		entity.Latitude = &lat.Float64
	}
	if lon.Valid {
		entity.Longitude = &lon.Float64
	}
	if alt.Valid {
		entity.Altitude = &alt.Float64
	}
	if heading.Valid {
		entity.Heading = &heading.Float64
	}
	if velocity.Valid {
		entity.Velocity = &velocity.Float64
	}

	entity.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	entity.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &entity, nil
}