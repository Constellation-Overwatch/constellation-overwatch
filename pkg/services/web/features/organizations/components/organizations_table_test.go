package components

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/ontology"
)

var dataSignalsPattern = regexp.MustCompile(`data-signals="([^"]*)"`)

func TestOrganizationEditRowEncodesDataSignalsAsJSON(t *testing.T) {
	t.Parallel()

	org := ontology.Organization{
		OrgID:       "org-1",
		Name:        `Pilot's "group"}; alert(1); //`,
		OrgType:     "military",
		Description: `It's a "test" organization`,
	}

	var rendered strings.Builder
	if err := OrganizationEditRow(org).Render(context.Background(), &rendered); err != nil {
		t.Fatalf("render organization edit row: %v", err)
	}

	signals := decodeDataSignals(t, rendered.String())
	if got := signals["edit_name"]; got != org.Name {
		t.Fatalf("edit_name round trip = %#v, want %#v", got, org.Name)
	}
	if got := signals["edit_description"]; got != org.Description {
		t.Fatalf("edit_description round trip = %#v, want %#v", got, org.Description)
	}
}

func decodeDataSignals(t *testing.T, rendered string) map[string]any {
	t.Helper()

	match := dataSignalsPattern.FindStringSubmatch(rendered)
	if len(match) != 2 {
		t.Fatalf("rendered HTML missing data-signals attribute: %s", rendered)
	}

	var signals map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &signals); err != nil {
		t.Fatalf("data-signals is not valid JSON: %v", err)
	}
	return signals
}
