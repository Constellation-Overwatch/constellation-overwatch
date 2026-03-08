package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/danielgtaylor/huma/v2"
)

type EntityHandler struct {
	service *services.EntityService
}

func NewEntityHandler(service *services.EntityService) *EntityHandler {
	return &EntityHandler{service: service}
}

// ── Input / Output types ────────────────────────────────────────────

type OrgPathParam struct {
	OrgID string `path:"org_id" doc:"Organization ID"`
}

type EntityPathParam struct {
	OrgID    string `path:"org_id" doc:"Organization ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}

type CreateEntityInput struct {
	OrgPathParam
	Body ontology.CreateEntityRequest
}

type UpdateEntityInput struct {
	EntityPathParam
	Body ontology.UpdateEntityRequest
}

type EntityOutput struct {
	Body ontology.Entity
}

type EntityListOutput struct {
	Body []ontology.Entity
}

type DeletedOutput struct {
	Body struct {
		Message string `json:"message" doc:"Confirmation message"`
	}
}

// ── Registration ────────────────────────────────────────────────────

func (h *EntityHandler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-entity",
		Method:        http.MethodPost,
		Path:          "/v1/organizations/{org_id}/entities",
		Summary:       "Create entity",
		Description:   "Create a new entity within an organization",
		Tags:          []string{"Entities"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"APIKeyAuth": {"entities:write"}}},
	}, func(ctx context.Context, input *CreateEntityInput) (*EntityOutput, error) {
		entity, err := h.service.CreateEntity(input.OrgID, &input.Body)
		if err != nil {
			if errors.Is(err, shared.ErrInvalidInput) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			logger.Errorw("Failed to create entity", "error", err, "org_id", input.OrgID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		return &EntityOutput{Body: *entity}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-entities",
		Method:      http.MethodGet,
		Path:        "/v1/organizations/{org_id}/entities",
		Summary:     "List entities",
		Description: "Get all entities for an organization",
		Tags:        []string{"Entities"},
		Security:    []map[string][]string{{"APIKeyAuth": {"entities:read"}}},
	}, func(ctx context.Context, input *struct{ OrgPathParam }) (*EntityListOutput, error) {
		entities, err := h.service.ListEntities(input.OrgID)
		if err != nil {
			logger.Errorw("Failed to list entities", "error", err, "org_id", input.OrgID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		if entities == nil {
			entities = []ontology.Entity{}
		}
		return &EntityListOutput{Body: entities}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-entity",
		Method:      http.MethodGet,
		Path:        "/v1/organizations/{org_id}/entities/{entity_id}",
		Summary:     "Get entity",
		Description: "Get a single entity by ID",
		Tags:        []string{"Entities"},
		Security:    []map[string][]string{{"APIKeyAuth": {"entities:read"}}},
	}, func(ctx context.Context, input *struct{ EntityPathParam }) (*EntityOutput, error) {
		entity, err := h.service.GetEntity(input.OrgID, input.EntityID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, huma.Error404NotFound("Entity not found")
			}
			logger.Errorw("Failed to get entity", "error", err, "org_id", input.OrgID, "entity_id", input.EntityID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		return &EntityOutput{Body: *entity}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-entity",
		Method:      http.MethodPut,
		Path:        "/v1/organizations/{org_id}/entities/{entity_id}",
		Summary:     "Update entity",
		Description: "Update an existing entity",
		Tags:        []string{"Entities"},
		Security:    []map[string][]string{{"APIKeyAuth": {"entities:write"}}},
	}, func(ctx context.Context, input *UpdateEntityInput) (*EntityOutput, error) {
		updates := buildEntityUpdates(&input.Body)
		entity, err := h.service.UpdateEntity(input.OrgID, input.EntityID, updates)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, huma.Error404NotFound("Entity not found")
			}
			if errors.Is(err, shared.ErrNoUpdates) {
				return nil, huma.Error400BadRequest("No updates provided")
			}
			logger.Errorw("Failed to update entity", "error", err, "org_id", input.OrgID, "entity_id", input.EntityID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		return &EntityOutput{Body: *entity}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-entity",
		Method:      http.MethodDelete,
		Path:        "/v1/organizations/{org_id}/entities/{entity_id}",
		Summary:     "Delete entity",
		Description: "Delete an entity by ID",
		Tags:        []string{"Entities"},
		Security:    []map[string][]string{{"APIKeyAuth": {"entities:write"}}},
	}, func(ctx context.Context, input *struct{ EntityPathParam }) (*DeletedOutput, error) {
		err := h.service.DeleteEntity(input.OrgID, input.EntityID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, huma.Error404NotFound("Entity not found")
			}
			logger.Errorw("Failed to delete entity", "error", err, "org_id", input.OrgID, "entity_id", input.EntityID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		out := &DeletedOutput{}
		out.Body.Message = "Entity deleted successfully"
		return out, nil
	})
}

// buildEntityUpdates converts a typed UpdateEntityRequest into the map the service expects.
func buildEntityUpdates(req *ontology.UpdateEntityRequest) map[string]interface{} {
	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Priority != "" {
		updates["priority"] = req.Priority
	}
	if req.IsLive != nil {
		updates["is_live"] = *req.IsLive
	}
	if req.Position != nil {
		updates["latitude"] = req.Position.Latitude
		updates["longitude"] = req.Position.Longitude
		if req.Position.Altitude != 0 {
			updates["altitude"] = req.Position.Altitude
		}
	}
	if req.Metadata != nil {
		updates["metadata"] = req.Metadata
	}
	if req.VideoConfig != nil {
		updates["video_config"] = req.VideoConfig
	}
	return updates
}
