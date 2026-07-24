package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
)

func TestBrowserRolePolicyMatrix(t *testing.T) {
	t.Parallel()

	policies := []struct {
		name    string
		handler func(http.Handler) http.Handler
		allowed map[string]bool
	}{
		{
			name:    "viewer reads",
			handler: RequireViewer,
			allowed: map[string]bool{"viewer": true, "operator": true, "admin": true},
		},
		{
			name:    "operator mutations",
			handler: RequireOperator,
			allowed: map[string]bool{"viewer": false, "operator": true, "admin": true},
		},
		{
			name:    "admin operations",
			handler: RequireAdmin,
			allowed: map[string]bool{"viewer": false, "operator": false, "admin": true},
		},
	}

	for _, policy := range policies {
		for _, role := range []string{"", "unknown", "viewer", "operator", "admin"} {
			t.Run(policy.name+"/"+role, func(t *testing.T) {
				called := false
				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				})
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req = req.WithContext(context.WithValue(
					req.Context(),
					middleware.ContextKeyUserRole,
					role,
				))
				rec := httptest.NewRecorder()

				policy.handler(next).ServeHTTP(rec, req)

				if policy.allowed[role] {
					if !called || rec.Code != http.StatusNoContent {
						t.Fatalf("role %q denied: called=%v status=%d", role, called, rec.Code)
					}
				} else if called || rec.Code != http.StatusForbidden {
					t.Fatalf("role %q allowed: called=%v status=%d", role, called, rec.Code)
				}
			})
		}
	}
}
