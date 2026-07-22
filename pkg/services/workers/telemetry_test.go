package workers

import (
	"reflect"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestGoldenGlobalPositionProjectsCanonicalUnits(t *testing.T) {
	envelope, err := protocol.DecodeTelemetry(protocol.TelemetryV1Golden)
	if err != nil {
		t.Fatal(err)
	}
	state := &shared.EntityState{
		Position: &shared.PositionState{Local: &shared.LocalPosition{X: 1, Y: 2, Z: 3}},
	}
	w := &TelemetryWorker{}
	if !w.updateEntityState(state, &envelope) {
		t.Fatal("golden message was not handled")
	}

	if got, want := state.Position.Global.Latitude, 32.94881; got != want {
		t.Fatalf("latitude = %v, want %v", got, want)
	}
	if got, want := state.Position.Global.Longitude, -96.80132; got != want {
		t.Fatalf("longitude = %v, want %v", got, want)
	}
	if got, want := state.Position.Global.AltitudeMSL, 152.4; got != want {
		t.Fatalf("altitude MSL = %v, want %v", got, want)
	}
	if got, want := state.Position.Global.AltitudeRelative, 30.48; got != want {
		t.Fatalf("relative altitude = %v, want %v", got, want)
	}
	if got, want := state.Position.Local.VX, 1.23; got != want {
		t.Fatalf("vx = %v, want %v", got, want)
	}
	if state.Position.Local.X != 1 || state.Position.Local.Y != 2 || state.Position.Local.Z != 3 {
		t.Fatalf("global position update destroyed local coordinates: %#v", state.Position.Local)
	}
}

func TestMergeTelemetryPreservesDetectionAndPersistsCursor(t *testing.T) {
	detection := &shared.DetectionState{FrameCount: 7}
	ts := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	existing := &shared.EntityState{EntityID: "gc35-e4b", Detections: detection, UpdatedAt: ts.Add(time.Minute), LastSeen: ts.Add(time.Minute)}
	telemetry := &shared.EntityState{
		EntityID:         "gc35-e4b",
		SystemID:         1,
		ComponentID:      2,
		LastSeen:         ts,
		TelemetryCursors: map[string]shared.TelemetryCursor{"HEARTBEAT": {MessageUID: "message-1", Timestamp: ts}},
	}

	merged := (&TelemetryWorker{}).mergeTelemetryWithDetections(existing, telemetry)
	if merged.Detections != detection {
		t.Fatal("detection state was not preserved")
	}
	if merged.SystemID != 1 || merged.ComponentID != 2 {
		t.Fatalf("telemetry identity was not merged: %#v", merged)
	}
	if got := merged.TelemetryCursors["HEARTBEAT"]; got.MessageUID != "message-1" || !got.Timestamp.Equal(ts) {
		t.Fatalf("telemetry cursor was not persisted: %#v", got)
	}
	if !merged.UpdatedAt.Equal(ts.Add(time.Minute)) || !merged.LastSeen.Equal(ts.Add(time.Minute)) {
		t.Fatalf("older telemetry moved aggregate timestamps backwards: updated=%s last_seen=%s", merged.UpdatedAt, merged.LastSeen)
	}
}

func TestUpdateBatteryRejectsOutOfRangeNarrowFields(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{
		Power: &shared.PowerState{
			BatteryRemain: 42,
			Consumed:      7,
			Cells:         []uint16{12000, 12001},
		},
	}

	w.updateBattery(state, map[string]any{
		"battery_remaining": 200.0,
		"current_consumed":  float64(maxInt32) + 1,
		"voltages":          []any{12000.0, 70000.0},
	}, time.Now())

	if state.Power.BatteryRemain != 42 {
		t.Fatalf("battery remaining wrapped/changed: got %d", state.Power.BatteryRemain)
	}
	if state.Power.Consumed != 7 {
		t.Fatalf("current consumed wrapped/changed: got %d", state.Power.Consumed)
	}
	if want := []uint16{12000, 12001}; !reflect.DeepEqual(state.Power.Cells, want) {
		t.Fatalf("cells changed on invalid voltage: got %v want %v", state.Power.Cells, want)
	}
}

func TestUpdateBatteryAcceptsMAVLinkUnknownRemaining(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{Power: &shared.PowerState{BatteryRemain: 42}}

	w.updateBattery(state, map[string]any{
		"battery_remaining": -1.0,
	}, time.Now())

	if state.Power.BatteryRemain != -1 {
		t.Fatalf("battery remaining: got %d want -1", state.Power.BatteryRemain)
	}
}
