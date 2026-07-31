package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrCredentialExists is returned when a WebAuthn credential ID is already
// owned by an account. Callers must not reveal which account owns it.
var ErrCredentialExists = errors.New("webauthn credential already exists")

// ErrCredentialStoreNotReady prevents registration when the database cannot
// prove global credential ID uniqueness. Existing credential login remains
// available so a blocked migration does not crash-loop or lock out the system.
var ErrCredentialStoreNotReady = errors.New("webauthn credential store is not ready for registration")

// WebAuthnUser implements the webauthn.User interface for passkey authentication.
type WebAuthnUser struct {
	ID             string
	Name           string
	DisplayName    string
	Role           string
	OrgID          string
	WebAuthnHandle []byte
	Credentials    []webauthn.Credential
}

// WebAuthnID returns the user handle used by the WebAuthn relying party.
// Uses the opaque WebAuthn handle when available, falling back to the user ID
// for legacy users that pre-date the random handle generation.
func (u *WebAuthnUser) WebAuthnID() []byte {
	if len(u.WebAuthnHandle) > 0 {
		return u.WebAuthnHandle
	}
	return []byte(u.ID)
}

// WebAuthnName returns the human-readable username.
func (u *WebAuthnUser) WebAuthnName() string {
	return u.Name
}

// WebAuthnDisplayName returns the display name shown during ceremony prompts.
func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.DisplayName
}

// WebAuthnCredentials returns all registered credentials for the user.
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.Credentials
}

// WebAuthnIcon returns an empty string; icon support is deprecated in the spec.
func (u *WebAuthnUser) WebAuthnIcon() string {
	return ""
}

// AuthService handles WebAuthn registration and authentication ceremonies.
type AuthService struct {
	db *sql.DB
	wa *webauthn.WebAuthn
}

// NewWebAuthn creates a configured WebAuthn relying party from environment
// variables OVERWATCH_RPID (default "localhost") and OVERWATCH_BASE_URL
// (default "http://localhost:8080").
func NewWebAuthn() (*webauthn.WebAuthn, error) {
	rpID := os.Getenv("OVERWATCH_RPID")
	if rpID == "" {
		rpID = "localhost"
	}

	baseURL := os.Getenv("OVERWATCH_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	return NewWebAuthnWithOrigins(rpID, []string{baseURL})
}

// NewWebAuthnWithOrigins creates a relying party from an already validated
// RP ID and exact-origin list.
func NewWebAuthnWithOrigins(rpID string, origins []string) (*webauthn.WebAuthn, error) {
	cfg := &webauthn.Config{
		RPDisplayName: "Constellation Overwatch",
		RPID:          rpID,
		RPOrigins:     origins,
	}

	wa, err := webauthn.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create WebAuthn relying party: %w", err)
	}

	return wa, nil
}

// NewAuthService creates a new AuthService with the provided database
// connection and WebAuthn relying party instance.
func NewAuthService(db *sql.DB, wa *webauthn.WebAuthn) *AuthService {
	return &AuthService{
		db: db,
		wa: wa,
	}
}

// WebAuthn returns the underlying webauthn.WebAuthn instance for use by handlers.
func (s *AuthService) WebAuthn() *webauthn.WebAuthn {
	return s.wa
}

// BeginRegistration starts a registration ceremony while excluding every
// credential already registered to the user. Authenticators should reject an
// attempt to recreate an existing credential; the database unique index is the
// final race-safe backstop.
func (s *AuthService) BeginRegistration(user *WebAuthnUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	if err := s.requireUniqueCredentialIDs(); err != nil {
		return nil, nil, err
	}

	return s.wa.BeginRegistration(
		user,
		webauthn.WithExclusions(
			webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors(),
		),
	)
}

// GetUserByID retrieves a WebAuthnUser by the primary user_id, loading all
// associated credentials from the webauthn_credentials table.
func (s *AuthService) GetUserByID(userID string) (*WebAuthnUser, error) {
	var username, role, orgID string
	var email, webauthnID sql.NullString

	err := s.db.QueryRow(
		`SELECT username, email, role, org_id, webauthn_id FROM users WHERE user_id = ?`, userID,
	).Scan(&username, &email, &role, &orgID, &webauthnID)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %s: %w", userID, shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	creds, err := s.GetUserCredentials(userID)
	if err != nil {
		return nil, err
	}

	displayName := username
	if email.Valid && email.String != "" {
		displayName = email.String
	}

	var handle []byte
	if webauthnID.Valid && webauthnID.String != "" {
		handle, _ = hex.DecodeString(webauthnID.String)
	}

	return &WebAuthnUser{
		ID:             userID,
		Name:           username,
		DisplayName:    displayName,
		Role:           role,
		OrgID:          orgID,
		WebAuthnHandle: handle,
		Credentials:    creds,
	}, nil
}

