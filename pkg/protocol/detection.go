package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const detectionPrefix = "constellation.events.isr."

// DetectionSchemaVersion is versioned independently from telemetry so either
// contract can evolve without silently changing the other.
const DetectionSchemaVersion = "1.0.0"

// DetectionEnvelope is the canonical v1 Pulsar detection event.
type DetectionEnvelope struct {
	SchemaVersion string    `json:"schema_version"`
	EventUID      string    `json:"event_uid"`
	OrgID         string    `json:"org_id"`
	EntityID      string    `json:"entity_id"`
	TrackID       string    `json:"track_id"`
	Label         string    `json:"label"`
	Confidence    float64   `json:"confidence"`
	X1            float64   `json:"x1"`
	Y1            float64   `json:"y1"`
	X2            float64   `json:"x2"`
	Y2            float64   `json:"y2"`
	Timestamp     time.Time `json:"timestamp"`
}

func DetectionSubject(orgID, entityID, trackID string) (string, error) {
	for name, value := range map[string]string{"org_id": orgID, "entity_id": entityID, "track_id": trackID} {
		if err := validateSubjectToken(name, value); err != nil {
			return "", err
		}
	}
	return detectionPrefix + orgID + "." + entityID + ".detection." + trackID, nil
}

func ParseDetectionSubject(subject string) (orgID, entityID, trackID string, err error) {
	parts := strings.Split(subject, ".")
	if len(parts) != 7 || parts[0] != "constellation" || parts[1] != "events" || parts[2] != "isr" || parts[5] != "detection" {
		return "", "", "", fmt.Errorf("detection subject must be constellation.events.isr.{org_id}.{entity_id}.detection.{track_id}")
	}
	for name, value := range map[string]string{"org_id": parts[3], "entity_id": parts[4], "track_id": parts[6]} {
		if err := validateSubjectToken(name, value); err != nil {
			return "", "", "", err
		}
	}
	return parts[3], parts[4], parts[6], nil
}

func DecodeDetection(data []byte) (DetectionEnvelope, error) {
	var envelope DetectionEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("decode detection envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return envelope, fmt.Errorf("decode detection envelope: trailing JSON value")
		}
		return envelope, fmt.Errorf("decode detection envelope: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return envelope, err
	}
	return envelope, nil
}

func (e DetectionEnvelope) Validate() error {
	if e.SchemaVersion != DetectionSchemaVersion {
		return fmt.Errorf("unsupported detection schema_version %q", e.SchemaVersion)
	}
	for name, value := range map[string]string{"event_uid": e.EventUID, "org_id": e.OrgID, "entity_id": e.EntityID, "track_id": e.TrackID} {
		if err := validateSubjectToken(name, value); err != nil {
			return err
		}
	}
	if e.Label == "" || len(e.Label) > 128 {
		return fmt.Errorf("label must be 1-128 characters")
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	for name, value := range map[string]float64{"x1": e.X1, "y1": e.Y1, "x2": e.X2, "y2": e.Y2} {
		if value < 0 || value > 1 {
			return fmt.Errorf("%s must be between 0 and 1", name)
		}
	}
	if e.X2 < e.X1 || e.Y2 < e.Y1 {
		return fmt.Errorf("detection bounding box is inverted")
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	return nil
}
