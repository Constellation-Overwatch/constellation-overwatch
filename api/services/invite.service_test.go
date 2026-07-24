package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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
			purpose TEXT NOT NULL,
			status TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE audit_log (
			audit_id TEXT PRIMARY KEY,
			org_id TEXT,
			user_id TEXT,
			action TEXT NOT NULL,
			resource_type TEXT,
			resource_id TEXT,
			changes TEXT,
			timestamp TEXT NOT NULL
		)`,
		`INSERT INTO users (user_id, org_id, email, role)
		 VALUES ('admin-1', 'org-1', 'admin@example.com', 'admin')`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("initialize test database: %v", err)
		}
	}

	return conn
}

func TestCreateInviteForExistingUserIsAuditedRecoveryAndPreservesRole(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-1", "org-1", "user@example.com", "admin",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	invite, plainToken, err := NewInviteService(conn).CreateInvite(
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
	if invite.Purpose != InvitePurposeAdminRecovery {
		t.Fatalf("invite purpose = %q, want %q", invite.Purpose, InvitePurposeAdminRecovery)
	}

	expiresAt, err := time.Parse(time.RFC3339, invite.ExpiresAt)
	if err != nil {
		t.Fatalf("parse invite expiry: %v", err)
	}
	remaining := time.Until(expiresAt)
	if remaining < 14*time.Minute || remaining > 16*time.Minute {
		t.Fatalf("recovery invite lifetime = %v, want about 15 minutes", remaining)
	}

	var action, resourceType, resourceID, changes string
	if err := conn.QueryRow(
		`SELECT action, resource_type, resource_id, changes
		 FROM audit_log WHERE resource_id = ?`,
		invite.InviteID,
	).Scan(&action, &resourceType, &resourceID, &changes); err != nil {
		t.Fatalf("read issuance audit: %v", err)
	}
	if action != "passkey.admin_recovery.issued" {
		t.Fatalf("audit action = %q", action)
	}
	if resourceType != inviteAuditResourceType || resourceID != invite.InviteID {
		t.Fatalf("audit resource = %q/%q", resourceType, resourceID)
	}
	for _, secret := range []string{"user@example.com", plainToken} {
		if strings.Contains(changes, secret) {
			t.Fatalf("audit changes leaked %q: %s", secret, changes)
		}
	}
	if !strings.Contains(changes, `"target_user_id":"user-1"`) {
		t.Fatalf("audit changes missing target user: %s", changes)
	}
	if !strings.Contains(changes, `"issued_by_user_id":"admin-1"`) {
		t.Fatalf("audit changes missing issuer: %s", changes)
	}
}

func TestCreateInitialSetupInviteKeepsExplicitPurposeForExistingBootstrapUser(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	invite, _, err := NewInviteService(conn).CreateInitialSetupInvite(
		"org-1",
		"admin@example.com",
		"admin",
		"admin-1",
	)
	if err != nil {
		t.Fatalf("create bootstrap invite: %v", err)
	}
	if invite.Purpose != InvitePurposeInitialSetup {
		t.Fatalf("purpose = %q, want %q", invite.Purpose, InvitePurposeInitialSetup)
	}

	var action string
	if err := conn.QueryRow(
		`SELECT action FROM audit_log WHERE resource_id = ?`,
		invite.InviteID,
	).Scan(&action); err != nil {
		t.Fatalf("read bootstrap audit: %v", err)
	}
	if action != "passkey.initial_setup.issued" {
		t.Fatalf("audit action = %q", action)
	}
}

func TestAcceptInviteIsAtomicSingleUseAndAuditedOnce(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-new", "org-1", "new@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert redeemed user: %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			errs <- svc.AcceptInvite(invite.InviteID, "user-new")
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

	var redeemedAudits int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM audit_log
		 WHERE resource_id = ? AND action = 'passkey.initial_setup.redeemed'`,
		invite.InviteID,
	).Scan(&redeemedAudits); err != nil {
		t.Fatalf("count redemption audits: %v", err)
	}
	if redeemedAudits != 1 {
		t.Fatalf("redemption audit count = %d, want 1", redeemedAudits)
	}
}

