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

func TestFleetEditRowEncodesDataSignalsAsJSON(t *testing.T) {
	t.Parallel()

	entity := ontology.Entity{
		EntityID:   "entity-1",
		OrgID:      "org-1",
		Name:       `Pilot's "console"}; alert(1); //`,
		EntityType: "operator_station",
		Status:     "active",
		Priority:   "high",
		IsLive:     true,
	}

	var rendered strings.Builder
	if err := FleetEditRow(nil, entity).Render(context.Background(), &rendered); err != nil {
		t.Fatalf("render fleet edit row: %v", err)
	}

	signals := decodeDataSignals(t, rendered.String())
	if got := signals["edit_name"]; got != entity.Name {
		t.Fatalf("edit_name round trip = %#v, want %#v", got, entity.Name)
	}
	if got := signals["edit_is_live"]; got != true {
		t.Fatalf("edit_is_live = %#v, want true", got)
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
