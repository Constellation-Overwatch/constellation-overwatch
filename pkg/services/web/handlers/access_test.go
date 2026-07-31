package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestAuthorizedOrganizationID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		role      string
		sessionID string
		requested string
		want      string
		wantErr   bool
	}{
		{name: "viewer defaults to session org", role: shared.RoleViewer, sessionID: "org-a", want: "org-a"},
		{name: "operator same org", role: shared.RoleOperator, sessionID: "org-a", requested: "org-a", want: "org-a"},
		{name: "viewer cross org denied", role: shared.RoleViewer, sessionID: "org-a", requested: "org-b", wantErr: true},
		{name: "operator cross org denied", role: shared.RoleOperator, sessionID: "org-a", requested: "org-b", wantErr: true},
		{name: "missing session org denied", role: shared.RoleViewer, wantErr: true},
		{name: "missing role denied", sessionID: "org-a", requested: "org-a", wantErr: true},
		{name: "unknown role denied", role: "owner", sessionID: "org-a", requested: "org-a", wantErr: true},
		{name: "admin explicit org", role: shared.RoleAdmin, sessionID: "org-a", requested: "org-b", want: "org-b"},
		{name: "admin global read", role: shared.RoleAdmin, sessionID: "org-a", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/", nil)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyUserRole, tt.role)
			ctx = context.WithValue(ctx, middleware.ContextKeyOrgID, tt.sessionID)
			req = req.WithContext(ctx)

			got, err := authorizedOrganizationID(req, tt.requested)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("org ID = %q, want %q", got, tt.want)
			}
		})
	}
}
