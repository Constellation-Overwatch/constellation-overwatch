package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/protocol"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type EventWorker struct {
	*BaseWorker
	registry *EntityRegistry
	kv       nats.KeyValue
}

func NewEventWorker(nc *nats.Conn, js nats.JetStreamContext, kv nats.KeyValue, registry *EntityRegistry) *EventWorker {
	return &EventWorker{
		BaseWorker: NewBaseWorker(
			"EventWorker",
			nc,
			js,
			shared.StreamEvents,
			shared.ConsumerEventProcessor,
			shared.SubjectDetectionsAll,
		),
		registry: registry,
		kv:       kv,
	}
}

func (w *EventWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) error {
		return w.handleEventContext(ctx, msg)
	})
}

func (w *EventWorker) handleEvent(msg *nats.Msg) error {
	return w.handleEventContext(context.Background(), msg)
}

func (w *EventWorker) handleEventContext(ctx context.Context, msg *nats.Msg) error {
	return w.handleDetection(ctx, msg)
}

func (w *EventWorker) handleDetection(ctx context.Context, msg *nats.Msg) error {
	orgID, entityID, trackID, err := protocol.ParseDetectionSubject(msg.Subject)
	if err != nil {
		return err
	}
	event, err := protocol.DecodeDetection(msg.Data)
	if err != nil {
		return err
	}
	if event.OrgID != orgID || event.EntityID != entityID || event.TrackID != trackID {
		return fmt.Errorf("detection subject identity %s/%s/%s does not match envelope %s/%s/%s", orgID, entityID, trackID, event.OrgID, event.EntityID, event.TrackID)
	}
	if !w.registry.IsRegistered(entityID) {
		return fmt.Errorf("detection entity %s is not registered", entityID)
	}
	return w.saveDetection(ctx, event)
}

func (w *EventWorker) saveDetection(ctx context.Context, event protocol.DetectionEnvelope) error {
	key := shared.EntityKey(event.EntityID)
	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := w.kv.Get(key)
		creating := false
		var state shared.EntityState
		if errors.Is(err, nats.ErrKeyNotFound) {
			// Entity creation normally seeds KV, but detection is a valid first
			// data-plane message. Recover from a missing projection instead of
			// quarantining every detection until telemetry happens to arrive.
			creating = true
			state = shared.EntityState{
				EntityID:  event.EntityID,
				OrgID:     event.OrgID,
				CreatedAt: event.Timestamp,
				UpdatedAt: event.Timestamp,
			}
		} else if err != nil {
			return fmt.Errorf("load entity state for detection: %w", err)
		} else if err := json.Unmarshal(entry.Value(), &state); err != nil {
			return fmt.Errorf("decode existing entity state for detection: %w", err)
		}
		if state.OrgID != event.OrgID || state.EntityID != event.EntityID {
			return fmt.Errorf("detection identity does not match persisted entity state")
		}
		if state.Detections == nil {
			state.Detections = &shared.DetectionState{}
		}
		if state.Detections.TrackedObjects == nil {
			state.Detections.TrackedObjects = make(map[string]shared.TrackedObject)
		}
		existing, exists := state.Detections.TrackedObjects[event.TrackID]
		if exists && !event.Timestamp.After(existing.LastSeen) {
			return nil
		}
		firstSeen := event.Timestamp
		frameCount := 1
		if exists {
			firstSeen = existing.FirstSeen
			frameCount = existing.FrameCount + 1
		}
		cx := (event.X1 + event.X2) / 2
		cy := (event.Y1 + event.Y2) / 2
		dx, dy := 0.0, 0.0
		if exists {
			dx, dy = cx-existing.CX, cy-existing.CY
		}
		state.Detections.TrackedObjects[event.TrackID] = shared.TrackedObject{
			TrackID: event.TrackID, Label: event.Label, Confidence: event.Confidence,
			FrameCount: frameCount, BBox: &shared.BoundingBox{X1: event.X1, Y1: event.Y1, X2: event.X2, Y2: event.Y2},
			CX: cx, CY: cy, DX: dx, DY: dy,
			FirstSeen: firstSeen, LastSeen: event.Timestamp, IsActive: true,
		}
		state.Detections.Status = "active"
		state.Detections.IsLive = true
		state.Detections.FrameCount++
		state.Detections.Timestamp = event.Timestamp
		state.UpdatedAt = laterTime(state.UpdatedAt, event.Timestamp)

		data, err := json.Marshal(&state)
		if err != nil {
			return fmt.Errorf("marshal entity detection state: %w", err)
		}
		if creating {
			if _, err := w.kv.Create(key, data); err != nil {
				if errors.Is(err, nats.ErrKeyExists) {
					if err := waitForRetry(ctx, attempt); err != nil {
						return err
					}
					continue
				}
				return fmt.Errorf("create entity detection state: %w", err)
			}
			return nil
		}
		if _, err := w.kv.Update(key, data, entry.Revision()); err != nil {
			if errors.Is(err, nats.ErrKeyExists) || strings.Contains(err.Error(), "wrong last sequence") {
				if err := waitForRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("update entity detection state: %w", err)
		}
		return nil
	}
	return fmt.Errorf("save detection state after %d revision conflicts", maxRetries)
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