func TestRevokeInviteOnlyTransitionsPendingAndAuditsActor(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-1", "org-1", "user@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert recovery user: %v", err)
	}
	invite, _, err := svc.CreateInvite("org-1", "user@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create recovery invite: %v", err)
	}

	if err := svc.RevokeInvite(invite.InviteID, "admin-1"); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}
	if err := svc.RevokeInvite(invite.InviteID, "admin-1"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("replay revoke error = %v, want not found", err)
	}

	var status, actor string
	if err := conn.QueryRow(
		`SELECT i.status, a.user_id
		 FROM invites i JOIN audit_log a ON a.resource_id = i.invite_id
		 WHERE i.invite_id = ? AND a.action = 'passkey.admin_recovery.revoked'`,
		invite.InviteID,
	).Scan(&status, &actor); err != nil {
		t.Fatalf("read revocation audit: %v", err)
	}
	if status != "revoked" || actor != "admin-1" {
		t.Fatalf("revocation state/auditor = %q/%q", status, actor)
	}
}

func TestAcceptRecoveryInviteAuditsRecoveryPurpose(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-1", "org-1", "user@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert recovery user: %v", err)
	}
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "user@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create recovery invite: %v", err)
	}
	if err := svc.AcceptInvite(invite.InviteID, "user-1"); err != nil {
		t.Fatalf("accept recovery invite: %v", err)
	}

	var actor, changes string
	if err := conn.QueryRow(
		`SELECT user_id, changes FROM audit_log
		 WHERE resource_id = ? AND action = 'passkey.admin_recovery.redeemed'`,
		invite.InviteID,
	).Scan(&actor, &changes); err != nil {
		t.Fatalf("read recovery redemption audit: %v", err)
	}
	if actor != "user-1" {
		t.Fatalf("redemption actor = %q, want user-1", actor)
	}
	if !strings.Contains(changes, `"issued_by_user_id":"admin-1"`) {
		t.Fatalf("redemption audit missing issuer: %s", changes)
	}
}

