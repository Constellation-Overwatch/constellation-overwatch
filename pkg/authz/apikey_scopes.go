package authz

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

	ScopeNATSTelemetry     = "nats:telemetry"
	ScopeNATSCommands      = "nats:commands"
	ScopeNATSCommandsWrite = "nats:commands:write"
	ScopeNATSEntities      = "nats:entities"
	ScopeNATSEvents        = "nats:events"
	ScopeNATSAll           = "nats:all"
)

var (
	ErrInvalidAPIKeyScope = errors.New("invalid API key scope")

	canonicalAPIKeyScopes = []string{
		ScopeOrganizationsRead,
		ScopeOrganizationsWrite,
		ScopeEntitiesRead,
		ScopeEntitiesWrite,
		ScopeAdmin,
		ScopeNATSTelemetry,
		ScopeNATSCommands,
		ScopeNATSCommandsWrite,
		ScopeNATSEntities,
		ScopeNATSEvents,
		ScopeNATSAll,
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

// APIKeyScopeError identifies a scope rejected at the credential creation
// boundary. Deprecated aliases are named separately so clients can repair
// their request instead of relying on silent privilege translation.
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

// CanonicalAPIKeyScopes returns the complete server-owned vocabulary accepted
// for newly created API keys.
func CanonicalAPIKeyScopes() []string {
	return append([]string(nil), canonicalAPIKeyScopes...)
}

// ValidateAPIKeyScopes trims and deduplicates an untrusted scope request.
// Unknown and deprecated names are rejected before any credential is generated.
func ValidateAPIKeyScopes(requested []string) ([]string, error) {
	normalized := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, raw := range requested {
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

// ParseStoredAPIKeyScopes reads the comma-separated representation used by
// SQLite. "[]" is accepted only as the historical empty default.
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

// FormatStoredAPIKeyScopes returns the canonical comma-separated storage form.
func FormatStoredAPIKeyScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}

// MigrateStoredAPIKeyScopes rewrites only explicitly deprecated aliases.
// Unknown stored values are preserved and reported so a migration can never
// widen an existing credential by guessing at its intended meaning.
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

// IsNATSScope reports whether a validated scope belongs to the NATS namespace.
func IsNATSScope(scope string) bool {
	return strings.HasPrefix(scope, "nats:")
}
