package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultKnowledgeWindowHours = 168
	defaultKnowledgeLimit       = 8
	maxKnowledgeWindowHours     = 8760
	maxKnowledgeLimit           = 20
)

var agentOpsTermPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_./-]{2,}`)

var agentOpsStopwords = map[string]struct{}{
	"about": {}, "active": {}, "agent": {}, "agents": {}, "and": {}, "are": {}, "for": {}, "from": {},
	"has": {}, "have": {}, "into": {}, "mission": {}, "node": {}, "none": {}, "observed": {}, "output": {},
	"pane": {}, "request": {}, "session": {}, "status": {}, "that": {}, "the": {}, "this": {}, "tool": {},
	"unknown": {}, "with": {}, "workspace": {},
}

type AgentOpsKnowledgeTopic struct {
	Subject    string    `json:"subject"`
	Heat       float64   `json:"heat"`
	EventCount int       `json:"event_count"`
	Agents     []string  `json:"agents,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
	Query      string    `json:"query"`
}

type AgentOpsKnowledgeHit struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	NodeID     string    `json:"node_id"`
	SessionID  string    `json:"session_id"`
	AgentLabel string    `json:"agent_label"`
	Provider   string    `json:"provider"`
	Role       string    `json:"role"`
	Workspace  string    `json:"workspace"`
	Preview    string    `json:"preview"`
	ObservedAt time.Time `json:"observed_at"`
}

type AgentOpsKnowledgeGradient struct {
	Query              string                   `json:"query,omitempty"`
	WindowHours        int                      `json:"window_hours"`
	TotalEvents        int                      `json:"total_events"`
	HotTopics          []AgentOpsKnowledgeTopic `json:"hot_topics"`
	RelatedTopics      []AgentOpsKnowledgeTopic `json:"related_topics,omitempty"`
	RecentParticipants []string                 `json:"recent_participants,omitempty"`
	SuggestedQueries   []string                 `json:"suggested_queries,omitempty"`
	Hits               []AgentOpsKnowledgeHit   `json:"hits,omitempty"`
	Capsule            string                   `json:"capsule"`
}

type agentOpsKnowledgeRow struct {
	event   AgentOpsStoredEvent
	payload map[string]interface{}
	text    string
	agent   string
}

func (s *AgentOpsService) KnowledgeGradient(ctx context.Context, query string, windowHours, limit int) (*AgentOpsKnowledgeGradient, error) {
	if s.db == nil {
		return nil, fmt.Errorf("agent ops database is nil")
	}

	query = normalizeKnowledgeText(query)
	if windowHours <= 0 {
		windowHours = defaultKnowledgeWindowHours
	}
	if windowHours > maxKnowledgeWindowHours {
		windowHours = maxKnowledgeWindowHours
	}
	if limit <= 0 {
		limit = defaultKnowledgeLimit
	}
	if limit > maxKnowledgeLimit {
		limit = maxKnowledgeLimit
	}

	since := time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour)
	rows, err := s.listKnowledgeRows(ctx, since)
	if err != nil {
		return nil, err
	}

	queryTerms := significantAgentOpsTerms(query)
	topics := aggregateAgentOpsTopics(rows, queryTerms)
	gradient := &AgentOpsKnowledgeGradient{
		Query:              query,
		WindowHours:        windowHours,
		TotalEvents:        len(rows),
		HotTopics:          limitAgentOpsTopics(topics, limit),
		RelatedTopics:      relatedAgentOpsTopics(topics, queryTerms, limit),
		RecentParticipants: recentAgentOpsParticipants(rows, 8),
		SuggestedQueries:   suggestedAgentOpsQueries(topics, queryTerms, 8),
		Hits:               matchingAgentOpsHits(rows, queryTerms, limit),
	}
	gradient.Capsule = renderAgentOpsKnowledgeCapsule(*gradient)
	return gradient, nil
}

