package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestRequireRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       string
		allowed    []string
		wantStatus int
		wantCalled bool
	}{
		{name: "anonymous denied", allowed: []string{shared.RoleOperator, shared.RoleAdmin}, wantStatus: http.StatusForbidden},
		{name: "viewer denied mutation", role: shared.RoleViewer, allowed: []string{shared.RoleOperator, shared.RoleAdmin}, wantStatus: http.StatusForbidden},
		{name: "operator allowed", role: shared.RoleOperator, allowed: []string{shared.RoleOperator, shared.RoleAdmin}, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "admin allowed", role: shared.RoleAdmin, allowed: []string{shared.RoleOperator, shared.RoleAdmin}, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "unknown denied", role: "owner", allowed: []string{shared.RoleAdmin}, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireRole(tt.allowed...)(next)

			req := httptest.NewRequest(http.MethodPost, "/mutation", nil)
			if tt.role != "" {
				ctx := context.WithValue(req.Context(), middleware.ContextKeyUserRole, tt.role)
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if called != tt.wantCalled {
				t.Fatalf("called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}
