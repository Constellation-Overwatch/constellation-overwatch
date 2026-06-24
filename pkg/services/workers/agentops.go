package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type AgentOpsWorker struct {
	*BaseWorker
	service *apiservices.AgentOpsService
}

func NewAgentOpsWorker(nc *nats.Conn, js nats.JetStreamContext, db *sql.DB) *AgentOpsWorker {
	return &AgentOpsWorker{
		BaseWorker: NewBaseWorker(
			"AgentOpsWorker",
			nc,
			js,
			shared.StreamAgentOps,
			shared.ConsumerAgentOpsProcessor,
			shared.SubjectAgentOpsAll,
		),
		service: apiservices.NewAgentOpsService(db, nil),
	}
}

func (w *AgentOpsWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, w.handleAgentOpsEvent)
}

func (w *AgentOpsWorker) handleAgentOpsEvent(msg *nats.Msg) error {
	event := apiservices.AgentOpsEvent{}
	generic := map[string]interface{}{}

	if err := json.Unmarshal(msg.Data, &generic); err != nil {
		logger.Warnw("Agent ops event had non-JSON payload", "subject", msg.Subject, "error", err)
		generic = map[string]interface{}{
			"raw": string(msg.Data),
		}
		event.Payload = generic
	} else if err := json.Unmarshal(msg.Data, &event); err != nil {
		event.Payload = generic
	}

	if event.Payload == nil {
		event.Payload = generic
	}
	applyAgentOpsSubject(&event, msg.Subject)
	applyAgentOpsPayload(&event, generic)

	return w.service.RecordEvent(context.Background(), &event)
}

func eventTypeFromSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 4 && parts[0] == "constellation" && parts[1] == "agentops" {
		if len(parts) >= 5 {
			switch parts[3] {
			case "tool":
				return apiservices.AgentOpsEventToolCall
			case "session":
				return apiservices.AgentOpsEventSessionEntry
			}
		}
		return strings.Join(parts[3:], ".")
	}
	return "observed"
}

func applyAgentOpsSubject(event *apiservices.AgentOpsEvent, subject string) {
	if event.Subject == "" {
		event.Subject = subject
	}
	if event.EventType == "" {
		event.EventType = eventTypeFromSubject(subject)
	}
	if event.NodeID == "" {
		event.NodeID = nodeIDFromAgentOpsSubject(subject)
	}
}

func nodeIDFromAgentOpsSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 3 && parts[0] == "constellation" && parts[1] == "agentops" {
		return parts[2]
	}
	return ""
}

func applyAgentOpsPayload(event *apiservices.AgentOpsEvent, values map[string]interface{}) {
	deriveAgentOpsPayload(event, values)
	if event.EventID == "" {
		event.EventID = stringFromMap(values, "event_id", "global_id", "id")
	}
	if event.NodeID == "" {
		event.NodeID = stringFromMap(values, "node_id", "origin_node", "node", "machine")
	}
	if event.SessionID == "" {
		event.SessionID = stringFromMap(values, "session_id", "session", "pane_id")
	}
	if event.TeamID == "" {
		event.TeamID = stringFromMap(values, "team_id", "team")
	}
	if event.AgentLabel == "" {
		event.AgentLabel = stringFromMap(values, "agent_label", "agent", "role")
	}
	if event.Model == "" {
		event.Model = stringFromMap(values, "model")
	}
	if event.Role == "" {
		event.Role = stringFromMap(values, "role")
	}
	if event.Workspace == "" {
		event.Workspace = stringFromMap(values, "workspace", "cwd")
	}
	if event.PaneID == "" {
		event.PaneID = stringFromMap(values, "pane_id")
	}
	if event.Status == "" {
		event.Status = stringFromMap(values, "status")
	}
	if event.Severity == "" {
		event.Severity = stringFromMap(values, "severity")
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = timeFromMap(values, "observed_at", "timestamp", "created_at")
	}
}

func deriveAgentOpsPayload(event *apiservices.AgentOpsEvent, values map[string]interface{}) {
	if event.EventType == apiservices.AgentOpsEventToolCall && stringFromMap(values, "target") == "" {
		if target := targetFromAgentOpsArgs(values["args"]); target != "" {
			values["target"] = target
		}
	}
}

func targetFromAgentOpsArgs(args interface{}) string {
	values, ok := args.(map[string]interface{})
	if !ok {
		return ""
	}
	return stringFromMap(values, "target", "pane_target", "team_name", "session_name", "name", "project", "file_path")
}

func stringFromMap(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if value == nil {
				continue
			}
			s := strings.TrimSpace(fmt.Sprint(value))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func timeFromMap(values map[string]interface{}, keys ...string) time.Time {
	raw := stringFromMap(values, keys...)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