func (s *AgentOpsService) listKnowledgeRows(ctx context.Context, since time.Time) ([]agentOpsKnowledgeRow, error) {
	dbRows, err := s.db.QueryContext(ctx, `
		SELECT event_id, node_id, session_id, event_type, subject, severity, payload, observed_at, created_at
		FROM agent_events
		WHERE observed_at >= ?
		ORDER BY observed_at DESC
		LIMIT 1000`, since.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("list agent ops knowledge rows: %w", err)
	}
	defer dbRows.Close()

	var rows []agentOpsKnowledgeRow
	for dbRows.Next() {
		var event AgentOpsStoredEvent
		var observedAt, createdAt string
		if err := dbRows.Scan(
			&event.EventID, &event.NodeID, &event.SessionID, &event.EventType,
			&event.Subject, &event.Severity, &event.Payload, &observedAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent ops knowledge row: %w", err)
		}
		event.ObservedAt = parseAgentOpsTime(observedAt)
		event.CreatedAt = parseAgentOpsTime(createdAt)

		payload := map[string]interface{}{}
		if event.Payload != "" {
			_ = json.Unmarshal([]byte(event.Payload), &payload)
		}
		rows = append(rows, agentOpsKnowledgeRow{
			event:   event,
			payload: payload,
			text:    agentOpsKnowledgeText(event, payload),
			agent:   firstKnowledgeValue(payloadString(payload, "agent_label"), payloadString(payload, "role"), event.SessionID, event.NodeID),
		})
	}
	if err := dbRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent ops knowledge rows: %w", err)
	}
	return rows, nil
}

func agentOpsKnowledgeText(event AgentOpsStoredEvent, payload map[string]interface{}) string {
	parts := []string{
		event.EventType,
		event.Subject,
		payloadString(payload, "message"),
		payloadString(payload, "preview"),
		payloadString(payload, "mission"),
		payloadString(payload, "team_name"),
		payloadString(payload, "tool"),
		payloadString(payload, "target"),
		payloadString(payload, "result"),
		payloadString(payload, "error"),
		payloadString(payload, "provider"),
		payloadString(payload, "role"),
		payloadString(payload, "model"),
		payloadString(payload, "workspace"),
		payloadString(payload, "source_path"),
		payloadString(payload, "last_output"),
	}
	return normalizeKnowledgeText(strings.Join(parts, " "))
}

func aggregateAgentOpsTopics(rows []agentOpsKnowledgeRow, queryTerms map[string]struct{}) []AgentOpsKnowledgeTopic {
	type acc struct {
		topic AgentOpsKnowledgeTopic
		seen  map[string]struct{}
	}

	topics := map[string]*acc{}
	for _, row := range rows {
		for term := range significantAgentOpsTerms(row.text) {
			item := topics[term]
			if item == nil {
				item = &acc{topic: AgentOpsKnowledgeTopic{Subject: term, Query: term}, seen: map[string]struct{}{}}
				topics[term] = item
			}
			item.topic.EventCount++
			item.topic.Heat += agentOpsTermHeat(term, row, queryTerms)
			if row.event.ObservedAt.After(item.topic.LastSeen) {
				item.topic.LastSeen = row.event.ObservedAt
			}
			if row.agent != "" {
				item.seen[row.agent] = struct{}{}
			}
		}
	}

	out := make([]AgentOpsKnowledgeTopic, 0, len(topics))
	for _, item := range topics {
		item.topic.Heat = math.Round(item.topic.Heat*10) / 10
		item.topic.Agents = limitAgentOpsStrings(sortedAgentOpsSet(item.seen), 4)
		out = append(out, item.topic)
	}
	sortAgentOpsTopics(out)
	return out
}

func agentOpsTermHeat(term string, row agentOpsKnowledgeRow, queryTerms map[string]struct{}) float64 {
	heat := 1.0
	if _, ok := queryTerms[term]; ok {
		heat += 4
	}
	switch row.event.EventType {
	case AgentOpsEventSessionEntry:
		heat += 2
	case AgentOpsEventToolCall:
		heat += 1.5
	case "launch.completed", "launch.failed", "launch.requested":
		heat += 1
	}
	return heat
}

func relatedAgentOpsTopics(topics []AgentOpsKnowledgeTopic, queryTerms map[string]struct{}, limit int) []AgentOpsKnowledgeTopic {
	if len(queryTerms) == 0 {
		return nil
	}
	var out []AgentOpsKnowledgeTopic
	for _, topic := range topics {
		if _, ok := queryTerms[topic.Subject]; ok {
			out = append(out, topic)
			continue
		}
		for term := range queryTerms {
			if strings.Contains(topic.Subject, term) || strings.Contains(term, topic.Subject) {
				out = append(out, topic)
				break
			}
		}
	}
	return limitAgentOpsTopics(out, limit)
}

