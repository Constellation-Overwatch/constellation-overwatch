package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
	"github.com/a-h/templ"
)

func renderFleetWithRole(t *testing.T, role string, component templ.Component) string {
	t.Helper()
	var output bytes.Buffer
	ctx := context.WithValue(context.Background(), middleware.ContextKeyUserRole, role)
	if err := component.Render(ctx, &output); err != nil {
		t.Fatalf("render component: %v", err)
	}
	return output.String()
}

func TestViewerFleetControlsAreReadOnly(t *testing.T) {
	orgs := []ontology.Organization{
		{OrgID: "org-a", Name: "Organization A", OrgType: "commercial"},
	}
	entity := ontology.Entity{
		EntityID:   "entity-a",
		OrgID:      "org-a",
		Name:       "Entity A",
		EntityType: "sensor_platform",
	}

	tableOutput := renderFleetWithRole(t, "viewer", FleetTable(orgs, []ontology.Entity{entity}))
	if strings.Contains(tableOutput, `<form id="new-fleet-form"`) {
		t.Fatal("viewer fleet table contains create form")
	}

	rowOutput := renderFleetWithRole(t, "viewer", FleetRow(orgs, entity))
	for _, forbidden := range []string{"/fleet/edit/", "@delete('/api/fleet/"} {
		if strings.Contains(rowOutput, forbidden) {
			t.Fatalf("viewer output contains mutation control %q", forbidden)
		}
	}
	if !strings.Contains(rowOutput, "Copy") {
		t.Fatal("viewer output omitted the read-only copy control")
	}
}
