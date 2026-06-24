package workers

import (
	"context"
	"path/filepath"
	"testing"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	dbpkg "github.com/Constellation-Overwatch/constellation-overwatch/db"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

func TestAgentOpsEventTypeFromSubject(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{shared.AgentOpsToolSubject("node-1", "send_keys"), apiservices.AgentOpsEventToolCall},
		{shared.AgentOpsSessionSubject("node-1", "codex"), apiservices.AgentOpsEventSessionEntry},
		{shared.AgentOpsEventSubject("node-1", "launch.completed"), "launch.completed"},
		{shared.AgentOpsEventSubject("node-1", "pane.observed"), "pane.observed"},
		{"constellation.events.system", "observed"},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			if got := eventTypeFromSubject(tt.subject); got != tt.want {
				t.Fatalf("eventTypeFromSubject(%q) = %q, want %q", tt.subject, got, tt.want)
			}
		})
	}
}

func TestAgentOpsWorkerRecordsNativeSubjectEnvelopes(t *testing.T) {
	ctx := context.Background()
	database := newWorkerAgentOpsTestDB(t)
	worker := &AgentOpsWorker{
		service: apiservices.NewAgentOpsService(database.GetDB(), nil),
	}

	if err := worker.handleAgentOpsEvent(&nats.Msg{
		Subject: shared.AgentOpsSessionSubject("node-1", "codex"),
		Data: []byte(`{
			"global_id": "session-entry-1",
			"session_id": "session-1",
			"team_id": "team-1",
			"agent_label": "codex-lead",
			"provider": "codex",
			"role": "assistant",
			"model": "gpt-5",
			"workspace": "/tmp/work",
			"message": "Native NATS transcript entry.",
			"observed_at": "2026-06-23T17:00:00Z"
		}`),
	}); err != nil {
		t.Fatalf("handle session entry: %v", err)
	}

	if err := worker.handleAgentOpsEvent(&nats.Msg{
		Subject: shared.AgentOpsToolSubject("node-1", "send_keys"),
		Data: []byte(`{
			"global_id": "tool-call-1",
			"session_id": "session-1",
			"team_id": "team-1",
			"agent_label": "codex-lead",
			"tool": "send_keys",
			"args": {"target": "team-1:0.0"},
			"result": "queued",
			"source": "mcp",
			"observed_at": "2026-06-23T17:01:00Z"
		}`),
	}); err != nil {
		t.Fatalf("handle tool call: %v", err)
	}

	summary, err := worker.service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.SessionEntryCount != 1 {
		t.Fatalf("SessionEntryCount = %d, want 1", summary.SessionEntryCount)
	}
	if summary.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", summary.ToolCallCount)
	}
	if len(summary.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(summary.Sessions))
	}
	session := summary.Sessions[0]
	if session.NodeID != "node-1" || session.Model != "gpt-5" || session.Workspace != "/tmp/work" {
		t.Fatalf("unexpected session projection: %#v", session)
	}
	if summary.SessionEntries[0].GlobalID != "session-entry-1" {
		t.Fatalf("unexpected session entry: %#v", summary.SessionEntries[0])
	}
	if summary.ToolCalls[0].GlobalID != "tool-call-1" || summary.ToolCalls[0].Target != "team-1:0.0" {
		t.Fatalf("unexpected tool call: %#v", summary.ToolCalls[0])
	}
}

func newWorkerAgentOpsTestDB(t *testing.T) *dbpkg.Service {
	t.Helper()
	database, err := dbpkg.New(&dbpkg.Config{
		DBPath:         filepath.Join(t.TempDir(), "constellation.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Start(context.Background()); err != nil {
		t.Fatalf("start database: %v", err)
	}
	return database
}
