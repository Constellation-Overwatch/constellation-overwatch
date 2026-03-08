package handlers

import (
	"net/http"
	"runtime"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/metrics"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	metrics_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/metrics/pages"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/signals"
)

// MetricsHandler handles metrics-related HTTP requests
type MetricsHandler struct {
	nats *embeddednats.EmbeddedNATS
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(nats *embeddednats.EmbeddedNATS) *MetricsHandler {
	return &MetricsHandler{nats: nats}
}

// HandleSSE streams metrics via Server-Sent Events using Datastar
func (h *MetricsHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	// CRITICAL: Set SSE headers BEFORE creating SSE generator or writing anything
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	sse := datastar.NewSSE(w, r)

	// Send initial connection signal using typed struct
	if err := sse.MarshalAndPatchSignals(signals.ConnectionSignal{
		IsConnected: true,
	}); err != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Collect runtime metrics
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			// Build typed signals struct
			sig := signals.MetricsSignals{
				MemTotal:             m.Sys,
				MemAlloc:             m.Alloc,
				MemHeapAlloc:         m.HeapAlloc,
				MemHeapSys:           m.HeapSys,
				MemStackInUse:        m.StackInuse,
				NumGoroutines:        runtime.NumGoroutine(),
				NumCPU:               runtime.NumCPU(),
				NumGC:                m.NumGC,
				GCPauseNs:            m.PauseNs[(m.NumGC+255)%256],
				HTTPRequestsTotal:    0,
				HTTPRequestsInFlight: 0,
				Timestamp:            time.Now().Format("15:04:05"),
			}

			// NATS server stats (native Varz/Jsz — no custom struct)
			if v := h.nats.Varz(); v != nil {
				sig.NATSConnections = v.Connections
				sig.NATSMsgsIn = v.InMsgs
				sig.NATSMsgsOut = v.OutMsgs
				sig.NATSSlowConsumers = v.SlowConsumers
				sig.NATSUptime = v.Uptime
			}
			if j := h.nats.Jsz(); j != nil {
				sig.JSStreams = j.Streams
				sig.JSConsumers = j.Consumers
				sig.JSMessages = j.Messages
				sig.JSMemUsed = j.Memory
				sig.JSStoreUsed = j.Store
			}

			// Add custom metrics from Prometheus registry
			mfs, err := metrics.Gather()
			if err == nil {
				for _, mf := range mfs {
					name := mf.GetName()
					if name == "overwatch_http_requests_total" {
						// Sum all HTTP request counters
						var total float64
						for _, metric := range mf.GetMetric() {
							if metric.GetCounter() != nil {
								total += metric.GetCounter().GetValue()
							}
						}
						sig.HTTPRequestsTotal = total
					}
					if name == "overwatch_http_requests_in_flight" {
						if len(mf.GetMetric()) > 0 && mf.GetMetric()[0].GetGauge() != nil {
							sig.HTTPRequestsInFlight = mf.GetMetric()[0].GetGauge().GetValue()
						}
					}
				}
			}

			if err := sse.MarshalAndPatchSignals(sig); err != nil {
				return
			}
		}
	}
}

// HandleMetricsPage renders the metrics dashboard page
func (h *MetricsHandler) HandleMetricsPage(w http.ResponseWriter, r *http.Request) {
	metrics_pages.MetricsPage().Render(r.Context(), w)
}
