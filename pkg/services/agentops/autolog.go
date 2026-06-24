package agentops

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

const defaultAutologInterval = 10 * time.Second

type AutologConfig struct {
	Enabled      bool
	NodeID       string
	PollInterval time.Duration
	Roots        []string
	Backfill     bool
}

type AutologObserver struct {
	cfg     AutologConfig
	service *apiservices.AgentOpsService

	mu       sync.Mutex
	files    map[string]*autologFileState
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	readFile func(string, int64) ([]byte, int64, error)
}

type autologFileState struct {
	offset    int64
	sessionID string
	workspace string
	model     string
}

type autologEntry struct {
	Provider   string
	Role       string
	Message    string
	SessionID  string
	Workspace  string
	Model      string
	ObservedAt time.Time
}

func DefaultAutologConfig() AutologConfig {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}

	return AutologConfig{
		Enabled:      shared.GetEnv("AGENTOPS_AUTOLOG_ENABLED", "true") != "false",
		NodeID:       shared.GetEnv("AGENTOPS_NODE_ID", hostname),
		PollInterval: parseDurationEnv("AGENTOPS_AUTOLOG_INTERVAL", defaultAutologInterval),
		Roots:        autologRootsFromEnv(),
		Backfill:     shared.GetEnv("AGENTOPS_AUTOLOG_BACKFILL", "false") == "true",
	}
}

func NewAutologObserver(db *sql.DB, nats *embeddednats.EmbeddedNATS, cfg AutologConfig) *AutologObserver {
	cfg = normalizeAutologConfig(cfg)
	return &AutologObserver{
		cfg:      cfg,
		service:  apiservices.NewAgentOpsService(db, nats),
		files:    make(map[string]*autologFileState),
		readFile: readAutologFileFromOffset,
	}
}

func (o *AutologObserver) Name() string {
	return "agentops-autolog"
}

func (o *AutologObserver) HealthCheck() error {
	return nil
}

