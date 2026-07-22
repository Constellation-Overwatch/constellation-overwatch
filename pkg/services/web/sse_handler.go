package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

// SSEHandler handles Server-Sent Events streaming
type SSEHandler struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// NewSSEHandler creates a new SSE handler
func NewSSEHandler(nc *nats.Conn, js nats.JetStreamContext) *SSEHandler {
	return &SSEHandler{
		nc: nc,
		js: js,
	}
}

// StreamMessages streams NATS messages to the client via SSE
func (h *SSEHandler) StreamMessages(w http.ResponseWriter, r *http.Request) {
	orgID, ok := streamOrganizationScope(r)
	if !ok {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	sse := datastar.NewSSE(w, r)

	// Subscribe to all constellation subjects
	sub, err := h.nc.Subscribe("constellation.>", func(msg *nats.Msg) {
		if !subjectAllowedForOrganization(msg.Subject, orgID) {
			return
		}
		// Try to parse as JSON for pretty formatting
		var displayData string
		var data interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			// Not valid JSON, just display raw data
			displayData = string(msg.Data)
		} else {
			// Valid JSON, format it nicely
			prettyJSON, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				displayData = string(msg.Data)
			} else {
				displayData = string(prettyJSON)
			}
		}

		// Create a stream message element
		timestamp := time.Now().Format("15:04:05")
		messageHTML := renderStreamMessage(msg.Subject, timestamp, displayData)

		// Patch the element into the stream (prepend new messages at top)
		patchErr := sse.PatchElements(messageHTML,
			datastar.WithSelector("#stream-messages"),
			datastar.WithModePrepend())
		if patchErr != nil {
			logger.Errorw("Error patching elements", "component", "SSE", "error", patchErr)
			return
		}
	})

	if err != nil {
		logger.Errorw("Error subscribing to NATS", "component", "SSE", "error", err)
		http.Error(w, "Failed to subscribe to stream", http.StatusInternalServerError)
		return
	}
	defer sub.Unsubscribe()

	logger.Infow("SSE client connected", "component", "SSE", "remote_addr", r.RemoteAddr)

	// Send initial connection message
	initialHTML := `<div class="empty-state">Connected to stream. Waiting for messages...</div>`
	sse.PatchElements(initialHTML,
		datastar.WithSelector("#stream-messages"),
		datastar.WithModeInner())

	// Keep the connection alive and send heartbeats
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			logger.Infow("SSE client disconnected", "component", "SSE", "remote_addr", r.RemoteAddr)
			return
		case <-ticker.C:
			// Send a comment as heartbeat to keep connection alive
			fmt.Fprintf(w, ": heartbeat\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

// renderStreamMessage renders a stream message HTML
func renderStreamMessage(subject, timestamp, data string) string {
	return fmt.Sprintf(`
		<div class="stream-message" data-subject="%s">
			<div class="msg-header">
				<span class="msg-subject">%s</span>
				<span class="msg-time">%s</span>
			</div>
			<div class="msg-body">
				<div class="msg-data"><pre>%s</pre></div>
			</div>
		</div>
	`, subject, subject, timestamp, data)
}

// StreamMessagesWithFilter streams filtered NATS messages
func (h *SSEHandler) StreamMessagesWithFilter(w http.ResponseWriter, r *http.Request) {
	orgID, ok := streamOrganizationScope(r)
	if !ok {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	// Get filter from query params
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}

	sse := datastar.NewSSE(w, r)

	// Determine which subjects to subscribe to based on filter
	var subjects []string
	switch filter {
	case "entities":
		subjects = []string{"constellation.entities.>"}
	case "events":
		subjects = []string{"constellation.events.>"}
	case "telemetry":
		subjects = []string{"constellation.telemetry.>"}
	case "commands":
		subjects = []string{"constellation.commands.>"}
	default:
		subjects = []string{"constellation.>"}
	}

	// Subscribe to the filtered subjects
	var subs []*nats.Subscription
	for _, subj := range subjects {
		sub, err := h.nc.Subscribe(subj, func(msg *nats.Msg) {
			if !subjectAllowedForOrganization(msg.Subject, orgID) {
				return
			}
			// Try to parse as JSON for pretty formatting
			var displayData string
			var data interface{}
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				// Not valid JSON, just display raw data
				displayData = string(msg.Data)
			} else {
				// Valid JSON, format it nicely
				prettyJSON, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					displayData = string(msg.Data)
				} else {
					displayData = string(prettyJSON)
				}
			}

			// Determine message type for styling
			msgType := getMessageType(msg.Subject)

			// Create a stream message element with type
			timestamp := time.Now().Format("15:04:05")
			messageHTML := renderStreamMessageWithType(msg.Subject, timestamp, msgType, displayData)

			// Patch the element into the stream
			patchErr := sse.PatchElements(messageHTML,
				datastar.WithSelector("#stream-messages"),
				datastar.WithModePrepend())
			if patchErr != nil {
				logger.Errorw("Error patching elements", "component", "SSE", "error", patchErr)
				return
			}
		})

		if err != nil {
			logger.Errorw("Error subscribing to NATS", "component", "SSE", "error", err)
			http.Error(w, "Failed to subscribe to stream", http.StatusInternalServerError)
			return
		}
		subs = append(subs, sub)
	}

	// Cleanup subscriptions on disconnect
	defer func() {
		for _, sub := range subs {
			sub.Unsubscribe()
		}
	}()

	logger.Infow("SSE client connected with filter", "component", "SSE", "remote_addr", r.RemoteAddr, "filter", filter)

	// Send initial connection message
	initialHTML := fmt.Sprintf(`<div class="empty-state">Connected to stream (filter: %s). Waiting for messages...</div>`, filter)
	sse.PatchElements(initialHTML,
		datastar.WithSelector("#stream-messages"),
		datastar.WithModeInner())

	// Keep the connection alive
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			logger.Infow("SSE client disconnected", "component", "SSE", "remote_addr", r.RemoteAddr)
			return
		case <-ticker.C:
			// Heartbeat
			fmt.Fprintf(w, ": heartbeat\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func streamOrganizationScope(r *http.Request) (string, bool) {
	role := middleware.UserRoleFromContext(r.Context())
	if role == shared.RoleAdmin {
		return "", true
	}
	if role != shared.RoleOperator && role != shared.RoleViewer {
		return "", false
	}
	orgID := middleware.OrgIDFromContext(r.Context())
	return orgID, orgID != ""
}

