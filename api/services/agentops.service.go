package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/google/uuid"
)

const (
	AgentOpsStatusActive  = "active"
	AgentOpsStatusIdle    = "idle"
	AgentOpsStatusRunning = "running"
	AgentOpsStatusBlocked = "blocked"
	AgentOpsStatusStopped = "stopped"
	AgentOpsStatusError   = "error"
	AgentOpsStatusUnknown = "unknown"

	AgentOpsLaunchStatusQueued    = "queued"
	AgentOpsLaunchStatusPublished = "published"
	AgentOpsLaunchStatusAccepted  = "accepted"
	AgentOpsLaunchStatusRunning   = "running"
	AgentOpsLaunchStatusCompleted = "completed"
	AgentOpsLaunchStatusFailed    = "failed"
	AgentOpsLaunchStatusCancelled = "cancelled"

	AgentOpsEventToolCall     = "tool.call"
	AgentOpsEventSessionEntry = "session.entry"
)

type AgentOpsService struct {
	db   *sql.DB
	nats *embeddednats.EmbeddedNATS
}

type AgentOpsEvent struct {
	EventID    string                 `json:"event_id,omitempty"`
	EventType  string                 `json:"event_type"`
	NodeID     string                 `json:"node_id,omitempty"`
	NodeLabel  string                 `json:"node_label,omitempty"`
	NodeClass  string                 `json:"node_class,omitempty"`
	MachineID  string                 `json:"machine_id,omitempty"`
	Address    string                 `json:"address,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	TeamID     string                 `json:"team_id,omitempty"`
	AgentLabel string                 `json:"agent_label,omitempty"`
	Model      string                 `json:"model,omitempty"`
	Role       string                 `json:"role,omitempty"`
	Workspace  string                 `json:"workspace,omitempty"`
	PaneID     string                 `json:"pane_id,omitempty"`
	Status     string                 `json:"status,omitempty"`
	Severity   string                 `json:"severity,omitempty"`
	Subject    string                 `json:"subject,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
	ObservedAt time.Time              `json:"observed_at,omitempty"`
}