// GetUserByWebAuthnID retrieves a WebAuthnUser by the webauthn_id handle
// stored in the users table.
func (s *AuthService) GetUserByWebAuthnID(handle []byte) (*WebAuthnUser, error) {
	var userID, username, role, orgID string
	var email, webauthnID sql.NullString

	err := s.db.QueryRow(
		`SELECT user_id, username, email, role, org_id, webauthn_id FROM users WHERE webauthn_id = ?`, string(handle),
	).Scan(&userID, &username, &email, &role, &orgID, &webauthnID)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user for webauthn handle: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user by webauthn ID: %w", err)
	}

	creds, err := s.GetUserCredentials(userID)
	if err != nil {
		return nil, err
	}

	displayName := username
	if email.Valid && email.String != "" {
		displayName = email.String
	}

	var waHandle []byte
	if webauthnID.Valid && webauthnID.String != "" {
		waHandle, _ = hex.DecodeString(webauthnID.String)
	}

	return &WebAuthnUser{
		ID:             userID,
		Name:           username,
		DisplayName:    displayName,
		Role:           role,
		OrgID:          orgID,
		WebAuthnHandle: waHandle,
		Credentials:    creds,
	}, nil
}

// GetUserByEmail retrieves a WebAuthnUser by email address, loading all
// associated credentials from the webauthn_credentials table.
func (s *AuthService) GetUserByEmail(email string) (*WebAuthnUser, error) {
	var userID, username, role, orgID string
	var webauthnID sql.NullString

	err := s.db.QueryRow(
		`SELECT user_id, username, role, org_id, webauthn_id FROM users WHERE email = ?`, email,
	).Scan(&userID, &username, &role, &orgID, &webauthnID)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user by email: %w", err)
	}

	creds, err := s.GetUserCredentials(userID)
	if err != nil {
		return nil, err
	}

	var handle []byte
	if webauthnID.Valid && webauthnID.String != "" {
		handle, _ = hex.DecodeString(webauthnID.String)
	}

	return &WebAuthnUser{
		ID:             userID,
		Name:           username,
		DisplayName:    email,
		Role:           role,
		OrgID:          orgID,
		WebAuthnHandle: handle,
		Credentials:    creds,
	}, nil
}

// GetUserCredentials loads all WebAuthn credentials for the given user from
// the webauthn_credentials table. Each row stores the credential as a JSON blob.
func (s *AuthService) GetUserCredentials(userID string) ([]webauthn.Credential, error) {
	rows, err := s.db.Query(
		`SELECT credential_data FROM webauthn_credentials WHERE user_id = ?`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query webauthn credentials: %w", err)
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var credJSON string
		if err := rows.Scan(&credJSON); err != nil {
			return nil, fmt.Errorf("failed to scan credential row: %w", err)
		}

		var cred webauthn.Credential
		if err := json.Unmarshal([]byte(credJSON), &cred); err != nil {
			logger.Errorw("Failed to unmarshal webauthn credential", "user_id", userID, "error", err)
			continue
		}
		creds = append(creds, cred)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating credential rows: %w", err)
	}

	return creds, nil
}

// GetCredentialCount returns the number of registered WebAuthn credentials for a user.
func (s *AuthService) GetCredentialCount(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count credentials: %w", err)
	}
	return count, nil
}

// AddCredential inserts a new WebAuthn credential for the given user.
func (s *AuthService) AddCredential(userID string, cred *webauthn.Credential) error {
	if err := s.requireUniqueCredentialIDs(); err != nil {
		return err
	}

	credJSON, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("failed to marshal credential: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO webauthn_credentials (user_id, credential_id, credential_data, created_at)
		 VALUES (?, ?, ?, ?)`,
		userID, fmt.Sprintf("%x", cred.ID), string(credJSON), time.Now().Format(time.RFC3339),
	)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) &&
			(sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
				sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY) {
			return fmt.Errorf("%w: %v", ErrCredentialExists, err)
		}
		return fmt.Errorf("failed to insert webauthn credential: %w", err)
	}

	return nil
}

func (s *AuthService) requireUniqueCredentialIDs() error {
	var ready int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_index_list('webauthn_credentials')
		WHERE name = 'idx_webauthn_creds_credential_id_unique'
		  AND [unique] = 1`,
	).Scan(&ready)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCredentialStoreNotReady, err)
	}
	if ready != 1 {
		return ErrCredentialStoreNotReady
	}
	return nil
}