func matchingAgentOpsHits(rows []agentOpsKnowledgeRow, queryTerms map[string]struct{}, limit int) []AgentOpsKnowledgeHit {
	if len(queryTerms) == 0 {
		return nil
	}
	var hits []AgentOpsKnowledgeHit
	for _, row := range rows {
		terms := significantAgentOpsTerms(row.text)
		matched := false
		for term := range queryTerms {
			if _, ok := terms[term]; ok || strings.Contains(row.text, term) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		hits = append(hits, AgentOpsKnowledgeHit{
			EventID:    row.event.EventID,
			EventType:  row.event.EventType,
			NodeID:     row.event.NodeID,
			SessionID:  row.event.SessionID,
			AgentLabel: payloadString(row.payload, "agent_label"),
			Provider:   payloadString(row.payload, "provider"),
			Role:       payloadString(row.payload, "role"),
			Workspace:  payloadString(row.payload, "workspace"),
			Preview:    firstKnowledgeValue(payloadString(row.payload, "preview"), payloadString(row.payload, "message"), row.event.EventType),
			ObservedAt: row.event.ObservedAt,
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits
}

func recentAgentOpsParticipants(rows []agentOpsKnowledgeRow, limit int) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, row := range rows {
		agent := strings.TrimSpace(row.agent)
		if agent == "" {
			continue
		}
		if _, ok := seen[agent]; ok {
			continue
		}
		seen[agent] = struct{}{}
		out = append(out, agent)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func suggestedAgentOpsQueries(topics []AgentOpsKnowledgeTopic, queryTerms map[string]struct{}, limit int) []string {
	var out []string
	for _, topic := range topics {
		if _, ok := queryTerms[topic.Subject]; ok {
			continue
		}
		out = append(out, topic.Subject)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func limitAgentOpsTopics(topics []AgentOpsKnowledgeTopic, limit int) []AgentOpsKnowledgeTopic {
	if limit <= 0 || len(topics) <= limit {
		return topics
	}
	return topics[:limit]
}

func sortAgentOpsTopics(topics []AgentOpsKnowledgeTopic) {
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Heat == topics[j].Heat {
			if topics[i].LastSeen.Equal(topics[j].LastSeen) {
				return topics[i].Subject < topics[j].Subject
			}
			return topics[i].LastSeen.After(topics[j].LastSeen)
		}
		return topics[i].Heat > topics[j].Heat
	})
}

func significantAgentOpsTerms(value string) map[string]struct{} {
	terms := map[string]struct{}{}
	for _, match := range agentOpsTermPattern.FindAllString(strings.ToLower(value), -1) {
		match = strings.Trim(match, "-_./")
		if len(match) < 3 {
			continue
		}
		if _, stop := agentOpsStopwords[match]; stop {
			continue
		}
		terms[match] = struct{}{}
	}
	return terms
}

func renderAgentOpsKnowledgeCapsule(gradient AgentOpsKnowledgeGradient) string {
	var b strings.Builder
	b.WriteString("AGENT OPS KNOWLEDGE GRADIENT\n")
	fmt.Fprintf(&b, "WINDOW: %dh | events=%d\n", gradient.WindowHours, gradient.TotalEvents)
	if gradient.Query != "" {
		fmt.Fprintf(&b, "QUERY: %s\n", gradient.Query)
	}
	b.WriteString("HOT SUBJECTS:\n")
	if len(gradient.HotTopics) == 0 {
		b.WriteString("  - none\n")
	} else {
		for _, topic := range gradient.HotTopics {
			fmt.Fprintf(&b, "  - %s: %.1f (%d refs)\n", topic.Subject, topic.Heat, topic.EventCount)
		}
	}
	if len(gradient.Hits) > 0 {
		b.WriteString("RECENT MATCHES:\n")
		for _, hit := range gradient.Hits {
			fmt.Fprintf(&b, "  - %s %s %s\n", hit.EventType, hit.NodeID, previewAgentOpsMessage(hit.Preview, 80))
		}
	}
	return b.String()
}

func normalizeKnowledgeText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func firstKnowledgeValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sortedAgentOpsSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func limitAgentOpsStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
