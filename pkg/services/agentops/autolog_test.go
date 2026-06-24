package agentops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutologObserverBackfillRecordsCodexSessionEntry(t *testing.T) {
	ctx := context.Background()
	database := newAgentOpsTestDB(t)
	root := filepath.Join(t.TempDir(), ".codex", "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	path := filepath.Join(root, "session.jsonl")
	data := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"codex-session-1","cwd":"/tmp/work","model":"gpt-5"}}`,
		`{"timestamp":"2026-06-23T17:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Autolog observer captured Codex output."}]}}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	observer := NewAutologObserver(database.GetDB(), nil, AutologConfig{
		Enabled:      true,
		NodeID:       "node-test",
		PollInterval: time.Hour,
		Roots:        []string{root},
		Backfill:     true,
	})
	observer.poll(ctx)

	summary, err := observer.service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.SessionEntryCount != 1 {
		t.Fatalf("SessionEntryCount = %d, want 1", summary.SessionEntryCount)
	}
	entry := summary.SessionEntries[0]
	if entry.Provider != "codex" || entry.SessionID != "codex-session-1" {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	if !strings.Contains(entry.Message, "Autolog observer captured") {
		t.Fatalf("message = %q", entry.Message)
	}
}

func TestAutologObserverTailsNewLinesOnly(t *testing.T) {
	ctx := context.Background()
	database := newAgentOpsTestDB(t)
	root := filepath.Join(t.TempDir(), ".codex", "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"codex-session-2","cwd":"/tmp/work","model":"gpt-5"}}`+"\n"), 0600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	observer := NewAutologObserver(database.GetDB(), nil, AutologConfig{
		Enabled:      true,
		NodeID:       "node-test",
		PollInterval: time.Hour,
		Roots:        []string{root},
		Backfill:     false,
	})
	observer.poll(ctx)
	if summary, err := observer.service.Summary(ctx); err != nil || summary.SessionEntryCount != 0 {
		t.Fatalf("initial summary = %#v err=%v, want no entries", summary, err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	_, err = file.WriteString(`{"timestamp":"2026-06-23T17:01:00Z","type":"event_msg","payload":{"type":"assistant_message","message":"Tail observer captured a new Codex line."}}` + "\n")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	observer.poll(ctx)
	summary, err := observer.service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.SessionEntryCount != 1 {
		t.Fatalf("SessionEntryCount = %d, want 1", summary.SessionEntryCount)
	}
	if !strings.Contains(summary.SessionEntries[0].Message, "Tail observer captured") {
		t.Fatalf("unexpected entry: %#v", summary.SessionEntries[0])
	}
}
