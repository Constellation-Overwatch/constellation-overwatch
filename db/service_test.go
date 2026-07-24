package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newLegacyCredentialDB(t *testing.T) *Service {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close legacy database: %v", err)
		}
	})
	conn.SetMaxOpenConns(1)

	for _, statement := range []string{
		`CREATE TABLE users (
			user_id TEXT PRIMARY KEY,
			email TEXT NOT NULL
		)`,
		`CREATE TABLE webauthn_sessions (
			ceremony TEXT NOT NULL,
			session_key TEXT NOT NULL,
			session_data TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			PRIMARY KEY (ceremony, session_key)
		)`,
		`CREATE TABLE webauthn_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			credential_data TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_webauthn_creds_cred_id ON webauthn_credentials(credential_id)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("initialize legacy database: %v", err)
		}
	}

	return &Service{DB: conn}
}

func TestMigrateSchemaAddsUniqueCredentialIndexIdempotently(t *testing.T) {
	svc := newLegacyCredentialDB(t)
	if _, err := svc.DB.Exec(
		`INSERT INTO webauthn_credentials
		 (user_id, credential_id, credential_data, created_at)
		 VALUES ('user-1', 'credential-1', '{}', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	if err := svc.MigrateSchema(); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := svc.MigrateSchema(); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}

	var unique int
	if err := svc.DB.QueryRow(
		`SELECT [unique] FROM pragma_index_list('webauthn_credentials')
		 WHERE name = 'idx_webauthn_creds_credential_id_unique'`,
	).Scan(&unique); err != nil {
		t.Fatalf("read unique index: %v", err)
	}
	if unique != 1 {
		t.Fatalf("credential index unique flag = %d, want 1", unique)
	}

	var legacyCount int
	if err := svc.DB.QueryRow(
		`SELECT COUNT(*) FROM pragma_index_list('webauthn_credentials')
		 WHERE name = 'idx_webauthn_creds_cred_id'`,
	).Scan(&legacyCount); err != nil {
		t.Fatalf("read legacy index: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy non-unique index count = %d, want 0", legacyCount)
	}
}

func TestMigrateSchemaFailsClosedWithoutDeletingDuplicateCredentials(t *testing.T) {
	svc := newLegacyCredentialDB(t)
	for _, data := range []string{`{"version":1}`, `{"version":2}`} {
		if _, err := svc.DB.Exec(
			`INSERT INTO webauthn_credentials
			 (user_id, credential_id, credential_data, created_at)
			 VALUES ('user-1', 'duplicate-credential', ?, '2026-01-01T00:00:00Z')`,
			data,
		); err != nil {
			t.Fatalf("insert duplicate credential: %v", err)
		}
	}

	err := svc.MigrateSchema()
	if err == nil || !strings.Contains(err.Error(), "operator reconciliation") {
		t.Fatalf("migration error = %v, want operator reconciliation error", err)
	}

	var rows int
	if err := svc.DB.QueryRow(
		`SELECT COUNT(*) FROM webauthn_credentials
		 WHERE credential_id = 'duplicate-credential'`,
	).Scan(&rows); err != nil {
		t.Fatalf("count duplicate rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("duplicate rows after failed migration = %d, want 2 preserved", rows)
	}
}

// TestMigrateSchemaExternalCopy validates the migration against an operator-
// supplied database copy. Release validation cross-compiles this test binary,
// runs it beside a temporary copy of the live SIM database, and never exposes
// credential rows in test output.
func TestMigrateSchemaExternalCopy(t *testing.T) {
	path := os.Getenv("OVERWATCH_MIGRATION_COPY_DB")
	if path == "" {
		t.Skip("OVERWATCH_MIGRATION_COPY_DB is not set")
	}

	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open external database copy: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	var before int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&before); err != nil {
		t.Fatalf("count credentials before migration: %v", err)
	}

	svc := &Service{DB: conn, DBPath: path}
	if err := svc.MigrateSchema(); err != nil {
		t.Fatalf("migrate external database copy: %v", err)
	}

	var after, unique int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&after); err != nil {
		t.Fatalf("count credentials after migration: %v", err)
	}
	if err := conn.QueryRow(
		`SELECT [unique] FROM pragma_index_list('webauthn_credentials')
		 WHERE name = 'idx_webauthn_creds_credential_id_unique'`,
	).Scan(&unique); err != nil {
		t.Fatalf("read migrated credential index: %v", err)
	}
	if before != after {
		t.Fatalf("credential row count changed: before=%d after=%d", before, after)
	}
	if unique != 1 {
		t.Fatalf("credential unique index flag = %d, want 1", unique)
	}
}

func TestMigrateAPIKeyScopeAliasesIsExplicitAndIdempotent(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "api-keys.db"))
	if err != nil {
		t.Fatalf("open API key database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close API key database: %v", err)
		}
	})
	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(`CREATE TABLE api_keys (
		key_id TEXT PRIMARY KEY,
		scopes TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create API key table: %v", err)
	}
	for _, row := range []struct {
		keyID  string
		scopes string
	}{
		{keyID: "legacy", scopes: "orgs:read,orgs:write,entities:read"},
		{keyID: "unknown", scopes: "organizations:read,nats:unknown"},
		{keyID: "empty", scopes: "[]"},
	} {
		if _, err := conn.Exec(
			`INSERT INTO api_keys (key_id, scopes) VALUES (?, ?)`,
			row.keyID,
			row.scopes,
		); err != nil {
			t.Fatalf("insert %s: %v", row.keyID, err)
		}
	}

	svc := &Service{DB: conn}
	if err := svc.migrateAPIKeyScopeAliases(); err != nil {
		t.Fatalf("first scope migration: %v", err)
	}
	if err := svc.migrateAPIKeyScopeAliases(); err != nil {
		t.Fatalf("idempotent scope migration: %v", err)
	}

	want := map[string]string{
		"legacy":  "organizations:read,organizations:write,entities:read",
		"unknown": "organizations:read,nats:unknown",
		"empty":   "",
	}
	rows, err := conn.Query(`SELECT key_id, scopes FROM api_keys`)
	if err != nil {
		t.Fatalf("query migrated scopes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var keyID, scopes string
		if err := rows.Scan(&keyID, &scopes); err != nil {
			t.Fatalf("scan migrated scopes: %v", err)
		}
		if scopes != want[keyID] {
			t.Fatalf("%s scopes = %q, want %q", keyID, scopes, want[keyID])
		}
		delete(want, keyID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated scopes: %v", err)
	}
	if len(want) != 0 {
		t.Fatalf("missing migrated rows: %v", want)
	}
}
