package api

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/handlers"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func NewRouter(db *sql.DB, nats *embeddednats.EmbeddedNATS) http.Handler {
	r := chi.NewRouter()

	// Services
	orgService := services.NewOrganizationService(db, nats)
	entityService := services.NewEntityService(db, nats)

	// Handlers
	healthHandler := handlers.NewHealthHandler(db, nats)
	orgHandler := handlers.NewOrganizationHandler(orgService)
	entityHandler := handlers.NewEntityHandler(entityService)
	monitorHandler := handlers.NewMonitorHandler()

	// API key authentication middleware
	apiKeyAuth := middleware.NewAPIKeyMiddleware(db)

	// Global middleware
	r.Use(chimw.Recoverer)
	r.Use(middleware.MaxBodySize(1 << 20))
	r.Use(middleware.CORS)

	// Huma API configuration (shared OpenAPI spec)
	config := huma.DefaultConfig("Constellation Overwatch API", "1.0.0")
	config.Info.Description = "C4ISR server mesh for agentic drones, robots, sensors, and video streams"
	// Server URL tells Huma the mount prefix — the docs page uses it to
	// construct the correct apiDescriptionUrl (e.g. /api/openapi.yaml).
	config.OpenAPI.Servers = []*huma.Server{{URL: "/api"}}
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"APIKeyAuth": {
			Type: "apiKey",
			Name: "X-API-Key",
			In:   "header",
		},
		"BearerAuth": {
			Type:   "http",
			Scheme: "bearer",
		},
	}

	// Public Huma API — serves /docs and /openapi.json on the main router
	publicAPI := humachi.New(r, config)
	_ = publicAPI

	// SSE monitor — raw chi (long-lived, not REST)
	r.With(
		apiKeyAuth.Authenticate,
		middleware.RequireScope("admin"),
		middleware.LimitConcurrentFor(4, time.Hour),
	).Get("/v1/sys/monitor/sse", monitorHandler.SSE)

	// Authenticated sub-router — chi middleware for auth + timeout
	authR := chi.NewRouter()
	authR.Use(chimw.Throttle(100))
	authR.Use(chimw.Timeout(30 * time.Second))
	authR.Use(apiKeyAuth.Authenticate)

	// Auth Huma API — shares the same OpenAPI spec, no duplicate doc routes
	authConfig := config
	authConfig.OpenAPIPath = ""
	authConfig.DocsPath = ""
	authConfig.SchemasPath = ""
	authAPI := humachi.New(authR, authConfig)

	// Scope enforcement via Huma middleware (reads context set by chi auth middleware)
	authAPI.UseMiddleware(scopeMiddleware(authAPI))
	healthHandler.Register(authAPI)

	// Register CRUD operations (written to shared spec, served on auth router)
	orgHandler.Register(authAPI)
	entityHandler.Register(authAPI)

	// Mount auth router into main router
	r.Mount("/", authR)

	return r
}

// scopeMiddleware enforces per-operation scope requirements by reading the
// Security annotations from the OpenAPI operation and checking against the
// scopes placed in context by the API key auth middleware.
func scopeMiddleware(api huma.API) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if len(op.Security) == 0 {
			next(ctx)
			return
		}

		scopes := middleware.ScopesFromContext(ctx.Context())

		for _, secReq := range op.Security {
			for _, requiredScopes := range secReq {
				for _, scope := range requiredScopes {
					if !middleware.HasScope(scopes, scope) {
						huma.WriteErr(api, ctx, http.StatusForbidden, "Insufficient scope: "+scope)
						return
					}
				}
			}
		}

		next(ctx)
	}
}
