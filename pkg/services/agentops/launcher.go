package agentops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
)

const (
	LaunchExecutorModeDryRun = "dry-run"
	LaunchExecutorModeTmux   = "tmux"
)

type LaunchExecutorConfig struct {
	Enabled        bool
	Mode           string
	Command        string
	NodeID         string
	NodeLabel      string
	NodeClass      string
	Shell          string
	CLIEnabled     bool
	CommandTimeout time.Duration
}

type LaunchExecutor struct {
	cfg     LaunchExecutorConfig
	runner  commandRunner
	service *apiservices.AgentOpsService
}

type launchCommand struct {
	Command      string `json:"command"`
	RequestID    string `json:"request_id"`
	OrgID        string `json:"org_id"`
	RequestedBy  string `json:"requested_by"`
	TeamName     string `json:"team_name"`
	Template     string `json:"template"`
	TargetNodeID string `json:"target_node_id"`
	Workspace    string `json:"workspace"`
	Mission      string `json:"mission"`
	ModelRoute   string `json:"model_route"`
	AgentCount   int    `json:"agent_count"`
	RequestedAt  string `json:"requested_at"`
}

type launchAgentPlan struct {
	Role   string `json:"role"`
	Model  string `json:"model"`
	Target string `json:"target"`
	Prompt string `json:"prompt,omitempty"`
}

type launchResult struct {
	TeamName   string            `json:"team_name"`
	Session    string            `json:"session"`
	Mode       string            `json:"mode"`
	CLIEnabled bool              `json:"cli_enabled"`
	Agents     []launchAgentPlan `json:"agents"`
}

func DefaultLaunchExecutorConfig() LaunchExecutorConfig {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}

	return LaunchExecutorConfig{
		Enabled:        shared.GetEnv("AGENTOPS_LAUNCH_EXECUTOR_ENABLED", "true") != "false",
		Mode:           fallbackLaunchExecutorValue(shared.GetEnv("AGENTOPS_LAUNCH_EXECUTOR_MODE", LaunchExecutorModeTmux), LaunchExecutorModeTmux),
		Command:        shared.GetEnv("AGENTOPS_MUX_COMMAND", "tmux"),
		NodeID:         shared.GetEnv("AGENTOPS_NODE_ID", hostname),
		NodeLabel:      shared.GetEnv("AGENTOPS_NODE_LABEL", hostname),
		NodeClass:      shared.GetEnv("AGENTOPS_NODE_CLASS", "workstation"),
		Shell:          shared.GetEnv("AGENTOPS_LAUNCH_SHELL", defaultShell()),
		CLIEnabled:     shared.GetEnv("AGENTOPS_LAUNCH_CLI_ENABLED", "false") == "true",
		CommandTimeout: parseDurationEnv("AGENTOPS_LAUNCH_COMMAND_TIMEOUT", 10*time.Second),
	}
}

func NewLaunchExecutor(db *sql.DB, nats *embeddednats.EmbeddedNATS, cfg LaunchExecutorConfig) *LaunchExecutor {
	cfg = normalizeLaunchExecutorConfig(cfg)
	return &LaunchExecutor{
		cfg:     cfg,
		runner:  execRunner{},
		service: apiservices.NewAgentOpsService(db, nats),
	}
}

func newLaunchExecutorWithRunner(db *sql.DB, nats *embeddednats.EmbeddedNATS, cfg LaunchExecutorConfig, runner commandRunner) *LaunchExecutor {
	executor := NewLaunchExecutor(db, nats, cfg)
	executor.runner = runner
	return executor
}