type AgentOpsNode struct {
	NodeID       string    `json:"node_id"`
	NodeLabel    string    `json:"node_label"`
	NodeClass    string    `json:"node_class"`
	MachineID    string    `json:"machine_id"`
	Address      string    `json:"address"`
	Status       string    `json:"status"`
	Capabilities string    `json:"capabilities"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AgentOpsSession struct {
	SessionID  string    `json:"session_id"`
	NodeID     string    `json:"node_id"`
	TeamID     string    `json:"team_id"`
	AgentLabel string    `json:"agent_label"`
	Model      string    `json:"model"`
	Role       string    `json:"role"`
	Workspace  string    `json:"workspace"`
	PaneID     string    `json:"pane_id"`
	Status     string    `json:"status"`
	LastOutput string    `json:"last_output"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AgentOpsStoredEvent struct {
	EventID    string    `json:"event_id"`
	NodeID     string    `json:"node_id"`
	SessionID  string    `json:"session_id"`
	EventType  string    `json:"event_type"`
	Subject    string    `json:"subject"`
	Severity   string    `json:"severity"`
	Payload    string    `json:"payload"`
	ObservedAt time.Time `json:"observed_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type AgentOpsLaunchRequest struct {
	RequestID      string    `json:"request_id"`
	OrgID          string    `json:"org_id"`
	RequestedBy    string    `json:"requested_by"`
	TeamName       string    `json:"team_name"`
	Template       string    `json:"template"`
	TargetNodeID   string    `json:"target_node_id"`
	Workspace      string    `json:"workspace"`
	Mission        string    `json:"mission"`
	ModelRoute     string    `json:"model_route"`
	AgentCount     int       `json:"agent_count"`
	Status         string    `json:"status"`
	CommandSubject string    `json:"command_subject"`
	CommandPayload string    `json:"command_payload"`
	Error          string    `json:"error,omitempty"`
	RequestedAt    time.Time `json:"requested_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AgentOpsToolCall struct {
	EventID    string    `json:"event_id"`
	GlobalID   string    `json:"global_id"`
	NodeID     string    `json:"node_id"`
	SessionID  string    `json:"session_id"`
	TeamID     string    `json:"team_id"`
	Tool       string    `json:"tool"`
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	Result     string    `json:"result"`
	Error      string    `json:"error,omitempty"`
	Args       string    `json:"args"`
	Severity   string    `json:"severity"`
	ObservedAt time.Time `json:"observed_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type AgentOpsSessionEntry struct {
	EventID    string    `json:"event_id"`
	GlobalID   string    `json:"global_id"`
	NodeID     string    `json:"node_id"`
	SessionID  string    `json:"session_id"`
	TeamID     string    `json:"team_id"`
	AgentLabel string    `json:"agent_label"`
	Provider   string    `json:"provider"`
	Role       string    `json:"role"`
	Model      string    `json:"model"`
	Workspace  string    `json:"workspace"`
	Source     string    `json:"source"`
	SourcePath string    `json:"source_path"`
	TurnID     string    `json:"turn_id"`
	Sequence   int       `json:"sequence"`
	Message    string    `json:"message"`
	Preview    string    `json:"preview"`
	ObservedAt time.Time `json:"observed_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateAgentOpsLaunchRequest struct {
	OrgID        string `json:"org_id"`
	RequestedBy  string `json:"requested_by"`
	TeamName     string `json:"team_name"`
	Template     string `json:"template"`
	TargetNodeID string `json:"target_node_id"`
	Workspace    string `json:"workspace"`
	Mission      string `json:"mission"`
	ModelRoute   string `json:"model_route"`
	AgentCount   int    `json:"agent_count"`
}

type CreateAgentOpsToolCall struct {
	GlobalID   string                 `json:"global_id"`
	OriginNode string                 `json:"origin_node"`
	NodeID     string                 `json:"node_id"`
	SessionID  string                 `json:"session_id"`
	TeamID     string                 `json:"team_id"`
	AgentLabel string                 `json:"agent_label"`
	Tool       string                 `json:"tool"`
	Args       map[string]interface{} `json:"args"`
	Result     string                 `json:"result"`
	Error      string                 `json:"error"`
	Source     string                 `json:"source"`
	ObservedAt time.Time              `json:"observed_at"`
}

type CreateAgentOpsSessionEntry struct {
	GlobalID   string    `json:"global_id"`
	OriginNode string    `json:"origin_node"`
	NodeID     string    `json:"node_id"`
	SessionID  string    `json:"session_id"`
	TeamID     string    `json:"team_id"`
	AgentLabel string    `json:"agent_label"`
	Provider   string    `json:"provider"`
	Role       string    `json:"role"`
	Model      string    `json:"model"`
	Workspace  string    `json:"workspace"`
	Source     string    `json:"source"`
	SourcePath string    `json:"source_path"`
	TurnID     string    `json:"turn_id"`
	Sequence   int       `json:"sequence"`
	Message    string    `json:"message"`
	Content    string    `json:"content"`
	ObservedAt time.Time `json:"observed_at"`
}

type AgentOpsSurface struct {
	Capability    string `json:"capability"`
	LegacyPackage string `json:"legacy_package"`
	NativeSurface string `json:"native_surface"`
	UIPath        string `json:"ui_path"`
	State         string `json:"state"`
}

type AgentOpsSummary struct {
	NodeCount          int                        `json:"node_count"`
	ActiveNodeCount    int                        `json:"active_node_count"`
	SessionCount       int                        `json:"session_count"`
	ActiveSessionCount int                        `json:"active_session_count"`
	EventCount         int                        `json:"event_count"`
	LaunchRequestCount int                        `json:"launch_request_count"`
	PendingLaunchCount int                        `json:"pending_launch_count"`
	ToolCallCount      int                        `json:"tool_call_count"`
	SessionEntryCount  int                        `json:"session_entry_count"`
	LastObservedAt     *time.Time                 `json:"last_observed_at,omitempty"`
	Nodes              []AgentOpsNode             `json:"nodes"`
	Sessions           []AgentOpsSession          `json:"sessions"`
	Events             []AgentOpsStoredEvent      `json:"events"`
	ToolCalls          []AgentOpsToolCall         `json:"tool_calls"`
	SessionEntries     []AgentOpsSessionEntry     `json:"session_entries"`
	LaunchRequests     []AgentOpsLaunchRequest    `json:"launch_requests"`
	Knowledge          *AgentOpsKnowledgeGradient `json:"knowledge,omitempty"`
	Surfaces           []AgentOpsSurface          `json:"surfaces"`
}

func NewAgentOpsService(db *sql.DB, nats *embeddednats.EmbeddedNATS) *AgentOpsService {
	return &AgentOpsService{db: db, nats: nats}
}

func AgentOpsSurfaces() []AgentOpsSurface {
	return []AgentOpsSurface{
		{
			Capability:    "Observe",
			LegacyPackage: "tools/ao-ops/internal/tmux + internal/autolog",
			NativeSurface: "pkg/services/agentops.Observer, agent_sessions, agent_events",
			UIPath:        "/agent-ops",
			State:         "tmux native",
		},
		{
			Capability:    "Connect",
			LegacyPackage: "tools/ao-ops/internal/gossip + internal/events",
			NativeSurface: "Embedded NATS streams, global KV, service workers",
			UIPath:        "/streams",
			State:         "mapped",
		},
		{
			Capability:    "Learn",
			LegacyPackage: "tools/ao-ops/internal/store + internal/neuralpulse",
			NativeSurface: "SQLite domain tables, session.entry events, native knowledge gradient",
			UIPath:        "/agent-ops",
			State:         "retrieval native",
		},
		{
			Capability:    "Autolog",
			LegacyPackage: "tools/ao-ops/internal/autolog + internal/exchange",
			NativeSurface: "Agent Ops session-entry envelopes, constellation.agentops.<node>.session.<provider>",
			UIPath:        "/agent-ops",
			State:         "entry native",
		},
		{
			Capability:    "Launch",
			LegacyPackage: "tools/ao-ops/internal/orchestrator + internal/mcpserver",
			NativeSurface: "agent_launch_requests, Constellation command subjects, native launch executor",
			UIPath:        "/agent-ops",
			State:         "executor native",
		},
		{
			Capability:    "Tool Events",
			LegacyPackage: "tools/ao-ops/internal/mcpserver/toolevents",
			NativeSurface: "Agent Ops tool-call envelopes, constellation.agentops.<node>.tool.<tool>",
			UIPath:        "/agent-ops",
			State:         "event native",
		},
		{
			Capability:    "Operate",
			LegacyPackage: "tools/ao-ops/internal/api + internal/ui",
			NativeSurface: "pkg/services/web/features/agentops",
			UIPath:        "/agent-ops",
			State:         "native UI",
		},
		{
			Capability:    "Bootstrap/Update",
			LegacyPackage: "cmd/ao-tray + internal/tray + release scripts",
			NativeSurface: "cmd/microlith bootstrap, cmd/microlith update, pkg/updater",
			UIPath:        "/agent-ops",
			State:         "mapped",
		},
	}
}

func (s *AgentOpsService) PublishEvent(ctx context.Context, event *AgentOpsEvent) error {
	if err := s.normalizeEvent(event); err != nil {
		return err
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal agent ops event: %w", err)
	}

	if s.nats != nil {
		subject := shared.AgentOpsEventSubject(event.NodeID, event.EventType)
		if strings.HasPrefix(event.Subject, shared.SubjectAgentOps+".") {
			subject = event.Subject
		}
		if err := s.nats.PublishWithDedup(subject, payload, event.EventID); err != nil {
			return err
		}
	}

	return s.RecordEvent(ctx, event)
}

func (s *AgentOpsService) RecordEvent(ctx context.Context, event *AgentOpsEvent) error {
	if s.db == nil {
		return fmt.Errorf("agent ops database is nil")
	}
	if err := s.normalizeEvent(event); err != nil {
		return err
	}

	payload := event.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal agent ops payload: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent ops transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				logger.Warnw("Failed to roll back agent ops transaction", "error", rbErr)
			}
		}
	}()

	if event.NodeID != "" {
		if err = upsertAgentOpsNode(ctx, tx, event, string(payloadJSON)); err != nil {
			return err
		}
	}

	if event.SessionID != "" {
		if err = upsertAgentOpsSession(ctx, tx, event, payload); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_events (
			event_id, node_id, session_id, event_type, subject, severity, payload, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID,
		event.NodeID,
		event.SessionID,
		event.EventType,
		event.Subject,
		event.Severity,
		string(payloadJSON),
		event.ObservedAt.Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert agent ops event: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit agent ops transaction: %w", err)
	}
	return nil
}

