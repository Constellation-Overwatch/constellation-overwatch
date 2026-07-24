package middleware

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestPasskeySetupSessionIsScopedAndShortLived(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	if _, err := conn.Exec(`CREATE TABLE sessions (
		session_token TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		role TEXT NOT NULL,
		org_id TEXT NOT NULL,
		needs_passkey_setup INTEGER NOT NULL,
		expires_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create sessions table: %v", err)
	}

	auth := &SessionAuth{db: conn}
	token, err := auth.CreateSessionForUser("user-1", "admin", true, "org-1")
	if err != nil {
		t.Fatalf("create passkey setup session: %v", err)
	}

	required, err := auth.PasskeySetupRequired(token)
	if err != nil {
		t.Fatalf("inspect setup session: %v", err)
	}
	if !required {
		t.Fatal("setup session was not marked as passkey-only")
	}

	var expiresAtText string
	if err := conn.QueryRow(
		`SELECT expires_at FROM sessions WHERE session_token = ?`,
		token,
	).Scan(&expiresAtText); err != nil {
		t.Fatalf("read setup session expiry: %v", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtText)
	if err != nil {
		t.Fatalf("parse setup session expiry: %v", err)
	}
	remaining := time.Until(expiresAt)
	if remaining < 9*time.Minute || remaining > 11*time.Minute {
		t.Fatalf("setup session lifetime = %v, want about 10 minutes", remaining)
	}
}
