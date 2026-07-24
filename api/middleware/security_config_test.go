package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSWithOriginCheckUsesExactPolicy(t *testing.T) {
	handler := CORSWithOriginCheck(func(origin string) bool {
		return origin == "https://hub.galaxyuas.com"
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name    string
		origin  string
		allowed bool
	}{
		{name: "exact", origin: "https://hub.galaxyuas.com", allowed: true},
		{name: "lookalike", origin: "https://hub.galaxyuas.com.evil.test", allowed: false},
		{name: "wrong scheme", origin: "http://hub.galaxyuas.com", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Origin", tc.origin)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); (got != "") != tc.allowed {
				t.Fatalf("allow origin header = %q", got)
			}
		})
	}
}

func TestHashKeyWithSecretUsesSnapshotValue(t *testing.T) {
	t.Setenv("OVERWATCH_KEY_HASH_SECRET", "different-environment-secret")
	raw := "c4_test_example"
	secret := "startup-snapshot-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(raw))
	want := hex.EncodeToString(mac.Sum(nil))

	if got := hashKeyWithSecret(raw, secret); got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}
}

func TestSessionCookieSecurePolicyIsExplicit(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookieWithSecure(rec, "token", true)
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("secure session cookie = %q", cookie)
	}

	rec = httptest.NewRecorder()
	SetSessionCookieWithSecure(rec, "token", false)
	if cookie := rec.Header().Get("Set-Cookie"); strings.Contains(cookie, "Secure") {
		t.Fatalf("development session cookie unexpectedly Secure: %q", cookie)
	}
}
