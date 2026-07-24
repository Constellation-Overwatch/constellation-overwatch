package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
)

// UserService manages user CRUD operations and role assignments.
type UserService struct {
	db DBTX
}

// NewUserService creates a new UserService with the given database connection.
func NewUserService(db DBTX) *UserService {
	return &UserService{db: db}
}

// User represents a row in the users table.
type User struct {
	UserID            string `json:"user_id"`
	OrgID             string `json:"org_id"`
	Username          string `json:"username"`
	Email             string `json:"email"`
	Role              string `json:"role"`
	Permissions       string `json:"permissions"`
	WebAuthnID        string `json:"webauthn_id,omitempty"`
	NeedsPasskeySetup bool   `json:"needs_passkey_setup"`
	LastLogin         string `json:"last_login,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// CreateUser inserts a new user into the users table. If user_id is empty,
// a new UUID is generated automatically.
func (s *UserService) CreateUser(user *User) error {
	if user.UserID == "" {
		user.UserID = uuid.New().String()
	}

	now := time.Now().Format(time.RFC3339)
	if user.CreatedAt == "" {
		user.CreatedAt = now
	}
	if user.UpdatedAt == "" {
		user.UpdatedAt = now
	}

	if user.Role == "" {
		user.Role = "viewer"
	}

	// Generate opaque WebAuthn user handle per spec (64 random bytes, hex-encoded).
	if user.WebAuthnID == "" {
		raw := make([]byte, 64)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("failed to generate webauthn_id: %w", err)
		}
		user.WebAuthnID = hex.EncodeToString(raw)
	}

	needsPasskey := 0
	if user.NeedsPasskeySetup {
		needsPasskey = 1
	}

	_, err := s.db.Exec(
		`INSERT INTO users (user_id, org_id, username, email, role, permissions, webauthn_id, needs_passkey_setup, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.UserID, user.OrgID, user.Username, user.Email, user.Role,
		user.Permissions, user.WebAuthnID, needsPasskey,
		user.CreatedAt, user.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// GetByID retrieves a user by their primary key user_id.
func (s *UserService) GetByID(userID string) (*User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT user_id, org_id, username, email, role, permissions, webauthn_id, needs_passkey_setup, last_login, created_at, updated_at
		 FROM users WHERE user_id = ?`, userID,
	))
}

// GetByEmail retrieves a user by their email address.
func (s *UserService) GetByEmail(email string) (*User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT user_id, org_id, username, email, role, permissions, webauthn_id, needs_passkey_setup, last_login, created_at, updated_at
		 FROM users WHERE email = ?`, email,
	))
}

// GetByUsername retrieves a user by their unique username.
func (s *UserService) GetByUsername(username string) (*User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT user_id, org_id, username, email, role, permissions, webauthn_id, needs_passkey_setup, last_login, created_at, updated_at
		 FROM users WHERE username = ?`, username,
	))
}

// ListByOrg returns all users belonging to the given organization.
func (s *UserService) ListByOrg(orgID string) ([]User, error) {
	rows, err := s.db.Query(
		`SELECT user_id, org_id, username, email, role, permissions, webauthn_id, needs_passkey_setup, last_login, created_at, updated_at
		 FROM users WHERE org_id = ?`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := s.scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user rows: %w", err)
	}

	return users, nil
}

// UpdateRole changes the role for the given user.
func (s *UserService) UpdateRole(userID, role string) error {
	result, err := s.db.Exec(
		`UPDATE users SET role = ?, updated_at = ? WHERE user_id = ?`,
		role, time.Now().Format(time.RFC3339), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user role: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user %s: %w", userID, shared.ErrNotFound)
	}

	return nil
}

// UpdateLastLogin sets the last_login timestamp to the current time.
func (s *UserService) UpdateLastLogin(userID string) error {
	_, err := s.db.Exec(
		`UPDATE users SET last_login = ?, updated_at = ? WHERE user_id = ?`,
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update last login: %w", err)
	}

	return nil
}

// MarkPasskeySetupComplete clears the needs_passkey_setup flag for the user.
func (s *UserService) MarkPasskeySetupComplete(userID string) error {
	result, err := s.db.Exec(
		`UPDATE users SET needs_passkey_setup = 0, updated_at = ? WHERE user_id = ?`,
		time.Now().Format(time.RFC3339), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark passkey setup complete: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user %s: %w", userID, shared.ErrNotFound)
	}

	return nil
}

// scanUser reads a single user from a *sql.Row.
func (s *UserService) scanUser(row *sql.Row) (*User, error) {
	var u User
	var webAuthnID, lastLogin, permissions sql.NullString
	var needsPasskey int

	err := row.Scan(
		&u.UserID, &u.OrgID, &u.Username, &u.Email, &u.Role,
		&permissions, &webAuthnID, &needsPasskey, &lastLogin,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan user: %w", err)
	}

	if permissions.Valid {
		u.Permissions = permissions.String
	}
	if webAuthnID.Valid {
		u.WebAuthnID = webAuthnID.String
	}
	u.NeedsPasskeySetup = needsPasskey == 1
	if lastLogin.Valid {
		u.LastLogin = lastLogin.String
	}

	return &u, nil
}

// scanUserRow reads a single user from a *sql.Rows iterator.
func (s *UserService) scanUserRow(rows *sql.Rows) (*User, error) {
	var u User
	var webAuthnID, lastLogin, permissions sql.NullString
	var needsPasskey int

	err := rows.Scan(
		&u.UserID, &u.OrgID, &u.Username, &u.Email, &u.Role,
		&permissions, &webAuthnID, &needsPasskey, &lastLogin,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan user row: %w", err)
	}

	if permissions.Valid {
		u.Permissions = permissions.String
	}
	if webAuthnID.Valid {
		u.WebAuthnID = webAuthnID.String
	}
	u.NeedsPasskeySetup = needsPasskey == 1
	if lastLogin.Valid {
		u.LastLogin = lastLogin.String
	}

	return &u, nil
}