func (s *AgentOpsService) CreateLaunchRequest(ctx context.Context, req CreateAgentOpsLaunchRequest) (*AgentOpsLaunchRequest, error) {
	if s.db == nil {
		return nil, fmt.Errorf("agent ops database is nil")
	}

	launch, payloadJSON, err := normalizeLaunchRequest(req)
	if err != nil {
		return nil, err
	}

	if err := s.insertLaunchRequest(ctx, launch); err != nil {
		return nil, err
	}

	if s.nats != nil {
		if err := s.nats.PublishWithDedup(launch.CommandSubject, payloadJSON, launch.RequestID); err != nil {
			launch.Status = AgentOpsLaunchStatusFailed
			launch.Error = err.Error()
			if updateErr := s.updateLaunchRequestStatus(ctx, launch); updateErr != nil {
				logger.Warnw("Failed to mark agent launch request failed", "request_id", launch.RequestID, "error", updateErr)
			}
			return launch, fmt.Errorf("publish agent launch command: %w", err)
		}

		launch.Status = AgentOpsLaunchStatusPublished
		if err := s.updateLaunchRequestStatus(ctx, launch); err != nil {
			return nil, err
		}
	}

	if err := s.RecordEvent(ctx, &AgentOpsEvent{
		EventType: "launch.requested",
		Subject:   launch.CommandSubject,
		Severity:  "info",
		Payload: map[string]interface{}{
			"request_id":     launch.RequestID,
			"org_id":         launch.OrgID,
			"requested_by":   launch.RequestedBy,
			"team_name":      launch.TeamName,
			"template":       launch.Template,
			"target_node_id": launch.TargetNodeID,
			"workspace":      launch.Workspace,
			"mission":        launch.Mission,
			"model_route":    launch.ModelRoute,
			"agent_count":    launch.AgentCount,
			"status":         launch.Status,
		},
	}); err != nil {
		return nil, fmt.Errorf("record agent launch event: %w", err)
	}

	return launch, nil
}

func (s *AgentOpsService) RecordToolCall(ctx context.Context, req CreateAgentOpsToolCall) (*AgentOpsToolCall, error) {
	event, err := normalizeToolCallEvent(req)
	if err != nil {
		return nil, err
	}

	if err := s.PublishEvent(ctx, event); err != nil {
		return nil, err
	}

	return toolCallFromEvent(event), nil
}

func (s *AgentOpsService) RecordSessionEntry(ctx context.Context, req CreateAgentOpsSessionEntry) (*AgentOpsSessionEntry, error) {
	event, err := normalizeSessionEntryEvent(req)
	if err != nil {
		return nil, err
	}

	if err := s.PublishEvent(ctx, event); err != nil {
		return nil, err
	}

	return sessionEntryFromEvent(event), nil
}

