package web

import (
	"net/http"
	"net/http/pprof"
	"time"

	apimiddleware "github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/metrics"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/mediamtx"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/dev"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/handlers"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/go-chi/chi/v5"
)

func NewRouter(
	orgSvc *services.OrganizationService,
	entitySvc *services.EntityService,
	natsEmbedded *embeddednats.EmbeddedNATS,
	sseHandler *SSEHandler,
	mtxClient *mediamtx.Client,
	apiHandler http.Handler,
	sessionAuth *apimiddleware.SessionAuth,
	authSvc *services.AuthService,
	userSvc *services.UserService,
	inviteSvc *services.InviteService,
	apiKeySvc *services.APIKeyService,
	runtimeCfg runtimeconfig.Runtime,
) chi.Router {
	r := chi.NewRouter()

	// Initialize handlers
	pageHandler := handlers.NewPageHandler(orgSvc, entitySvc)
	datastarHandler := handlers.NewDatastarHandler(orgSvc, entitySvc, natsEmbedded.Connection())
	overwatchHandler := handlers.NewOverwatchHandler(natsEmbedded, orgSvc, entitySvc)
	videoHandler := handlers.NewVideoHandler(mtxClient, entitySvc)
	authHandler := handlers.NewAuthHandler(sessionAuth, authSvc, userSvc)
	inviteHandler := handlers.NewInviteHandler(inviteSvc, userSvc, authSvc, sessionAuth)
	adminHandler := handlers.NewAdminHandler(userSvc, apiKeySvc, inviteSvc, natsEmbedded)
	metricsHandler := handlers.NewMetricsHandler()
	// Capacity is isolated by role and user so a viewer cannot starve operator
	// or admin observability. Each signed-in user may hold up to four streams.
	sseLimit := apimiddleware.LimitSSEConcurrentFor(24, 8, 8, 4, 12*time.Hour)

	// Serve static files (no auth required) — uses embedded filesystem
	staticHandler, err := StaticFileServer()
	if err != nil {
		logger.Fatalw("Failed to initialize static file server", "error", err)
	}

	// ── Public (no auth) ──────────────────────────────────────────────
	r.Handle("/static/*", http.StripPrefix("/static/", staticHandler))
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(`{"status":"ok"}`))
	})
	r.HandleFunc("/logout", authHandler.HandleLogout)

	// ── Auth (rate-limited, no session required) ──────────────────────
	r.Group(func(r chi.Router) {
		r.Use(RateLimitByIPFromTrustedProxies(10, runtimeCfg.TrustedProxies))
		r.HandleFunc("/login", authHandler.HandleLogin)
		r.Post("/auth/passkey/login/begin", authHandler.HandlePasskeyLoginBegin)
		r.Post("/auth/passkey/login/finish", authHandler.HandlePasskeyLoginFinish)
		r.Get("/invite/{token}", inviteHandler.HandleAcceptInvite)
		r.Post("/invite/{token}/accept", inviteHandler.HandleFinalizeInvite)
	})

	// ── Development (hot reload) ──────────────────────────────────────
	if !runtimeCfg.Production() && dev.IsDev() {
		hotReload := dev.NewHotReload()
		r.Get("/dev/reload", hotReload.HandleReloadSSE)
		r.Get("/dev/trigger-reload", hotReload.HandleTriggerReload)
	}

	// ── Session-protected ─────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(sessionAuth.RequireSession)

		// Pages
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/map", http.StatusFound)
		})
		r.Get("/map", pageHandler.HandleMapPage)
		r.Get("/organizations", pageHandler.HandleEntitiesPage)
		r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Get("/organizations/entities/new", pageHandler.HandleEntityForm)
		r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Get("/organizations/entities/edit", pageHandler.HandleEntityForm)
		r.With(RequireAdmin).Get("/organizations/new", pageHandler.HandleOrganizationForm)
		r.With(RequireAdmin).Get("/organizations/edit/{org_id}", datastarHandler.HandleOrganizationEdit)
		r.With(RequireAdmin).Get("/organizations/cancel/{org_id}", datastarHandler.HandleOrganizationCancel)
		r.Get("/streams", pageHandler.HandleStreamsPage)
		r.Get("/overwatch", pageHandler.HandleOverwatchPage)
		r.Get("/fleet", pageHandler.HandleFleetPage)
		r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Get("/fleet/edit/{entity_id}", datastarHandler.HandleFleetEdit)
		r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Get("/fleet/cancel/{entity_id}", datastarHandler.HandleFleetCancel)
		r.Get("/video", pageHandler.HandleVideoPage)
		r.With(RequireAdmin).Get("/admin", pageHandler.HandleAdminPage)

		// Passkey setup & registration
		r.Get("/setup-passkey", authHandler.HandleSetupPasskey)
		r.Post("/auth/passkey/register/begin", authHandler.HandlePasskeyRegisterBegin)
		r.Post("/auth/passkey/register/finish", authHandler.HandlePasskeyRegisterFinish)

		// Metrics dashboard
		r.With(RequireAdmin).Handle("/metrics", metrics.Handler())
		r.With(RequireAdmin).Get("/metrics-ui", metricsHandler.HandleMetricsPage)
		r.With(RequireAdmin, sseLimit).Get("/api/metrics/sse", metricsHandler.HandleSSE)

		// Web API: Organizations (Datastar/SSE)
		r.Route("/api/organizations", func(r chi.Router) {
			r.Get("/", datastarHandler.HandleListOrganizations)
			r.With(RequireAdmin).Post("/", datastarHandler.HandleCreateOrganization)
			r.With(RequireAdmin).Put("/update", datastarHandler.HandleUpdateOrganization)
			r.With(RequireAdmin).Put("/{org_id}", datastarHandler.HandleUpdateOrganizationByID)
			r.With(RequireAdmin).Delete("/{org_id}", datastarHandler.HandleDeleteOrganization)
		})

		// Web API: Entities (Datastar/SSE)
		r.Route("/api/entities", func(r chi.Router) {
			r.Get("/", datastarHandler.HandleListEntities)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Post("/", datastarHandler.HandleCreateEntity)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Put("/{entity_id}", datastarHandler.HandleUpdateEntity)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Delete("/{entity_id}", datastarHandler.HandleDeleteEntity)
		})

		// Web API: Fleet (Datastar/SSE)
		r.Route("/api/fleet", func(r chi.Router) {
			r.Get("/", datastarHandler.HandleListFleet)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Post("/", datastarHandler.HandleCreateFleetEntity)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Put("/update", datastarHandler.HandleUpdateFleetEntity)
			r.With(RequireRole(shared.RoleOperator, shared.RoleAdmin)).Delete("/{entity_id}", datastarHandler.HandleDeleteFleetEntity)
			r.With(sseLimit).Get("/sse", datastarHandler.HandleFleetSSE)
		})

		// Realtime SSE: Organizations
		r.With(sseLimit).Get("/api/organizations/sse", datastarHandler.HandleOrganizationsSSE)

		// Web API: Overwatch
		r.With(RequireAdmin).Get("/api/overwatch/kv", overwatchHandler.HandleAPIOverwatchKV)
		r.With(sseLimit).Get("/api/overwatch/kv/watch", overwatchHandler.HandleAPIOverwatchKVWatch)
		r.With(RequireAdmin).Get("/api/overwatch/kv/debug", overwatchHandler.HandleAPIOverwatchKVDebug)

		// Web API: Video
		r.With(sseLimit).Get("/api/video/list", videoHandler.HandleAPIVideoList)
		r.Get("/api/video/status", videoHandler.HandleAPIVideoStatus)

		// Web API: Streams
		r.With(sseLimit).Get("/api/streams/sse", func(w http.ResponseWriter, r *http.Request) {
			sseHandler.StreamMessages(w, r)
		})

		// Admin API (requires admin role)
		r.Route("/api/admin", func(r chi.Router) {
			r.Use(RequireAdmin)
			r.Get("/users", adminHandler.HandleListUsers)
			r.Post("/invites", adminHandler.HandleCreateInvite)
			r.Delete("/invites/{id}", adminHandler.HandleRevokeInvite)
			r.Get("/apikeys", adminHandler.HandleListAPIKeys)
			r.Post("/apikeys", adminHandler.HandleCreateAPIKey)
			r.Delete("/apikeys/{id}", adminHandler.HandleRevokeAPIKey)
		})

		// Debug/profiling (admin only)
		r.Route("/debug/pprof", func(r chi.Router) {
			r.Use(RequireAdmin)
			r.HandleFunc("/", pprof.Index)
			r.HandleFunc("/cmdline", pprof.Cmdline)
			r.HandleFunc("/profile", pprof.Profile)
			r.HandleFunc("/symbol", pprof.Symbol)
			r.HandleFunc("/trace", pprof.Trace)
			r.Handle("/goroutine", pprof.Handler("goroutine"))
			r.Handle("/heap", pprof.Handler("heap"))
			r.Handle("/allocs", pprof.Handler("allocs"))
			r.Handle("/block", pprof.Handler("block"))
			r.Handle("/mutex", pprof.Handler("mutex"))
			r.Handle("/threadcreate", pprof.Handler("threadcreate"))
		})
	})

	// ── REST API (has its own API key auth) ───────────────────────────
	if apiHandler != nil {
		r.Mount("/api", apiHandler)
	}

	return r
}
