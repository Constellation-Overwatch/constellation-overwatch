package services

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func newAuthServiceTestDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "auth.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	conn.SetMaxOpenConns(1)

	for _, statement := range []string{
		`CREATE TABLE webauthn_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			credential_data TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_webauthn_creds_credential_id_unique
			ON webauthn_credentials(credential_id)`,
		`CREATE TABLE webauthn_sessions (
			ceremony TEXT NOT NULL,
			session_key TEXT NOT NULL,
			session_data TEXT NOT NULL,
			user_ref TEXT DEFAULT '',
			expires_at TEXT NOT NULL,
			PRIMARY KEY (ceremony, session_key)
		)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("initialize test database: %v", err)
		}
	}

	return conn
}

func newTestWebAuthn(t *testing.T) *webauthn.WebAuthn {
	t.Helper()

	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Test RP",
		RPID:          "example.com",
		RPOrigins:     []string{"https://example.com"},
	})
	if err != nil {
		t.Fatalf("create WebAuthn instance: %v", err)
	}
	return wa
}

func TestBeginRegistrationExcludesExistingCredentials(t *testing.T) {
	svc := NewAuthService(newAuthServiceTestDB(t), newTestWebAuthn(t))
	user := &WebAuthnUser{
		ID:             "user-1",
		Name:           "user@example.com",
		DisplayName:    "User",
		WebAuthnHandle: []byte("opaque-user-handle"),
		Credentials: []webauthn.Credential{
			{
				ID:        []byte{0x01, 0x02, 0x03},
				Transport: []protocol.AuthenticatorTransport{protocol.USB},
			},
			{
				ID:        []byte{0x04, 0x05, 0x06},
				Transport: []protocol.AuthenticatorTransport{protocol.Internal},
			},
		},
	}

	creation, _, err := svc.BeginRegistration(user)
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	if got := len(creation.Response.CredentialExcludeList); got != 2 {
		t.Fatalf("exclude credential count = %d, want 2", got)
	}
	for i, credential := range user.Credentials {
		if got := creation.Response.CredentialExcludeList[i].CredentialID; string(got) != string(credential.ID) {
			t.Errorf("exclude credential %d ID = %x, want %x", i, got, credential.ID)
		}
	}
}

func TestAddCredentialRejectsDuplicateID(t *testing.T) {
	svc := NewAuthService(newAuthServiceTestDB(t), newTestWebAuthn(t))
	credential := &webauthn.Credential{ID: []byte{0xaa, 0xbb}}

	if err := svc.AddCredential("user-1", credential); err != nil {
		t.Fatalf("add first credential: %v", err)
	}
	if err := svc.AddCredential("user-2", credential); !errors.Is(err, ErrCredentialExists) {
		t.Fatalf("duplicate error = %v, want ErrCredentialExists", err)
	}

	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 1 {
		t.Fatalf("credential rows = %d, want 1", count)
	}
}

func TestRegistrationFailsClosedWithoutUniqueCredentialIndex(t *testing.T) {
	conn := newAuthServiceTestDB(t)
	if _, err := conn.Exec(`DROP INDEX idx_webauthn_creds_credential_id_unique`); err != nil {
		t.Fatalf("drop unique credential index: %v", err)
	}
	svc := NewAuthService(conn, newTestWebAuthn(t))
	user := &WebAuthnUser{
		ID:             "user-1",
		Name:           "user@example.com",
		DisplayName:    "User",
		WebAuthnHandle: []byte("opaque-user-handle"),
	}

	if _, _, err := svc.BeginRegistration(user); !errors.Is(err, ErrCredentialStoreNotReady) {
		t.Fatalf("begin registration error = %v, want ErrCredentialStoreNotReady", err)
	}
	if err := svc.AddCredential("user-1", &webauthn.Credential{ID: []byte{0x01}}); !errors.Is(err, ErrCredentialStoreNotReady) {
		t.Fatalf("add credential error = %v, want ErrCredentialStoreNotReady", err)
	}
}

func TestGetWebAuthnSessionConsumesExactlyOnce(t *testing.T) {
	svc := NewAuthService(newAuthServiceTestDB(t), newTestWebAuthn(t))
	key, err := svc.SaveWebAuthnSessionRandom(
		"register",
		"user-1",
		&webauthn.SessionData{Challenge: "0123456789abcdef"},
	)
	if err != nil {
		t.Fatalf("save WebAuthn session: %v", err)
	}

	type result struct {
		userRef string
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, userRef, err := svc.GetWebAuthnSession("register", key)
			results <- result{userRef: userRef, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var successes, notFound int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			if result.userRef != "user-1" {
				t.Errorf("user_ref = %q, want user-1", result.userRef)
			}
		case errors.Is(result.err, shared.ErrNotFound):
			notFound++
		default:
			t.Errorf("unexpected consume error: %v", result.err)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("consume outcomes: successes=%d not_found=%d, want 1 and 1", successes, notFound)
	}
}

func TestNewWebAuthnWithConfigUsesExactValidatedOrigins(t *testing.T) {
	wa, err := NewWebAuthnWithConfig(
		"galaxyuas.com",
		[]string{"https://hub.galaxyuas.com", "https://backup.galaxyuas.com"},
	)
	if err != nil {
		t.Fatalf("NewWebAuthnWithConfig: %v", err)
	}
	if wa.Config.RPID != "galaxyuas.com" {
		t.Fatalf("RPID = %q", wa.Config.RPID)
	}
	if len(wa.Config.RPOrigins) != 2 ||
		wa.Config.RPOrigins[0] != "https://hub.galaxyuas.com" ||
		wa.Config.RPOrigins[1] != "https://backup.galaxyuas.com" {
		t.Fatalf("RPOrigins = %#v", wa.Config.RPOrigins)
	}
}
