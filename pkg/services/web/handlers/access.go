package handlers

import (
	"errors"
	"net/http"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

var (
	errSessionAccessForbidden = errors.New("session access forbidden")
	errSessionResourceHidden  = errors.New("session resource not found")
)

type sessionAccess struct {
	role  string
	orgID string
}

func sessionAccessFromRequest(r *http.Request) (sessionAccess, error) {
	access := sessionAccess{
		role:  middleware.UserRoleFromContext(r.Context()),
		orgID: middleware.OrgIDFromContext(r.Context()),
	}
	switch access.role {
	case "admin":
		return access, nil
	case "operator", "viewer":
		if access.orgID == "" {
			return sessionAccess{}, errSessionAccessForbidden
		}
		return access, nil
	default:
		return sessionAccess{}, errSessionAccessForbidden
	}
}

func (a sessionAccess) isAdmin() bool {
	return a.role == "admin"
}

func requireAdminSession(r *http.Request) (sessionAccess, error) {
	access, err := sessionAccessFromRequest(r)
	if err != nil {
		return sessionAccess{}, err
	}
	if !access.isAdmin() {
		return sessionAccess{}, errSessionAccessForbidden
	}
	return access, nil
}

func requireOperatorSession(r *http.Request) (sessionAccess, error) {
	access, err := sessionAccessFromRequest(r)
	if err != nil {
		return sessionAccess{}, err
	}
	if access.role != "operator" && !access.isAdmin() {
		return sessionAccess{}, errSessionAccessForbidden
	}
	return access, nil
}

// authorizeOrg resolves organization ownership from the authenticated session.
// Admins may select an organization explicitly; other roles are always scoped
// to the organization stored in their session. A conflicting client-supplied ID
// is hidden with a not-found response to avoid tenant enumeration.
func (a sessionAccess) authorizeOrg(requestedOrgID string) (string, error) {
	if a.isAdmin() {
		return requestedOrgID, nil
	}
	if requestedOrgID != "" && requestedOrgID != a.orgID {
		return "", errSessionResourceHidden
	}
	return a.orgID, nil
}

func writeSessionAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSessionResourceHidden), errors.Is(err, shared.ErrNotFound):
		http.Error(w, "Not found", http.StatusNotFound)
	default:
		http.Error(w, "Forbidden", http.StatusForbidden)
	}
}

func isSessionAccessError(err error) bool {
	return errors.Is(err, errSessionAccessForbidden) ||
		errors.Is(err, errSessionResourceHidden) ||
		errors.Is(err, shared.ErrNotFound)
}

func organizationsForSession(
	access sessionAccess,
	orgSvc *services.OrganizationService,
) ([]ontology.Organization, error) {
	if access.isAdmin() {
		return orgSvc.ListOrganizations()
	}
	org, err := orgSvc.GetOrganization(access.orgID)
	if err != nil {
		return nil, err
	}
	return []ontology.Organization{*org}, nil
}

func entitiesForSession(
	access sessionAccess,
	requestedOrgID string,
	entitySvc *services.EntityService,
) (string, []ontology.Entity, error) {
	orgID, err := access.authorizeOrg(requestedOrgID)
	if err != nil {
		return "", nil, err
	}
	if orgID == "" {
		entities, err := entitySvc.ListAllEntities()
		return "", entities, err
	}
	entities, err := entitySvc.ListEntities(orgID)
	return orgID, entities, err
}

func entityForSession(
	access sessionAccess,
	requestedOrgID string,
	entityID string,
	entitySvc *services.EntityService,
) (string, *ontology.Entity, error) {
	orgID, err := access.authorizeOrg(requestedOrgID)
	if err != nil {
		return "", nil, err
	}

	var entity *ontology.Entity
	if orgID == "" {
		entity, err = entitySvc.GetEntityByID(entityID)
	} else {
		entity, err = entitySvc.GetEntity(orgID, entityID)
	}
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return "", nil, errSessionResourceHidden
		}
		return "", nil, err
	}
	return entity.OrgID, entity, nil
}
