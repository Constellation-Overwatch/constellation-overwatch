package workers

import (
	"context"
	"fmt"
	"log"
	"sync"

	embeddednats "constellation-api/pkg/services/embedded-nats"
	"github.com/nats-io/nats.go"
)

type Manager struct {
	workers []Worker
	nc      *nats.Conn
	js      nats.JetStreamContext
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewManager(natsClient *embeddednats.EmbeddedNATS) (*Manager, error) {
	nc := natsClient.Connection()
	if nc == nil {
		return nil, fmt.Errorf("NATS connection not initialized")
	}

	js := natsClient.JetStream()
	if js == nil {
		return nil, fmt.Errorf("JetStream not initialized")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		nc:     nc,
		js:     js,
		ctx:    ctx,
		cancel: cancel,
		workers: []Worker{
			NewTelemetryWorker(nc, js),
			NewEntityWorker(nc, js),
			NewEventWorker(nc, js),
			NewCommandWorker(nc, js),
		},
	}, nil
}

func (m *Manager) Start() error {
	log.Println("Starting NATS workers...")

	for _, worker := range m.workers {
		m.wg.Add(1)
		go func(w Worker) {
			defer m.wg.Done()
			
			log.Printf("Starting worker: %s", w.Name())
			if err := w.Start(m.ctx); err != nil && err != context.Canceled {
				log.Printf("Worker %s error: %v", w.Name(), err)
			}
			log.Printf("Worker %s stopped", w.Name())
		}(worker)
	}

	log.Printf("Started %d workers", len(m.workers))
	return nil
}

func (m *Manager) Stop() error {
	log.Println("Stopping NATS workers...")
	
	m.cancel()
	
	for _, worker := range m.workers {
		if err := worker.Stop(); err != nil {
			log.Printf("Error stopping worker %s: %v", worker.Name(), err)
		}
	}
	
	m.wg.Wait()
	
	if m.nc != nil {
		m.nc.Close()
	}
	
	log.Println("All workers stopped")
	return nil
}