func (e *LaunchExecutor) HandleCommand(ctx context.Context, subject string, data []byte) (bool, error) {
	if !strings.Contains(subject, ".agentops.launch") {
		return false, nil
	}
	if !e.cfg.Enabled {
		logger.Infow("Agent Ops launch executor disabled", "subject", subject)
		return true, nil
	}

	cmd, err := decodeLaunchCommand(data)
	if err != nil {
		return true, err
	}
	if err := validateLaunchCommand(cmd); err != nil {
		return true, err
	}

	if err := e.service.SetLaunchRequestStatus(ctx, cmd.RequestID, apiservices.AgentOpsLaunchStatusAccepted, ""); err != nil {
		return true, err
	}
	_ = e.recordLaunchEvent(ctx, "launch.accepted", cmd, map[string]interface{}{
		"mode":        e.cfg.Mode,
		"cli_enabled": e.cfg.CLIEnabled,
	})

	result, err := e.execute(ctx, cmd)
	if err != nil {
		_ = e.service.SetLaunchRequestStatus(ctx, cmd.RequestID, apiservices.AgentOpsLaunchStatusFailed, err.Error())
		_ = e.recordLaunchEvent(ctx, "launch.failed", cmd, map[string]interface{}{
			"error": err.Error(),
			"mode":  e.cfg.Mode,
		})
		return true, err
	}

	if err := e.service.SetLaunchRequestStatus(ctx, cmd.RequestID, apiservices.AgentOpsLaunchStatusCompleted, ""); err != nil {
		return true, err
	}
	_ = e.recordLaunchEvent(ctx, "launch.completed", cmd, map[string]interface{}{
		"mode":        result.Mode,
		"session":     result.Session,
		"agents":      result.Agents,
		"cli_enabled": result.CLIEnabled,
	})

	return true, nil
}

func (e *LaunchExecutor) execute(ctx context.Context, cmd launchCommand) (launchResult, error) {
	sessionName := sanitizeTmuxSessionName(cmd.TeamName)
	if sessionName == "" {
		return launchResult{}, fmt.Errorf("team_name does not contain a usable tmux session token")
	}

	plans := buildLaunchPlan(cmd, sessionName)
	result := launchResult{
		TeamName:   cmd.TeamName,
		Session:    sessionName,
		Mode:       e.cfg.Mode,
		CLIEnabled: e.cfg.CLIEnabled,
		Agents:     plans,
	}

	switch e.cfg.Mode {
	case LaunchExecutorModeDryRun:
		return result, nil
	case LaunchExecutorModeTmux:
		if err := e.service.SetLaunchRequestStatus(ctx, cmd.RequestID, apiservices.AgentOpsLaunchStatusRunning, ""); err != nil {
			return result, err
		}
		if err := e.launchTmux(ctx, cmd, sessionName, plans); err != nil {
			return result, err
		}
		return result, nil
	default:
		return result, fmt.Errorf("unsupported launch executor mode %q", e.cfg.Mode)
	}
}

func (e *LaunchExecutor) launchTmux(ctx context.Context, cmd launchCommand, sessionName string, plans []launchAgentPlan) error {
	if len(plans) == 0 {
		return fmt.Errorf("launch plan is empty")
	}

	if _, err := e.run(ctx, "new-session", "-d", "-s", sessionName, "-n", "team", e.cfg.Shell); err != nil {
		return fmt.Errorf("create tmux session %q: %w", sessionName, err)
	}

	targets := []string{fmt.Sprintf("%s:team.0", sessionName)}
	for i := 1; i < len(plans); i++ {
		out, err := e.run(ctx, "split-window", "-d", "-t", targets[i-1], "-P", "-F", "#{pane_index}", e.cfg.Shell)
		if err != nil {
			return fmt.Errorf("split pane %d: %w", i, err)
		}
		targets = append(targets, fmt.Sprintf("%s:team.%s", sessionName, strings.TrimSpace(out)))
	}
	if len(targets) > 1 {
		if _, err := e.run(ctx, "select-layout", "-t", sessionName+":team", "tiled"); err != nil {
			logger.Warnw("Failed to select Agent Ops launch layout", "team", cmd.TeamName, "error", err)
		}
	}

	teamID := uuid.NewString()
	for i := range plans {
		plans[i].Target = targets[i]
		if err := e.configurePane(ctx, cmd, teamID, plans[i]); err != nil {
			return err
		}
	}

	return nil
}

