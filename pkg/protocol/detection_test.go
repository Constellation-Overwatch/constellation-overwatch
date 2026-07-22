package protocol

import (
	"strings"
	"testing"
)

func TestDetectionContract(t *testing.T) {
	payload := `{"schema_version":"1.0.0","event_uid":"event-1","org_id":"org-galaxy","entity_id":"gc35-e4b","track_id":"track-1","label":"airplane","confidence":0.9,"x1":0.1,"y1":0.2,"x2":0.3,"y2":0.4,"timestamp":"2026-07-21T20:00:00Z"}`
	envelope, err := DecodeDetection([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := DetectionSubject(envelope.OrgID, envelope.EntityID, envelope.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "constellation.events.isr.org-galaxy.gc35-e4b.detection.track-1" {
		t.Fatalf("subject = %q", subject)
	}
	if _, _, _, err := ParseDetectionSubject(subject + ".extra"); err == nil {
		t.Fatal("extended detection subject accepted")
	}
	if _, err := DecodeDetection([]byte(strings.Replace(payload, `"confidence":0.9`, `"confidence":2`, 1))); err == nil {
		t.Fatal("invalid confidence accepted")
	}
}
