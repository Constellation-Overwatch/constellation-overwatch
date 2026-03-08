package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
)

// APIKeyMiddleware handles API key authentication and scope enforcement.
type APIKeyMiddleware struct {
	db *sql.DB
}

// NewAPIKeyMiddleware creates a new APIKeyMiddleware with the given database connection.
func NewAPIKeyMiddleware(db *sql.DB) *APIKeyMiddleware {
	return &APIKeyMiddleware{db: db}
}

// Authenticate is HTTP middleware that validates API keys from the X-API-Key header
// or from Bearer tokens. Keys must carry the c4_live_ or c4_test_ prefix.
func (m *APIKeyMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := extractAPIKey(r)
		if raw == "" {
			writeJSONError(w, http.StatusUnauthorized, "Missing API key or Authorization header")
			return
		}

		// If the key carries the c4_ prefix, validate against the database.
		if strings.HasPrefix(raw, "c4_live_") || strings.HasPrefix(raw, "c4_test_") {
			m.authenticateDBKey(w, r, next, raw)
			return
		}

		// No recognized prefix — reject.
		writeJSONError(w, http.StatusUnauthorized, "Invalid API key")
	})
}

// authenticateDBKey hashes the raw key, looks it up in the api_keys table,
// and injects identity claims into the request context.
func (m *APIKeyMiddleware) authenticateDBKey(w http.ResponseWriter, r *http.Request, next http.Handler, raw string) {
	hash := hashKey(raw)

	var keyID, userID, orgID, role, scopesJSON string
	var revoked int
	var expiresAt sql.NullString

	err := m.db.QueryRow(
		`SELECT key_id, user_id, org_id, role, scopes, revoked, expires_at
		 FROM api_keys WHERE key_hash = ?`, hash,
	).Scan(&keyID, &userID, &orgID, &role, &scopesJSON, &revoked, &expiresAt)

	// If HMAC lookup failed and HMAC is enabled, try legacy SHA-256 fallback.
	if errors.Is(err, sql.ErrNoRows) && os.Getenv("OVERWATCH_KEY_HASH_SECRET") != "" {
		legacyHash := sha256Hex(raw)
		err = m.db.QueryRow(
			`SELECT key_id, user_id, org_id, role, scopes, revoked, expires_at
			 FROM api_keys WHERE key_hash = ?`, legacyHash,
		).Scan(&keyID, &userID, &orgID, &role, &scopesJSON, &revoked, &expiresAt)
		if err == nil {
			// Transparently upgrade to HMAC hash.
			go func() {
				if _, dbErr := m.db.Exec(`UPDATE api_keys SET key_hash = ? WHERE key_hash = ?`, hash, legacyHash); dbErr != nil {
					logger.Warnw("Failed to upgrade API key hash", "error", dbErr, "key_id", keyID)
				}
			}()
		}
	}

	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}
	if err != nil {
		logger.Errorw("Failed to query API key", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Authentication failed")
		return
	}

	if revoked == 1 {
		writeJSONError(w, http.StatusUnauthorized, "API key has been revoked")
		return
	}

	if expiresAt.Valid {
		exp, parseErr := time.Parse(time.RFC3339, expiresAt.String)
		if parseErr == nil && time.Now().After(exp) {
			writeJSONError(w, http.StatusUnauthorized, "API key has expired")
			return
		}
	}

	// Parse scopes from comma-separated string.
	scopes := parseScopes(scopesJSON)

	// Update last_used inline with a short timeout so it doesn't block the
	// request for long, but avoids unbounded goroutine-per-request growth.
	updateCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := m.db.ExecContext(updateCtx,
		`UPDATE api_keys SET last_used_at = ? WHERE key_id = ?`,
		time.Now().Format(time.RFC3339), keyID,
	); err != nil {
		logger.Warnw("Failed to update API key last_used_at", "error", err, "key_id", keyID)
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, ContextKeyUserID, userID)
	ctx = context.WithValue(ctx, ContextKeyUserRole, role)
	ctx = context.WithValue(ctx, ContextKeyOrgID, orgID)
	ctx = context.WithValue(ctx, ContextKeyAPIKey, keyID)
	ctx = context.WithValue(ctx, ContextKeyScopes, scopes)

	next.ServeHTTP(w, r.WithContext(ctx))
}

// RequireScope returns middleware that ensures the authenticated API key
// possesses the given scope (or the "admin" scope, which implies all scopes).
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes := ScopesFromContext(r.Context())
			if !HasScope(scopes, scope) {
				writeJSONError(w, http.StatusForbidden, "Insufficient scope: "+scope)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractAPIKey retrieves the API key from the X-API-Key header first,
// then falls back to the Authorization: Bearer header.
func extractAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	return strings.TrimSpace(parts[1])
}

var hmacWarnOnce sync.Once

// hashKey computes the HMAC-SHA256 hex digest of a raw API key when
// OVERWATCH_KEY_HASH_SECRET is set, falling back to plain SHA-256 for
// development.
func hashKey(raw string) string {
	secret := os.Getenv("OVERWATCH_KEY_HASH_SECRET")
	if secret == "" {
		hmacWarnOnce.Do(func() {
			logger.Warn("OVERWATCH_KEY_HASH_SECRET not set, using insecure SHA-256 hash")
		})
		return sha256Hex(raw)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

// sha256Hex computes a plain SHA-256 hex digest (legacy, for migration).
func sha256Hex(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// parseScopes splits a comma-separated scope string into a slice.
func parseScopes(s string) []string {
	if s == "" {
		return nil
	}
	var scopes []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			scopes = append(scopes, trimmed)
		}
	}
	return scopes
}

// HasScope checks whether the given scope (or "admin") is present in the list.
func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required || s == "admin" {
			return true
		}
	}
	return false
}

// writeJSONError writes an RFC 9457 Problem Details error response.
func writeJSONError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": status,
		"title":  http.StatusText(status),
		"detail": detail,
	})
}
