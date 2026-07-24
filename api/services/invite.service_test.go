package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func newInviteServiceTestDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "invite.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	// Production uses one SQLite connection so writes serialize without
	// surfacing SQLITE_BUSY to concurrent HTTP requests.
	conn.SetMaxOpenConns(1)

	for _, statement := range []string{
		`CREATE TABLE users (
			user_id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL
		)`,
		`CREATE TABLE invites (
			invite_id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL,
			email TEXT NOT NULL,
			role TEXT NOT NULL,
			invited_by_user_id TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("initialize test database: %v", err)
		}
	}

	return conn
}

func TestCreateInviteForExistingUserIsShortLivedAndPreservesRole(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-1", "org-1", "user@example.com", "admin",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	invite, _, err := NewInviteService(conn).CreateInvite(
		"org-1",
		"user@example.com",
		"viewer",
		"admin-1",
	)
	if err != nil {
		t.Fatalf("create recovery invite: %v", err)
	}
	if invite.Role != "admin" {
		t.Fatalf("invite role = %q, want existing role admin", invite.Role)
	}

	expiresAt, err := time.Parse(time.RFC3339, invite.ExpiresAt)
	if err != nil {
		t.Fatalf("parse invite expiry: %v", err)
	}
	remaining := time.Until(expiresAt)
	if remaining < 14*time.Minute || remaining > 16*time.Minute {
		t.Fatalf("recovery invite lifetime = %v, want about 15 minutes", remaining)
	}
}

func TestAcceptInviteIsAtomicAndSingleUse(t *testing.T) {
	svc := NewInviteService(newInviteServiceTestDB(t))
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			errs <- svc.AcceptInvite(invite.InviteID)
		}()
	}
	wg.Wait()
	close(errs)

	var successes, rejected int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, shared.ErrNotFound):
			rejected++
		default:
			t.Errorf("unexpected accept error: %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("accept outcomes: successes=%d rejected=%d, want 1 and 1", successes, rejected)
	}
}