func (o *AutologObserver) Start(ctx context.Context) error {
	if !o.cfg.Enabled {
		logger.Info("Agent Ops autolog observer disabled")
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

func (o *AutologObserver) Stop(ctx context.Context) error {
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

func (o *AutologObserver) loop(ctx context.Context) {
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

func (o *AutologObserver) poll(ctx context.Context) {
	files := o.discoverFiles()
	for _, path := range files {
		if err := o.processFile(ctx, path); err != nil {
			logger.Debugw("Agent Ops autolog file skipped", "path", path, "error", err)
		}
	}
}

func (o *AutologObserver) processFile(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}

	o.mu.Lock()
	state := o.files[path]
	if state == nil {
		offset := info.Size()
		if o.cfg.Backfill {
			offset = 0
		}
		state = &autologFileState{offset: offset}
		o.files[path] = state
	}
	offset := state.offset
	o.mu.Unlock()

	data, nextOffset, err := o.readFile(path, offset)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineOffset := offset
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineOffset += int64(len(scanner.Text())) + 1
		if line == "" {
			continue
		}
		entry, ok := parseAutologLine(path, line, state)
		if !ok {
			continue
		}
		if err := o.recordEntry(ctx, path, lineOffset, entry); err != nil {
			logger.Warnw("Agent Ops autolog entry rejected", "path", path, "error", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	o.mu.Lock()
	state.offset = nextOffset
	o.mu.Unlock()
	return nil
}

func (o *AutologObserver) recordEntry(ctx context.Context, path string, offset int64, entry autologEntry) error {
	sessionID := strings.TrimSpace(entry.SessionID)
	if sessionID == "" {
		sessionID = filepath.Base(path)
	}

	_, err := o.service.RecordSessionEntry(ctx, apiservices.CreateAgentOpsSessionEntry{
		GlobalID:   stableAutologEventID(path, offset, entry.Message),
		NodeID:     o.cfg.NodeID,
		SessionID:  sessionID,
		AgentLabel: strings.TrimSpace(entry.Role),
		Provider:   entry.Provider,
		Role:       entry.Role,
		Model:      entry.Model,
		Workspace:  entry.Workspace,
		Source:     "conversation",
		SourcePath: path,
		Message:    entry.Message,
		ObservedAt: entry.ObservedAt,
	})
	return err
}

func (o *AutologObserver) discoverFiles() []string {
	seen := map[string]struct{}{}
	var files []string
	for _, root := range o.cfg.Roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if isAutologFile(root) {
				files = append(files, root)
			}
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !isAutologFile(path) {
				return nil
			}
			if _, ok := seen[path]; ok {
				return nil
			}
			seen[path] = struct{}{}
			files = append(files, path)
			return nil
		})
	}
	return files
}

func parseAutologLine(path, line string, state *autologFileState) (autologEntry, bool) {
	provider := providerFromPath(path)
	switch provider {
	case "codex":
		return parseCodexAutologLine(line, path, state)
	case "claude":
		return parseClaudeAutologLine(line, path, state)
	case "gemini":
		return parseGeminiAutologLine(line, path, state)
	default:
		return autologEntry{}, false
	}
}

func parseCodexAutologLine(line, path string, state *autologFileState) (autologEntry, bool) {
	var raw struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Message   string `json:"message"`
			Text      string `json:"text"`
			ID        string `json:"id"`
			Cwd       string `json:"cwd"`
			Model     string `json:"model"`
			Timestamp string `json:"timestamp"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return autologEntry{}, false
	}
	if raw.Type == "session_meta" || raw.Type == "turn_context" {
		if raw.Payload.ID != "" {
			state.sessionID = raw.Payload.ID
		}
		if raw.Payload.Cwd != "" {
			state.workspace = raw.Payload.Cwd
		}
		if raw.Payload.Model != "" {
			state.model = raw.Payload.Model
		}
		return autologEntry{}, false
	}

	role := raw.Payload.Role
	message := ""
	switch raw.Type {
	case "event_msg":
		switch raw.Payload.Type {
		case "user_message":
			role = "user"
		case "assistant_message", "agent_message":
			role = "assistant"
		default:
			return autologEntry{}, false
		}
		message = firstNonEmpty(raw.Payload.Message, raw.Payload.Text)
	case "response_item":
		if raw.Payload.Type != "message" {
			return autologEntry{}, false
		}
		message = codexContentText(raw.Payload.Content, role)
	default:
		return autologEntry{}, false
	}
	if strings.TrimSpace(message) == "" || role == "" {
		return autologEntry{}, false
	}
	return autologEntry{
		Provider:   "codex",
		Role:       role,
		Message:    message,
		SessionID:  firstNonEmpty(state.sessionID, raw.Payload.ID, filepath.Base(path)),
		Workspace:  firstNonEmpty(state.workspace, raw.Payload.Cwd),
		Model:      firstNonEmpty(state.model, raw.Payload.Model, "codex"),
		ObservedAt: parseAutologTime(firstNonEmpty(raw.Payload.Timestamp, raw.Timestamp)),
	}, true
}

func parseClaudeAutologLine(line, path string, state *autologFileState) (autologEntry, bool) {
	var raw struct {
		Timestamp string          `json:"timestamp"`
		SessionID string          `json:"sessionId"`
		Cwd       string          `json:"cwd"`
		IsMeta    bool            `json:"isMeta"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil || raw.IsMeta {
		return autologEntry{}, false
	}
	var msg struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return autologEntry{}, false
	}
	message := claudeContentText(msg.Content)
	if strings.TrimSpace(message) == "" || msg.Role == "" {
		return autologEntry{}, false
	}
	if raw.SessionID != "" {
		state.sessionID = raw.SessionID
	}
	if raw.Cwd != "" {
		state.workspace = raw.Cwd
	}
	if msg.Model != "" {
		state.model = msg.Model
	}
	return autologEntry{
		Provider:   "claude",
		Role:       msg.Role,
		Message:    message,
		SessionID:  firstNonEmpty(state.sessionID, raw.SessionID, filepath.Base(path)),
		Workspace:  firstNonEmpty(state.workspace, raw.Cwd),
		Model:      firstNonEmpty(state.model, msg.Model, "claude"),
		ObservedAt: parseAutologTime(raw.Timestamp),
	}, true
}

func parseGeminiAutologLine(line, path string, state *autologFileState) (autologEntry, bool) {
	var raw struct {
		Timestamp string          `json:"timestamp"`
		SessionID string          `json:"sessionId"`
		Type      string          `json:"type"`
		Message   string          `json:"message"`
		Content   json.RawMessage `json:"content"`
		Model     string          `json:"model"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return autologEntry{}, false
	}
	role := ""
	switch strings.ToLower(strings.TrimSpace(raw.Type)) {
	case "user":
		role = "user"
	case "model", "assistant":
		role = "assistant"
	default:
		return autologEntry{}, false
	}
	message := firstNonEmpty(raw.Message, geminiContentText(raw.Content))
	if strings.TrimSpace(message) == "" {
		return autologEntry{}, false
	}
	if raw.SessionID != "" {
		state.sessionID = raw.SessionID
	}
	if raw.Model != "" {
		state.model = raw.Model
	}
	return autologEntry{
		Provider:   "gemini",
		Role:       role,
		Message:    message,
		SessionID:  firstNonEmpty(state.sessionID, raw.SessionID, filepath.Base(path)),
		Workspace:  state.workspace,
		Model:      firstNonEmpty(state.model, raw.Model, "gemini"),
		ObservedAt: parseAutologTime(raw.Timestamp),
	}, true
}

func codexContentText(items []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}, role string) string {
	var parts []string
	for _, item := range items {
		if role == "user" && item.Type == "input_text" || role == "assistant" && item.Type == "output_text" {
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, strings.TrimSpace(item.Text))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func claudeContentText(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	var parts []string
	for _, item := range items {
		if (item.Type == "text" || item.Type == "input_text") && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func geminiContentText(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}

func readAutologFileFromOffset(path string, offset int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, offset, err
	}
	return data, offset + int64(len(data)), nil
}

func autologRootsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("AGENTOPS_AUTOLOG_PATHS"))
	if raw != "" {
		return strings.FieldsFunc(raw, func(r rune) bool { return r == ':' || r == ',' })
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".codex", "sessions"),
		filepath.Join(home, ".codex", "history.jsonl"),
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".gemini", "history"),
	}
}

func normalizeAutologConfig(cfg AutologConfig) AutologConfig {
	defaults := DefaultAutologConfig()
	if cfg.NodeID == "" {
		cfg.NodeID = defaults.NodeID
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if len(cfg.Roots) == 0 {
		cfg.Roots = defaults.Roots
	}
	return cfg
}

func isAutologFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".jsonl" || ext == ".json"
}

func providerFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, ".codex"):
		return "codex"
	case strings.Contains(lower, ".claude"):
		return "claude"
	case strings.Contains(lower, ".gemini"):
		return "gemini"
	default:
		return ""
	}
}

func parseAutologTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func stableAutologEventID(path string, offset int64, message string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%s:%d:%s", path, offset, message)))
	return fmt.Sprintf("autolog-%x", h.Sum64())
}