func (s *AgentOpsService) SetLaunchRequestStatus(ctx context.Context, requestID, status, errorMessage string) error {
	if s.db == nil {
		return fmt.Errorf("agent ops database is nil")
	}

	status = normalizeLaunchStatus(status)
	if status == "" {
		return fmt.Errorf("invalid launch status")
	}

	launch := &AgentOpsLaunchRequest{
		RequestID: strings.TrimSpace(requestID),
		Status:    status,
		Error:     strings.TrimSpace(errorMessage),
	}
	if launch.RequestID == "" {
		return fmt.Errorf("request_id is required")
	}

	return s.updateLaunchRequestStatus(ctx, launch)
}

func (s *AgentOpsService) Summary(ctx context.Context) (*AgentOpsSummary, error) {
	if s.db == nil {
		return nil, fmt.Errorf("agent ops database is nil")
	}

	summary := &AgentOpsSummary{
		Surfaces: AgentOpsSurfaces(),
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('active', 'running') THEN 1 ELSE 0 END), 0)
		FROM agent_nodes`).Scan(&summary.NodeCount, &summary.ActiveNodeCount); err != nil {
		return nil, fmt.Errorf("count agent nodes: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('active', 'running') THEN 1 ELSE 0 END), 0)
		FROM agent_sessions`).Scan(&summary.SessionCount, &summary.ActiveSessionCount); err != nil {
		return nil, fmt.Errorf("count agent sessions: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events`).Scan(&summary.EventCount); err != nil {
		return nil, fmt.Errorf("count agent events: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status IN ('queued', 'published', 'accepted', 'running') THEN 1 ELSE 0 END), 0)
		FROM agent_launch_requests`).Scan(&summary.LaunchRequestCount, &summary.PendingLaunchCount); err != nil {
		return nil, fmt.Errorf("count agent launch requests: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events WHERE event_type = ?`, AgentOpsEventToolCall).Scan(&summary.ToolCallCount); err != nil {
		return nil, fmt.Errorf("count agent tool calls: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events WHERE event_type = ?`, AgentOpsEventSessionEntry).Scan(&summary.SessionEntryCount); err != nil {
		return nil, fmt.Errorf("count agent session entries: %w", err)
	}

	var lastObserved sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(observed_at) FROM agent_events`).Scan(&lastObserved); err != nil {
		return nil, fmt.Errorf("get last observed agent event: %w", err)
	}
	if lastObserved.Valid {
		if parsed := parseAgentOpsTime(lastObserved.String); !parsed.IsZero() {
			summary.LastObservedAt = &parsed
		}
	}

	nodes, err := s.listNodes(ctx, 25)
	if err != nil {
		return nil, err
	}
	summary.Nodes = nodes

	sessions, err := s.listSessions(ctx, 50)
	if err != nil {
		return nil, err
	}
	summary.Sessions = sessions

	events, err := s.listEvents(ctx, 50)
	if err != nil {
		return nil, err
	}
	summary.Events = events

	toolCalls, err := s.listToolCalls(ctx, 25)
	if err != nil {
		return nil, err
	}
	summary.ToolCalls = toolCalls

	sessionEntries, err := s.listSessionEntries(ctx, 25)
	if err != nil {
		return nil, err
	}
	summary.SessionEntries = sessionEntries

	launchRequests, err := s.listLaunchRequests(ctx, 25)
	if err != nil {
		return nil, err
	}
	summary.LaunchRequests = launchRequests

	knowledge, err := s.KnowledgeGradient(ctx, "", defaultKnowledgeWindowHours, defaultKnowledgeLimit)
	if err != nil {
		return nil, err
	}
	summary.Knowledge = knowledge

	return summary, nil
}

func (s *AgentOpsService) normalizeEvent(event *AgentOpsEvent) error {
	if event == nil {
		return fmt.Errorf("agent ops event is nil")
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.EventType == "" {
		event.EventType = "observed"
	}
	event.EventType = strings.TrimSpace(strings.ToLower(event.EventType))
	event.Status = normalizeAgentOpsStatus(event.Status)
	if event.Severity == "" {
		event.Severity = "info"
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	if event.Payload == nil {
		event.Payload = map[string]interface{}{}
	}
	return nil
}

func upsertAgentOpsNode(ctx context.Context, tx *sql.Tx, event *AgentOpsEvent, capabilities string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_nodes (
			node_id, node_label, node_class, machine_id, address, status, capabilities, last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			node_label = CASE WHEN excluded.node_label != '' THEN excluded.node_label ELSE agent_nodes.node_label END,
			node_class = CASE WHEN excluded.node_class != '' THEN excluded.node_class ELSE agent_nodes.node_class END,
			machine_id = CASE WHEN excluded.machine_id != '' THEN excluded.machine_id ELSE agent_nodes.machine_id END,
			address = CASE WHEN excluded.address != '' THEN excluded.address ELSE agent_nodes.address END,
			status = excluded.status,
			capabilities = CASE WHEN excluded.capabilities != '{}' THEN excluded.capabilities ELSE agent_nodes.capabilities END,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at`,
		event.NodeID,
		event.NodeLabel,
		event.NodeClass,
		event.MachineID,
		event.Address,
		AgentOpsStatusActive,
		capabilities,
		event.ObservedAt.Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert agent ops node: %w", err)
	}
	return nil
}

func upsertAgentOpsSession(ctx context.Context, tx *sql.Tx, event *AgentOpsEvent, payload map[string]interface{}) error {
	lastOutput := firstPayloadString(payload, "last_output", "output", "message", "summary")
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			session_id, node_id, team_id, agent_label, model, role, workspace, pane_id,
			status, last_output, last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			node_id = CASE WHEN excluded.node_id != '' THEN excluded.node_id ELSE agent_sessions.node_id END,
			team_id = CASE WHEN excluded.team_id != '' THEN excluded.team_id ELSE agent_sessions.team_id END,
			agent_label = CASE WHEN excluded.agent_label != '' THEN excluded.agent_label ELSE agent_sessions.agent_label END,
			model = CASE WHEN excluded.model != '' THEN excluded.model ELSE agent_sessions.model END,
			role = CASE WHEN excluded.role != '' THEN excluded.role ELSE agent_sessions.role END,
			workspace = CASE WHEN excluded.workspace != '' THEN excluded.workspace ELSE agent_sessions.workspace END,
			pane_id = CASE WHEN excluded.pane_id != '' THEN excluded.pane_id ELSE agent_sessions.pane_id END,
			status = excluded.status,
			last_output = CASE WHEN excluded.last_output != '' THEN excluded.last_output ELSE agent_sessions.last_output END,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at`,
		event.SessionID,
		event.NodeID,
		event.TeamID,
		event.AgentLabel,
		event.Model,
		event.Role,
		event.Workspace,
		event.PaneID,
		event.Status,
		lastOutput,
		event.ObservedAt.Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert agent ops session: %w", err)
	}
	return nil
}

func normalizeLaunchRequest(req CreateAgentOpsLaunchRequest) (*AgentOpsLaunchRequest, []byte, error) {
	now := time.Now().UTC()
	teamName := strings.TrimSpace(req.TeamName)
	mission := strings.TrimSpace(req.Mission)
	if teamName == "" {
		return nil, nil, fmt.Errorf("team_name is required")
	}
	if mission == "" {
		return nil, nil, fmt.Errorf("mission is required")
	}

	orgID := strings.TrimSpace(req.OrgID)
	if orgID == "" {
		orgID = "default"
	}

	agentCount := req.AgentCount
	if agentCount <= 0 {
		agentCount = 1
	}
	if agentCount > 24 {
		agentCount = 24
	}

	launch := &AgentOpsLaunchRequest{
		RequestID:      uuid.NewString(),
		OrgID:          orgID,
		RequestedBy:    strings.TrimSpace(req.RequestedBy),
		TeamName:       teamName,
		Template:       fallbackLaunchValue(req.Template, "custom"),
		TargetNodeID:   strings.TrimSpace(req.TargetNodeID),
		Workspace:      strings.TrimSpace(req.Workspace),
		Mission:        mission,
		ModelRoute:     fallbackLaunchValue(req.ModelRoute, "default"),
		AgentCount:     agentCount,
		Status:         AgentOpsLaunchStatusQueued,
		CommandSubject: shared.CommandAgentOpsLaunchSubject(orgID),
		RequestedAt:    now,
		UpdatedAt:      now,
	}

	commandPayload := map[string]interface{}{
		"command":        "agentops.launch",
		"request_id":     launch.RequestID,
		"org_id":         launch.OrgID,
		"requested_by":   launch.RequestedBy,
		"team_name":      launch.TeamName,
		"template":       launch.Template,
		"target_node_id": launch.TargetNodeID,
		"workspace":      launch.Workspace,
		"mission":        launch.Mission,
		"model_route":    launch.ModelRoute,
		"agent_count":    launch.AgentCount,
		"requested_at":   launch.RequestedAt.Format(time.RFC3339),
	}

	payloadJSON, err := json.Marshal(commandPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal agent launch command: %w", err)
	}
	launch.CommandPayload = string(payloadJSON)

	return launch, payloadJSON, nil
}

func fallbackLaunchValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeLaunchStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case AgentOpsLaunchStatusQueued,
		AgentOpsLaunchStatusPublished,
		AgentOpsLaunchStatusAccepted,
		AgentOpsLaunchStatusRunning,
		AgentOpsLaunchStatusCompleted,
		AgentOpsLaunchStatusFailed,
		AgentOpsLaunchStatusCancelled:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func normalizeToolCallEvent(req CreateAgentOpsToolCall) (*AgentOpsEvent, error) {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return nil, fmt.Errorf("tool is required")
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSpace(req.OriginNode)
	}
	if nodeID == "" {
		nodeID = "unknown"
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "mcp"
	}

	globalID := strings.TrimSpace(req.GlobalID)
	if globalID == "" {
		globalID = uuid.NewString()
	}

	args := req.Args
	if args == nil {
		args = map[string]interface{}{}
	}

	observedAt := req.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	severity := "info"
	status := AgentOpsStatusActive
	if strings.TrimSpace(req.Error) != "" {
		severity = "error"
		status = AgentOpsStatusError
	}

	toolToken := normalizeAgentOpsToken(tool)
	target := summarizeToolTarget(args)
	payload := map[string]interface{}{
		"global_id":   globalID,
		"origin_node": nodeID,
		"session_id":  strings.TrimSpace(req.SessionID),
		"team_id":     strings.TrimSpace(req.TeamID),
		"tool":        tool,
		"tool_token":  toolToken,
		"args":        args,
		"result":      strings.TrimSpace(req.Result),
		"error":       strings.TrimSpace(req.Error),
		"source":      source,
		"target":      target,
	}

	return &AgentOpsEvent{
		EventID:    globalID,
		EventType:  AgentOpsEventToolCall,
		NodeID:     nodeID,
		SessionID:  strings.TrimSpace(req.SessionID),
		TeamID:     strings.TrimSpace(req.TeamID),
		AgentLabel: strings.TrimSpace(req.AgentLabel),
		Status:     status,
		Severity:   severity,
		Subject:    shared.AgentOpsToolSubject(nodeID, toolToken),
		Payload:    payload,
		ObservedAt: observedAt,
	}, nil
}

func normalizeSessionEntryEvent(req CreateAgentOpsSessionEntry) (*AgentOpsEvent, error) {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = strings.TrimSpace(req.Content)
	}
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSpace(req.OriginNode)
	}
	if nodeID == "" {
		nodeID = "unknown"
	}

	provider := strings.TrimSpace(strings.ToLower(req.Provider))
	if provider == "" {
		provider = "unknown"
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "conversation"
	}

	role := strings.TrimSpace(strings.ToLower(req.Role))
	if role == "" {
		role = "unknown"
	}

	globalID := strings.TrimSpace(req.GlobalID)
	if globalID == "" {
		globalID = uuid.NewString()
	}

	observedAt := req.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	providerToken := normalizeAgentOpsToken(provider)
	payload := map[string]interface{}{
		"global_id":      globalID,
		"origin_node":    nodeID,
		"session_id":     strings.TrimSpace(req.SessionID),
		"team_id":        strings.TrimSpace(req.TeamID),
		"agent_label":    strings.TrimSpace(req.AgentLabel),
		"provider":       provider,
		"provider_token": providerToken,
		"role":           role,
		"model":          strings.TrimSpace(req.Model),
		"workspace":      strings.TrimSpace(req.Workspace),
		"source":         source,
		"source_path":    strings.TrimSpace(req.SourcePath),
		"turn_id":        strings.TrimSpace(req.TurnID),
		"sequence":       req.Sequence,
		"message":        message,
		"preview":        previewAgentOpsMessage(message, 180),
	}

	return &AgentOpsEvent{
		EventID:    globalID,
		EventType:  AgentOpsEventSessionEntry,
		NodeID:     nodeID,
		SessionID:  strings.TrimSpace(req.SessionID),
		TeamID:     strings.TrimSpace(req.TeamID),
		AgentLabel: strings.TrimSpace(req.AgentLabel),
		Model:      strings.TrimSpace(req.Model),
		Role:       role,
		Workspace:  strings.TrimSpace(req.Workspace),
		Status:     AgentOpsStatusActive,
		Severity:   "info",
		Subject:    shared.AgentOpsSessionSubject(nodeID, providerToken),
		Payload:    payload,
		ObservedAt: observedAt,
	}, nil
}

func normalizeAgentOpsToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(".", "_", ":", "_", " ", "_", "/", "_", "*", "_", ">", "_")
	return replacer.Replace(value)
}

func summarizeToolTarget(args map[string]interface{}) string {
	for _, key := range []string{"target", "pane_target", "team_name", "session_name", "name", "project", "file_path"} {
		if value, ok := args[key]; ok && value != nil {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

func (s *AgentOpsService) insertLaunchRequest(ctx context.Context, launch *AgentOpsLaunchRequest) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_launch_requests (
			request_id, org_id, requested_by, team_name, template, target_node_id, workspace,
			mission, model_route, agent_count, status, command_subject, command_payload,
			error, requested_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		launch.RequestID,
		launch.OrgID,
		launch.RequestedBy,
		launch.TeamName,
		launch.Template,
		launch.TargetNodeID,
		launch.Workspace,
		launch.Mission,
		launch.ModelRoute,
		launch.AgentCount,
		launch.Status,
		launch.CommandSubject,
		launch.CommandPayload,
		launch.Error,
		launch.RequestedAt.Format(time.RFC3339),
		launch.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert agent launch request: %w", err)
	}
	return nil
}

func (s *AgentOpsService) updateLaunchRequestStatus(ctx context.Context, launch *AgentOpsLaunchRequest) error {
	launch.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE agent_launch_requests
		SET status = ?, error = ?, updated_at = ?
		WHERE request_id = ?`,
		launch.Status,
		launch.Error,
		launch.UpdatedAt.Format(time.RFC3339),
		launch.RequestID,
	)
	if err != nil {
		return fmt.Errorf("update agent launch request status: %w", err)
	}
	return nil
}

