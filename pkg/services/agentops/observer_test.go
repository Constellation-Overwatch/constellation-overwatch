package agentops

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Constellation-Overwatch/constellation-overwatch/db"
)

type fakeRunner struct {
	responses map[string]string
}

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case len(args) > 0 && args[0] == "list-panes":
		return f.responses["list-panes"], nil
	case strings.HasPrefix(joined, "show-options -p -t ops:0.1 -v @ao_role"):
		return "lead\n", nil
	case strings.HasPrefix(joined, "show-options -p -t ops:0.1 -v @ao_model"):
		return "codex\n", nil
	case strings.HasPrefix(joined, "show-options -p -t ops:0.1 -v @ao_team_id"):
		return "team-ops\n", nil
	case strings.HasPrefix(joined, "capture-pane -t ops:0.1"):
		return "$ codex\nOpenAI Codex ready\nWorking on native agent ops\n", nil
	default:
		return "", nil
	}
}

func TestObserverPollRecordsAgentOpsSummary(t *testing.T) {
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

	runner := fakeRunner{responses: map[string]string{
		"list-panes": "ops\t0\t1\tmain\t%2\t1234\tcodex\t0\t1\tlead codex\t/tmp/work\n",
	}}
	observer := newObserverWithRunner(database.GetDB(), nil, ObserverConfig{
		Enabled:      true,
		Command:      "tmux",
		NodeID:       "node-test",
		NodeLabel:    "Node Test",
		NodeClass:    "workstation",
		PollInterval: time.Hour,
		CaptureLines: 20,
	}, runner)

	observer.poll(ctx)

	summary, err := observer.service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.NodeCount != 1 {
		t.Fatalf("NodeCount = %d, want 1", summary.NodeCount)
	}
	if summary.SessionCount != 1 {
		t.Fatalf("SessionCount = %d, want 1", summary.SessionCount)
	}
	if summary.EventCount != 2 {
		t.Fatalf("EventCount = %d, want 2", summary.EventCount)
	}

	session := summary.Sessions[0]
	if session.SessionID != "ops:0.1" {
		t.Fatalf("SessionID = %q, want ops:0.1", session.SessionID)
	}
	if session.Model != "codex" {
		t.Fatalf("Model = %q, want codex", session.Model)
	}
	if session.Role != "lead" {
		t.Fatalf("Role = %q, want lead", session.Role)
	}
	if session.Workspace != "/tmp/work" {
		t.Fatalf("Workspace = %q, want /tmp/work", session.Workspace)
	}
	if session.LastOutput != "Working on native agent ops" {
		t.Fatalf("LastOutput = %q", session.LastOutput)
	}
}
