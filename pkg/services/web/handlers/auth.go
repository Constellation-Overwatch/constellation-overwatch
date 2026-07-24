package handlers

import (
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	auth_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/auth/pages"
)

const webauthnSessionCookie = "__ow_wa_session"

// AuthHandler handles WebAuthn passkey login/registration ceremonies
type AuthHandler struct {
	sessionAuth *middleware.SessionAuth
	authSvc     *services.AuthService
	userSvc     *services.UserService
	secure      bool
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(sessionAuth *middleware.SessionAuth, authSvc *services.AuthService, userSvc *services.UserService) *AuthHandler {
	return NewAuthHandlerWithSecureCookies(sessionAuth, authSvc, userSvc, secureCookies())
}

// NewAuthHandlerWithSecureCookies binds cookie attributes to the validated
// startup policy.
func NewAuthHandlerWithSecureCookies(sessionAuth *middleware.SessionAuth, authSvc *services.AuthService, userSvc *services.UserService, secure bool) *AuthHandler {
	return &AuthHandler{
		sessionAuth: sessionAuth,
		authSvc:     authSvc,
		userSvc:     userSvc,
		secure:      secure,
	}
}

// HandleLogin renders the passkey login page
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	component := auth_pages.LoginPage("")
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render login page: %v", err)
	}
}

// HandlePasskeyLoginBegin starts the WebAuthn login ceremony for a specific user (by email).
// If the user is not found or has no credentials, a fake challenge is returned
// to prevent user enumeration (the browser will fail to find a matching credential).
func (h *AuthHandler) HandlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil || h.authSvc.WebAuthn() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WebAuthn not configured"})
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email is required"})
		return
	}

	user, err := h.authSvc.GetUserByEmail(req.Email)
	if err != nil || len(user.Credentials) == 0 {
		// User not found or has no credentials: return a fake challenge to prevent enumeration.
		writeFakeChallenge(w, h.authSvc.WebAuthn().Config.RPID, h.secure)
		return
	}

	// Normal login: begin with allowCredentials scoped to this user
	options, sessionData, err := h.authSvc.WebAuthn().BeginLogin(user)
	if err != nil {
		logger.Errorw("Failed to begin passkey login", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to start authentication"})
		return
	}

	sessionKey, err := h.authSvc.SaveWebAuthnSessionRandom("login", req.Email, sessionData)
	if err != nil {
		logger.Errorw("Failed to save WebAuthn session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
		return
	}

	setWebAuthnSessionCookie(w, sessionKey, h.secure)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

// HandlePasskeyLoginFinish completes the WebAuthn login ceremony
func (h *AuthHandler) HandlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil || h.authSvc.WebAuthn() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WebAuthn not configured"})
		return
	}

	// Retrieve the session key from the cookie set during Begin
	cookie, err := r.Cookie(webauthnSessionCookie)
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}
	sessionKey := cookie.Value
	clearWebAuthnSessionCookie(w, h.secure)

	// The fake key is set by writeFakeChallenge for unknown/credentialless users.
	if sessionKey == "fake" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	sessionData, email, err := h.authSvc.GetWebAuthnSession("login", sessionKey)
	if err != nil {
		logger.Errorw("Failed to get WebAuthn session", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Session expired, please try again"})
		return
	}

	user, err := h.authSvc.GetUserByEmail(email)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	credential, err := h.authSvc.WebAuthn().FinishLogin(user, *sessionData, r)
	if err != nil {
		logger.Errorw("Failed to finish passkey login", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	// Update credential sign count
	if err := h.authSvc.UpdateCredentialSignCount(credential.ID, credential.Authenticator.SignCount); err != nil {
		logger.Warnw("Failed to update credential sign count", "error", err)
	}

	// Create session with user identity and org_id
	token, err := h.sessionAuth.CreateSessionForUser(user.ID, user.Role, false, user.OrgID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create session"})
		return
	}

	middleware.SetSessionCookieWithSecure(w, token, h.secure)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "redirect": "/overwatch"})
}

