package handlers

import (
	"errors"
	"net/http"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

var errResourceNotFound = errors.New("resource not found")

// authorizedOrganizationID derives the effective organization from the
// authenticated session. Admins may explicitly target any organization and
// may use an empty target for global reads. Non-admin input can never override
// the organization stored in the session.
func authorizedOrganizationID(r *http.Request, requested string) (string, error) {
	role := middleware.UserRoleFromContext(r.Context())
	if role == shared.RoleAdmin {
		return requested, nil
	}
	if role != shared.RoleOperator && role != shared.RoleViewer {
		return "", errResourceNotFound
	}

	sessionOrgID := middleware.OrgIDFromContext(r.Context())
	if sessionOrgID == "" || (requested != "" && requested != sessionOrgID) {
		return "", errResourceNotFound
	}
	return sessionOrgID, nil
}

func organizationEventAllowed(r *http.Request, eventOrgID string) bool {
	_, err := authorizedOrganizationID(r, eventOrgID)
	return err == nil
}

func organizationsForRequest(r *http.Request, service *services.OrganizationService) ([]ontology.Organization, error) {
	orgID, err := authorizedOrganizationID(r, "")
	if err != nil {
		return nil, err
	}
	if orgID == "" {
		return service.ListOrganizations()
	}
	org, err := service.GetOrganization(orgID)
	if err != nil {
		return nil, err
	}
	return []ontology.Organization{*org}, nil
}

func writeResourceNotFound(w http.ResponseWriter) {
	http.Error(w, "Not Found", http.StatusNotFound)
}
