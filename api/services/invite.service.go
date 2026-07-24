package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
)

const (
	newUserInviteDuration  = 7 * 24 * time.Hour
	recoveryInviteDuration = 15 * time.Minute
)

// InviteService manages organization invitation tokens.
type InviteService struct {
	db DBTX
}

// NewInviteService creates a new InviteService with the given database connection.
func NewInviteService(db DBTX) *InviteService {
	return &InviteService{db: db}
}

// Invite represents a row in the invites table.
type Invite struct {
	InviteID        string `json:"invite_id"`
	OrgID           string `json:"org_id"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	InvitedByUserID string `json:"invited_by_user_id"`
	Status          string `json:"status"`
	ExpiresAt       string `json:"expires_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateInvite generates a new invitation for the given email and role. It
// returns the Invite record, the plaintext invite token (to be sent to the
// invitee), and any error. The token is hashed with SHA-256 before storage.
func (s *InviteService) CreateInvite(orgID, email, role, invitedByUserID string) (*Invite, string, error) {
	inviteID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(newUserInviteDuration)

	// An invite for an existing account is a privileged recovery enrollment,
	// not a role-changing invitation. Keep its lifetime short and preserve the
	// account's existing organization and role.
	var existingOrgID, existingRole string
	err := s.db.QueryRow(
		`SELECT org_id, role FROM users WHERE email = ?`,
		email,
	).Scan(&existingOrgID, &existingRole)
	switch {
	case err == nil:
		if existingOrgID != orgID {
			return nil, "", fmt.Errorf("existing user belongs to another organization")
		}
		role = existingRole
		expiresAt = now.Add(recoveryInviteDuration)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, "", fmt.Errorf("failed to inspect invite identity: %w", err)
	}

	// Generate 32 random bytes for the invite token.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("failed to generate invite token: %w", err)
	}
	plainToken := hex.EncodeToString(raw)

	h := sha256.Sum256([]byte(plainToken))
	tokenHash := hex.EncodeToString(h[:])

	_, err = s.db.Exec(
		`INSERT INTO invites (invite_id, org_id, email, role, invited_by_user_id, token_hash, status, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		inviteID, orgID, email, role, invitedByUserID, tokenHash,
		expiresAt.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to insert invite: %w", err)
	}

	invite := &Invite{
		InviteID:        inviteID,
		OrgID:           orgID,
		Email:           email,
		Role:            role,
		InvitedByUserID: invitedByUserID,
		Status:          "pending",
		ExpiresAt:       expiresAt.Format(time.RFC3339),
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
	}

	return invite, plainToken, nil
}

// GetInviteByTokenHash retrieves an invite by its SHA-256 token hash.
func (s *InviteService) GetInviteByTokenHash(hash string) (*Invite, error) {
	var inv Invite

	err := s.db.QueryRow(
		`SELECT invite_id, org_id, email, role, invited_by_user_id, status, expires_at, created_at, updated_at
		 FROM invites WHERE token_hash = ?`, hash,
	).Scan(&inv.InviteID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedByUserID,
		&inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("invite: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query invite: %w", err)
	}

	return &inv, nil
}

// AcceptInvite marks an invite as accepted.
func (s *InviteService) AcceptInvite(inviteID string) error {
	now := time.Now().Format(time.RFC3339)
	result, err := s.db.Exec(
		`UPDATE invites
		 SET status = 'accepted', updated_at = ?
		 WHERE invite_id = ? AND status = 'pending' AND expires_at > ?`,
		now, inviteID, now,
	)
	if err != nil {
		return fmt.Errorf("failed to accept invite: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("pending invite %s: %w", inviteID, shared.ErrNotFound)
	}

	return nil
}

// ListInvites returns all invites for the given organization.
func (s *InviteService) ListInvites(orgID string) ([]Invite, error) {
	rows, err := s.db.Query(
		`SELECT invite_id, org_id, email, role, invited_by_user_id, status, expires_at, created_at, updated_at
		 FROM invites WHERE org_id = ?`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.InviteID, &inv.OrgID, &inv.Email, &inv.Role,
			&inv.InvitedByUserID, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite row: %w", err)
		}
		invites = append(invites, inv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating invite rows: %w", err)
	}

	return invites, nil
}

// RevokeInvite marks an invite as revoked so it can no longer be accepted.
func (s *InviteService) RevokeInvite(inviteID string) error {
	result, err := s.db.Exec(
		`UPDATE invites SET status = 'revoked', updated_at = ? WHERE invite_id = ?`,
		time.Now().Format(time.RFC3339), inviteID,
	)
	if err != nil {
		return fmt.Errorf("failed to revoke invite: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("invite %s: %w", inviteID, shared.ErrNotFound)
	}

	return nil
}

// CleanupExpiredInvites removes all invites whose expiry time has passed and
// whose status is still pending.
func (s *InviteService) CleanupExpiredInvites() error {
	_, err := s.db.Exec(
		`UPDATE invites SET status = 'expired', updated_at = ?
		 WHERE status = 'pending' AND expires_at < ?`,
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired invites: %w", err)
	}

	return nil
}
