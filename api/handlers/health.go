package handlers

import (
	"context"
	"database/sql"
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
		Description: "Get the health status of the API and its dependencies",
		Tags:        []string{"System"},
		Security:    []map[string][]string{{"APIKeyAuth": {"admin"}}},
	}, func(ctx context.Context, input *struct{}) (*HealthOutput, error) {
		health := shared.HealthStatus{
			Status:    "healthy",
			Service:   "constellation-overwatch",
			Timestamp: time.Now(),
			Details:   make(map[string]string),
		}

		if err := h.db.Ping(); err != nil {
			health.Status = "unhealthy"
			health.Details["database"] = "unhealthy: " + err.Error()
		} else {
			health.Details["database"] = "healthy"
		}

		if err := h.nats.HealthCheck(); err != nil {
			health.Status = "unhealthy"
			health.Details["nats"] = "unhealthy: " + err.Error()
		} else {
			health.Details["nats"] = "healthy"
		}

		return &HealthOutput{Body: health}, nil
	})
}