// UpdateCredentialSignCount updates the signature counter for a credential,
// which helps detect cloned authenticators.
func (s *AuthService) UpdateCredentialSignCount(credID []byte, count uint32) error {
	// Load the stored credential, update the counter, and write it back.
	credIDHex := fmt.Sprintf("%x", credID)

	var credJSON string
	err := s.db.QueryRow(
		`SELECT credential_data FROM webauthn_credentials WHERE credential_id = ?`, credIDHex,
	).Scan(&credJSON)
	if err != nil {
		return fmt.Errorf("failed to find credential for sign count update: %w", err)
	}

	var cred webauthn.Credential
	if err := json.Unmarshal([]byte(credJSON), &cred); err != nil {
		return fmt.Errorf("failed to unmarshal credential for sign count update: %w", err)
	}

	cred.Authenticator.SignCount = count

	updated, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("failed to marshal updated credential: %w", err)
	}

	_, err = s.db.Exec(
		`UPDATE webauthn_credentials SET credential_data = ? WHERE credential_id = ?`,
		string(updated), credIDHex,
	)
	if err != nil {
		return fmt.Errorf("failed to update sign count: %w", err)
	}

	return nil
}

// SaveWebAuthnSessionRandom stores WebAuthn session data with a random
// session key (32 bytes, hex-encoded) and an associated user reference
// (email for login, user_id for register). Returns the random key.
func (s *AuthService) SaveWebAuthnSessionRandom(ceremony, userRef string, data *webauthn.SessionData) (string, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session data: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate session key: %w", err)
	}
	key := hex.EncodeToString(raw)

	expiresAt := time.Now().Add(5 * time.Minute).Format(time.RFC3339)

	_, err = s.db.Exec(
		`INSERT INTO webauthn_sessions (ceremony, session_key, session_data, user_ref, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		ceremony, key, string(dataJSON), userRef, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("failed to save webauthn session: %w", err)
	}

	return key, nil
}

// GetWebAuthnSession retrieves and deletes a WebAuthn session by ceremony type
// and key. Expired sessions are treated as not found. Returns session data and
// the user_ref stored during SaveWebAuthnSessionRandom.
func (s *AuthService) GetWebAuthnSession(ceremony, key string) (*webauthn.SessionData, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, "", fmt.Errorf("failed to begin WebAuthn session transaction: %w", err)
	}
	defer tx.Rollback()

	var dataJSON string
	var expiresAt string
	var userRef sql.NullString

	err = tx.QueryRow(
		`SELECT session_data, user_ref, expires_at FROM webauthn_sessions
		 WHERE ceremony = ? AND session_key = ?`,
		ceremony, key,
	).Scan(&dataJSON, &userRef, &expiresAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("webauthn session: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to query webauthn session: %w", err)
	}

	// Check expiry BEFORE deleting to avoid timing oracle.
	exp, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil || time.Now().After(exp) {
		if _, err := tx.Exec(
			`DELETE FROM webauthn_sessions WHERE ceremony = ? AND session_key = ?`,
			ceremony, key,
		); err != nil {
			return nil, "", fmt.Errorf("failed to delete invalid WebAuthn session: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, "", fmt.Errorf("failed to commit invalid WebAuthn session cleanup: %w", err)
		}
		return nil, "", fmt.Errorf("webauthn session: %w", shared.ErrNotFound) // same error as not-found
	}

	result, err := tx.Exec(
		`DELETE FROM webauthn_sessions WHERE ceremony = ? AND session_key = ?`,
		ceremony, key,
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to consume WebAuthn session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, "", fmt.Errorf("failed to inspect WebAuthn session consumption: %w", err)
	}
	if rows != 1 {
		return nil, "", fmt.Errorf("webauthn session: %w", shared.ErrNotFound)
	}

	var data webauthn.SessionData
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal session data: %w", err)
	}

	ref := ""
	if userRef.Valid {
		ref = userRef.String
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("failed to commit WebAuthn session consumption: %w", err)
	}

	return &data, ref, nil
}

// GetUserByCredentialID looks up the user who owns a given credential.
func (s *AuthService) GetUserByCredentialID(credID []byte) (*WebAuthnUser, error) {
	credIDHex := fmt.Sprintf("%x", credID)

	var userID string
	err := s.db.QueryRow(
		`SELECT user_id FROM webauthn_credentials WHERE credential_id = ?`, credIDHex,
	).Scan(&userID)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user for credential: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user by credential ID: %w", err)
	}

	return s.GetUserByID(userID)
}

// CleanupExpiredSessions removes all WebAuthn sessions whose expiry has passed.
func (s *AuthService) CleanupExpiredSessions() error {
	_, err := s.db.Exec(
		`DELETE FROM webauthn_sessions WHERE expires_at < ?`,
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired webauthn sessions: %w", err)
	}

	return nil
}
