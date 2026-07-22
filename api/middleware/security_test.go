package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSForOriginsUsesExactAllowlist(t *testing.T) {
	handler := CORSForOrigins([]string{"https://constellation.tailnet.example"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tt := range []struct {
		name       string
		origin     string
		wantOrigin string
	}{
		{name: "exact", origin: "https://constellation.tailnet.example", wantOrigin: "https://constellation.tailnet.example"},
		{name: "subdomain denied", origin: "https://evil.constellation.tailnet.example"},
		{name: "suffix attack denied", origin: "https://constellation.tailnet.example.evil.invalid"},
		{name: "wildcard entry ignored", origin: "https://arbitrary.invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			origins := []string{"https://constellation.tailnet.example"}
			if strings.Contains(tt.name, "wildcard") {
				origins = []string{"*"}
			}
			h := CORSForOrigins(origins)(handler)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Origin", tt.origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOrigin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantOrigin)
			}
		})
	}
}

func TestSessionCookiesUseFixedSecurePolicy(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "development", true: "production"}[secure], func(t *testing.T) {
			sessions := &SessionAuth{secureCookies: secure}
			rec := httptest.NewRecorder()
			sessions.SetSessionCookie(rec, "secret")
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Secure != secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie = %#v, want secure=%v HttpOnly SameSite=Lax", cookies, secure)
			}
		})
	}
}