// subjectAllowedForOrganization recognizes the versioned Constellation
// data-plane layouts. Events support both organization-first and the legacy
// one-category prefix used by ISR detections; unknown layouts fail closed.
func subjectAllowedForOrganization(subject, orgID string) bool {
	if orgID == "" {
		return true
	}
	parts := strings.Split(subject, ".")
	if len(parts) < 3 || parts[0] != "constellation" {
		return false
	}

	switch parts[1] {
	case "entities", "telemetry", "commands":
		return parts[2] == orgID
	case "events":
		return parts[2] == orgID || (len(parts) > 3 && parts[3] == orgID)
	default:
		return false
	}
}

// getMessageType extracts message type from subject
func getMessageType(subject string) string {
	if len(subject) > 21 && subject[:21] == "constellation.entities" {
		if len(subject) > 22 {
			parts := subject[22:]
			if len(parts) > 0 {
				lastDot := -1
				for i := len(parts) - 1; i >= 0; i-- {
					if parts[i] == '.' {
						lastDot = i
						break
					}
				}
				if lastDot > 0 {
					action := parts[lastDot+1:]
					return "Entity " + action
				}
			}
		}
		return "Entity Event"
	} else if len(subject) > 22 && subject[:22] == "constellation.telemetry" {
		return "Telemetry"
	} else if len(subject) > 21 && subject[:21] == "constellation.commands" {
		return "Command"
	} else if len(subject) > 19 && subject[:19] == "constellation.events" {
		return "Event"
	} else if len(subject) > 25 && subject[:25] == "constellation.organizations" {
		return "Organization Event"
	}

	return "Message"
}

// renderStreamMessageWithType renders a stream message with type
func renderStreamMessageWithType(subject, timestamp, msgType, data string) string {
	return fmt.Sprintf(`
		<div class="stream-message" data-subject="%s">
			<div class="msg-header">
				<span class="msg-subject">%s</span>
				<span class="msg-time">%s</span>
			</div>
			<div class="msg-type">%s</div>
			<div class="msg-body">
				<div class="msg-data"><pre>%s</pre></div>
			</div>
		</div>
	`, subject, subject, timestamp, msgType, data)
}
