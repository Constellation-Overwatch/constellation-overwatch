package authz

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateAPIKeyScopesNormalizesAndDeduplicates(t *testing.T) {
	got, err := ValidateAPIKeyScopes([]string{
		" " + ScopeOrganizationsRead + " ",
		ScopeEntitiesRead,
		ScopeOrganizationsRead,
	})
	if err != nil {
		t.Fatalf("validate scopes: %v", err)
	}
	want := []string{ScopeOrganizationsRead, ScopeEntitiesRead}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("validated scopes = %v, want %v", got, want)
	}
}

func TestValidateAPIKeyScopesRejectsUnknownAndDeprecated(t *testing.T) {
	for _, requested := range []string{"orgs:read", "nats:telemetry:write", ""} {
		t.Run(requested, func(t *testing.T) {
			_, err := ValidateAPIKeyScopes([]string{requested})
			if !errors.Is(err, ErrInvalidAPIKeyScope) {
				t.Fatalf("error = %v, want ErrInvalidAPIKeyScope", err)
			}
		})
	}
}

func TestMigrateStoredAPIKeyScopesChangesOnlyExplicitAliases(t *testing.T) {
	got, unknown := MigrateStoredAPIKeyScopes([]string{
		"orgs:read",
		ScopeEntitiesRead,
		"nats:telemetry:write",
		ScopeOrganizationsRead,
	})
	want := []string{
		ScopeOrganizationsRead,
		ScopeEntitiesRead,
		"nats:telemetry:write",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated scopes = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(unknown, []string{"nats:telemetry:write"}) {
		t.Fatalf("unknown scopes = %v", unknown)
	}
}

func TestParseStoredAPIKeyScopesHistoricalEmptyDefault(t *testing.T) {
	if got := ParseStoredAPIKeyScopes("[]"); got != nil {
		t.Fatalf("parsed historical empty default = %v, want nil", got)
	}
}
