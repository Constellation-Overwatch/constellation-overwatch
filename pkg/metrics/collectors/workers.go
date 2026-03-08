package collectors

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Worker interface defines minimal interface for worker status
type Worker interface {
	Name() string
}

// WorkerManager interface for getting workers
type WorkerManager interface {
	GetWorkers() []Worker
}

// WorkerManagerAdapter wraps any type that returns a slice of named workers,
// bridging concrete worker types to the collectors.Worker interface.
type WorkerManagerAdapter[T Worker] struct {
	GetWorkersFunc func() []T
}

// GetWorkers implements WorkerManager by converting the concrete slice.
func (a *WorkerManagerAdapter[T]) GetWorkers() []Worker {
	concrete := a.GetWorkersFunc()
	result := make([]Worker, len(concrete))
	for i, w := range concrete {
		result[i] = w
	}
	return result
}

// WorkersCollector collects metrics from workers
type WorkersCollector struct {
	manager     WorkerManager
	healthyDesc *prometheus.Desc
	countDesc   *prometheus.Desc
}

// NewWorkersCollector creates a new workers collector
func NewWorkersCollector(manager WorkerManager) *WorkersCollector {
	return &WorkersCollector{
		manager: manager,
		healthyDesc: prometheus.NewDesc(
			"overwatch_worker_healthy",
			"Worker health status (1 = healthy, 0 = unhealthy)",
			[]string{"worker"}, nil,
		),
		countDesc: prometheus.NewDesc(
			"overwatch_workers_total",
			"Total number of workers",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector
func (c *WorkersCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.healthyDesc
	ch <- c.countDesc
}

// Collect implements prometheus.Collector
func (c *WorkersCollector) Collect(ch chan<- prometheus.Metric) {
	if c.manager == nil {
		return
	}

	workers := c.manager.GetWorkers()

	// Report total worker count
	ch <- prometheus.MustNewConstMetric(
		c.countDesc, prometheus.GaugeValue,
		float64(len(workers)),
	)

	// Report health status for each worker
	// Workers are considered healthy if they exist (basic check)
	for _, worker := range workers {
		ch <- prometheus.MustNewConstMetric(
			c.healthyDesc, prometheus.GaugeValue,
			1.0, worker.Name(),
		)
	}
}
