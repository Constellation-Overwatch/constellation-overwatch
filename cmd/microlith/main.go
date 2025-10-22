package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"constellation-api/api"
	"constellation-api/api/middleware"
	"constellation-api/pkg/shared"
	embeddednats "constellation-api/pkg/services/embedded-nats"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db   *sql.DB
	nats *embeddednats.EmbeddedNATS
)

func initDB() error {
	var err error
	db, err = sql.Open("sqlite3", "./constellation.db")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	
	// Test the connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings for SQLite
	db.SetMaxOpenConns(1) // SQLite doesn't handle concurrent writes well
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	log.Println("Database initialized successfully")
	return nil
}

func initNATS() error {
	var err error
	
	config := embeddednats.DefaultConfig()
	config.DataDir = "./data/nats"
	config.Port = 4222
	
	nats, err = embeddednats.New(config)
	if err != nil {
		return fmt.Errorf("failed to create embedded NATS: %w", err)
	}

	if err := nats.Start(); err != nil {
		return fmt.Errorf("failed to start embedded NATS: %w", err)
	}

	// Create constellation streams
	if err := nats.CreateConstellationStreams(); err != nil {
		return fmt.Errorf("failed to create constellation streams: %w", err)
	}

	// Create durable consumers
	consumers := []struct {
		stream   string
		consumer string
		filter   string
	}{
		{shared.StreamEntities, shared.ConsumerEntityProcessor, shared.SubjectEntitiesAll},
		{shared.StreamCommands, shared.ConsumerCommandProcessor, shared.SubjectCommandsAll},
		{shared.StreamEvents, shared.ConsumerEventProcessor, shared.SubjectEventsAll},
		{shared.StreamTelemetry, shared.ConsumerTelemetryProcessor, shared.SubjectTelemetryAll},
	}

	for _, c := range consumers {
		if err := nats.CreateDurableConsumer(c.stream, c.consumer, c.filter); err != nil {
			return fmt.Errorf("failed to create consumer %s: %w", c.consumer, err)
		}
	}

	log.Println("NATS JetStream initialized successfully")
	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

	// Initialize embedded NATS
	if err := initNATS(); err != nil {
		log.Fatal("Failed to initialize NATS:", err)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create HTTP server mux
	mux := http.NewServeMux()

	// Initialize handlers
	handlers := api.NewHandlers(db, nats)
	handlers.RegisterRoutes(mux, nats)

	// Apply CORS middleware to all routes
	handler := middleware.CORS(middleware.RequestLogger(mux))

	// Configure server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting Constellation API server on port %s", port)
		log.Printf("Bearer token: %s", getAPIToken())
		
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start:", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down server...")

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Failed to shutdown server gracefully: %v", err)
	}

	// Shutdown NATS
	if nats != nil {
		if err := nats.Shutdown(shutdownCtx); err != nil {
			log.Printf("Failed to shutdown NATS: %v", err)
		}
	}

	log.Println("Server shutdown complete")
}

func getAPIToken() string {
	token := os.Getenv("API_BEARER_TOKEN")
	if token == "" {
		token = "constellation-dev-token"
	}
	return token
}