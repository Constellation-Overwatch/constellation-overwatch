package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
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

func TestSecurityHeadersWithProductionConfig(t *testing.T) {
	cfg := &runtimeconfig.Runtime{
		Environment:     runtimeconfig.EnvironmentProduction,
		ContentSecurity: "default-src 'self'; object-src 'none'",
		StrictTransport: "max-age=31536000",
	}
	handler := SecurityHeadersWithConfig(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != cfg.ContentSecurity {
		t.Fatalf("CSP = %q", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != cfg.StrictTransport {
		t.Fatalf("HSTS = %q", got)
	}
	if got := rec.Header().Get("X-Overwatch-Environment"); got != runtimeconfig.EnvironmentProduction {
		t.Fatalf("environment header = %q", got)
	}
}

func TestClientIPTrustsHeadersOnlyFromConfiguredProxy(t *testing.T) {
	cfg := &runtimeconfig.Runtime{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}

	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "192.0.2.10:1234"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := clientIP(untrusted, cfg); got != "192.0.2.10" {
		t.Fatalf("untrusted peer spoofed client IP: %q", got)
	}

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.0.0.5:1234"
	trusted.Header.Set("X-Forwarded-For", "198.51.100.99, 203.0.113.7")
	if got := clientIP(trusted, cfg); got != "203.0.113.7" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}

	trustedChain := httptest.NewRequest(http.MethodGet, "/", nil)
	trustedChain.RemoteAddr = "10.0.0.5:1234"
	trustedChain.Header.Set("X-Forwarded-For", "203.0.113.7, 10.1.2.3")
	if got := clientIP(trustedChain, cfg); got != "203.0.113.7" {
		t.Fatalf("trusted proxy chain client IP = %q", got)
	}

	malformed := httptest.NewRequest(http.MethodGet, "/", nil)
	malformed.RemoteAddr = "10.0.0.5:1234"
	malformed.Header.Set("X-Forwarded-For", "203.0.113.7, not-an-ip")
	if got := clientIP(malformed, cfg); got != "10.0.0.5" {
		t.Fatalf("malformed forwarding chain trusted: %q", got)
	}
}
