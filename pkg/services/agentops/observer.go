package agentops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

const (
	defaultPollInterval = 15 * time.Second
	defaultCaptureLines = 40
	commandTimeout      = 3 * time.Second
)

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

type ObserverConfig struct {
	Enabled      bool
	Command      string
	NodeID       string
	NodeLabel    string
	NodeClass    string
	PollInterval time.Duration
	CaptureLines int
}

type Observer struct {
	cfg     ObserverConfig
	runner  commandRunner
	service *apiservices.AgentOpsService

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type observedPane struct {
	Target         string
	SessionName    string
	WindowIndex    string
	PaneIndex      string
	WindowName     string
	PaneID         string
	PanePID        string
	CurrentCommand string
	PaneDead       bool
	PaneActive     bool
	PaneTitle      string
	Workspace      string
	Role           string
	Model          string
	TeamID         string
	LastOutput     string
}

func DefaultObserverConfig() ObserverConfig {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}

	return ObserverConfig{
		Enabled:      shared.GetEnv("AGENTOPS_OBSERVER_ENABLED", "true") != "false",
		Command:      shared.GetEnv("AGENTOPS_MUX_COMMAND", "tmux"),
		NodeID:       shared.GetEnv("AGENTOPS_NODE_ID", hostname),
		NodeLabel:    shared.GetEnv("AGENTOPS_NODE_LABEL", hostname),
		NodeClass:    shared.GetEnv("AGENTOPS_NODE_CLASS", "workstation"),
		PollInterval: parseDurationEnv("AGENTOPS_OBSERVER_INTERVAL", defaultPollInterval),
		CaptureLines: parseIntEnv("AGENTOPS_CAPTURE_LINES", defaultCaptureLines),
	}
}

func NewObserver(db *sql.DB, nats *embeddednats.EmbeddedNATS, cfg ObserverConfig) *Observer {
	cfg = normalizeConfig(cfg)
	return &Observer{
		cfg:     cfg,
		runner:  execRunner{},
		service: apiservices.NewAgentOpsService(db, nats),
	}
}

func newObserverWithRunner(db *sql.DB, nats *embeddednats.EmbeddedNATS, cfg ObserverConfig, runner commandRunner) *Observer {
	observer := NewObserver(db, nats, cfg)
	observer.runner = runner
	return observer
}

func (o *Observer) Name() string {
	return "agentops-observer"
}

func (o *Observer) HealthCheck() error {
	if !o.cfg.Enabled {
		return nil
	}
	if o.cfg.Command == "" {
		return fmt.Errorf("agent ops observer command is empty")
	}
	return nil
}

func (o *Observer) Start(ctx context.Context) error {
	if !o.cfg.Enabled {
		logger.Info("Agent Ops observer disabled")
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel

	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.loop(runCtx)
	}()

	return nil
}

func (o *Observer) Stop(ctx context.Context) error {
	if o.cancel != nil {
		o.cancel()
	}

	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Observer) loop(ctx context.Context) {
	ticker := time.NewTicker(o.cfg.PollInterval)
	defer ticker.Stop()

	o.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.poll(ctx)
		}
	}
}

func (o *Observer) poll(ctx context.Context) {
	events, err := o.collect(ctx)
	if err != nil {
		if isMuxUnavailable(err) {
			logger.Debugw("Agent Ops observer mux unavailable", "command", o.cfg.Command, "error", err)
			return
		}
		logger.Warnw("Agent Ops observer poll failed", "command", o.cfg.Command, "error", err)
		return
	}

	for i := range events {
		if err := o.service.PublishEvent(ctx, &events[i]); err != nil {
			logger.Warnw("Agent Ops observer failed to publish event", "event_type", events[i].EventType, "error", err)
		}
	}
}

func (o *Observer) collect(ctx context.Context) ([]apiservices.AgentOpsEvent, error) {
	panes, err := o.listPanes(ctx)
	if err != nil {
		return nil, err
	}

	events := make([]apiservices.AgentOpsEvent, 0, len(panes)+1)
	now := time.Now().UTC()
	nodePayload := map[string]interface{}{
		"observer":      "tmux-compatible",
		"mux_command":   o.cfg.Command,
		"pane_count":    len(panes),
		"capture_lines": o.cfg.CaptureLines,
	}
	events = append(events, apiservices.AgentOpsEvent{
		EventType:  "node.observed",
		NodeID:     o.cfg.NodeID,
		NodeLabel:  o.cfg.NodeLabel,
		NodeClass:  o.cfg.NodeClass,
		MachineID:  o.cfg.NodeLabel,
		Status:     apiservices.AgentOpsStatusActive,
		Payload:    nodePayload,
		ObservedAt: now,
	})

	for _, pane := range panes {
		role, model := inferRoleModel(pane)
		if pane.Role != "" {
			role = pane.Role
		}
		if pane.Model != "" {
			model = pane.Model
		}

		status := apiservices.AgentOpsStatusRunning
		if pane.PaneDead {
			status = apiservices.AgentOpsStatusStopped
		} else if isShellCommand(pane.CurrentCommand) {
			status = apiservices.AgentOpsStatusIdle
		}

		payload := map[string]interface{}{
			"target":          pane.Target,
			"session_name":    pane.SessionName,
			"window_index":    pane.WindowIndex,
			"pane_index":      pane.PaneIndex,
			"window_name":     pane.WindowName,
			"pane_id":         pane.PaneID,
			"pane_pid":        pane.PanePID,
			"current_command": pane.CurrentCommand,
			"pane_dead":       pane.PaneDead,
			"pane_active":     pane.PaneActive,
			"pane_title":      pane.PaneTitle,
			"last_output":     pane.LastOutput,
		}

		events = append(events, apiservices.AgentOpsEvent{
			EventType:  "pane.observed",
			NodeID:     o.cfg.NodeID,
			NodeLabel:  o.cfg.NodeLabel,
			NodeClass:  o.cfg.NodeClass,
			MachineID:  o.cfg.NodeLabel,
			SessionID:  pane.Target,
			TeamID:     firstNonEmpty(pane.TeamID, pane.SessionName),
			AgentLabel: firstNonEmpty(role, pane.PaneTitle, pane.Target),
			Model:      model,
			Role:       role,
			Workspace:  pane.Workspace,
			PaneID:     pane.PaneID,
			Status:     status,
			Payload:    payload,
			ObservedAt: now,
		})
	}

	return events, nil
}

