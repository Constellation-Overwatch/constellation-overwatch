package workers

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type conflictKV struct {
	nats.KeyValue
	value    []byte
	revision uint64
}

func (kv *conflictKV) Get(string) (nats.KeyValueEntry, error) {
	return memoryKVEntry{value: kv.value, revision: kv.revision}, nil
}

func (kv *conflictKV) Update(string, []byte, uint64) (uint64, error) {
	return 0, nats.ErrKeyExists
}

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

func TestGlobalPositionRejectsOutOfRangeMAVLinkCoordinates(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{Position: &shared.PositionState{
		Global: &shared.GlobalPosition{Latitude: 32.9, Longitude: -96.8, AltitudeMSL: 152.4, AltitudeRelative: 30.48},
		Local:  &shared.LocalPosition{VX: 1.23, VY: 2.34, VZ: -0.45},
	}}

	w.updateGlobalPosition(state, map[string]any{
		"lat":          900000001.0,
		"lon":          -1800000001.0,
		"alt":          float64(maxInt32) + 1,
		"relative_alt": float64(minInt32) - 1,
		"vx":           32768.0,
		"vy":           -32769.0,
		"vz":           1.5,
	}, time.Now())

	global := state.Position.Global
	local := state.Position.Local
	if global.Latitude != 32.9 || global.Longitude != -96.8 || global.AltitudeMSL != 152.4 || global.AltitudeRelative != 30.48 {
		t.Fatalf("invalid global position changed prior state: %#v", global)
	}
	if local.VX != 1.23 || local.VY != 2.34 || local.VZ != -0.45 {
		t.Fatalf("invalid local velocity changed prior state: %#v", local)
	}
}

func TestGPSRawRejectsOutOfRangeNarrowFields(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{Position: &shared.PositionState{Global: &shared.GlobalPosition{
		Latitude: 32.9, Longitude: -96.8, AltitudeMSL: 152.4, AccuracyH: 1.2, AccuracyV: 2.3,
	}}}

	w.updateGPSRaw(state, map[string]any{
		"lat": 900000001.0, "lon": 1800000001.0,
		"alt": float64(maxInt32) + 1, "eph": -1.0, "epv": 65536.0,
	}, time.Now())

	global := state.Position.Global
	if global.Latitude != 32.9 || global.Longitude != -96.8 || global.AltitudeMSL != 152.4 || global.AccuracyH != 1.2 || global.AccuracyV != 2.3 {
		t.Fatalf("invalid GPS_RAW_INT changed prior state: %#v", global)
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

func TestFailedTelemetryPersistDoesNotAdvanceCachedCursor(t *testing.T) {
	ts := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	initial := &shared.EntityState{
		EntityID: "gc35-e4b", OrgID: "org-galaxy",
		TelemetryCursors: map[string]shared.TelemetryCursor{},
	}
	encodedState, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	kv := &conflictKV{value: encodedState, revision: 1}
	w := &TelemetryWorker{
		BaseWorker: &BaseWorker{name: "TelemetryWorker"},
		kv:         kv,
		registry:   &EntityRegistry{entities: map[string]bool{"gc35-e4b": true}},
		entityCache: map[string]*shared.EntityState{
			"gc35-e4b": initial,
		},
	}
	envelope := protocol.TelemetryEnvelope{
		SchemaVersion: protocol.TelemetrySchemaVersion,
		MessageUID:    "message-conflict",
		OrgID:         "org-galaxy",
		EntityID:      "gc35-e4b",
		MessageType:   "HEARTBEAT",
		SystemID:      1,
		ComponentID:   1,
		Data:          map[string]any{"type": float64(1), "autopilot": float64(1)},
		Timestamp:     ts,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := protocol.TelemetrySubject(envelope.OrgID, envelope.EntityID)
	if err != nil {
		t.Fatal(err)
	}
	msg := &nats.Msg{Subject: subject, Data: data}

	if err := w.handleTelemetryMessageContext(context.Background(), msg); err == nil {
		t.Fatal("expected exhausted CAS conflict")
	}
	if _, ok := w.entityCache[envelope.EntityID].TelemetryCursors[envelope.MessageType]; ok {
		t.Fatal("failed persist poisoned cached telemetry cursor")
	}
	if err := w.handleTelemetryMessageContext(context.Background(), msg); err == nil {
		t.Fatal("redelivery was incorrectly treated as a persisted duplicate")
	}
}
