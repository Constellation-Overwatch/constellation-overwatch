package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	agentops_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/agentops/pages"
)

type AgentOpsHandler struct {
	agentOpsSvc *services.AgentOpsService
}

func NewAgentOpsHandler(agentOpsSvc *services.AgentOpsService) *AgentOpsHandler {
	return &AgentOpsHandler{agentOpsSvc: agentOpsSvc}
}

func (h *AgentOpsHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.agentOpsSvc.Summary(r.Context())
	if err != nil {
		logger.Errorw("Failed to load agent ops summary", "error", err)
		sendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load agent ops summary")
		return
	}

	sendSuccess(w, http.StatusOK, summary)
}

func (h *AgentOpsHandler) HandleKnowledge(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	windowHours := parsePositiveQueryInt(r, "window_hours", 168)
	limit := parsePositiveQueryInt(r, "limit", 8)

	knowledge, err := h.agentOpsSvc.KnowledgeGradient(r.Context(), query, windowHours, limit)
	if err != nil {
		logger.Errorw("Failed to load agent ops knowledge gradient", "error", err)
		sendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load agent ops knowledge gradient")
		return
	}

	sendSuccess(w, http.StatusOK, knowledge)
}

func (h *AgentOpsHandler) HandleLaunch(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	req, err := decodeAgentOpsLaunchRequest(r)
	if err != nil {
		sendError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	req.OrgID = middleware.OrgIDFromContext(r.Context())
	if req.OrgID == "" {
		req.OrgID = "default"
	}
	req.RequestedBy = middleware.UserIDFromContext(r.Context())

	launch, err := h.agentOpsSvc.CreateLaunchRequest(r.Context(), req)
	if err != nil {
		logger.Errorw("Failed to create agent launch request", "error", err)
		sendError(w, http.StatusBadGateway, "LAUNCH_REQUEST_FAILED", "Failed to queue launch request")
		return
	}

	if acceptsEventStream(r) {
		summary, err := h.agentOpsSvc.Summary(r.Context())
		if err != nil {
			logger.Warnw("Failed to reload agent ops summary after launch request", "error", err)
			sendSuccess(w, http.StatusCreated, launch)
			return
		}

		sse := datastar.NewSSE(w, r)
		if err := sse.PatchElementTempl(agentops_pages.AgentOpsPanel(summary, activeAgentOpsTab(r, "launches")),
			datastar.WithSelector("#agentops-panel"),
			datastar.WithModeOuter()); err != nil {
			logger.Warnw("Failed to patch agent ops panel after launch request", "error", err)
		}
		return
	}

	sendSuccess(w, http.StatusCreated, launch)
}

func (h *AgentOpsHandler) HandleToolCall(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAgentOpsToolCallRequest(r)
	if err != nil {
		sendError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	call, err := h.agentOpsSvc.RecordToolCall(r.Context(), req)
	if err != nil {
		sendError(w, http.StatusBadRequest, "TOOL_CALL_REJECTED", err.Error())
		return
	}

	sendSuccess(w, http.StatusCreated, call)
}

func (h *AgentOpsHandler) HandleSessionEntry(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAgentOpsSessionEntryRequest(r)
	if err != nil {
		sendError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	entry, err := h.agentOpsSvc.RecordSessionEntry(r.Context(), req)
	if err != nil {
		sendError(w, http.StatusBadRequest, "SESSION_ENTRY_REJECTED", err.Error())
		return
	}

	sendSuccess(w, http.StatusCreated, entry)
}

func (h *AgentOpsHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sse := datastar.NewSSE(w, r)
	activeTab := activeAgentOpsTab(r, "overview")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	patch := func() bool {
		summary, err := h.agentOpsSvc.Summary(r.Context())
		if err != nil {
			logger.Warnw("Failed to stream agent ops summary", "error", err)
			return false
		}
		if err := sse.PatchElementTempl(agentops_pages.AgentOpsPanel(summary, activeTab),
			datastar.WithSelector("#agentops-panel"),
			datastar.WithModeOuter()); err != nil {
			logger.Warnw("Failed to patch agent ops panel", "error", err)
			return false
		}
		return true
	}

	if !patch() {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !patch() {
				return
			}
		}
	}
}

func decodeAgentOpsLaunchRequest(r *http.Request) (services.CreateAgentOpsLaunchRequest, error) {
	var req services.CreateAgentOpsLaunchRequest

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
		return req, nil
	}

	if err := r.ParseForm(); err != nil {
		return req, err
	}

	req.TeamName = r.FormValue("team_name")
	req.Template = r.FormValue("template")
	req.TargetNodeID = r.FormValue("target_node_id")
	req.Workspace = r.FormValue("workspace")
	req.Mission = r.FormValue("mission")
	req.ModelRoute = r.FormValue("model_route")
	if rawCount := strings.TrimSpace(r.FormValue("agent_count")); rawCount != "" {
		count, err := strconv.Atoi(rawCount)
		if err != nil {
			return req, err
		}
		req.AgentCount = count
	}

	return req, nil
}

func decodeAgentOpsToolCallRequest(r *http.Request) (services.CreateAgentOpsToolCall, error) {
	var req services.CreateAgentOpsToolCall

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
		return req, nil
	}

	if err := r.ParseForm(); err != nil {
		return req, err
	}

	req.GlobalID = r.FormValue("global_id")
	req.OriginNode = r.FormValue("origin_node")
	req.NodeID = r.FormValue("node_id")
	req.SessionID = r.FormValue("session_id")
	req.TeamID = r.FormValue("team_id")
	req.AgentLabel = r.FormValue("agent_label")
	req.Tool = r.FormValue("tool")
	req.Result = r.FormValue("result")
	req.Error = r.FormValue("error")
	req.Source = r.FormValue("source")

	if rawArgs := strings.TrimSpace(r.FormValue("args_json")); rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &req.Args); err != nil {
			return req, err
		}
	}

	return req, nil
}

func decodeAgentOpsSessionEntryRequest(r *http.Request) (services.CreateAgentOpsSessionEntry, error) {
	var req services.CreateAgentOpsSessionEntry

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
		return req, nil
	}

	if err := r.ParseForm(); err != nil {
		return req, err
	}

	req.GlobalID = r.FormValue("global_id")
	req.OriginNode = r.FormValue("origin_node")
	req.NodeID = r.FormValue("node_id")
	req.SessionID = r.FormValue("session_id")
	req.TeamID = r.FormValue("team_id")
	req.AgentLabel = r.FormValue("agent_label")
	req.Provider = r.FormValue("provider")
	req.Role = r.FormValue("role")
	req.Model = r.FormValue("model")
	req.Workspace = r.FormValue("workspace")
	req.Source = r.FormValue("source")
	req.SourcePath = r.FormValue("source_path")
	req.TurnID = r.FormValue("turn_id")
	req.Message = r.FormValue("message")
	req.Content = r.FormValue("content")
	if rawSequence := strings.TrimSpace(r.FormValue("sequence")); rawSequence != "" {
		sequence, err := strconv.Atoi(rawSequence)
		if err != nil {
			return req, err
		}
		req.Sequence = sequence
	}

	return req, nil
}

func acceptsEventStream(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func activeAgentOpsTab(r *http.Request, fallback string) string {
	if tab := agentops_pages.NormalizeAgentOpsTab(r.URL.Query().Get("tab")); tab != "overview" {
		return tab
	}
	return agentops_pages.NormalizeAgentOpsTab(fallback)
}

func parsePositiveQueryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