func (s *AgentOpsService) listNodes(ctx context.Context, limit int) ([]AgentOpsNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, node_label, node_class, machine_id, address, status, capabilities,
		       last_seen_at, created_at, updated_at
		FROM agent_nodes
		ORDER BY last_seen_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent nodes: %w", err)
	}
	defer rows.Close()

	var nodes []AgentOpsNode
	for rows.Next() {
		var node AgentOpsNode
		var lastSeenAt, createdAt, updatedAt string
		if err := rows.Scan(
			&node.NodeID, &node.NodeLabel, &node.NodeClass, &node.MachineID, &node.Address,
			&node.Status, &node.Capabilities, &lastSeenAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent node: %w", err)
		}
		node.LastSeenAt = parseAgentOpsTime(lastSeenAt)
		node.CreatedAt = parseAgentOpsTime(createdAt)
		node.UpdatedAt = parseAgentOpsTime(updatedAt)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent nodes: %w", err)
	}
	return nodes, nil
}

func (s *AgentOpsService) listSessions(ctx context.Context, limit int) ([]AgentOpsSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, node_id, team_id, agent_label, model, role, workspace, pane_id,
		       status, last_output, last_seen_at, created_at, updated_at
		FROM agent_sessions
		ORDER BY last_seen_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []AgentOpsSession
	for rows.Next() {
		var session AgentOpsSession
		var lastSeenAt, createdAt, updatedAt string
		if err := rows.Scan(
			&session.SessionID, &session.NodeID, &session.TeamID, &session.AgentLabel,
			&session.Model, &session.Role, &session.Workspace, &session.PaneID,
			&session.Status, &session.LastOutput, &lastSeenAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent session: %w", err)
		}
		session.LastSeenAt = parseAgentOpsTime(lastSeenAt)
		session.CreatedAt = parseAgentOpsTime(createdAt)
		session.UpdatedAt = parseAgentOpsTime(updatedAt)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent sessions: %w", err)
	}
	return sessions, nil
}