func (o *Observer) listPanes(ctx context.Context) ([]observedPane, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{window_index}",
		"#{pane_index}",
		"#{window_name}",
		"#{pane_id}",
		"#{pane_pid}",
		"#{pane_current_command}",
		"#{pane_dead}",
		"#{pane_active}",
		"#{pane_title}",
		"#{pane_current_path}",
	}, "\t")

	out, err := o.run(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		fallbackFormat := strings.Join([]string{
			"#{session_name}",
			"#{window_index}",
			"#{pane_index}",
			"#{window_name}",
			"#{pane_id}",
			"#{pane_pid}",
			"#{pane_current_command}",
			"#{pane_dead}",
			"#{pane_active}",
			"#{pane_title}",
		}, "\t")
		out, err = o.run(ctx, "list-panes", "-a", "-F", fallbackFormat)
		if err != nil {
			return nil, err
		}
	}

	var panes []observedPane
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 10 {
			continue
		}

		pane := observedPane{
			SessionName:    parts[0],
			WindowIndex:    parts[1],
			PaneIndex:      parts[2],
			WindowName:     parts[3],
			PaneID:         parts[4],
			PanePID:        parts[5],
			CurrentCommand: parts[6],
			PaneDead:       parts[7] == "1",
			PaneActive:     parts[8] == "1",
			PaneTitle:      strings.TrimSpace(parts[9]),
		}
		if len(parts) >= 11 {
			pane.Workspace = strings.TrimSpace(parts[10])
		}
		pane.Target = fmt.Sprintf("%s:%s.%s", pane.SessionName, pane.WindowIndex, pane.PaneIndex)
		pane.Role = strings.TrimSpace(o.paneOption(ctx, pane.Target, "ao_role"))
		pane.Model = strings.TrimSpace(o.paneOption(ctx, pane.Target, "ao_model"))
		pane.TeamID = strings.TrimSpace(o.paneOption(ctx, pane.Target, "ao_team_id"))
		pane.LastOutput = lastMeaningfulLine(o.capturePane(ctx, pane.Target))
		panes = append(panes, pane)
	}

	return panes, nil
}

func (o *Observer) paneOption(ctx context.Context, target, key string) string {
	out, err := o.run(ctx, "show-options", "-p", "-t", target, "-v", "@"+key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (o *Observer) capturePane(ctx context.Context, target string) string {
	if o.cfg.CaptureLines <= 0 {
		return ""
	}
	out, err := o.run(ctx, "capture-pane", "-t", target, "-p", "-J", "-S", fmt.Sprintf("-%d", o.cfg.CaptureLines))
	if err != nil {
		out, err = o.run(ctx, "capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", o.cfg.CaptureLines))
	}
	if err != nil {
		return ""
	}
	return out
}

func (o *Observer) run(ctx context.Context, args ...string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return o.runner.Run(timeoutCtx, o.cfg.Command, args...)
}

func inferRoleModel(pane observedPane) (string, string) {
	role := ""
	model := inferModelFromText(pane.CurrentCommand + " " + pane.PaneTitle + " " + pane.LastOutput)

	title := strings.TrimSpace(pane.PaneTitle)
	if title != "" {
		role = title
		if before, after, ok := strings.Cut(title, " "); ok && strings.Contains(after, "(") {
			role = strings.TrimSpace(before)
		}
		if before, after, ok := strings.Cut(role, "-"); ok && model == "" {
			if inferred := inferModelFromText(before); inferred != "" {
				model = inferred
				role = strings.TrimSpace(after)
			}
		}
	}

	if role == "" {
		role = inferRoleFromSession(pane.SessionName)
	}

	return role, model
}

func inferModelFromText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(text, "claude-code"), strings.Contains(text, "claude_code"), strings.Contains(text, "claude"):
		return "claude"
	case strings.Contains(text, "gemini"), strings.Contains(text, "agy"):
		return "gemini"
	case strings.Contains(text, "codex"), strings.Contains(text, "openai codex"):
		return "codex"
	default:
		return ""
	}
}

func inferRoleFromSession(sessionName string) string {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return ""
	}
	for _, token := range strings.FieldsFunc(sessionName, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ':'
	}) {
		switch strings.ToLower(token) {
		case "lead", "worker", "adversary", "researcher", "architect", "scribe", "operator":
			return token
		}
	}
	return ""
}

func isShellCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "bash", "zsh", "fish", "sh", "dash", "ksh", "pwsh", "powershell", "cmd":
		return true
	default:
		return false
	}
}

func lastMeaningfulLine(captured string) string {
	lines := strings.Split(captured, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "$ ") || strings.HasPrefix(line, "> ") {
			continue
		}
		if len(line) > 220 {
			return line[:220]
		}
		return line
	}
	return ""
}

func normalizeConfig(cfg ObserverConfig) ObserverConfig {
	defaults := DefaultObserverConfig()
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
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.CaptureLines < 0 {
		cfg.CaptureLines = defaults.CaptureLines
	}
	return cfg
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func isMuxUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "can't find server") ||
		strings.Contains(lower, "no such file")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
