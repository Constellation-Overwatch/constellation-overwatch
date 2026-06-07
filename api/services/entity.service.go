package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
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
	if !ontology.IsValidEntityType(req.EntityType) {
		return nil, fmt.Errorf("invalid entity_type %q: %w", req.EntityType, shared.ErrInvalidInput)
	}

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
		bytes, err := json.Marshal(req.Metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal metadata: %w", err)
		}
		metadataJSON = string(bytes)
	}

	videoConfigJSON := "{}"
	if req.VideoConfig != nil {
		bytes, err := json.Marshal(req.VideoConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal video config: %w", err)
		}
		videoConfigJSON = string(bytes)
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
		`INSERT INTO entities (entity_id, org_id, name, entity_type, status, priority, latitude, longitude, altitude, metadata, video_config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entityID, orgID, req.Name, req.EntityType, status, priority, latitude, longitude, altitude, metadataJSON, videoConfigJSON,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	entity := &ontology.Entity{
		EntityID:    entityID,
		OrgID:       orgID,
		Name:        req.Name,
		EntityType:  req.EntityType,
		Status:      status,
		Priority:    priority,
		Metadata:    metadataJSON,
		VideoConfig: videoConfigJSON,
		CreatedAt:   now,
		UpdatedAt:   now,
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

	// Sync to KV store
	go s.syncToKV(entity)

	return entity, nil
}

func (s *EntityService) ListEntities(orgID string) ([]ontology.Entity, error) {
	rows, err := s.db.Query(
		`SELECT entity_id, org_id, name, entity_type, status, priority, is_live,
		        latitude, longitude, altitude, heading, velocity,
		        components, tags, metadata, video_config, created_at, updated_at
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating entity rows: %w", err)
	}

	return entities, nil
}

func (s *EntityService) ListAllEntities() ([]ontology.Entity, error) {
	rows, err := s.db.Query(
		`SELECT entity_id, org_id, name, entity_type, status, priority, is_live,
		        latitude, longitude, altitude, heading, velocity,
		        components, tags, metadata, video_config, created_at, updated_at
		 FROM entities ORDER BY updated_at DESC`,
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating entity rows: %w", err)
	}

	return entities, nil
}

func (s *EntityService) GetEntity(orgID, entityID string) (*ontology.Entity, error) {
	row := s.db.QueryRow(
		`SELECT entity_id, org_id, name, entity_type, status, priority, is_live,
		        latitude, longitude, altitude, heading, velocity,
		        components, tags, metadata, video_config, created_at, updated_at
		 FROM entities WHERE org_id = ? AND entity_id = ?`,
		orgID, entityID,
	)

	entity, err := s.scanEntity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("entity: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	return entity, nil
}

func (s *EntityService) UpdateEntity(orgID, entityID string, updates map[string]interface{}) (*ontology.Entity, error) {
	if len(updates) == 0 {
		return nil, shared.ErrNoUpdates
	}

	// Build dynamic update query
	query := "UPDATE entities SET updated_at = ?"
	args := []interface{}{time.Now().Format(time.RFC3339)}

	for key, value := range updates {
		switch key {
		case "status", "priority", "entity_type", "name":
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
		case "metadata", "components", "tags", "video_config":
			bytes, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal %s: %w", key, err)
			}
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

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, fmt.Errorf("entity: %w", shared.ErrNotFound)
	}

	// Get updated entity
	entity, err := s.GetEntity(orgID, entityID)
	if err != nil {
		return nil, err
	}

	// Publish entity updated event
	go s.publishEntityEvent(entity, shared.EventTypeUpdated)

	// Sync to KV store
	go s.syncToKV(entity)

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

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("entity: %w", shared.ErrNotFound)
	}

	// Publish entity deleted event
	go s.publishEntityEvent(entity, shared.EventTypeDeleted)

	// Remove from KV store
	go s.removeFromKV(entity.EntityID)

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
		logger.Warn("NATS not available for publishing event")
		return
	}

	var subjectFn func(string) string
	switch eventType {
	case shared.EventTypeCreated:
		subjectFn = shared.EntityCreatedSubject
	case shared.EventTypeUpdated:
		subjectFn = shared.EntityUpdatedSubject
	case shared.EventTypeDeleted:
		subjectFn = shared.EntityDeletedSubject
	case shared.EventTypeStatus:
		subjectFn = shared.EntityStatusSubject
	default:
		subjectFn = shared.EntityCreatedSubject
	}

	event := shared.Event{
		ID:      uuid.New().String(),
		Type:    eventType,
		Subject: subjectFn(entity.OrgID),
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
		logger.Error("Failed to marshal entity event", zap.Error(err))
		return
	}

	msgID := fmt.Sprintf("%s-%s-%d", entity.EntityID, eventType, time.Now().UnixNano())

	if err := s.nats.PublishWithDedup(event.Subject, data, msgID); err != nil {
		logger.Error("Failed to publish entity event", zap.Error(err))
	} else {
		logger.Info("Published entity event", zap.String("event_type", eventType), zap.String("subject", event.Subject))
	}
}

