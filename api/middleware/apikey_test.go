package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireScopeIfAPIKeyAllowsBrowserSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/agent-ops/summary", nil)
	rr := httptest.NewRecorder()

	handler := RequireScopeIfAPIKey("agentops:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestRequireScopeIfAPIKeyRejectsMissingScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/agent-ops/summary", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, "key-1")
	ctx = context.WithValue(ctx, ContextKeyScopes, []string{"entities:read"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler := RequireScopeIfAPIKey("agentops:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireScopeIfAPIKeyAllowsAdminScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/agent-ops/summary", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKey, "key-1")
	ctx = context.WithValue(ctx, ContextKeyScopes, []string{"admin"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler := RequireScopeIfAPIKey("agentops:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}
