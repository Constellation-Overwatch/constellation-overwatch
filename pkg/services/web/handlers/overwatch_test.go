package handlers

import "testing"

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