func (s *AgentOpsService) listEvents(ctx context.Context, limit int) ([]AgentOpsStoredEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, node_id, session_id, event_type, subject, severity, payload, observed_at, created_at
		FROM agent_events
		ORDER BY observed_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent events: %w", err)
	}
	defer rows.Close()

	var events []AgentOpsStoredEvent
	for rows.Next() {
		var event AgentOpsStoredEvent
		var observedAt, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.NodeID, &event.SessionID, &event.EventType,
			&event.Subject, &event.Severity, &event.Payload, &observedAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent event: %w", err)
		}
		event.ObservedAt = parseAgentOpsTime(observedAt)
		event.CreatedAt = parseAgentOpsTime(createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent events: %w", err)
	}
	return events, nil
}

func (s *AgentOpsService) listToolCalls(ctx context.Context, limit int) ([]AgentOpsToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, node_id, session_id, event_type, subject, severity, payload, observed_at, created_at
		FROM agent_events
		WHERE event_type = ?
		ORDER BY observed_at DESC
		LIMIT ?`, AgentOpsEventToolCall, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent tool calls: %w", err)
	}
	defer rows.Close()

	var calls []AgentOpsToolCall
	for rows.Next() {
		var event AgentOpsStoredEvent
		var observedAt, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.NodeID, &event.SessionID, &event.EventType,
			&event.Subject, &event.Severity, &event.Payload, &observedAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent tool call: %w", err)
		}
		event.ObservedAt = parseAgentOpsTime(observedAt)
		event.CreatedAt = parseAgentOpsTime(createdAt)
		calls = append(calls, toolCallFromStoredEvent(event))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent tool calls: %w", err)
	}
	return calls, nil
}

func toolCallFromEvent(event *AgentOpsEvent) *AgentOpsToolCall {
	if event == nil {
		return nil
	}
	stored := AgentOpsStoredEvent{
		EventID:    event.EventID,
		NodeID:     event.NodeID,
		SessionID:  event.SessionID,
		EventType:  event.EventType,
		Subject:    event.Subject,
		Severity:   event.Severity,
		ObservedAt: event.ObservedAt,
		CreatedAt:  time.Now().UTC(),
	}
	payloadJSON, _ := json.Marshal(event.Payload)
	stored.Payload = string(payloadJSON)
	call := toolCallFromStoredEvent(stored)
	call.TeamID = event.TeamID
	return &call
}

func toolCallFromStoredEvent(event AgentOpsStoredEvent) AgentOpsToolCall {
	payload := map[string]interface{}{}
	if event.Payload != "" {
		_ = json.Unmarshal([]byte(event.Payload), &payload)
	}

	call := AgentOpsToolCall{
		EventID:    event.EventID,
		GlobalID:   payloadString(payload, "global_id"),
		NodeID:     event.NodeID,
		SessionID:  event.SessionID,
		TeamID:     payloadString(payload, "team_id"),
		Tool:       payloadString(payload, "tool"),
		Source:     payloadString(payload, "source"),
		Target:     payloadString(payload, "target"),
		Result:     payloadString(payload, "result"),
		Error:      payloadString(payload, "error"),
		Args:       compactPayloadJSON(payload["args"]),
		Severity:   event.Severity,
		ObservedAt: event.ObservedAt,
		CreatedAt:  event.CreatedAt,
	}
	if call.GlobalID == "" {
		call.GlobalID = event.EventID
	}
	if call.Tool == "" {
		call.Tool = "unknown"
	}
	if call.Source == "" {
		call.Source = "mcp"
	}
	return call
}

func payloadString(payload map[string]interface{}, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func compactPayloadJSON(value interface{}) string {
	if value == nil {
		return "{}"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s *AgentOpsService) listSessionEntries(ctx context.Context, limit int) ([]AgentOpsSessionEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, node_id, session_id, event_type, subject, severity, payload, observed_at, created_at
		FROM agent_events
		WHERE event_type = ?
		ORDER BY observed_at DESC
		LIMIT ?`, AgentOpsEventSessionEntry, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent session entries: %w", err)
	}
	defer rows.Close()

	var entries []AgentOpsSessionEntry
	for rows.Next() {
		var event AgentOpsStoredEvent
		var observedAt, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.NodeID, &event.SessionID, &event.EventType,
			&event.Subject, &event.Severity, &event.Payload, &observedAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent session entry: %w", err)
		}
		event.ObservedAt = parseAgentOpsTime(observedAt)
		event.CreatedAt = parseAgentOpsTime(createdAt)
		entries = append(entries, sessionEntryFromStoredEvent(event))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent session entries: %w", err)
	}
	return entries, nil
}

