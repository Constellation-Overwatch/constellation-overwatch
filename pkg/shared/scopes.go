package shared

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ScopeOrganizationsRead  = "organizations:read"
	ScopeOrganizationsWrite = "organizations:write"
	ScopeEntitiesRead       = "entities:read"
	ScopeEntitiesWrite      = "entities:write"
	ScopeAdmin              = "admin"

	ScopeNATSTelemetryWrite = "nats:telemetry:write"
	ScopeNATSCommandsRead   = "nats:commands:read"
	ScopeNATSCommandsWrite  = "nats:commands:write"
	ScopeNATSEntitiesRead   = "nats:entities:read"
	ScopeNATSEntitiesWrite  = "nats:entities:write"
	ScopeNATSEventsRead     = "nats:events:read"
	ScopeNATSEventsWrite    = "nats:events:write"
)

var (
	ErrInvalidAPIKeyScope = errors.New("invalid API key scope")

	canonicalAPIKeyScopes = []string{
		ScopeOrganizationsRead,
		ScopeOrganizationsWrite,
		ScopeEntitiesRead,
		ScopeEntitiesWrite,
		ScopeAdmin,
		ScopeNATSTelemetryWrite,
		ScopeNATSCommandsRead,
		ScopeNATSCommandsWrite,
		ScopeNATSEntitiesRead,
		ScopeNATSEntitiesWrite,
		ScopeNATSEventsRead,
		ScopeNATSEventsWrite,
	}

	canonicalAPIKeyScopeSet = func() map[string]struct{} {
		result := make(map[string]struct{}, len(canonicalAPIKeyScopes))
		for _, scope := range canonicalAPIKeyScopes {
			result[scope] = struct{}{}
		}
		return result
	}()

	deprecatedAPIKeyScopeAliases = map[string]string{
		"orgs:read":  ScopeOrganizationsRead,
		"orgs:write": ScopeOrganizationsWrite,
	}
)

// APIKeyScopeError identifies an unsupported or deprecated creation request.
type APIKeyScopeError struct {
	Scope       string
	Replacement string
}

func (e *APIKeyScopeError) Error() string {
	if e.Replacement != "" {
		return fmt.Sprintf("%v %q: use %q", ErrInvalidAPIKeyScope, e.Scope, e.Replacement)
	}
	return fmt.Sprintf("%v %q", ErrInvalidAPIKeyScope, e.Scope)
}

func (e *APIKeyScopeError) Unwrap() error {
	return ErrInvalidAPIKeyScope
}

// CanonicalAPIKeyScopes returns a copy of the server-owned vocabulary.
// NATS scopes deliberately describe one data-plane direction; there is no
// scope that grants JetStream administration or unrestricted subjects.
func CanonicalAPIKeyScopes() []string {
	return append([]string(nil), canonicalAPIKeyScopes...)
}

// NormalizeAPIKeyScopes trims, validates, and de-duplicates API-key scopes.
// Unknown and deprecated spellings are rejected instead of silently
// broadening access or creating credentials that cannot perform their job.
func NormalizeAPIKeyScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, &APIKeyScopeError{}
	}

	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if replacement, deprecated := deprecatedAPIKeyScopeAliases[scope]; deprecated {
			return nil, &APIKeyScopeError{Scope: scope, Replacement: replacement}
		}
		if _, ok := canonicalAPIKeyScopeSet[scope]; !ok {
			return nil, &APIKeyScopeError{Scope: scope}
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}

	return normalized, nil
}

// ParseStoredAPIKeyScopes reads the comma-separated SQLite representation.
// "[]" is accepted only as the historical empty default.
func ParseStoredAPIKeyScopes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}

	result := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		if scope := strings.TrimSpace(part); scope != "" {
			result = append(result, scope)
		}
	}
	return result
}

func FormatStoredAPIKeyScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}

// MigrateStoredAPIKeyScopes rewrites only explicitly deprecated aliases.
// Unknown stored values remain inert and are reported; guessing their meaning
// could widen an existing credential.
func MigrateStoredAPIKeyScopes(stored []string) (normalized []string, unknown []string) {
	normalized = make([]string, 0, len(stored))
	seen := make(map[string]struct{}, len(stored))
	for _, raw := range stored {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if replacement, deprecated := deprecatedAPIKeyScopeAliases[scope]; deprecated {
			scope = replacement
		}
		if _, known := canonicalAPIKeyScopeSet[scope]; !known {
			unknown = append(unknown, scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	return normalized, unknown
}

func IsNATSScope(scope string) bool {
	return strings.HasPrefix(scope, "nats:")
}
