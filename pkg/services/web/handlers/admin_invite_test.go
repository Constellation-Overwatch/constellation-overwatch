package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestHandleCreateInviteMapsMissingAdministratorIdentityToForbidden(t *testing.T) {
	dbSvc := newAPIKeyConformanceDB(t)
	handler := NewAdminHandler(
		nil,
		nil,
		services.NewInviteService(dbSvc.DB),
		nil,
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/invites",
		strings.NewReader(`{"email":"new-user@example.test","role":"viewer"}`),
	)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserRole, shared.RoleAdmin)
	ctx = context.WithValue(ctx, middleware.ContextKeyOrgID, "org-a")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.HandleCreateInvite(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("response does not identify forbidden actor: %s", rec.Body.String())
	}

	var count int
	if err := dbSvc.DB.QueryRow(`SELECT COUNT(*) FROM invites`).Scan(&count); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if count != 0 {
		t.Fatalf("missing administrator identity created %d invites", count)
	}
}