func sessionEntryFromEvent(event *AgentOpsEvent) *AgentOpsSessionEntry {
	if event == nil {
		return nil
	}
	stored := AgentOpsStoredEvent{
		EventID:    event.EventID,
		NodeID:     event.NodeID,
		SessionID:  event.SessionID,
		EventType:  event.EventType,
		Subject:    event.Subject,
		Severity:   event.Severity,
		ObservedAt: event.ObservedAt,
		CreatedAt:  time.Now().UTC(),
	}
	payloadJSON, _ := json.Marshal(event.Payload)
	stored.Payload = string(payloadJSON)
	entry := sessionEntryFromStoredEvent(stored)
	entry.TeamID = event.TeamID
	entry.AgentLabel = event.AgentLabel
	return &entry
}

func sessionEntryFromStoredEvent(event AgentOpsStoredEvent) AgentOpsSessionEntry {
	payload := map[string]interface{}{}
	if event.Payload != "" {
		_ = json.Unmarshal([]byte(event.Payload), &payload)
	}

	entry := AgentOpsSessionEntry{
		EventID:    event.EventID,
		GlobalID:   payloadString(payload, "global_id"),
		NodeID:     event.NodeID,
		SessionID:  event.SessionID,
		TeamID:     payloadString(payload, "team_id"),
		AgentLabel: payloadString(payload, "agent_label"),
		Provider:   payloadString(payload, "provider"),
		Role:       payloadString(payload, "role"),
		Model:      payloadString(payload, "model"),
		Workspace:  payloadString(payload, "workspace"),
		Source:     payloadString(payload, "source"),
		SourcePath: payloadString(payload, "source_path"),
		TurnID:     payloadString(payload, "turn_id"),
		Sequence:   payloadInt(payload, "sequence"),
		Message:    payloadString(payload, "message"),
		Preview:    payloadString(payload, "preview"),
		ObservedAt: event.ObservedAt,
		CreatedAt:  event.CreatedAt,
	}
	if entry.GlobalID == "" {
		entry.GlobalID = event.EventID
	}
	if entry.Provider == "" {
		entry.Provider = "unknown"
	}
	if entry.Source == "" {
		entry.Source = "conversation"
	}
	if entry.Preview == "" {
		entry.Preview = previewAgentOpsMessage(entry.Message, 180)
	}
	return entry
}

