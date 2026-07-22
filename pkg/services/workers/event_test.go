package workers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type memoryKVEntry struct {
	nats.KeyValueEntry
	value    []byte
	revision uint64
}

func (e memoryKVEntry) Value() []byte    { return e.value }
func (e memoryKVEntry) Revision() uint64 { return e.revision }

type memoryKV struct {
	nats.KeyValue
	value    []byte
	revision uint64
}

func (kv *memoryKV) Get(string) (nats.KeyValueEntry, error) {
	return memoryKVEntry{value: kv.value, revision: kv.revision}, nil
}

func (kv *memoryKV) Update(_ string, value []byte, revision uint64) (uint64, error) {
	if revision != kv.revision {
		return 0, nats.ErrKeyExists
	}
	kv.revision++
	kv.value = append([]byte(nil), value...)
	return kv.revision, nil
}

func TestSaveDetectionPreservesTelemetryAndRejectsStaleTrackReplay(t *testing.T) {
	telemetryTime := time.Date(2026, 7, 21, 20, 1, 0, 0, time.UTC)
	initial := shared.EntityState{
		EntityID: "gc35-e4b", OrgID: "org-galaxy", SystemID: 1,
		Position:  &shared.PositionState{Global: &shared.GlobalPosition{Latitude: 32.9, Timestamp: telemetryTime}},
		UpdatedAt: telemetryTime,
	}
	encoded, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	kv := &memoryKV{value: encoded, revision: 1}
	w := &EventWorker{kv: kv}
	event := protocol.DetectionEnvelope{
		SchemaVersion: protocol.TelemetrySchemaVersion, EventUID: "event-1",
		OrgID: "org-galaxy", EntityID: "gc35-e4b", TrackID: "track-1",
		Label: "airplane", Confidence: .9, X1: .1, Y1: .2, X2: .3, Y2: .4,
		Timestamp: telemetryTime.Add(-time.Minute),
	}
	if err := w.saveDetection(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	var got shared.EntityState
	if err := json.Unmarshal(kv.value, &got); err != nil {
		t.Fatal(err)
	}
	if got.Position == nil || got.Position.Global == nil || got.Position.Global.Latitude != 32.9 {
		t.Fatalf("telemetry projection was lost: %#v", got.Position)
	}
	if !got.UpdatedAt.Equal(telemetryTime) {
		t.Fatalf("older detection moved entity UpdatedAt backwards: %s", got.UpdatedAt)
	}
	track := got.Detections.TrackedObjects["track-1"]
	if track.FrameCount != 1 || track.Confidence != .9 {
		t.Fatalf("detection projection = %#v", track)
	}

	stale := event
	stale.EventUID = "event-stale"
	stale.Timestamp = event.Timestamp.Add(-time.Second)
	stale.Confidence = .1
	revision := kv.revision
	if err := w.saveDetection(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if kv.revision != revision {
		t.Fatal("stale detection replay wrote a new KV revision")
	}
}