func (s *EntityService) scanEntity(scanner interface{ Scan(...interface{}) error }) (*ontology.Entity, error) {
	var entity ontology.Entity
	var createdAt, updatedAt string
	var isLive int
	var lat, lon, alt, heading, velocity sql.NullFloat64
	var name sql.NullString

	err := scanner.Scan(
		&entity.EntityID, &entity.OrgID, &name, &entity.EntityType, &entity.Status, &entity.Priority,
		&isLive, &lat, &lon, &alt, &heading, &velocity,
		&entity.Components, &entity.Tags, &entity.Metadata, &entity.VideoConfig, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entity: %w", err)
	}

	if name.Valid {
		entity.Name = name.String
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

	if t, err := time.Parse(time.RFC3339, createdAt); err != nil {
		logger.Debugw("Failed to parse created_at timestamp", "value", createdAt, "error", err)
	} else {
		entity.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		logger.Debugw("Failed to parse updated_at timestamp", "value", updatedAt, "error", err)
	} else {
		entity.UpdatedAt = t
	}

	return &entity, nil
}

// syncToKV updates the entity state in the KV store
func (s *EntityService) syncToKV(entity *ontology.Entity) {
	if s.nats == nil {
		return
	}

	kv := s.nats.KeyValue()
	if kv == nil {
		return
	}

	// Get existing state if possible to preserve telemetry
	key := shared.EntityKey(entity.EntityID)
	var state shared.EntityState

	entry, err := kv.Get(key)
	if err == nil {
		// Unmarshal existing state
		if err := json.Unmarshal(entry.Value(), &state); err != nil {
			logger.Errorw("Failed to unmarshal existing KV entry", "entity_id", entity.EntityID, "error", err)
			// Continue with new state if unmarshal fails
		}
	}

	// Update static fields from DB entity
	state.EntityID = entity.EntityID
	state.OrgID = entity.OrgID
	state.Name = entity.Name
	state.EntityType = entity.EntityType
	state.Status = entity.Status
	state.Priority = entity.Priority
	state.IsLive = entity.IsLive
	state.UpdatedAt = time.Now()

	// Parse and set video config
	if entity.VideoConfig != "" && entity.VideoConfig != "{}" {
		var vc ontology.VideoConfig
		if json.Unmarshal([]byte(entity.VideoConfig), &vc) == nil {
			state.VideoConfig = &vc
		}
	}

	// If we have an org name available (we might need to fetch it if not in entity struct), set it
	// For now, we'll rely on the fact that if it was already there, we kept it.
	// If it wasn't there, we might want to fetch it, but that adds a DB query.
	// Let's do a quick check if we need to fetch org name
	if state.OrgName == "" {
		var orgName string
		err := s.db.QueryRow("SELECT name FROM organizations WHERE org_id = ?", entity.OrgID).Scan(&orgName)
		if err == nil {
			state.OrgName = orgName
		}
	}

	// Marshal back to JSON
	data, err := json.Marshal(state)
	if err != nil {
		logger.Errorw("Failed to marshal entity state for KV", "entity_id", entity.EntityID, "error", err)
		return
	}

	// Put to KV
	if _, err := kv.Put(key, data); err != nil {
		logger.Errorw("Failed to put entity to KV", "entity_id", entity.EntityID, "error", err)
	} else {
		logger.Infow("Synced entity to KV", "entity_id", entity.EntityID)
	}
}

// removeFromKV removes the entity from the KV store
func (s *EntityService) removeFromKV(entityID string) {
	if s.nats == nil {
		return
	}

	kv := s.nats.KeyValue()
	if kv == nil {
		return
	}

	key := shared.EntityKey(entityID)
	if err := kv.Delete(key); err != nil {
		logger.Errorw("Failed to delete entity from KV", "entity_id", entityID, "error", err)
	} else {
		logger.Infow("Removed entity from KV", "entity_id", entityID)
	}
}