// HandlePasskeyRegisterBegin starts the WebAuthn registration ceremony (requires active session)
func (h *AuthHandler) HandlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil || h.authSvc.WebAuthn() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WebAuthn not configured"})
		return
	}

	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}

	user, err := h.authSvc.GetUserByID(userID)
	if err != nil {
		logger.Errorw("Failed to get user for registration", "error", err, "user_id", userID)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "User not found"})
		return
	}

	options, sessionData, err := h.authSvc.BeginRegistration(user)
	if err != nil {
		if errors.Is(err, services.ErrCredentialStoreNotReady) {
			logger.Errorw("Passkey registration disabled until credential migration is reconciled", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Passkey registration is temporarily unavailable"})
			return
		}
		logger.Errorw("Failed to begin passkey registration", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to start registration"})
		return
	}

	sessionKey, err := h.authSvc.SaveWebAuthnSessionRandom("register", userID, sessionData)
	if err != nil {
		logger.Errorw("Failed to save WebAuthn session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
		return
	}

	setWebAuthnSessionCookie(w, sessionKey, h.secure)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

// HandlePasskeyRegisterFinish completes the WebAuthn registration ceremony
func (h *AuthHandler) HandlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if h.authSvc == nil || h.authSvc.WebAuthn() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WebAuthn not configured"})
		return
	}

	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}

	sessionCookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil || sessionCookie.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Not authenticated"})
		return
	}
	enrollmentSession, err := h.sessionAuth.PasskeySetupRequired(sessionCookie.Value)
	if err != nil {
		logger.Errorw("Failed to inspect passkey enrollment session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
		return
	}

	// Retrieve the session key from the cookie
	waCookie, err := r.Cookie(webauthnSessionCookie)
	if err != nil || waCookie.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Session expired, please try again"})
		return
	}
	clearWebAuthnSessionCookie(w, h.secure)

	user, err := h.authSvc.GetUserByID(userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "User not found"})
		return
	}

	sessionData, ceremonyUserID, err := h.authSvc.GetWebAuthnSession("register", waCookie.Value)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Session expired, please try again"})
		return
	}
	if ceremonyUserID != userID {
		logger.Warnw("Rejected passkey registration with mismatched ceremony user", "user_id", userID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Registration failed"})
		return
	}

	credential, err := h.authSvc.WebAuthn().FinishRegistration(user, *sessionData, r)
	if err != nil {
		logger.Errorw("Failed to finish passkey registration", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Registration failed"})
		return
	}

	if err := h.authSvc.AddCredential(userID, credential); err != nil {
		if errors.Is(err, services.ErrCredentialExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Passkey is already registered"})
			return
		}
		if errors.Is(err, services.ErrCredentialStoreNotReady) {
			logger.Errorw("Passkey registration disabled until credential migration is reconciled", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Passkey registration is temporarily unavailable"})
			return
		}
		logger.Errorw("Failed to store credential", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to store credential"})
		return
	}

	// Mark the account setup complete. Enrollment sessions remain scoped and
	// are destroyed below; they never become full authenticated sessions.
	if err := h.userSvc.MarkPasskeySetupComplete(userID); err != nil {
		logger.Warnw("Failed to mark passkey setup complete", "error", err, "user_id", userID)
	}

	redirect := "/overwatch"
	if enrollmentSession {
		if err := h.sessionAuth.DestroySession(sessionCookie.Value); err != nil {
			logger.Errorw("Failed to destroy completed passkey enrollment session", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Passkey registered; please log out before continuing"})
			return
		}
		middleware.ClearSessionCookieWithSecure(w, h.secure)
		redirect = "/login"
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "redirect": redirect})
}

// HandleLogout handles the logout request
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil {
		h.sessionAuth.DestroySession(cookie.Value)
	}

	middleware.ClearSessionCookieWithSecure(w, h.secure)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// HandleSetupPasskey renders the passkey setup page for first-time users
func (h *AuthHandler) HandleSetupPasskey(w http.ResponseWriter, r *http.Request) {
	component := auth_pages.SetupPasskeyPage()
	if err := component.Render(r.Context(), w); err != nil {
		logger.Errorf("Failed to render setup passkey page: %v", err)
	}
}

// writeFakeChallenge returns a plausible WebAuthn login response with a random
// challenge and a fake credential ID in allowCredentials. This prevents user
// enumeration: the response looks identical to a real one, but the browser will
// fail to find a matching credential. Using a fake allowCredentials entry
// (rather than an empty list) prevents the browser from prompting discoverable
// credentials stored in iCloud Keychain / platform authenticators.
func writeFakeChallenge(w http.ResponseWriter, rpID string, secure bool) {
	challenge := make([]byte, 32)
	_, _ = crand.Read(challenge)

	fakeCredID := make([]byte, 32)
	_, _ = crand.Read(fakeCredID)

	resp := map[string]interface{}{
		"publicKey": map[string]interface{}{
			"challenge":        base64URLEncode(challenge),
			"timeout":          60000,
			"rpId":             rpID,
			"userVerification": "preferred",
			"allowCredentials": []map[string]interface{}{
				{
					"type": "public-key",
					"id":   base64URLEncode(fakeCredID),
				},
			},
		},
	}

	// Set a throwaway session cookie so the finish handler path is consistent.
	setWebAuthnSessionCookie(w, "fake", secure)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// base64URLEncode encodes bytes to unpadded base64url.
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// secureCookies returns true when cookies should be marked Secure. This is
// determined by checking OVERWATCH_INSECURE (explicit override) and falling
// back to whether the configured base URL starts with "https://".
func secureCookies() bool {
	if v := os.Getenv("OVERWATCH_INSECURE"); v == "true" {
		return false
	}
	baseURL := os.Getenv("OVERWATCH_BASE_URL")
	if strings.HasPrefix(baseURL, "https://") {
		return true
	}
	// Default to insecure for http:// or unset base URL (local dev)
	return false
}

// setWebAuthnSessionCookie writes the WebAuthn session key cookie.
func setWebAuthnSessionCookie(w http.ResponseWriter, key string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     webauthnSessionCookie,
		Value:    key,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearWebAuthnSessionCookie clears the WebAuthn session key cookie.
// Attributes mirror setWebAuthnSessionCookie so browsers reliably delete it.
func clearWebAuthnSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     webauthnSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