func (e *LaunchExecutor) configurePane(ctx context.Context, cmd launchCommand, teamID string, plan launchAgentPlan) error {
	title := strings.TrimSpace(plan.Role + " (" + plan.Model + ")")
	if _, err := e.run(ctx, "select-pane", "-t", plan.Target, "-T", title); err != nil {
		return fmt.Errorf("set pane title for %s: %w", plan.Target, err)
	}

	options := map[string]string{
		"@ao_team_id":    teamID,
		"@ao_role":       plan.Role,
		"@ao_model":      plan.Model,
		"@ao_launch_req": cmd.RequestID,
	}
	for key, value := range options {
		if _, err := e.run(ctx, "set-option", "-p", "-t", plan.Target, key, value); err != nil {
			return fmt.Errorf("set %s for %s: %w", key, plan.Target, err)
		}
	}

	if cmd.Workspace != "" {
		if _, err := e.sendKeys(ctx, plan.Target, "cd "+strconv.Quote(cmd.Workspace), true); err != nil {
			return fmt.Errorf("set workdir for %s: %w", plan.Target, err)
		}
	}

	if !e.cfg.CLIEnabled {
		return nil
	}

	cliCommand := cliCommandForModel(plan.Model)
	if cliCommand == "" {
		return nil
	}
	if _, err := e.sendKeys(ctx, plan.Target, cliCommand, true); err != nil {
		return fmt.Errorf("start %s in %s: %w", plan.Model, plan.Target, err)
	}
	if plan.Prompt != "" {
		if _, err := e.sendKeys(ctx, plan.Target, plan.Prompt, true); err != nil {
			return fmt.Errorf("send launch prompt to %s: %w", plan.Target, err)
		}
	}
	return nil
}

func (e *LaunchExecutor) sendKeys(ctx context.Context, target, message string, enter bool) (string, error) {
	args := []string{"send-keys", "-t", target, message}
	if enter {
		args = append(args, "Enter")
	}
	return e.run(ctx, args...)
}

func (e *LaunchExecutor) run(ctx context.Context, args ...string) (string, error) {
	timeout := e.cfg.CommandTimeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return e.runner.Run(timeoutCtx, e.cfg.Command, args...)
}

func (e *LaunchExecutor) recordLaunchEvent(ctx context.Context, eventType string, cmd launchCommand, extra map[string]interface{}) error {
	payload := map[string]interface{}{
		"request_id":     cmd.RequestID,
		"org_id":         cmd.OrgID,
		"requested_by":   cmd.RequestedBy,
		"team_name":      cmd.TeamName,
		"template":       cmd.Template,
		"target_node_id": cmd.TargetNodeID,
		"workspace":      cmd.Workspace,
		"mission":        cmd.Mission,
		"model_route":    cmd.ModelRoute,
		"agent_count":    cmd.AgentCount,
	}
	for key, value := range extra {
		payload[key] = value
	}

	return e.service.PublishEvent(ctx, &apiservices.AgentOpsEvent{
		EventType:  eventType,
		NodeID:     e.cfg.NodeID,
		NodeLabel:  e.cfg.NodeLabel,
		NodeClass:  e.cfg.NodeClass,
		MachineID:  e.cfg.NodeLabel,
		Status:     apiservices.AgentOpsStatusActive,
		Severity:   "info",
		Subject:    shared.AgentOpsEventSubject(e.cfg.NodeID, eventType),
		Payload:    payload,
		ObservedAt: time.Now().UTC(),
	})
}

func decodeLaunchCommand(data []byte) (launchCommand, error) {
	var cmd launchCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return cmd, fmt.Errorf("decode agent launch command: %w", err)
	}
	return cmd, nil
}