func payloadInt(payload map[string]interface{}, key string) int {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func previewAgentOpsMessage(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func (s *AgentOpsService) listLaunchRequests(ctx context.Context, limit int) ([]AgentOpsLaunchRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT request_id, org_id, requested_by, team_name, template, target_node_id, workspace,
		       mission, model_route, agent_count, status, command_subject, command_payload,
		       error, requested_at, updated_at
		FROM agent_launch_requests
		ORDER BY requested_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent launch requests: %w", err)
	}
	defer rows.Close()

	var requests []AgentOpsLaunchRequest
	for rows.Next() {
		var request AgentOpsLaunchRequest
		var requestedAt, updatedAt string
		if err := rows.Scan(
			&request.RequestID, &request.OrgID, &request.RequestedBy, &request.TeamName,
			&request.Template, &request.TargetNodeID, &request.Workspace, &request.Mission,
			&request.ModelRoute, &request.AgentCount, &request.Status, &request.CommandSubject,
			&request.CommandPayload, &request.Error, &requestedAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent launch request: %w", err)
		}
		request.RequestedAt = parseAgentOpsTime(requestedAt)
		request.UpdatedAt = parseAgentOpsTime(updatedAt)
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent launch requests: %w", err)
	}
	return requests, nil
}

func normalizeAgentOpsStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case AgentOpsStatusActive, AgentOpsStatusIdle, AgentOpsStatusRunning, AgentOpsStatusBlocked, AgentOpsStatusStopped, AgentOpsStatusError:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return AgentOpsStatusUnknown
	}
}

func parseAgentOpsTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstPayloadString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
