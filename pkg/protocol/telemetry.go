// Package protocol defines the versioned Pulsar-to-Hub wire contract.
package protocol

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	TelemetrySchemaVersion = "1.0.0"
	telemetryPrefix        = "constellation.telemetry."
)

var (
	//go:embed testdata/telemetry-v1.schema.json
	TelemetryV1Schema []byte

	//go:embed testdata/telemetry-v1.golden.json
	TelemetryV1Golden []byte

	subjectToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	messageType  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// TelemetryEnvelope is the canonical v1 MAVLink telemetry payload. Identity is
// repeated in the body so the Hub can reject subject/payload attribution drift.
type TelemetryEnvelope struct {
	SchemaVersion string         `json:"schema_version"`
	MessageUID    string         `json:"message_uid"`
	OrgID         string         `json:"org_id"`
	EntityID      string         `json:"entity_id"`
	Source        string         `json:"source,omitempty"`
	MessageID     uint32         `json:"message_id"`
	MessageType   string         `json:"message_type"`
	SystemID      uint8          `json:"system_id"`
	ComponentID   uint8          `json:"component_id"`
	Data          map[string]any `json:"data"`
	Timestamp     time.Time      `json:"timestamp"`
}

// TelemetrySubject returns the exact organization/entity telemetry subject.
func TelemetrySubject(orgID, entityID string) (string, error) {
	if err := validateSubjectToken("org_id", orgID); err != nil {
		return "", err
	}
	if err := validateSubjectToken("entity_id", entityID); err != nil {
		return "", err
	}
	return telemetryPrefix + orgID + "." + entityID, nil
}

// ParseTelemetrySubject accepts only the canonical four-token subject.
func ParseTelemetrySubject(subject string) (orgID, entityID string, err error) {
	if !strings.HasPrefix(subject, telemetryPrefix) {
		return "", "", fmt.Errorf("telemetry subject has invalid prefix")
	}
	parts := strings.Split(subject, ".")
	if len(parts) != 4 {
		return "", "", fmt.Errorf("telemetry subject must be constellation.telemetry.{org_id}.{entity_id}")
	}
	if err := validateSubjectToken("org_id", parts[2]); err != nil {
		return "", "", err
	}
	if err := validateSubjectToken("entity_id", parts[3]); err != nil {
		return "", "", err
	}
	return parts[2], parts[3], nil
}

// DecodeTelemetry strictly decodes and validates one v1 envelope.
func DecodeTelemetry(data []byte) (TelemetryEnvelope, error) {
	var envelope TelemetryEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("decode telemetry envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return envelope, fmt.Errorf("decode telemetry envelope: trailing JSON value")
		}
		return envelope, fmt.Errorf("decode telemetry envelope: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return envelope, err
	}
	return envelope, nil
}

// Validate verifies the v1 identity and envelope invariants.
func (e TelemetryEnvelope) Validate() error {
	if e.SchemaVersion != TelemetrySchemaVersion {
		return fmt.Errorf("unsupported telemetry schema_version %q", e.SchemaVersion)
	}
	if err := validateSubjectToken("message_uid", e.MessageUID); err != nil {
		return err
	}
	if err := validateSubjectToken("org_id", e.OrgID); err != nil {
		return err
	}
	if err := validateSubjectToken("entity_id", e.EntityID); err != nil {
		return err
	}
	if !messageType.MatchString(e.MessageType) {
		return fmt.Errorf("message_type %q is not canonical MAVLink uppercase", e.MessageType)
	}
	if e.Data == nil {
		return fmt.Errorf("data is required")
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	return nil
}

func validateSubjectToken(name, value string) error {
	if len(value) == 0 || len(value) > 128 || !subjectToken.MatchString(value) {
		return fmt.Errorf("%s %q is not a safe NATS subject token", name, value)
	}
	return nil
}
