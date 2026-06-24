package embeddednats

import "testing"

func TestBuildNATSPermissionsAgentOpsScope(t *testing.T) {
	perms := BuildNATSPermissions([]string{"nats:agentops"}, "org-1")
	if perms == nil || perms.Publish == nil || perms.Subscribe == nil {
		t.Fatalf("expected publish and subscribe permissions, got %#v", perms)
	}

	if !containsSubject(perms.Publish.Allow, "constellation.agentops.>") {
		t.Fatalf("publish allow = %#v, want constellation.agentops.>", perms.Publish.Allow)
	}
	if !containsSubject(perms.Subscribe.Allow, "constellation.agentops.>") {
		t.Fatalf("subscribe allow = %#v, want constellation.agentops.>", perms.Subscribe.Allow)
	}
}

func containsSubject(subjects []string, want string) bool {
	for _, subject := range subjects {
		if subject == want {
			return true
		}
	}
	return false
}
