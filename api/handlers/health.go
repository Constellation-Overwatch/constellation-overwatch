package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/danielgtaylor/huma/v2"
)

type HealthHandler struct {
	db   *sql.DB
	nats *embeddednats.EmbeddedNATS
}

func NewHealthHandler(db *sql.DB, nats *embeddednats.EmbeddedNATS) *HealthHandler {
	return &HealthHandler{db: db, nats: nats}
}

type HealthOutput struct {
	Body shared.HealthStatus
}

func (h *HealthHandler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health-check",
		Method:      http.MethodGet,
		Path:        "/v1/health",
		Summary:     "Health check",
		Description: "Get the health status of the API and its dependencies including NATS JetStream",
		Tags:        []string{"System"},
	}, func(ctx context.Context, input *struct{}) (*HealthOutput, error) {
		health := shared.HealthStatus{
			Status:    "healthy",
			Service:   "constellation-overwatch",
			Timestamp: time.Now(),
			Details:   make(map[string]string),
		}

		// Database
		if err := h.db.Ping(); err != nil {
			health.Status = "unhealthy"
			health.Details["database"] = "unhealthy: " + err.Error()
		} else {
			health.Details["database"] = "healthy"
		}

		// NATS connection
		if err := h.nats.HealthCheck(); err != nil {
			health.Status = "unhealthy"
			health.Details["nats"] = "unhealthy: " + err.Error()
		} else {
			health.Details["nats"] = "healthy"
		}

		// NATS server stats (native server.Varz)
		if v := h.nats.Varz(); v != nil {
			health.Details["nats_connections"] = fmt.Sprintf("%d", v.Connections)
			health.Details["nats_uptime"] = v.Uptime
		}

		// JetStream stats (native server.JSInfo)
		if j := h.nats.Jsz(); j != nil {
			health.Details["jetstream"] = "healthy"
			health.Details["jetstream_streams"] = fmt.Sprintf("%d", j.Streams)
			health.Details["jetstream_consumers"] = fmt.Sprintf("%d", j.Consumers)
			health.Details["jetstream_messages"] = fmt.Sprintf("%d", j.Messages)
			health.Details["jetstream_memory_used"] = fmt.Sprintf("%d", j.Memory)
			health.Details["jetstream_store_used"] = fmt.Sprintf("%d", j.Store)
		} else {
			health.Status = "unhealthy"
			health.Details["jetstream"] = "unavailable"
		}

		return &HealthOutput{Body: health}, nil
	})
}
