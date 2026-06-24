package agentops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	dbpkg "github.com/Constellation-Overwatch/constellation-overwatch/db"
)

type recordingRunner struct {
	calls []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if len(args) > 0 && args[0] == "split-window" {
		return "1\n", nil
	}
	return "", nil
}

func TestLaunchExecutorDryRunCompletesRequest(t *testing.T) {
	ctx := context.Background()
	database := newAgentOpsTestDB(t)

	service := apiservices.NewAgentOpsService(database.GetDB(), nil)
	launch, err := service.CreateLaunchRequest(ctx, apiservices.CreateAgentOpsLaunchRequest{
		OrgID:      "default",
		TeamName:   "alpha team",
		Mission:    "prove native launch lifecycle",
		ModelRoute: "mixed",
		AgentCount: 2,
	})
	if err != nil {
		t.Fatalf("create launch request: %v", err)
	}

	payload := launchCommandPayload(t, launch)
	runner := &recordingRunner{}
	executor := newLaunchExecutorWithRunner(database.GetDB(), nil, LaunchExecutorConfig{
		Enabled:        true,
		Mode:           LaunchExecutorModeDryRun,
		Command:        "tmux",
		NodeID:         "node-test",
		NodeLabel:      "Node Test",
		NodeClass:      "workstation",
		Shell:          "bash",
		CommandTimeout: time.Second,
	}, runner)

	handled, err := executor.HandleCommand(ctx, launch.CommandSubject, payload)
	if err != nil {
		t.Fatalf("handle launch command: %v", err)
	}
	if !handled {
		t.Fatalf("expected launch command to be handled")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry-run called tmux: %#v", runner.calls)
	}

	summary, err := service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.LaunchRequests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(summary.LaunchRequests))
	}
	if summary.LaunchRequests[0].Status != apiservices.AgentOpsLaunchStatusCompleted {
		t.Fatalf("status = %q, want completed", summary.LaunchRequests[0].Status)
	}
	if summary.EventCount < 3 {
		t.Fatalf("EventCount = %d, want launch lifecycle events", summary.EventCount)
	}
}

func TestLaunchExecutorTmuxCreatesPanesAndMetadata(t *testing.T) {
	ctx := context.Background()
	database := newAgentOpsTestDB(t)

	service := apiservices.NewAgentOpsService(database.GetDB(), nil)
	launch, err := service.CreateLaunchRequest(ctx, apiservices.CreateAgentOpsLaunchRequest{
		OrgID:      "default",
		TeamName:   "beta team",
		Workspace:  "/tmp/work",
		Mission:    "create native panes",
		ModelRoute: "codex",
		AgentCount: 2,
	})
	if err != nil {
		t.Fatalf("create launch request: %v", err)
	}

	runner := &recordingRunner{}
	executor := newLaunchExecutorWithRunner(database.GetDB(), nil, LaunchExecutorConfig{
		Enabled:        true,
		Mode:           LaunchExecutorModeTmux,
		Command:        "tmux",
		NodeID:         "node-test",
		NodeLabel:      "Node Test",
		NodeClass:      "workstation",
		Shell:          "bash",
		CLIEnabled:     false,
		CommandTimeout: time.Second,
	}, runner)

	handled, err := executor.HandleCommand(ctx, launch.CommandSubject, launchCommandPayload(t, launch))
	if err != nil {
		t.Fatalf("handle launch command: %v", err)
	}
	if !handled {
		t.Fatalf("expected launch command to be handled")
	}

	got := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"tmux new-session -d -s beta-team -n team bash",
		"tmux split-window -d -t beta-team:team.0 -P -F #{pane_index} bash",
		"tmux set-option -p -t beta-team:team.0 @ao_role lead",
		"tmux set-option -p -t beta-team:team.1 @ao_role worker-1",
		"tmux set-option -p -t beta-team:team.0 @ao_model codex",
		"tmux send-keys -t beta-team:team.0 cd \"/tmp/work\" Enter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing tmux call %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "codex --dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("CLI command should not be sent when CLIEnabled=false:\n%s", got)
	}

	summary, err := service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.LaunchRequests[0].Status != apiservices.AgentOpsLaunchStatusCompleted {
		t.Fatalf("status = %q, want completed", summary.LaunchRequests[0].Status)
	}
}

func TestLaunchExecutorMarksFailedOnTmuxError(t *testing.T) {
	ctx := context.Background()
	database := newAgentOpsTestDB(t)

	service := apiservices.NewAgentOpsService(database.GetDB(), nil)
	launch, err := service.CreateLaunchRequest(ctx, apiservices.CreateAgentOpsLaunchRequest{
		OrgID:      "default",
		TeamName:   "gamma team",
		Mission:    "fail launch",
		ModelRoute: "codex",
		AgentCount: 1,
	})
	if err != nil {
		t.Fatalf("create launch request: %v", err)
	}

	executor := newLaunchExecutorWithRunner(database.GetDB(), nil, LaunchExecutorConfig{
		Enabled:        true,
		Mode:           LaunchExecutorModeTmux,
		Command:        "tmux",
		NodeID:         "node-test",
		NodeLabel:      "Node Test",
		NodeClass:      "workstation",
		Shell:          "bash",
		CommandTimeout: time.Second,
	}, failingRunner{})

	handled, err := executor.HandleCommand(ctx, launch.CommandSubject, launchCommandPayload(t, launch))
	if err == nil {
		t.Fatalf("expected launch error")
	}
	if !handled {
		t.Fatalf("expected launch command to be handled")
	}

	summary, err := service.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.LaunchRequests[0].Status != apiservices.AgentOpsLaunchStatusFailed {
		t.Fatalf("status = %q, want failed", summary.LaunchRequests[0].Status)
	}
	if summary.LaunchRequests[0].Error == "" {
		t.Fatalf("expected stored launch error")
	}
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, string, ...string) (string, error) {
	return "", errors.New("tmux unavailable")
}

func newAgentOpsTestDB(t *testing.T) *dbpkg.Service {
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

func launchCommandPayload(t *testing.T, launch *apiservices.AgentOpsLaunchRequest) []byte {
	t.Helper()
	if launch.CommandPayload != "" {
		return []byte(launch.CommandPayload)
	}
	data, err := json.Marshal(launchCommand{
		Command:    "agentops.launch",
		RequestID:  launch.RequestID,
		OrgID:      launch.OrgID,
		TeamName:   launch.TeamName,
		Mission:    launch.Mission,
		ModelRoute: launch.ModelRoute,
		AgentCount: launch.AgentCount,
	})
	if err != nil {
		t.Fatalf("marshal launch command: %v", err)
	}
	return data
}
