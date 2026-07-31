package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
)

const (
	newUserInviteDuration  = 7 * 24 * time.Hour
	recoveryInviteDuration = 15 * time.Minute

	InvitePurposeInitialSetup  = "initial_setup"
	InvitePurposeAdminRecovery = "admin_recovery"

	inviteAuditResourceType = "passkey_enrollment_grant"
)

var ErrInviteForbidden = errors.New("invite operation forbidden")

// InviteService manages organization invitation tokens.
type InviteService struct {
	db *sql.DB
}

// NewInviteService creates a new InviteService with the given database connection.
func NewInviteService(db *sql.DB) *InviteService {
	return &InviteService{db: db}
}

// Invite represents a row in the invites table.
type Invite struct {
	InviteID        string `json:"invite_id"`
	OrgID           string `json:"org_id"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	InvitedByUserID string `json:"invited_by_user_id"`
	Purpose         string `json:"purpose"`
	Status          string `json:"status"`
	ExpiresAt       string `json:"expires_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateInvite generates a new invitation for the given email and role. It
// returns the Invite record, the plaintext invite token (to be sent to the
// invitee), and any error. The token is hashed with SHA-256 before storage.
func (s *InviteService) CreateInvite(orgID, email, role, invitedByUserID string) (*Invite, string, error) {
	return s.createInvite(orgID, email, role, invitedByUserID, "")
}

// CreateInitialSetupInvite creates the bootstrap administrator enrollment
// grant. Bootstrap creates the user before the grant, so the purpose must be
// explicit rather than inferred from account existence.
func (s *InviteService) CreateInitialSetupInvite(orgID, email, role, invitedByUserID string) (*Invite, string, error) {
	return s.createInvite(orgID, email, role, invitedByUserID, InvitePurposeInitialSetup)
}

func (s *InviteService) createInvite(orgID, email, role, invitedByUserID, requestedPurpose string) (*Invite, string, error) {
	inviteID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(newUserInviteDuration)
	purpose := requestedPurpose

	tx, err := s.db.Begin()
	if err != nil {
		return nil, "", fmt.Errorf("failed to begin invite transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var inviterOrgID, inviterRole string
	if err := tx.QueryRow(
		`SELECT org_id, role FROM users WHERE user_id = ?`,
		invitedByUserID,
	).Scan(&inviterOrgID, &inviterRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", fmt.Errorf("%w: invite issuer does not exist", ErrInviteForbidden)
		}
		return nil, "", fmt.Errorf("failed to inspect invite issuer: %w", err)
	}
	if inviterOrgID != orgID {
		return nil, "", fmt.Errorf("%w: invite issuer belongs to another organization", ErrInviteForbidden)
	}
	if inviterRole != "admin" {
		return nil, "", fmt.Errorf("%w: invite issuer is not an administrator", ErrInviteForbidden)
	}

	// An invite for an existing account is a privileged recovery enrollment,
	// not a role-changing invitation. Keep its lifetime short and preserve the
	// account's existing organization and role.
	var existingUserID, existingOrgID, existingRole string
	err = tx.QueryRow(
		`SELECT user_id, org_id, role FROM users WHERE email = ?`,
		email,
	).Scan(&existingUserID, &existingOrgID, &existingRole)
	switch {
	case err == nil:
		if existingOrgID != orgID {
			return nil, "", fmt.Errorf("existing user belongs to another organization")
		}
		role = existingRole
		expiresAt = now.Add(recoveryInviteDuration)
		if purpose == "" {
			purpose = InvitePurposeAdminRecovery
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, "", fmt.Errorf("failed to inspect invite identity: %w", err)
	}
	if purpose == "" {
		purpose = InvitePurposeInitialSetup
	}
	if purpose != InvitePurposeInitialSetup && purpose != InvitePurposeAdminRecovery {
		return nil, "", fmt.Errorf("invalid invite purpose %q", purpose)
	}

	// Generate 32 random bytes for the invite token.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("failed to generate invite token: %w", err)
	}
	plainToken := hex.EncodeToString(raw)

	h := sha256.Sum256([]byte(plainToken))
	tokenHash := hex.EncodeToString(h[:])

	_, err = tx.Exec(
		`INSERT INTO invites (invite_id, org_id, email, role, invited_by_user_id, token_hash, purpose, status, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		inviteID, orgID, email, role, invitedByUserID, tokenHash, purpose,
		expiresAt.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to insert invite: %w", err)
	}

	if err := writeInviteAudit(tx, inviteAuditRecord{
		OrgID:          orgID,
		ActorUserID:    invitedByUserID,
		Action:         inviteAuditAction(purpose, "issued"),
		InviteID:       inviteID,
		Purpose:        purpose,
		TargetUserID:   existingUserID,
		IssuedByUserID: invitedByUserID,
		ExpiresAt:      expiresAt.Format(time.RFC3339),
		Trigger:        "user",
	}); err != nil {
		return nil, "", fmt.Errorf("failed to audit invite issuance: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("failed to commit invite: %w", err)
	}

	invite := &Invite{
		InviteID:        inviteID,
		OrgID:           orgID,
		Email:           email,
		Role:            role,
		InvitedByUserID: invitedByUserID,
		Purpose:         purpose,
		Status:          "pending",
		ExpiresAt:       expiresAt.Format(time.RFC3339),
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
	}

	return invite, plainToken, nil
}

type inviteAuditRecord struct {
	OrgID          string
	ActorUserID    string
	Action         string
	InviteID       string
	Purpose        string
	TargetUserID   string
	IssuedByUserID string
	ExpiresAt      string
	Trigger        string
}

func inviteAuditAction(purpose, lifecycle string) string {
	return "passkey." + purpose + "." + lifecycle
}

func writeInviteAudit(tx *sql.Tx, record inviteAuditRecord) error {
	changes, err := json.Marshal(map[string]string{
		"purpose":           record.Purpose,
		"target_user_id":    record.TargetUserID,
		"issued_by_user_id": record.IssuedByUserID,
		"expires_at":        record.ExpiresAt,
		"trigger":           record.Trigger,
	})
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}

	var actor any
	if record.ActorUserID != "" {
		actor = record.ActorUserID
	}
	_, err = tx.Exec(
		`INSERT INTO audit_log
		 (audit_id, org_id, user_id, action, resource_type, resource_id, changes, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(),
		record.OrgID,
		actor,
		record.Action,
		inviteAuditResourceType,
		record.InviteID,
		string(changes),
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

// GetInviteByTokenHash retrieves an invite by its SHA-256 token hash.
func (s *InviteService) GetInviteByTokenHash(hash string) (*Invite, error) {
	var inv Invite

	err := s.db.QueryRow(
		`SELECT invite_id, org_id, email, role, invited_by_user_id, purpose, status, expires_at, created_at, updated_at
		 FROM invites WHERE token_hash = ?`, hash,
	).Scan(&inv.InviteID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedByUserID,
		&inv.Purpose, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("invite: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query invite: %w", err)
	}

	return &inv, nil
}

// AcceptInvite atomically marks a live grant accepted and records who redeemed
// it. A replay cannot create a second audit event because only a pending row
// can transition.
func (s *InviteService) AcceptInvite(inviteID, redeemedByUserID string) error {
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin invite acceptance: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var orgID, email, purpose, issuedByUserID, expiresAt string
	if err := tx.QueryRow(
		`SELECT org_id, email, purpose, invited_by_user_id, expires_at
		 FROM invites WHERE invite_id = ?`,
		inviteID,
	).Scan(&orgID, &email, &purpose, &issuedByUserID, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("invite %s: %w", inviteID, shared.ErrNotFound)
		}
		return fmt.Errorf("failed to inspect invite acceptance: %w", err)
	}

	var targetUserID string
	if err := tx.QueryRow(
		`SELECT user_id FROM users WHERE email = ? AND org_id = ?`,
		email,
		orgID,
	).Scan(&targetUserID); err != nil {
		return fmt.Errorf("failed to resolve invite target: %w", err)
	}
	if redeemedByUserID == "" || redeemedByUserID != targetUserID {
		return fmt.Errorf("invite target mismatch: %w", shared.ErrNotFound)
	}

	result, err := tx.Exec(
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

	if err := writeInviteAudit(tx, inviteAuditRecord{
		OrgID:          orgID,
		ActorUserID:    redeemedByUserID,
		Action:         inviteAuditAction(purpose, "redeemed"),
		InviteID:       inviteID,
		Purpose:        purpose,
		TargetUserID:   redeemedByUserID,
		IssuedByUserID: issuedByUserID,
		ExpiresAt:      expiresAt,
		Trigger:        "user",
	}); err != nil {
		return fmt.Errorf("failed to audit invite redemption: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit invite acceptance: %w", err)
	}
	return nil
}

// ListInvites returns all invites for the given organization.
func (s *InviteService) ListInvites(orgID string) ([]Invite, error) {
	rows, err := s.db.Query(
		`SELECT invite_id, org_id, email, role, invited_by_user_id, purpose, status, expires_at, created_at, updated_at
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
			&inv.InvitedByUserID, &inv.Purpose, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite row: %w", err)
		}
		invites = append(invites, inv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating invite rows: %w", err)
	}

	return invites, nil
}

// RevokeInvite atomically revokes a pending grant and records the actor. Used,
// accepted, expired, and already-revoked grants cannot be rewritten.
func (s *InviteService) RevokeInvite(inviteID, revokedByUserID string) error {
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin invite revocation: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var orgID, purpose, issuedByUserID, expiresAt string
	if err := tx.QueryRow(
		`SELECT org_id, purpose, invited_by_user_id, expires_at
		 FROM invites WHERE invite_id = ?`,
		inviteID,
	).Scan(&orgID, &purpose, &issuedByUserID, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("invite %s: %w", inviteID, shared.ErrNotFound)
		}
		return fmt.Errorf("failed to inspect invite revocation: %w", err)
	}

	var revokerOrgID, revokerRole string
	if err := tx.QueryRow(
		`SELECT org_id, role FROM users WHERE user_id = ?`,
		revokedByUserID,
	).Scan(&revokerOrgID, &revokerRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: invite revoker does not exist", ErrInviteForbidden)
		}
		return fmt.Errorf("failed to inspect invite revoker: %w", err)
	}
	if revokerOrgID != orgID {
		return fmt.Errorf("%w: invite revoker belongs to another organization", ErrInviteForbidden)
	}
	if revokerRole != "admin" {
		return fmt.Errorf("%w: invite revoker is not an administrator", ErrInviteForbidden)
	}

	result, err := tx.Exec(
		`UPDATE invites SET status = 'revoked', updated_at = ?
		 WHERE invite_id = ? AND status = 'pending'`,
		now, inviteID,
	)
	if err != nil {
		return fmt.Errorf("failed to revoke invite: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("pending invite %s: %w", inviteID, shared.ErrNotFound)
	}

	if err := writeInviteAudit(tx, inviteAuditRecord{
		OrgID:          orgID,
		ActorUserID:    revokedByUserID,
		Action:         inviteAuditAction(purpose, "revoked"),
		InviteID:       inviteID,
		Purpose:        purpose,
		IssuedByUserID: issuedByUserID,
		ExpiresAt:      expiresAt,
		Trigger:        "user",
	}); err != nil {
		return fmt.Errorf("failed to audit invite revocation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit invite revocation: %w", err)
	}
	return nil
}

// RevokePendingInvitesByIssuer atomically revokes and audits every pending
// grant created by one administrator. Bootstrap uses this instead of a raw SQL
// sweep so startup hygiene cannot silently revoke recovery grants.
func (s *InviteService) RevokePendingInvitesByIssuer(issuerUserID string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin pending invite revocation: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var issuerRole string
	if err := tx.QueryRow(
		`SELECT role FROM users WHERE user_id = ?`,
		issuerUserID,
	).Scan(&issuerRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: invite issuer does not exist", ErrInviteForbidden)
		}
		return 0, fmt.Errorf("failed to inspect pending invite issuer: %w", err)
	}
	if issuerRole != "admin" {
		return 0, fmt.Errorf("%w: invite issuer is not an administrator", ErrInviteForbidden)
	}

	rows, err := tx.Query(
		`SELECT i.invite_id, i.org_id, i.purpose, i.expires_at, COALESCE(u.user_id, '')
		 FROM invites i
		 LEFT JOIN users u ON u.email = i.email AND u.org_id = i.org_id
		 WHERE i.invited_by_user_id = ? AND i.status = 'pending'`,
		issuerUserID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to list pending invites: %w", err)
	}

	type pendingInvite struct {
		inviteID     string
		orgID        string
		purpose      string
		expiresAt    string
		targetUserID string
	}
	var pending []pendingInvite
	for rows.Next() {
		var invite pendingInvite
		if err := rows.Scan(
			&invite.inviteID,
			&invite.orgID,
			&invite.purpose,
			&invite.expiresAt,
			&invite.targetUserID,
		); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan pending invite: %w", err)
		}
		pending = append(pending, invite)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("failed to iterate pending invites: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("failed to close pending invites: %w", err)
	}

	now := time.Now().Format(time.RFC3339)
	revoked := 0
	for _, invite := range pending {
		result, err := tx.Exec(
			`UPDATE invites SET status = 'revoked', updated_at = ?
			 WHERE invite_id = ? AND status = 'pending'`,
			now,
			invite.inviteID,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to revoke pending invite: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("failed to inspect pending invite revocation: %w", err)
		}
		if changed == 0 {
			continue
		}
		if err := writeInviteAudit(tx, inviteAuditRecord{
			OrgID:          invite.orgID,
			Action:         inviteAuditAction(invite.purpose, "revoked"),
			InviteID:       invite.inviteID,
			Purpose:        invite.purpose,
			TargetUserID:   invite.targetUserID,
			IssuedByUserID: issuerUserID,
			ExpiresAt:      invite.expiresAt,
			Trigger:        "bootstrap_hygiene",
		}); err != nil {
			return 0, fmt.Errorf("failed to audit pending invite revocation: %w", err)
		}
		revoked++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit pending invite revocation: %w", err)
	}
	return revoked, nil
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
