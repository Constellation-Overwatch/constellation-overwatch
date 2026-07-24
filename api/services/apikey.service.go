package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
)

var apiKeyHMACWarnOnce sync.Once

// hashAPIKey computes the HMAC-SHA256 hex digest when OVERWATCH_KEY_HASH_SECRET
// is set, falling back to plain SHA-256 for development.
func hashAPIKey(raw string) string {
	secret := os.Getenv("OVERWATCH_KEY_HASH_SECRET")
	if secret == "" {
		apiKeyHMACWarnOnce.Do(func() {
			logger.Warn("OVERWATCH_KEY_HASH_SECRET not set, using insecure SHA-256 hash for API keys")
		})
		h := sha256.Sum256([]byte(raw))
		return hex.EncodeToString(h[:])
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

// APIKeyService manages the lifecycle of API keys including creation,
// revocation, validation, and usage tracking.
type APIKeyService struct {
	db *sql.DB
}

// NewAPIKeyService creates a new APIKeyService with the given database connection.
func NewAPIKeyService(db *sql.DB) *APIKeyService {
	return &APIKeyService{db: db}
}

// CreatedKey is returned from CreateKey and contains the plaintext key (shown
// once to the user), the database key ID, visible prefix, and NATS credentials.
type CreatedKey struct {
	APIKey     string   `json:"api_key"`
	KeyID      string   `json:"key_id"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	NATSSeed   string   `json:"nats_seed,omitempty"`
	NATSPubKey string   `json:"nats_pub_key,omitempty"`
}

// StoredKey represents a non-sensitive view of an API key record.
type StoredKey struct {
	KeyID      string   `json:"key_id"`
	UserID     string   `json:"user_id"`
	OrgID      string   `json:"org_id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	Revoked    bool     `json:"revoked"`
	NATSPubKey string   `json:"nats_pub_key,omitempty"`
}

// CreateKey generates a new API key with the c4_live_ prefix, hashes it with
// SHA-256 for storage, and inserts a record in the api_keys table. When NATS
// scopes are present, an NKey pair is generated and the public key is stored.
// The plaintext API key and NATS seed are returned exactly once.
func (s *APIKeyService) CreateKey(userID, orgID, name string, scopes []string, expiresAt *time.Time) (*CreatedKey, error) {
	canonicalScopes, err := shared.NormalizeAPIKeyScopes(scopes)
	if err != nil {
		return nil, err
	}
	scopes = canonicalScopes

	keyID := uuid.New().String()

	// Generate 32 random bytes and hex-encode them.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("failed to generate random key material: %w", err)
	}

	plaintext := "c4_live_" + hex.EncodeToString(raw)
	prefix := plaintext[:16] // "c4_live_" plus first 8 hex chars

	keyHash := hashAPIKey(plaintext)

	scopesStr := shared.FormatStoredAPIKeyScopes(scopes)

	var expiresAtStr sql.NullString
	if expiresAt != nil {
		expiresAtStr = sql.NullString{String: expiresAt.Format(time.RFC3339), Valid: true}
	}

	// Generate NATS NKey pair if any NATS scopes are present.
	var natsPubKey sql.NullString
	var natsSeed string
	if hasNATSScopes(scopes) {
		kp, err := nkeys.CreateUser()
		if err != nil {
			return nil, fmt.Errorf("failed to generate NATS NKey: %w", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return nil, fmt.Errorf("failed to get NATS public key: %w", err)
		}
		seed, err := kp.Seed()
		if err != nil {
			return nil, fmt.Errorf("failed to get NATS seed: %w", err)
		}
		natsPubKey = sql.NullString{String: pub, Valid: true}
		natsSeed = string(seed)
	}

	now := time.Now().Format(time.RFC3339)

	_, err = s.db.Exec(
		`INSERT INTO api_keys (key_id, user_id, org_id, name, key_hash, key_prefix, scopes, role, nats_pub_key, revoked, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		keyID, userID, orgID, name, keyHash, prefix, scopesStr, "operator", natsPubKey, expiresAtStr, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert API key: %w", err)
	}

	result := &CreatedKey{
		APIKey: plaintext,
		KeyID:  keyID,
		Prefix: prefix,
		Scopes: scopes,
	}
	if natsSeed != "" {
		result.NATSSeed = natsSeed
		result.NATSPubKey = natsPubKey.String
	}
	return result, nil
}

// GetNATSPubKey returns the NATS public key for a given API key ID, if any.
func (s *APIKeyService) GetNATSPubKey(keyID string) (string, error) {
	var natsPubKey sql.NullString
	err := s.db.QueryRow(
		`SELECT nats_pub_key FROM api_keys WHERE key_id = ?`, keyID,
	).Scan(&natsPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to get NATS pub key: %w", err)
	}
	return natsPubKey.String, nil
}

// RevokeKey marks an API key as revoked so it can no longer authenticate.
func (s *APIKeyService) RevokeKey(keyID string) error {
	result, err := s.db.Exec(
		`UPDATE api_keys SET revoked = 1 WHERE key_id = ?`, keyID,
	)
	if err != nil {
		return fmt.Errorf("failed to revoke API key: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("API key %s: %w", keyID, shared.ErrNotFound)
	}

	return nil
}

// ListKeys returns all non-revoked API keys belonging to the given organization.
func (s *APIKeyService) ListKeys(orgID string) ([]StoredKey, error) {
	rows, err := s.db.Query(
		`SELECT key_id, user_id, org_id, name, scopes, revoked, COALESCE(nats_pub_key, '')
		 FROM api_keys WHERE org_id = ? AND revoked = 0`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	defer rows.Close()

	var keys []StoredKey
	for rows.Next() {
		var k StoredKey
		var scopesStr string
		var revokedInt int

		if err := rows.Scan(&k.KeyID, &k.UserID, &k.OrgID, &k.Name, &scopesStr, &revokedInt, &k.NATSPubKey); err != nil {
			return nil, fmt.Errorf("failed to scan API key row: %w", err)
		}

		k.Scopes = shared.ParseStoredAPIKeyScopes(scopesStr)
		k.Revoked = revokedInt == 1
		keys = append(keys, k)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating API key rows: %w", err)
	}

	return keys, nil
}

// NKeyData holds raw NKey fields for a single API key record.
type NKeyData struct {
	NATSPubKey string
	Scopes     []string
	OrgID      string
}

// ListNKeyData returns NKey data for all non-revoked keys that have NATS credentials.
func (s *APIKeyService) ListNKeyData() ([]NKeyData, error) {
	rows, err := s.db.Query(
		`SELECT nats_pub_key, scopes, org_id FROM api_keys WHERE revoked = 0 AND nats_pub_key IS NOT NULL AND nats_pub_key != ''`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list NKey records: %w", err)
	}
	defer rows.Close()

	var records []NKeyData
	for rows.Next() {
		var r NKeyData
		var scopesStr string
		if err := rows.Scan(&r.NATSPubKey, &scopesStr, &r.OrgID); err != nil {
			return nil, fmt.Errorf("failed to scan NKey record: %w", err)
		}
		r.Scopes = shared.ParseStoredAPIKeyScopes(scopesStr)
		records = append(records, r)
	}
	return records, nil
}

// ValidateKey looks up an API key by its SHA-256 hash and returns the stored
// record if the key is not revoked and not expired.
func (s *APIKeyService) ValidateKey(keyHash string) (*StoredKey, error) {
	var k StoredKey
	var scopesStr string
	var revokedInt int
	var expiresAt sql.NullString

	err := s.db.QueryRow(
		`SELECT key_id, user_id, org_id, name, scopes, revoked, expires_at
		 FROM api_keys WHERE key_hash = ?`, keyHash,
	).Scan(&k.KeyID, &k.UserID, &k.OrgID, &k.Name, &scopesStr, &revokedInt, &expiresAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("API key: %w", shared.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query API key: %w", err)
	}

	if revokedInt == 1 {
		return nil, fmt.Errorf("API key: %w", shared.ErrRevoked)
	}

	if expiresAt.Valid {
		exp, parseErr := time.Parse(time.RFC3339, expiresAt.String)
		if parseErr == nil && time.Now().After(exp) {
			return nil, fmt.Errorf("API key: %w", shared.ErrExpired)
		}
	}

	k.Scopes = shared.ParseStoredAPIKeyScopes(scopesStr)
	k.Revoked = false

	return &k, nil
}

// UpdateLastUsed records the current time as the most recent usage of the key.
func (s *APIKeyService) UpdateLastUsed(keyID string) error {
	_, err := s.db.Exec(
		`UPDATE api_keys SET last_used_at = ? WHERE key_id = ?`,
		time.Now().Format(time.RFC3339), keyID,
	)
	if err != nil {
		return fmt.Errorf("failed to update last used: %w", err)
	}

	return nil
}

// hasNATSScopes returns true if any scope starts with "nats:".
func hasNATSScopes(scopes []string) bool {
	for _, s := range scopes {
		if shared.IsNATSScope(s) {
			return true
		}
	}
	return false
}
