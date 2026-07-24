package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	auth_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/auth/pages"

	"github.com/go-chi/chi/v5"
)

// InviteHandler handles the public-facing invite acceptance flow where an
// invited user follows a link, chooses a username, and has their account
// created.
type InviteHandler struct {
	inviteSvc   *services.InviteService
	userSvc     *services.UserService
	authSvc     *services.AuthService
	sessionAuth *middleware.SessionAuth
	secure      bool
}

// NewInviteHandler creates a new InviteHandler with the required service
// dependencies.
func NewInviteHandler(inviteSvc *services.InviteService, userSvc *services.UserService, authSvc *services.AuthService, sessionAuth *middleware.SessionAuth) *InviteHandler {
	return NewInviteHandlerWithSecureCookies(inviteSvc, userSvc, authSvc, sessionAuth, false)
}

// NewInviteHandlerWithSecureCookies binds the enrollment cookie to the
// validated startup policy.
func NewInviteHandlerWithSecureCookies(inviteSvc *services.InviteService, userSvc *services.UserService, authSvc *services.AuthService, sessionAuth *middleware.SessionAuth, secure bool) *InviteHandler {
	return &InviteHandler{
		inviteSvc:   inviteSvc,
		userSvc:     userSvc,
		authSvc:     authSvc,
		sessionAuth: sessionAuth,
		secure:      secure,
	}
}

// hashToken returns the hex-encoded SHA-256 digest of a plaintext token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// HandleAcceptInvite serves GET /invite/{token}. It looks up the invite by
// the SHA-256 hash of the token, verifies the invite is pending and not
// expired, and renders the InviteAcceptPage templ for the user.
func (h *InviteHandler) HandleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Missing invite token", http.StatusBadRequest)
		return
	}

	tokenHash := hashToken(token)

	invite, err := h.inviteSvc.GetInviteByTokenHash(tokenHash)
	if err != nil {
		logger.Errorf("Invite lookup failed: %v", err)
		http.Error(w, "Invalid or expired invite", http.StatusNotFound)
		return
	}

	if invite.Status != "pending" {
		http.Error(w, "This invite has already been used or revoked", http.StatusGone)
		return
	}

	expiresAt, parseErr := time.Parse(time.RFC3339, invite.ExpiresAt)
	if parseErr == nil && time.Now().After(expiresAt) {
		http.Error(w, "This invite has expired", http.StatusGone)
		return
	}

	component := auth_pages.InviteAcceptPage(invite.Email, invite.OrgID, token, "")
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render invite accept page: %v", err)
	}
}

// HandleFinalizeInvite handles POST /invite/{token}/accept. It validates the
// invite, creates a new user account with the chosen username, marks the
// invite as accepted, and returns the new user ID along with a flag
// indicating that passkey setup is required.
func (h *InviteHandler) HandleFinalizeInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		sendError(w, http.StatusBadRequest, "BAD_REQUEST", "Missing invite token")
		return
	}

	tokenHash := hashToken(token)

	invite, err := h.inviteSvc.GetInviteByTokenHash(tokenHash)
	if err != nil {
		logger.Errorf("Invite lookup failed during finalize: %v", err)
		sendError(w, http.StatusNotFound, "NOT_FOUND", "Invalid or expired invite")
		return
	}

	if invite.Status != "pending" {
		sendError(w, http.StatusGone, "GONE", "This invite has already been used or revoked")
		return
	}

	expiresAt, parseErr := time.Parse(time.RFC3339, invite.ExpiresAt)
	if parseErr == nil && time.Now().After(expiresAt) {
		sendError(w, http.StatusGone, "GONE", "This invite has expired")
		return
	}

	// Try to look up an existing user first (bootstrap flow creates the user
	// before the invite, so the email may already exist).
	existing, err := h.userSvc.GetByEmail(invite.Email)
	if err != nil {
		logger.Debugw("No existing user found for invite email (expected for new invites)", "email", invite.Email, "error", err)
	}

	var user *services.User
	if existing != nil {
		if existing.OrgID != invite.OrgID {
			sendError(w, http.StatusConflict, "ORG_MISMATCH", "User already belongs to another organization")
			return
		}
		user = existing
	} else {
		// Create the user account from the invite details (email is the identity).
		user = &services.User{
			OrgID:             invite.OrgID,
			Username:          invite.Email,
			Email:             invite.Email,
			Role:              invite.Role,
			NeedsPasskeySetup: true,
		}
		if err := h.userSvc.CreateUser(user); err != nil {
			logger.Errorf("Failed to create user from invite: %v", err)
			sendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create user account")
			return
		}
	}

	// Mark the invite as accepted.
	if err := h.inviteSvc.AcceptInvite(invite.InviteID); err != nil {
		logger.Errorf("Failed to mark invite %s as accepted: %v", invite.InviteID, err)
		sendError(w, http.StatusGone, "GONE", "This invite has already been used or expired")
		return
	}

	// Create a session for the new user so they can register their passkey.
	sessionToken, err := h.sessionAuth.CreateSessionForUser(user.UserID, user.Role, true, invite.OrgID)
	if err != nil {
		logger.Errorf("Failed to create session for invited user %s: %v", user.UserID, err)
		sendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Account created but session failed")
		return
	}
	middleware.SetSessionCookieWithSecure(w, sessionToken, h.secure)

	http.Redirect(w, r, "/setup-passkey", http.StatusSeeOther)
}
