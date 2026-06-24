package services

import (
	"context"
	"path/filepath"
	"testing"

	dbpkg "github.com/Constellation-Overwatch/constellation-overwatch/db"
)

func TestAgentOpsRecordEventSummary(t *testing.T) {
	ctx := context.Background()
	database, err := dbpkg.New(&dbpkg.Config{
		DBPath:         filepath.Join(t.TempDir(), "constellation.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("new database: %v", err)
	}
	defer database.Close()

	if err := database.Start(ctx); err != nil {
		t.Fatalf("start database: %v", err)
	}

	service := NewAgentOpsService(database.GetDB(), nil)
	if err := service.RecordEvent(ctx, &AgentOpsEvent{
		EventType:  "session.started",
		NodeID:     "node-1",
		NodeLabel:  "Node One",
		NodeClass:  "edge",
		SessionID:  "session-1",
		AgentLabel: "codex-lead",
		Model:      "codex",
		Role:       "lead",
		Workspace:  "/tmp/work",
		Status:     AgentOpsStatusRunning,
		Payload: map[string]interface{}{
			"message": "started",
		},
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	summary, err := service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	if summary.NodeCount != 1 {
		t.Fatalf("NodeCount = %d, want 1", summary.NodeCount)
	}
	if summary.SessionCount != 1 {
		t.Fatalf("SessionCount = %d, want 1", summary.SessionCount)
	}
	if summary.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", summary.EventCount)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].NodeLabel != "Node One" {
		t.Fatalf("unexpected nodes: %#v", summary.Nodes)
	}
	if len(summary.Sessions) != 1 || summary.Sessions[0].Status != AgentOpsStatusRunning {
		t.Fatalf("unexpected sessions: %#v", summary.Sessions)
	}
	if len(summary.Surfaces) == 0 {
		t.Fatalf("expected native surface parity map")
	}

	launch, err := service.CreateLaunchRequest(ctx, CreateAgentOpsLaunchRequest{
		OrgID:        "default",
		RequestedBy:  "user-1",
		TeamName:     "alpha",
		Template:     "review",
		TargetNodeID: "node-1",
		Workspace:    "/tmp/work",
		Mission:      "review the native agent ops launch path",
		ModelRoute:   "mixed",
		AgentCount:   3,
	})
	if err != nil {
		t.Fatalf("create launch request: %v", err)
	}
	if launch.Status != AgentOpsLaunchStatusQueued {
		t.Fatalf("launch status = %q, want %q", launch.Status, AgentOpsLaunchStatusQueued)
	}
	if launch.CommandSubject != "constellation.commands.default.agentops.launch" {
		t.Fatalf("command subject = %q", launch.CommandSubject)
	}

	summary, err = service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after launch: %v", err)
	}
	if summary.LaunchRequestCount != 1 || summary.PendingLaunchCount != 1 {
		t.Fatalf("launch counts = %d/%d, want 1/1", summary.LaunchRequestCount, summary.PendingLaunchCount)
	}
	if len(summary.LaunchRequests) != 1 || summary.LaunchRequests[0].TeamName != "alpha" {
		t.Fatalf("unexpected launch requests: %#v", summary.LaunchRequests)
	}
	if summary.EventCount != 2 {
		t.Fatalf("EventCount after launch = %d, want 2", summary.EventCount)
	}

	toolCall, err := service.RecordToolCall(ctx, CreateAgentOpsToolCall{
		GlobalID:   "tool-event-1",
		NodeID:     "node-1",
		SessionID:  "session-1",
		TeamID:     "alpha",
		AgentLabel: "codex-lead",
		Tool:       "send_keys",
		Args: map[string]interface{}{
			"target": "alpha:0.0",
		},
		Result: "queued",
		Source: "mcp",
	})
	if err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	if toolCall.Tool != "send_keys" || toolCall.Target != "alpha:0.0" {
		t.Fatalf("unexpected tool call: %#v", toolCall)
	}

	summary, err = service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after tool call: %v", err)
	}
	if summary.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", summary.ToolCallCount)
	}
	if len(summary.ToolCalls) != 1 || summary.ToolCalls[0].GlobalID != "tool-event-1" {
		t.Fatalf("unexpected tool calls: %#v", summary.ToolCalls)
	}
	if summary.EventCount != 3 {
		t.Fatalf("EventCount after tool call = %d, want 3", summary.EventCount)
	}

	entry, err := service.RecordSessionEntry(ctx, CreateAgentOpsSessionEntry{
		GlobalID:   "session-entry-1",
		NodeID:     "node-1",
		SessionID:  "session-1",
		TeamID:     "alpha",
		AgentLabel: "codex-lead",
		Provider:   "codex",
		Role:       "assistant",
		Model:      "gpt-5",
		Workspace:  "/tmp/work",
		Source:     "conversation",
		SourcePath: "/home/user/.codex/sessions/session.jsonl",
		TurnID:     "turn-1",
		Sequence:   2,
		Message:    "Native autolog entry recorded for Agent Ops.",
	})
	if err != nil {
		t.Fatalf("record session entry: %v", err)
	}
	if entry.Provider != "codex" || entry.Role != "assistant" {
		t.Fatalf("unexpected session entry: %#v", entry)
	}

	summary, err = service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after session entry: %v", err)
	}
	if summary.SessionEntryCount != 1 {
		t.Fatalf("SessionEntryCount = %d, want 1", summary.SessionEntryCount)
	}
	if len(summary.SessionEntries) != 1 || summary.SessionEntries[0].GlobalID != "session-entry-1" {
		t.Fatalf("unexpected session entries: %#v", summary.SessionEntries)
	}
	if summary.EventCount != 4 {
		t.Fatalf("EventCount after session entry = %d, want 4", summary.EventCount)
	}
	if summary.Knowledge == nil || len(summary.Knowledge.HotTopics) == 0 {
		t.Fatalf("expected summary knowledge gradient, got %#v", summary.Knowledge)
	}

	knowledge, err := service.KnowledgeGradient(ctx, "autolog", 168, 5)
	if err != nil {
		t.Fatalf("knowledge gradient: %v", err)
	}
	if knowledge.TotalEvents == 0 {
		t.Fatalf("TotalEvents = 0, want recorded events")
	}
	if len(knowledge.Hits) == 0 || knowledge.Hits[0].EventID != "session-entry-1" {
		t.Fatalf("unexpected knowledge hits: %#v", knowledge.Hits)
	}
}
