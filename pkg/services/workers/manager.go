package workers

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"

	"github.com/nats-io/nats.go"
)

type Manager struct {
	workers  []Worker
	nc       *nats.Conn
	js       nats.JetStreamContext
	db       *sql.DB
	kv       nats.KeyValue
	registry *EntityRegistry
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewManager creates a worker manager with database and KV store access
func NewManager(natsClient *embeddednats.EmbeddedNATS, db *sql.DB) (*Manager, error) {
	nc := natsClient.Connection()
	if nc == nil {
		return nil, fmt.Errorf("NATS connection not initialized")
	}

	js := natsClient.JetStream()
	if js == nil {
		return nil, fmt.Errorf("JetStream not initialized")
	}

	// Get or create KV store for global state
	kv := natsClient.KeyValue()
	if kv == nil {
		return nil, fmt.Errorf("KV store not initialized")
	}

	// Create entity registry and load existing entities from DB
	registry, err := NewEntityRegistry(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity registry: %w", err)
	}

	// Initialize KV store with all entities from database
	// This ensures entities have KV entries even before receiving telemetry
	if err := registry.InitializeKVStoreFromDB(kv); err != nil {
		logger.Errorw("Failed to initialize KV store from DB (non-fatal, will continue)", "error", err)
		// Don't return error - this is not critical, entities will be created when telemetry arrives
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		nc:       nc,
		js:       js,
		db:       db,
		kv:       kv,
		registry: registry,
		ctx:      ctx,
		cancel:   cancel,
		workers: []Worker{
			NewTelemetryWorker(nc, js, db, kv, registry),
			NewEntityWorker(nc, js),
			NewEventWorker(nc, js, kv, registry),
			NewCommandWorker(nc, js),
		},
	}, nil
}

// Name returns the service name for logging.
func (m *Manager) Name() string {
	return "workers"
}

// HealthCheck returns the health status of the worker manager.
func (m *Manager) HealthCheck() error {
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	for _, worker := range m.workers {
		m.wg.Add(1)
		go func(w Worker) {
			defer m.wg.Done()
			if err := w.Start(m.ctx); err != nil && err != context.Canceled {
				logger.Errorw("Worker error", "worker", w.Name(), "error", err)
			}
		}(worker)
	}

	logger.Infow("Workers started", "count", len(m.workers))
	return nil
}

// GetWorkers returns all workers for status monitoring
func (m *Manager) GetWorkers() []Worker {
	return m.workers
}

// GetRegistry returns the entity registry
func (m *Manager) GetRegistry() *EntityRegistry {
	return m.registry
}

// GetJetStream returns the JetStream context
func (m *Manager) GetJetStream() nats.JetStreamContext {
	return m.js
}

// GetKeyValue returns the KV store
func (m *Manager) GetKeyValue() nats.KeyValue {
	return m.kv
}

func (m *Manager) Stop(ctx context.Context) error {
	logger.Info("Stopping NATS workers...")

	// Step 1: Stop all workers (unsubscribe from consumers)
	// This must happen BEFORE canceling context to prevent race conditions
	for _, worker := range m.workers {
		if err := worker.Stop(ctx); err != nil {
			logger.Errorw("Error stopping worker", "worker", worker.Name(), "error", err)
		}
	}

	// Step 2: Cancel context to break any remaining fetch loops
	m.cancel()

	// Step 3: Wait for all worker goroutines to complete (with timeout)
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All workers stopped")
	case <-ctx.Done():
		logger.Warn("Timeout waiting for workers to stop")
		// Close NATS connection even on timeout to help unblock stuck workers
		if m.nc != nil {
			m.nc.Close()
		}
		return ctx.Err()
	}

	// Step 4: Finally close NATS connection
	if m.nc != nil {
		m.nc.Close()
	}

	return nil
}