func TestAcceptInviteRejectsMismatchedUserWithoutChangingState(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-new", "org-1", "new@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert target user: %v", err)
	}

	if err := svc.AcceptInvite(invite.InviteID, "admin-1"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("mismatched redemption error = %v, want not found", err)
	}

	var status string
	var redeemedAudits int
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM audit_log
		 WHERE resource_id = ? AND action LIKE '%.redeemed'`,
		invite.InviteID,
	).Scan(&redeemedAudits); err != nil {
		t.Fatalf("count redemption audits: %v", err)
	}
	if status != "pending" || redeemedAudits != 0 {
		t.Fatalf("mismatched redemption state/audits = %q/%d", status, redeemedAudits)
	}
}

func TestRevokeInviteRejectsCrossOrganizationActor(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"admin-2", "org-2", "admin2@example.com", "admin",
	); err != nil {
		t.Fatalf("insert cross-org admin: %v", err)
	}
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if err := svc.RevokeInvite(invite.InviteID, "admin-2"); err == nil {
		t.Fatal("cross-org revocation succeeded")
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after cross-org revoke, want pending", status)
	}
}

func TestInviteLifecycleRequiresAdministratorIssuerAndRevoker(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"viewer-1", "org-1", "viewer@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	svc := NewInviteService(conn)
	if _, _, err := svc.CreateInvite(
		"org-1", "new@example.com", "viewer", "viewer-1",
	); err == nil {
		t.Fatal("non-admin created invite")
	}

	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("admin create invite: %v", err)
	}
	if err := svc.RevokeInvite(invite.InviteID, "viewer-1"); err == nil {
		t.Fatal("non-admin revoked invite")
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after non-admin revoke, want pending", status)
	}
}

func TestInviteLifecycleTreatsMissingActorsAsForbidden(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)

	if _, _, err := svc.CreateInvite(
		"org-1", "new@example.com", "viewer", "",
	); !errors.Is(err, ErrInviteForbidden) {
		t.Fatalf("CreateInvite() error = %v, want ErrInviteForbidden", err)
	}

	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("admin create invite: %v", err)
	}
	if err := svc.RevokeInvite(invite.InviteID, "missing-admin"); !errors.Is(err, ErrInviteForbidden) {
		t.Fatalf("RevokeInvite() error = %v, want ErrInviteForbidden", err)
	}
	if _, err := svc.RevokePendingInvitesByIssuer("missing-admin"); !errors.Is(err, ErrInviteForbidden) {
		t.Fatalf("RevokePendingInvitesByIssuer() error = %v, want ErrInviteForbidden", err)
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after missing-actor operations, want pending", status)
	}
}

func TestRevokePendingInvitesByIssuerAuditsEveryLifecycleChange(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-1", "org-1", "user@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert recovery user: %v", err)
	}
	svc := NewInviteService(conn)
	initial, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create initial invite: %v", err)
	}
	recovery, _, err := svc.CreateInvite("org-1", "user@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create recovery invite: %v", err)
	}
	legacyCrossOrgID := "legacy-cross-org"
	now := time.Now()
	if _, err := conn.Exec(
		`INSERT INTO invites
		 (invite_id, org_id, email, role, invited_by_user_id, token_hash, purpose,
		  status, expires_at, created_at, updated_at)
		 VALUES (?, 'org-2', 'legacy@example.com', 'viewer', 'admin-1', ?,
		         'admin_recovery', 'pending', ?, ?, ?)`,
		legacyCrossOrgID,
		"legacy-cross-org-token-hash",
		now.Add(time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert legacy cross-org invite: %v", err)
	}

	revoked, err := svc.RevokePendingInvitesByIssuer("admin-1")
	if err != nil {
		t.Fatalf("revoke pending invites: %v", err)
	}
	if revoked != 3 {
		t.Fatalf("revoked = %d, want 3", revoked)
	}

	for _, want := range []struct {
		inviteID string
		action   string
	}{
		{initial.InviteID, "passkey.initial_setup.revoked"},
		{recovery.InviteID, "passkey.admin_recovery.revoked"},
		{legacyCrossOrgID, "passkey.admin_recovery.revoked"},
	} {
		var status string
		var actor, changes string
		var audits int
		if err := conn.QueryRow(
			`SELECT status FROM invites WHERE invite_id = ?`,
			want.inviteID,
		).Scan(&status); err != nil {
			t.Fatalf("read invite status: %v", err)
		}
		if err := conn.QueryRow(
			`SELECT COUNT(*), COALESCE(MAX(user_id), ''), MAX(changes)
			 FROM audit_log WHERE resource_id = ? AND action = ?`,
			want.inviteID,
			want.action,
		).Scan(&audits, &actor, &changes); err != nil {
			t.Fatalf("count revocation audits: %v", err)
		}
		if status != "revoked" || audits != 1 || actor != "" {
			t.Fatalf("invite %s status/audits/actor = %q/%d/%q", want.inviteID, status, audits, actor)
		}
		if !strings.Contains(changes, `"trigger":"bootstrap_hygiene"`) {
			t.Fatalf("bootstrap audit trigger missing: %s", changes)
		}
	}

	revoked, err = svc.RevokePendingInvitesByIssuer("admin-1")
	if err != nil {
		t.Fatalf("repeat pending revocation: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("repeat revoked = %d, want 0", revoked)
	}
}

func TestRevokePendingInvitesByIssuerRollsBackWithoutAuditStore(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := conn.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	if _, err := svc.RevokePendingInvitesByIssuer("admin-1"); err == nil {
		t.Fatal("pending revoke succeeded without audit table")
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after audit failure, want pending", status)
	}
}

func TestAcceptInviteRollsBackWhenAuditWriteFails(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO users (user_id, org_id, email, role) VALUES (?, ?, ?, ?)`,
		"user-new", "org-1", "new@example.com", "viewer",
	); err != nil {
		t.Fatalf("insert target user: %v", err)
	}
	if _, err := conn.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	if err := svc.AcceptInvite(invite.InviteID, "user-new"); err == nil {
		t.Fatal("accept succeeded without audit table")
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after audit failure, want pending", status)
	}
}

func TestRevokeInviteRollsBackWhenAuditWriteFails(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	svc := NewInviteService(conn)
	invite, _, err := svc.CreateInvite("org-1", "new@example.com", "viewer", "admin-1")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := conn.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	if err := svc.RevokeInvite(invite.InviteID, "admin-1"); err == nil {
		t.Fatal("revoke succeeded without audit table")
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM invites WHERE invite_id = ?`,
		invite.InviteID,
	).Scan(&status); err != nil {
		t.Fatalf("read invite status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q after audit failure, want pending", status)
	}
}

func TestCreateInviteRollsBackWhenAuditWriteFails(t *testing.T) {
	conn := newInviteServiceTestDB(t)
	if _, err := conn.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	if _, _, err := NewInviteService(conn).CreateInvite(
		"org-1", "new@example.com", "viewer", "admin-1",
	); err == nil {
		t.Fatal("create succeeded without audit table")
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM invites`).Scan(&count); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if count != 0 {
		t.Fatalf("invite count = %d after audit failure, want 0", count)
	}
}
