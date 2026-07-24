package handlers

import (
	"net/http"
	"os"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	admin_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/admin/pages"
	fleet_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/fleet/pages"
	map_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/map/pages"
	org_components "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/organizations/components"
	org_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/organizations/pages"
	overwatch_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/overwatch/pages"
	streams_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/streams/pages"
	video_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/video/pages"
)

type PageHandler struct {
	orgSvc    *services.OrganizationService
	entitySvc *services.EntityService
}

func NewPageHandler(orgSvc *services.OrganizationService, entitySvc *services.EntityService) *PageHandler {
	return &PageHandler{
		orgSvc:    orgSvc,
		entitySvc: entitySvc,
	}
}

func (h *PageHandler) HandleEntitiesPage(w http.ResponseWriter, r *http.Request) {
	access, err := sessionAccessFromRequest(r)
	if err != nil {
		writeSessionAccessError(w, err)
		return
	}

	orgs, err := organizationsForSession(access, h.orgSvc)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	orgID, entities, err := entitiesForSession(access, r.URL.Query().Get("org_id"), h.entitySvc)
	if err != nil {
		if isSessionAccessError(err) {
			writeSessionAccessError(w, err)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := org_pages.OrganizationsPage(orgs, orgID, entities)
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching entities page: %v", err)
		}
		return
	}

	component := org_pages.OrganizationsPage(orgs, orgID, entities)
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render entities page: %v", err)
	}
}

func (h *PageHandler) HandleEntityForm(w http.ResponseWriter, r *http.Request) {
	access, err := sessionAccessFromRequest(r)
	if err != nil {
		writeSessionAccessError(w, err)
		return
	}

	orgID, err := access.authorizeOrg(r.URL.Query().Get("org_id"))
	if err != nil {
		writeSessionAccessError(w, err)
		return
	}
	entityID := r.URL.Query().Get("entity_id")

	var entity *ontology.Entity
	isEdit := false

	if entityID != "" {
		isEdit = true
		resolvedOrgID, e, err := entityForSession(access, orgID, entityID, h.entitySvc)
		if err != nil {
			if isSessionAccessError(err) {
				writeSessionAccessError(w, err)
				return
			}
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		orgID = resolvedOrgID
		entity = e
	}

	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := org_components.EntityForm(orgID, entity, isEdit)
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("#entity-form-modal"),
			datastar.WithModeInner())
		if err != nil {
			logger.Infof("Error patching entity form: %v", err)
		}
		return
	}

	component := org_components.EntityForm(orgID, entity, isEdit)
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render entity form: %v", err)
	}
}

func (h *PageHandler) HandleStreamsPage(w http.ResponseWriter, r *http.Request) {
	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := streams_pages.StreamsPage()
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching streams page: %v", err)
		}
		return
	}

	component := streams_pages.StreamsPage()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render streams page: %v", err)
	}
}

func (h *PageHandler) HandleOrganizationForm(w http.ResponseWriter, r *http.Request) {
	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := org_components.OrganizationForm()
		// Try using fragment/morph mode instead of inner
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("#org-form-modal"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching organization form: %v", err)
		}
		return
	}

	component := org_components.OrganizationForm()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render organization form: %v", err)
	}
}

func (h *PageHandler) HandleOverwatchPage(w http.ResponseWriter, r *http.Request) {
	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := overwatch_pages.OverwatchPage()
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching overwatch page: %v", err)
		}
		return
	}

	component := overwatch_pages.OverwatchPage()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render overwatch page: %v", err)
	}
}

func (h *PageHandler) HandleFleetPage(w http.ResponseWriter, r *http.Request) {
	access, err := sessionAccessFromRequest(r)
	if err != nil {
		writeSessionAccessError(w, err)
		return
	}

	orgs, err := organizationsForSession(access, h.orgSvc)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	_, entities, err := entitiesForSession(access, "", h.entitySvc)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := fleet_pages.FleetPage(orgs, entities)
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching fleet page: %v", err)
		}
		return
	}

	component := fleet_pages.FleetPage(orgs, entities)
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render fleet page: %v", err)
	}
}

func (h *PageHandler) HandleVideoPage(w http.ResponseWriter, r *http.Request) {
	// Fetch all entities to populate the dropdown
	entities, err := h.entitySvc.ListAllEntities()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Extract entity IDs for the template
	var entityIDs []string
	for _, entity := range entities {
		entityIDs = append(entityIDs, entity.EntityID)
	}

	// Get MediaMTX WebRTC base URL for WHEP playback
	webrtcBaseURL := os.Getenv("MEDIAMTX_WEBRTC_URL")

	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := video_pages.VideoPage(entityIDs, webrtcBaseURL)
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching video page: %v", err)
		}
		return
	}

	component := video_pages.VideoPage(entityIDs, webrtcBaseURL)
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render video page: %v", err)
	}
}

func (h *PageHandler) HandleMapPage(w http.ResponseWriter, r *http.Request) {
	// If this is a Datastar request, return SSE format
	if r.Header.Get("Accept") == "text/event-stream" {
		sse := datastar.NewSSE(w, r)
		component := map_pages.MapPage()
		err := sse.PatchElementTempl(component,
			datastar.WithSelector("body"),
			datastar.WithModeOuter())
		if err != nil {
			logger.Infof("Error patching map page: %v", err)
		}
		return
	}

	component := map_pages.MapPage()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render map page: %v", err)
	}
}

func (h *PageHandler) HandleAdminPage(w http.ResponseWriter, r *http.Request) {
	component := admin_pages.AdminPage()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render admin page: %v", err)
	}
}
