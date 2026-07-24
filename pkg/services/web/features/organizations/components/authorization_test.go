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

func renderWithRole(t *testing.T, role string, component templ.Component) string {
	t.Helper()
	var output bytes.Buffer
	ctx := context.WithValue(context.Background(), middleware.ContextKeyUserRole, role)
	if err := component.Render(ctx, &output); err != nil {
		t.Fatalf("render component: %v", err)
	}
	return output.String()
}

func TestViewerOrganizationControlsAreReadOnly(t *testing.T) {
	org := ontology.Organization{OrgID: "org-a", Name: "Organization A", OrgType: "commercial"}
	output := renderWithRole(t, "viewer", OrganizationsTable([]ontology.Organization{org}, "org-a"))

	for _, forbidden := range []string{`<form id="new-org-form"`, "/organizations/edit/", "@delete('/api/organizations/"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("viewer output contains mutation control %q", forbidden)
		}
	}
	if !strings.Contains(output, "Copy") {
		t.Fatal("viewer output omitted the read-only copy control")
	}
}

func TestViewerEntityControlsAreReadOnly(t *testing.T) {
	entity := ontology.Entity{
		EntityID:   "entity-a",
		OrgID:      "org-a",
		Name:       "Entity A",
		EntityType: "sensor_platform",
	}
	output := renderWithRole(t, "viewer", EntityRow("org-a", entity))

	if !strings.Contains(output, "Read only") {
		t.Fatal("viewer entity row did not render read-only state")
	}
	for _, forbidden := range []string{"/organizations/entities/edit", "@delete('/api/entities/"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("viewer output contains mutation control %q", forbidden)
		}
	}
}