func validateLaunchCommand(cmd launchCommand) error {
	if strings.TrimSpace(cmd.RequestID) == "" {
		return fmt.Errorf("request_id is required")
	}
	if strings.TrimSpace(cmd.TeamName) == "" {
		return fmt.Errorf("team_name is required")
	}
	if strings.TrimSpace(cmd.Mission) == "" {
		return fmt.Errorf("mission is required")
	}
	return nil
}

func buildLaunchPlan(cmd launchCommand, sessionName string) []launchAgentPlan {
	count := cmd.AgentCount
	if count <= 0 {
		count = 1
	}
	if count > 24 {
		count = 24
	}

	models := modelsForRoute(cmd.ModelRoute, count)
	plans := make([]launchAgentPlan, 0, count)
	for i := 0; i < count; i++ {
		role := launchRole(i, count)
		target := fmt.Sprintf("%s:team.%d", sessionName, i)
		plans = append(plans, launchAgentPlan{
			Role:   role,
			Model:  models[i],
			Target: target,
			Prompt: buildNativeLaunchPrompt(cmd, role, target),
		})
	}
	return plans
}

func modelsForRoute(route string, count int) []string {
	route = strings.ToLower(strings.TrimSpace(route))
	if route == "" || route == "default" {
		route = "codex"
	}

	models := make([]string, count)
	if route == "mixed" {
		cycle := []string{"codex", "claude", "gemini"}
		for i := range models {
			models[i] = cycle[i%len(cycle)]
		}
		return models
	}

	for i := range models {
		models[i] = normalizeLaunchModel(route)
	}
	return models
}

func normalizeLaunchModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "claude", "gemini", "codex", "agy", "antigravity":
		return model
	default:
		return "codex"
	}
}

func launchRole(index, count int) string {
	if index == 0 {
		return "lead"
	}
	if count >= 3 && index == count-1 {
		return "adversary"
	}
	return fmt.Sprintf("worker-%d", index)
}

func buildNativeLaunchPrompt(cmd launchCommand, role, target string) string {
	var b strings.Builder
	b.WriteString("You are ")
	b.WriteString(role)
	b.WriteString(" in Constellation Agent Ops pane ")
	b.WriteString(target)
	b.WriteString(". Mission: ")
	b.WriteString(strings.TrimSpace(cmd.Mission))
	if cmd.Workspace != "" {
		b.WriteString("\nWorkspace: ")
		b.WriteString(cmd.Workspace)
	}
	b.WriteString("\nCoordinate through the Agent Ops surface and write concrete progress into the active workspace.")
	return b.String()
}

func cliCommandForModel(model string) string {
	switch normalizeLaunchModel(model) {
	case "claude":
		return "unset CLAUDECODE && claude --dangerously-skip-permissions"
	case "gemini":
		return "gemini -y --sandbox=none"
	case "agy", "antigravity":
		return "agy --dangerously-skip-permissions"
	case "codex":
		return "codex --dangerously-bypass-approvals-and-sandbox"
	default:
		return ""
	}
}

func sanitizeTmuxSessionName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "bash"
}

func normalizeLaunchExecutorConfig(cfg LaunchExecutorConfig) LaunchExecutorConfig {
	defaults := DefaultLaunchExecutorConfig()
	if cfg.Mode == "" {
		cfg.Mode = defaults.Mode
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Command == "" {
		cfg.Command = defaults.Command
	}
	if cfg.NodeID == "" {
		cfg.NodeID = defaults.NodeID
	}
	if cfg.NodeLabel == "" {
		cfg.NodeLabel = defaults.NodeLabel
	}
	if cfg.NodeClass == "" {
		cfg.NodeClass = defaults.NodeClass
	}
	if cfg.Shell == "" {
		cfg.Shell = defaults.Shell
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = defaults.CommandTimeout
	}
	return cfg
}

func fallbackLaunchExecutorValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
