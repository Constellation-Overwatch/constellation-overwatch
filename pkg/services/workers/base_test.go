package workers

import "testing"

func TestQuarantineSubjectToken(t *testing.T) {
	tests := map[string]string{
		"TelemetryWorker": "telemetryworker",
		"event worker/v1": "event-worker-v1",
		"":                "worker",
	}
	for input, want := range tests {
		if got := quarantineSubjectToken(input); got != want {
			t.Fatalf("quarantineSubjectToken(%q) = %q, want %q", input, got, want)
		}
	}
}
