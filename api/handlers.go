package api

import (
	"constellation-api/api/middleware"
	"constellation-api/api/services"
	"constellation-api/pkg/ontology"
	"constellation-api/pkg/shared"
	embeddednats "constellation-api/pkg/services/embedded-nats"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

type Handlers struct {
	orgService    *services.OrganizationService
	entityService *services.EntityService
}

func NewHandlers(db *sql.DB, nats *embeddednats.EmbeddedNATS) *Handlers {
	return &Handlers{
		orgService:    services.NewOrganizationService(db),
		entityService: services.NewEntityService(db, nats),
	}
}

// Organization handlers
func (h *Handlers) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	var req ontology.CreateOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	org, err := h.orgService.CreateOrganization(&req)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}

	sendSuccess(w, http.StatusCreated, org)
}

func (h *Handlers) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.orgService.ListOrganizations()
	if err != nil {
		sendError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	sendSuccess(w, http.StatusOK, orgs)
}

func (h *Handlers) GetOrganization(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_ORG_ID", "org_id is required")
		return
	}

	org, err := h.orgService.GetOrganization(orgID)
	if err != nil {
		if err.Error() == "organization not found" {
			sendError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		} else {
			sendError(w, http.StatusInternalServerError, "GET_FAILED", err.Error())
		}
		return
	}

	sendSuccess(w, http.StatusOK, org)
}

// Entity handlers
func (h *Handlers) CreateEntity(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_ORG_ID", "org_id is required")
		return
	}

	var req ontology.CreateEntityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	entity, err := h.entityService.CreateEntity(orgID, &req)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}

	sendSuccess(w, http.StatusCreated, entity)
}

func (h *Handlers) ListEntities(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_ORG_ID", "org_id is required")
		return
	}

	entities, err := h.entityService.ListEntities(orgID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	sendSuccess(w, http.StatusOK, entities)
}

func (h *Handlers) GetEntity(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	entityID := r.URL.Query().Get("entity_id")

	if orgID == "" || entityID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_PARAMS", "org_id and entity_id are required")
		return
	}

	entity, err := h.entityService.GetEntity(orgID, entityID)
	if err != nil {
		if err.Error() == "entity not found" {
			sendError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		} else {
			sendError(w, http.StatusInternalServerError, "GET_FAILED", err.Error())
		}
		return
	}

	sendSuccess(w, http.StatusOK, entity)
}

func (h *Handlers) UpdateEntity(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	entityID := r.URL.Query().Get("entity_id")

	if orgID == "" || entityID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_PARAMS", "org_id and entity_id are required")
		return
	}

	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		sendError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	entity, err := h.entityService.UpdateEntity(orgID, entityID, updates)
	if err != nil {
		if err.Error() == "entity not found" {
			sendError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		} else {
			sendError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		}
		return
	}

	sendSuccess(w, http.StatusOK, entity)
}

func (h *Handlers) DeleteEntity(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	entityID := r.URL.Query().Get("entity_id")

	if orgID == "" || entityID == "" {
		sendError(w, http.StatusBadRequest, "MISSING_PARAMS", "org_id and entity_id are required")
		return
	}

	err := h.entityService.DeleteEntity(orgID, entityID)
	if err != nil {
		if err.Error() == "entity not found" {
			sendError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		} else {
			sendError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		}
		return
	}

	sendSuccess(w, http.StatusOK, map[string]string{"message": "Entity deleted successfully"})
}

// Health check
func (h *Handlers) HealthCheck(nats *embeddednats.EmbeddedNATS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := shared.HealthStatus{
			Status:    "healthy",
			Service:   "constellation-api",
			Timestamp: time.Now(),
			Details:   make(map[string]string),
		}

		// Check database
		if err := h.orgService.DB().Ping(); err != nil {
			health.Status = "unhealthy"
			health.Details["database"] = "unhealthy: " + err.Error()
		} else {
			health.Details["database"] = "healthy"
		}

		// Check NATS
		if err := nats.HealthCheck(); err != nil {
			health.Status = "unhealthy"
			health.Details["nats"] = "unhealthy: " + err.Error()
		} else {
			health.Details["nats"] = "healthy"
		}

		statusCode := http.StatusOK
		if health.Status == "unhealthy" {
			statusCode = http.StatusServiceUnavailable
		}

		sendSuccess(w, statusCode, health)
	}
}

// Helper functions
func sendSuccess(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	
	response := shared.Response{
		Success: true,
		Data:    data,
	}
	
	json.NewEncoder(w).Encode(response)
}

func sendError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	
	response := shared.Response{
		Success: false,
		Error: &shared.Error{
			Code:    code,
			Message: message,
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// RegisterRoutes sets up all API routes
func (h *Handlers) RegisterRoutes(mux *http.ServeMux, nats *embeddednats.EmbeddedNATS) {
	// Health check (no auth required)
	mux.HandleFunc("/health", h.HealthCheck(nats))

	// Organization endpoints
	mux.HandleFunc("/api/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			middleware.BearerAuth(h.CreateOrganization)(w, r)
		case http.MethodGet:
			if r.URL.Query().Get("org_id") != "" {
				middleware.BearerAuth(h.GetOrganization)(w, r)
			} else {
				middleware.BearerAuth(h.ListOrganizations)(w, r)
			}
		default:
			sendError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		}
	})

	// Entity endpoints
	mux.HandleFunc("/api/v1/entities", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			middleware.BearerAuth(h.CreateEntity)(w, r)
		case http.MethodGet:
			if r.URL.Query().Get("entity_id") != "" {
				middleware.BearerAuth(h.GetEntity)(w, r)
			} else {
				middleware.BearerAuth(h.ListEntities)(w, r)
			}
		case http.MethodPut:
			middleware.BearerAuth(h.UpdateEntity)(w, r)
		case http.MethodDelete:
			middleware.BearerAuth(h.DeleteEntity)(w, r)
		default:
			sendError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		}
	})
}