package handlers

import (
	"context"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/authz"

	"github.com/danielgtaylor/huma/v2"
)

func hasAllOrgAccess(ctx context.Context) bool {
	return middleware.HasScope(middleware.ScopesFromContext(ctx), authz.ScopeAdmin)
}

func scopedOrgID(ctx context.Context) string {
	return middleware.OrgIDFromContext(ctx)
}

func requireAdminAccess(ctx context.Context) error {
	if hasAllOrgAccess(ctx) {
		return nil
	}
	return huma.Error403Forbidden("API key is not authorized for all organizations")
}

func requireOrgAccess(ctx context.Context, orgID string) error {
	if hasAllOrgAccess(ctx) {
		return nil
	}

	contextOrgID := middleware.OrgIDFromContext(ctx)
	if contextOrgID == "" || orgID == "" || contextOrgID != orgID {
		return huma.Error403Forbidden("API key is not authorized for this organization")
	}
	return nil
}
