package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/api/middleware"
	"github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/mediamtx"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go"
)

type Server struct {
	db           *db.Service
	nc           *nats.Conn
	natsEmbedded *embeddednats.EmbeddedNATS
	orgSvc       *services.OrganizationService
	entitySvc    *services.EntityService
	sseHandler   *SSEHandler
	mtxClient    *mediamtx.Client
	apiHandler   http.Handler
	mux          chi.Router
	server       *http.Server
	bindAddr     string
	runtimeCfg   runtimeconfig.Runtime
}

// NewServer creates a new web server instance
func NewServer(dbService *db.Service, nc *nats.Conn, natsEmbedded *embeddednats.EmbeddedNATS, apiHandler http.Handler, runtimeCfg runtimeconfig.Runtime) (*Server, error) {
	// Create MediaMTX client (nil if MEDIAMTX_API_URL is not set)
	mtxClient := mediamtx.New(mediamtx.DefaultConfig())

	s := &Server{
		db:           dbService,
		nc:           nc,
		natsEmbedded: natsEmbedded,
		apiHandler:   apiHandler,
		orgSvc:       services.NewOrganizationService(dbService.GetDB(), natsEmbedded),
		entitySvc:    services.NewEntityService(dbService.GetDB(), natsEmbedded),
		sseHandler:   NewSSEHandler(natsEmbedded.Connection(), natsEmbedded.JetStream()),
		mtxClient:    mtxClient,
		runtimeCfg:   runtimeCfg,
	}

	database := dbService.GetDB()

	// Initialize session auth (backed by SQLite for restart persistence)
	sessionAuth := middleware.NewSessionAuthWithCookieSecurity(database, runtimeCfg.SecureCookies)

	// Initialize WebAuthn relying party
	wa, err := services.NewWebAuthnWithOrigins(runtimeCfg.RPID, []string{runtimeCfg.BaseURL})
	if err != nil {
		return nil, fmt.Errorf("initialize WebAuthn: %w", err)
	}

	// Initialize services
	authSvc := services.NewAuthService(database, wa)
	userSvc := services.NewUserService(database)
	inviteSvc := services.NewInviteService(database)
	apiKeySvc := services.NewAPIKeyService(database)

	// Restore NATS NKey users from API keys on startup.
	if nkeyData, err := apiKeySvc.ListNKeyData(); err == nil && len(nkeyData) > 0 {
		records := make([]embeddednats.NKeyRecord, len(nkeyData))
		for i, d := range nkeyData {
			records[i] = embeddednats.NKeyRecord{
				NATSPubKey: d.NATSPubKey,
				Scopes:     d.Scopes,
				OrgID:      d.OrgID,
			}
		}
		if err := natsEmbedded.RestoreNKeyUsers(records); err != nil {
			logger.Warnw("Failed to restore NATS NKey users", "error", err)
		}
	}

	// Initialize the router
	s.mux = NewRouter(
		s.orgSvc, s.entitySvc, s.natsEmbedded, s.sseHandler,
		s.mtxClient, s.apiHandler, sessionAuth,
		authSvc, userSvc, inviteSvc, apiKeySvc, runtimeCfg,
	)
	s.bindAddr = net.JoinHostPort(runtimeCfg.Host, runtimeCfg.Port)

	return s, nil
}

// NewWebService creates a new web service with environment-based configuration
func NewWebService(dbService *db.Service, nc *nats.Conn, natsEmbedded *embeddednats.EmbeddedNATS, apiHandler http.Handler, runtimeCfg runtimeconfig.Runtime) (*Server, error) {
	server, err := NewServer(dbService, nc, natsEmbedded, apiHandler, runtimeCfg)
	if err != nil {
		return nil, err
	}

	return server, nil
}

// Start starts the web server and MediaMTX client
func (s *Server) Start(ctx context.Context) error {
	logger.Infof("Starting web server on %s", s.bindAddr)

	// Start MediaMTX polling client (nil-safe, no-op if disabled)
	s.mtxClient.Start(ctx)

	// Bind to the port first to ensure it's available before returning
	listener, err := net.Listen("tcp", s.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.bindAddr, err)
	}

	s.server = &http.Server{
		Addr:              s.bindAddr,
		Handler:           RecoverPanic(SecurityHeadersForConfig(s.runtimeCfg)(MaxBodySize(1 << 20)(s.mux))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // Disabled — SSE endpoints are long-lived
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Errorw("Web server failed", "error", err)
		}
	}()

	return nil
}

// Stop stops the web server and MediaMTX client
func (s *Server) Stop(ctx context.Context) error {
	// Stop MediaMTX polling client (nil-safe)
	s.mtxClient.Stop()

	if s.server != nil {
		logger.Info("Stopping web server...")
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Name returns the service name (implements Service interface)
func (s *Server) Name() string {
	return "web-server"
}

// HealthCheck returns the health status of the web service (implements Service interface)
func (s *Server) HealthCheck() error {
	// Simple check that server is configured
	if s.server == nil {
		return fmt.Errorf("web server not initialized")
	}
	return nil
}

// HandleHealthCheck handles the health check endpoint
func (s *Server) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	health := shared.HealthStatus{
		Status:    "healthy",
		Service:   "constellation-overwatch",
		Timestamp: time.Now(),
		Details:   make(map[string]string),
	}

	// Check database
	if err := s.db.GetDB().Ping(); err != nil {
		health.Status = "unhealthy"
		health.Details["database"] = "unhealthy: " + err.Error()
	} else {
		health.Details["database"] = "healthy"
	}

	// Check NATS
	if err := s.natsEmbedded.HealthCheck(); err != nil {
		health.Status = "unhealthy"
		health.Details["nats"] = "unhealthy: " + err.Error()
	} else {
		health.Details["nats"] = "healthy"
	}

	statusCode := http.StatusOK
	if health.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, `{"status":"%s"}`, health.Status)
}
