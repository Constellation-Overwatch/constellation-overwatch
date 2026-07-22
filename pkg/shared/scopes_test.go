package shared

import (
	"reflect"
	"testing"
)

func TestNormalizeAPIKeyScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{
			name:  "canonical scopes are trimmed and deduplicated",
			input: []string{" organizations:read ", "nats:telemetry:write", "organizations:read"},
			want:  []string{"organizations:read", "nats:telemetry:write"},
		},
		{name: "empty is rejected", wantErr: true},
		{name: "legacy organizations alias is rejected", input: []string{"orgs:read"}, wantErr: true},
		{name: "legacy broad NATS scope is rejected", input: []string{"nats:all"}, wantErr: true},
		{name: "legacy ambiguous telemetry scope is rejected", input: []string{"nats:telemetry"}, wantErr: true},
		{name: "unknown scope is rejected", input: []string{"entities:delete"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeAPIKeyScopes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeAPIKeyScopes() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeAPIKeyScopes() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
