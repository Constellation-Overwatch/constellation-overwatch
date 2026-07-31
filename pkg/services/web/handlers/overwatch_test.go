package handlers

import (
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestOrganizationHeaderEscapesStoredName(t *testing.T) {
	got := renderOrganizationHeader(`<img src=x onerror="alert(1)">`)
	if strings.Contains(got, "<img") || !strings.Contains(got, "&lt;img") {
		t.Fatalf("organization header was not escaped: %s", got)
	}
}

func TestParseOverwatchKVKeyPreservesDottedEntityIDs(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		entityID string
		kind     overwatchKVKind
	}{
		{
			name:     "full state key with dots",
			key:      "device.1.1",
			entityID: "device.1.1",
			kind:     overwatchKVKindFullState,
		},
		{
			name:     "mavlink suffix",
			key:      "device.1.1.mavlink",
			entityID: "device.1.1",
			kind:     overwatchKVKindMAVLink,
		},
		{
			name:     "analytics suffix",
			key:      "device.1.1.analytics.summary",
			entityID: "device.1.1",
			kind:     overwatchKVKindAnalytics,
		},
		{
			name:     "detection track suffix",
			key:      "device.1.1.detections.objects.track-7",
			entityID: "device.1.1",
			kind:     overwatchKVKindDetections,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, ok := parseOverwatchKVKey(tt.key)
			if !ok {
				t.Fatalf("parseOverwatchKVKey(%q) returned ok=false", tt.key)
			}
			if parsed.EntityID != tt.entityID {
				t.Fatalf("entity ID: got %q want %q", parsed.EntityID, tt.entityID)
			}
			if parsed.Kind != tt.kind {
				t.Fatalf("kind: got %v want %v", parsed.Kind, tt.kind)
			}
		})
	}
}

func TestMergeOverwatchEntityDataUsesFullDottedKey(t *testing.T) {
	state := mergeOverwatchEntityData("device.1.1", map[string][]byte{
		"device.1.1": []byte(`{"org_id":"org-1","name":"Camera 1","entity_type":"sensor_fixed","status":"active"}`),
	})

	if state.EntityID != "device.1.1" {
		t.Fatalf("entity ID: got %q want device.1.1", state.EntityID)
	}
	if state.OrgID != "org-1" {
		t.Fatalf("org ID: got %q want org-1", state.OrgID)
	}
	if state.Name != "Camera 1" {
		t.Fatalf("name: got %q want Camera 1", state.Name)
	}
	if state.EntityType != "sensor_fixed" {
		t.Fatalf("entity type: got %q want sensor_fixed", state.EntityType)
	}
}

func TestOverwatchUpdateAllowed(t *testing.T) {
	t.Parallel()

	known := map[string]string{"entity-a": "org-a", "entity-b": "org-b"}
	tests := []struct {
		name   string
		update overwatchKVUpdate
		orgID  string
		want   bool
	}{
		{name: "admin sees any update", update: overwatchKVUpdate{State: shared.EntityState{OrgID: "org-b"}}, want: true},
		{name: "own update", update: overwatchKVUpdate{State: shared.EntityState{OrgID: "org-a"}}, orgID: "org-a", want: true},
		{name: "cross org update", update: overwatchKVUpdate{State: shared.EntityState{OrgID: "org-b"}}, orgID: "org-a"},
		{name: "own removal", update: overwatchKVUpdate{EntityID: "entity-a", Removed: true}, orgID: "org-a", want: true},
		{name: "cross org removal", update: overwatchKVUpdate{EntityID: "entity-b", Removed: true}, orgID: "org-a"},
		{name: "unknown removal fails closed", update: overwatchKVUpdate{EntityID: "unknown", Removed: true}, orgID: "org-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := overwatchUpdateAllowed(tt.update, tt.orgID, known); got != tt.want {
				t.Fatalf("overwatchUpdateAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
