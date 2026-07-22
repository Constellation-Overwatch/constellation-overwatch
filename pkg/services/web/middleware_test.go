package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestSecurityHeadersForProduction(t *testing.T) {
	cfg := runtimeconfig.Development()
	cfg.Mode = runtimeconfig.ModeProduction
	cfg.HSTS = true
	handler := SecurityHeadersForConfig(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Fatal("production response missing HSTS")
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self' 'nonce-") || scriptDirectiveContains(got, "'unsafe-inline'") {
		t.Fatalf("production CSP does not enforce nonce-only scripts: %q", got)
	}
}

func scriptDirectiveContains(policy, value string) bool {
	for _, directive := range strings.Split(policy, ";") {
		if strings.HasPrefix(strings.TrimSpace(directive), "script-src ") {
			return strings.Contains(directive, value)
		}
	}
	return false
}

func TestSecurityHeadersDevelopmentOmitsHSTS(t *testing.T) {
	handler := SecurityHeadersForConfig(runtimeconfig.Development())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("development HSTS = %q, want empty", got)
	}
}

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

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeers(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-Ip", "198.51.100.21")
	if got := clientIP(req); got != "203.0.113.10" {
		t.Fatalf("clientIP() = %q, want direct peer", got)
	}
}

func TestClientIPHonorsForwardingHeadersFromConfiguredProxy(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 127.0.0.1")
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	if got := clientIPFromTrustedProxies(req, trusted); got != "198.51.100.20" {
		t.Fatalf("clientIPFromTrustedProxies() = %q, want forwarded client", got)
	}
}

func TestClientIPRejectsInvalidForwardingHeader(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "spoofed")
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	if got := clientIPFromTrustedProxies(req, trusted); got != "127.0.0.1" {
		t.Fatalf("clientIPFromTrustedProxies() = %q, want direct peer fallback", got)
	}
}
