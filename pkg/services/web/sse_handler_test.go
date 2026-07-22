package web

import "testing"

func TestSubjectAllowedForOrganization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		orgID   string
		want    bool
	}{
		{name: "admin sees canonical subject", subject: "constellation.telemetry.org-b.entity-1", want: true},
		{name: "own telemetry", subject: "constellation.telemetry.org-a.entity-1", orgID: "org-a", want: true},
		{name: "cross org telemetry", subject: "constellation.telemetry.org-b.entity-1", orgID: "org-a"},
		{name: "own entity event", subject: "constellation.entities.org-a.created", orgID: "org-a", want: true},
		{name: "own category event", subject: "constellation.events.isr.org-a.entity-1.detection.track-1", orgID: "org-a", want: true},
		{name: "cross org category event", subject: "constellation.events.isr.org-b.entity-1.detection.track-1", orgID: "org-a"},
		{name: "unknown layout fails closed", subject: "constellation.audit.org-a.record", orgID: "org-a"},
		{name: "malformed fails closed", subject: "constellation.telemetry", orgID: "org-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := subjectAllowedForOrganization(tt.subject, tt.orgID); got != tt.want {
				t.Fatalf("subjectAllowedForOrganization(%q, %q) = %v, want %v", tt.subject, tt.orgID, got, tt.want)
			}
		})
	}
}
