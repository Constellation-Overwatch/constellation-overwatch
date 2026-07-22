package shared

import (
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

// APIKeyScopes is the canonical, closed vocabulary accepted for API keys.
// NATS scopes deliberately describe a single data-plane direction; there is
// no scope that grants JetStream administration or unrestricted subjects.
var APIKeyScopes = map[string]struct{}{
	ScopeOrganizationsRead:  {},
	ScopeOrganizationsWrite: {},
	ScopeEntitiesRead:       {},
	ScopeEntitiesWrite:      {},
	ScopeAdmin:              {},
	ScopeNATSTelemetryWrite: {},
	ScopeNATSCommandsRead:   {},
	ScopeNATSCommandsWrite:  {},
	ScopeNATSEntitiesRead:   {},
	ScopeNATSEntitiesWrite:  {},
	ScopeNATSEventsRead:     {},
	ScopeNATSEventsWrite:    {},
}

// NormalizeAPIKeyScopes trims, validates, and de-duplicates API-key scopes.
// Unknown and deprecated spellings are rejected instead of silently
// broadening access or creating credentials that cannot perform their job.
func NormalizeAPIKeyScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("at least one API-key scope is required")
	}

	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if _, ok := APIKeyScopes[scope]; !ok {
			return nil, fmt.Errorf("unsupported API-key scope %q", scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}

	return normalized, nil
}

func IsNATSScope(scope string) bool {
	return strings.HasPrefix(scope, "nats:")
}
