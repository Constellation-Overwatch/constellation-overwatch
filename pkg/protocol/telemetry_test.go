package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGoldenTelemetryV1(t *testing.T) {
	envelope, err := DecodeTelemetry(TelemetryV1Golden)
	if err != nil {
		t.Fatalf("DecodeTelemetry(golden) error = %v", err)
	}
	if envelope.MessageType != "GLOBAL_POSITION_INT" || envelope.OrgID != "org-galaxy" || envelope.EntityID != "gc35-e4b" {
		t.Fatalf("decoded golden identity = %#v", envelope)
	}
	subject, err := TelemetrySubject(envelope.OrgID, envelope.EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "constellation.telemetry.org-galaxy.gc35-e4b" {
		t.Fatalf("subject = %q", subject)
	}
	var schema map[string]any
	if err := json.Unmarshal(TelemetryV1Schema, &schema); err != nil {
		t.Fatalf("schema JSON invalid: %v", err)
	}
}

func TestTelemetryContractFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "unknown version", payload: strings.Replace(string(TelemetryV1Golden), `"1.0.0"`, `"2.0.0"`, 1), want: "unsupported"},
		{name: "unknown field", payload: strings.Replace(string(TelemetryV1Golden), `"source":`, `"unexpected": true, "source":`, 1), want: "unknown field"},
		{name: "unsafe entity", payload: strings.Replace(string(TelemetryV1Golden), `"gc35-e4b"`, `"gc35.>"`, 1), want: "safe NATS"},
		{name: "unsafe message UID", payload: strings.Replace(string(TelemetryV1Golden), `"018f0f10-7b4c-7a8d-9000-000000000001"`, `"bad\r\nheader"`, 1), want: "safe NATS"},
		{name: "legacy envelope", payload: `{"msg_id":33,"msg_name":"GlobalPositionInt","fields":{}}`, want: "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeTelemetry([]byte(tt.payload))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseTelemetrySubjectRejectsExtendedOrUnsafeSubjects(t *testing.T) {
	for _, subject := range []string{
		"constellation.telemetry",
		"constellation.telemetry.category.org.entity",
		"constellation.telemetry.org.entity.extra",
		"constellation.telemetry.org.>",
	} {
		if _, _, err := ParseTelemetrySubject(subject); err == nil {
			t.Fatalf("ParseTelemetrySubject(%q) unexpectedly succeeded", subject)
		}
	}
}
