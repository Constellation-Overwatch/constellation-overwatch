package middleware

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
)

const (
	SessionCookieName           = "overwatch_session"
	sessionDuration             = 24 * time.Hour
	passkeySetupSessionDuration = 10 * time.Minute
)

// session is the in-process representation of a session row.
type session struct {
	token             string
	userID            string
	role              string
	orgID             string
	needsPasskeySetup bool
	expiresAt         time.Time
}

// SessionAuth handles session-based authentication for the web UI.
// Sessions are persisted in SQLite so they survive server restarts.
type SessionAuth struct {
	db *sql.DB
}

// NewSessionAuth creates a new session auth handler backed by the given database.
func NewSessionAuth(db *sql.DB) *SessionAuth {
	sa := &SessionAuth{db: db}
	// Start periodic cleanup of expired sessions.
	go sa.cleanupLoop()
	return sa
}

// CreateSessionForUser creates a new session with user identity and returns the session token.
// Set needsPasskey to true for users that still need to register a passkey (bootstrap admin, invite flow).
// orgID is stored in the session for use by admin handlers to scope operations.
func (s *SessionAuth) CreateSessionForUser(userID, role string, needsPasskey bool, orgID string) (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	tokenStr := hex.EncodeToString(token)

	duration := sessionDuration
	needsSetup := 0
	if needsPasskey {
		needsSetup = 1
		duration = passkeySetupSessionDuration
	}
	expiresAt := time.Now().Add(duration).Format(time.RFC3339)

	_, err := s.db.Exec(
		`INSERT INTO sessions (session_token, user_id, role, org_id, needs_passkey_setup, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		tokenStr, userID, role, orgID, needsSetup, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return tokenStr, nil
}

// ClearPasskeySetup removes the needsPasskeySetup flag from an existing session.
func (s *SessionAuth) ClearPasskeySetup(token string) {
	_, err := s.db.Exec(
		`UPDATE sessions SET needs_passkey_setup = 0 WHERE session_token = ?`, token,
	)
	if err != nil {
		logger.Warnw("Failed to clear passkey setup flag", "error", err)
	}
}

// IsPasskeySetup returns true if the session has the needsPasskeySetup flag set.
func (s *SessionAuth) IsPasskeySetup(token string) bool {
	required, err := s.PasskeySetupRequired(token)
	return err == nil && required
}

// PasskeySetupRequired returns the setup scope for a live session. Callers
// making authorization decisions use the error-returning form so a database
// failure cannot silently widen an enrollment-only session.
func (s *SessionAuth) PasskeySetupRequired(token string) (bool, error) {
	var flag int
	err := s.db.QueryRow(
		`SELECT needs_passkey_setup FROM sessions WHERE session_token = ? AND expires_at > ?`,
		token, time.Now().Format(time.RFC3339),
	).Scan(&flag)
	if err != nil {
		return false, fmt.Errorf("failed to read passkey setup session: %w", err)
	}
	return flag == 1, nil
}

// ValidateSession checks if the session token is valid and not expired.
func (s *SessionAuth) ValidateSession(token string) bool {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE session_token = ? AND expires_at > ?`,
		token, time.Now().Format(time.RFC3339),
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// DestroySession removes a session.
func (s *SessionAuth) DestroySession(token string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE session_token = ?`, token); err != nil {
		return fmt.Errorf("failed to destroy session: %w", err)
	}
	return nil
}

// cleanupLoop periodically removes expired sessions.
func (s *SessionAuth) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.cleanupExpiredSessions()
	}
}

// cleanupExpiredSessions removes expired sessions from the database.
func (s *SessionAuth) cleanupExpiredSessions() {
	result, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Format(time.RFC3339))
	if err != nil {
		logger.Warnw("Failed to cleanup expired sessions", "error", err)
		return
	}
	if count, _ := result.RowsAffected(); count > 0 {
		logger.Debugw("Cleaned up expired sessions", "count", count)
	}
}

// getSession returns the session for a token, or nil if invalid/expired.
func (s *SessionAuth) getSession(token string) *session {
	var userID, role, orgID, expiresAt string
	var needsSetup int

	err := s.db.QueryRow(
		`SELECT user_id, role, org_id, needs_passkey_setup, expires_at
		 FROM sessions WHERE session_token = ?`, token,
	).Scan(&userID, &role, &orgID, &needsSetup, &expiresAt)

	if err != nil {
		return nil
	}

	exp, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil || time.Now().After(exp) {
		return nil
	}

	return &session{
		token:             token,
		userID:            userID,
		role:              role,
		orgID:             orgID,
		needsPasskeySetup: needsSetup == 1,
		expiresAt:         exp,
	}
}

// RequireSession is middleware that checks for a valid session cookie
// and injects user identity into the request context.
// If the session has needsPasskeySetup=true, non-passkey paths redirect to /setup-passkey.
func (s *SessionAuth) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		sess := s.getSession(cookie.Value)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// Enforce passkey setup redirect for users who haven't registered a passkey yet
		if sess.needsPasskeySetup {
			path := r.URL.Path
			allowed := path == "/setup-passkey" ||
				path == "/logout" ||
				strings.HasPrefix(path, "/auth/passkey/") ||
				strings.HasPrefix(path, "/static/")
			if !allowed {
				http.Redirect(w, r, "/setup-passkey", http.StatusFound)
				return
			}
		}

		// Inject user identity into request context
		ctx := r.Context()
		if sess.userID != "" {
			ctx = context.WithValue(ctx, ContextKeyUserID, sess.userID)
			ctx = context.WithValue(ctx, ContextKeyUserRole, sess.role)
		}
		if sess.orgID != "" {
			ctx = context.WithValue(ctx, ContextKeyOrgID, sess.orgID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// secureCookies returns true when cookies should be marked Secure. This is
// determined by checking OVERWATCH_INSECURE (explicit override) and falling
// back to whether the configured base URL starts with "https://".
func secureCookies() bool {
	if v := os.Getenv("OVERWATCH_INSECURE"); v == "true" {
		return false
	}
	baseURL := os.Getenv("OVERWATCH_BASE_URL")
	if strings.HasPrefix(baseURL, "https://") {
		return true
	}
	return false
}

// SetSessionCookie sets the session cookie on the response
func SetSessionCookie(w http.ResponseWriter, token string) {
	SetSessionCookieWithSecure(w, token, secureCookies())
}

// SetSessionCookieWithSecure writes the session cookie using the validated
// startup policy supplied by the caller.
func SetSessionCookieWithSecure(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie clears the session cookie
func ClearSessionCookie(w http.ResponseWriter) {
	ClearSessionCookieWithSecure(w, secureCookies())
}

// ClearSessionCookieWithSecure clears the session cookie with attributes that
// match the validated startup policy.
func ClearSessionCookieWithSecure(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// GetAllowedOrigins returns the list of allowed origins from environment
func GetAllowedOrigins() []string {
	origins := os.Getenv("ALLOWED_ORIGINS")
	if origins == "" || origins == "*" {
		return []string{}
	}
	var result []string
	for _, o := range strings.Split(origins, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// IsOriginAllowed checks if an origin is in the allowed list.
// Returns false when ALLOWED_ORIGINS is not configured (deny by default).
func IsOriginAllowed(origin string) bool {
	origins := os.Getenv("ALLOWED_ORIGINS")
	if origins == "" {
		return false // Deny when not configured
	}
	if origins == "*" {
		return true // Explicit wildcard for dev
	}
	for _, allowed := range strings.Split(origins, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}
