package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/authz"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/danielgtaylor/huma/v2"
)

type OrganizationHandler struct {
	service *services.OrganizationService
}

func NewOrganizationHandler(service *services.OrganizationService) *OrganizationHandler {
	return &OrganizationHandler{service: service}
}

// ── Input / Output types ────────────────────────────────────────────

type CreateOrgInput struct {
	Body ontology.CreateOrganizationRequest
}

type OrgOutput struct {
	Body ontology.Organization
}

type OrgListOutput struct {
	Body []ontology.Organization
}

// ── Registration ────────────────────────────────────────────────────

func (h *OrganizationHandler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-organization",
		Method:        http.MethodPost,
		Path:          "/v1/organizations",
		Summary:       "Create organization",
		Description:   "Create a new organization",
		Tags:          []string{"Organizations"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"APIKeyAuth": {authz.ScopeOrganizationsWrite}}},
	}, func(ctx context.Context, input *CreateOrgInput) (*OrgOutput, error) {
		if err := requireAdminAccess(ctx); err != nil {
			return nil, err
		}

		org, err := h.service.CreateOrganization(&input.Body)
		if err != nil {
			logger.Errorw("Failed to create organization", "error", err)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		return &OrgOutput{Body: *org}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-organizations",
		Method:      http.MethodGet,
		Path:        "/v1/organizations",
		Summary:     "List organizations",
		Description: "Get all organizations",
		Tags:        []string{"Organizations"},
		Security:    []map[string][]string{{"APIKeyAuth": {authz.ScopeOrganizationsRead}}},
	}, func(ctx context.Context, input *struct{}) (*OrgListOutput, error) {
		if !hasAllOrgAccess(ctx) {
			orgID := scopedOrgID(ctx)
			if orgID == "" {
				return nil, huma.Error403Forbidden("API key is not authorized for any organization")
			}
			org, err := h.service.GetOrganization(orgID)
			if err != nil {
				if errors.Is(err, shared.ErrNotFound) {
					return nil, huma.Error404NotFound("Organization not found")
				}
				logger.Errorw("Failed to get scoped organization", "error", err, "org_id", orgID)
				return nil, huma.Error500InternalServerError("An internal error occurred")
			}
			return &OrgListOutput{Body: []ontology.Organization{*org}}, nil
		}

		orgs, err := h.service.ListOrganizations()
		if err != nil {
			logger.Errorw("Failed to list organizations", "error", err)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		if orgs == nil {
			orgs = []ontology.Organization{}
		}
		return &OrgListOutput{Body: orgs}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-organization",
		Method:      http.MethodGet,
		Path:        "/v1/organizations/{org_id}",
		Summary:     "Get organization",
		Description: "Get a single organization by ID",
		Tags:        []string{"Organizations"},
		Security:    []map[string][]string{{"APIKeyAuth": {authz.ScopeOrganizationsRead}}},
	}, func(ctx context.Context, input *struct{ OrgPathParam }) (*OrgOutput, error) {
		if err := requireOrgAccess(ctx, input.OrgID); err != nil {
			return nil, err
		}

		org, err := h.service.GetOrganization(input.OrgID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, huma.Error404NotFound("Organization not found")
			}
			logger.Errorw("Failed to get organization", "error", err, "org_id", input.OrgID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		return &OrgOutput{Body: *org}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-organization",
		Method:      http.MethodDelete,
		Path:        "/v1/organizations/{org_id}",
		Summary:     "Delete organization",
		Description: "Delete an organization by ID",
		Tags:        []string{"Organizations"},
		Security:    []map[string][]string{{"APIKeyAuth": {authz.ScopeOrganizationsWrite}}},
	}, func(ctx context.Context, input *struct{ OrgPathParam }) (*DeletedOutput, error) {
		if err := requireOrgAccess(ctx, input.OrgID); err != nil {
			return nil, err
		}

		err := h.service.DeleteOrganization(input.OrgID)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, huma.Error404NotFound("Organization not found")
			}
			logger.Errorw("Failed to delete organization", "error", err, "org_id", input.OrgID)
			return nil, huma.Error500InternalServerError("An internal error occurred")
		}
		out := &DeletedOutput{}
		out.Body.Message = "Organization deleted successfully"
		return out, nil
	})
}
