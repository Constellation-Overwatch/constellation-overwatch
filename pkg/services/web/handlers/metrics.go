package handlers

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/metrics"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/metrics/collectors"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/metrics/timeseries"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/datastar"
	metrics_components "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/metrics/components"
	metrics_pages "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/features/metrics/pages"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/web/signals"
)

// Series name constants for the time-series store.
const (
	seriesCPU          = "cpu_percent"
	seriesMemPercent   = "mem_percent"
	seriesLoadAvg      = "load_avg_1"
	seriesHeapMB       = "heap_mb"
	seriesGoroutines   = "goroutines"
	seriesGCPause      = "gc_pause_ms"
	seriesHTTPRate     = "http_req_rate"
	seriesHTTPInFlight = "http_in_flight"
	seriesNATSMsgIn    = "nats_msg_in_rate"
	seriesNATSMsgOut   = "nats_msg_out_rate"
	seriesNATSConns    = "nats_connections"
	seriesJSMemMB      = "js_mem_mb"
	seriesJSStoreMB    = "js_store_mb"
	seriesJSMessages   = "js_messages"
)

// metricsSnapshot holds raw values from the latest collection for stat cards.
type metricsSnapshot struct {
	// Runtime
	memSys        uint64
	memAlloc      uint64
	memHeapAlloc  uint64
	memHeapSys    uint64
	memStackInUse uint64
	numCPU        int
	numGC         uint32

	// HTTP
	httpTotal float64

	// NATS
	natsMsgsIn        int64
	natsMsgsOut       int64
	natsSlowConsumers int64
	natsUptime        string

	// JetStream
	jsStreams   int
	jsConsumers int
}

// MetricsHandler handles metrics-related HTTP requests with sparkline charts.
type MetricsHandler struct {
	nats            *embeddednats.EmbeddedNATS
	systemCollector *collectors.SystemCollector
	natsDetail      *collectors.NATSDetailCollector
	store           *timeseries.Store

	// Previous values for rate calculations
	prevHTTPTotal   float64
	prevNATSMsgsIn  int64
	prevNATSMsgsOut int64

	// Latest raw snapshot for stat cards
	snap metricsSnapshot
}

// NewMetricsHandler creates a new metrics handler with collectors and time-series store.
func NewMetricsHandler(nats *embeddednats.EmbeddedNATS) *MetricsHandler {
	return &MetricsHandler{
		nats:            nats,
		systemCollector: collectors.NewSystemCollector(),
		natsDetail:      collectors.NewNATSDetailCollector(nats),
		store:           timeseries.NewStore(timeseries.DefaultCapacity),
	}
}

// HandleSSE streams metrics via Server-Sent Events using Datastar fragment patching.
func (h *MetricsHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	sse := datastar.NewSSE(w, r)

	// Send initial connection signal
	if err := sse.MarshalAndPatchSignals(signals.ConnectionSignal{
		IsConnected: true,
	}); err != nil {
		return
	}

	var tick int

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			tick++
			h.collectAndRecord()

			// Build chart sections from store
			sections := h.buildChartSections()

			// Fragment-patch each section
			for _, section := range sections {
				component := metrics_components.Section(section)
				if err := sse.PatchElementTempl(component,
					datastar.WithSelectorID(section.ID),
					datastar.WithModeOuter(),
				); err != nil {
					return
				}
			}

			// Every 5 ticks, patch detail tables
			if tick%5 == 0 {
				details := h.natsDetail.Collect()

				streams := convertStreams(details.Streams)
				consumers := convertConsumers(details.Consumers)
				connections := convertConnections(details.Connections)

				if err := sse.PatchElementTempl(
					metrics_components.StreamsTable(streams),
					datastar.WithSelectorID("streams-detail"),
					datastar.WithModeOuter(),
				); err != nil {
					return
				}
				if err := sse.PatchElementTempl(
					metrics_components.ConsumersTable(consumers),
					datastar.WithSelectorID("consumers-detail"),
					datastar.WithModeOuter(),
				); err != nil {
					return
				}
				if err := sse.PatchElementTempl(
					metrics_components.ConnectionsTable(connections),
					datastar.WithSelectorID("connections-detail"),
					datastar.WithModeOuter(),
				); err != nil {
					return
				}
			}

			// Patch timestamp + connection signal
			if err := sse.MarshalAndPatchSignals(signals.ConnectionSignal{
				IsConnected: true,
			}); err != nil {
				return
			}
			if err := sse.MarshalAndPatchSignals(struct {
				Timestamp string `json:"timestamp"`
			}{
				Timestamp: time.Now().Format("15:04:05"),
			}); err != nil {
				return
			}
		}
	}
}

// collectAndRecord gathers all metrics and records them in the time-series store.
func (h *MetricsHandler) collectAndRecord() {
	// OS-level metrics
	sys, err := h.systemCollector.Collect()
	if err == nil {
		h.store.Record(seriesCPU, sys.CPUPercent)
		h.store.Record(seriesMemPercent, sys.MemPercent)
		h.store.Record(seriesLoadAvg, sys.LoadAvg1)
	}

	// Go runtime metrics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	h.store.Record(seriesHeapMB, float64(m.HeapAlloc)/1024/1024)
	h.store.Record(seriesGoroutines, float64(runtime.NumGoroutine()))
	h.store.Record(seriesGCPause, float64(m.PauseNs[(m.NumGC+255)%256])/1e6)

	// Capture raw runtime values for stat cards
	h.snap.memSys = m.Sys
	h.snap.memAlloc = m.Alloc
	h.snap.memHeapAlloc = m.HeapAlloc
	h.snap.memHeapSys = m.HeapSys
	h.snap.memStackInUse = m.StackInuse
	h.snap.numCPU = runtime.NumCPU()
	h.snap.numGC = m.NumGC

	// HTTP metrics from Prometheus
	mfs, err := metrics.Gather()
	if err == nil {
		var httpTotal float64
		var httpInFlight float64
		for _, mf := range mfs {
			switch mf.GetName() {
			case "overwatch_http_requests_total":
				for _, metric := range mf.GetMetric() {
					if metric.GetCounter() != nil {
						httpTotal += metric.GetCounter().GetValue()
					}
				}
			case "overwatch_http_requests_in_flight":
				if len(mf.GetMetric()) > 0 && mf.GetMetric()[0].GetGauge() != nil {
					httpInFlight = mf.GetMetric()[0].GetGauge().GetValue()
				}
			}
		}
		// Compute request rate (delta per second)
		rate := httpTotal - h.prevHTTPTotal
		if h.prevHTTPTotal == 0 {
			rate = 0
		}
		h.prevHTTPTotal = httpTotal
		h.snap.httpTotal = httpTotal
		h.store.Record(seriesHTTPRate, rate)
		h.store.Record(seriesHTTPInFlight, httpInFlight)
	}

	// NATS server metrics
	if v := h.nats.Varz(); v != nil {
		inRate := float64(v.InMsgs - h.prevNATSMsgsIn)
		outRate := float64(v.OutMsgs - h.prevNATSMsgsOut)
		if h.prevNATSMsgsIn == 0 {
			inRate = 0
		}
		if h.prevNATSMsgsOut == 0 {
			outRate = 0
		}
		h.prevNATSMsgsIn = v.InMsgs
		h.prevNATSMsgsOut = v.OutMsgs

		h.snap.natsMsgsIn = v.InMsgs
		h.snap.natsMsgsOut = v.OutMsgs
		h.snap.natsSlowConsumers = v.SlowConsumers
		h.snap.natsUptime = v.Uptime

		h.store.Record(seriesNATSMsgIn, inRate)
		h.store.Record(seriesNATSMsgOut, outRate)
		h.store.Record(seriesNATSConns, float64(v.Connections))
	}

	// JetStream metrics
	if j := h.nats.Jsz(); j != nil {
		h.snap.jsStreams = j.Streams
		h.snap.jsConsumers = j.Consumers

		h.store.Record(seriesJSMemMB, float64(j.Memory)/1024/1024)
		h.store.Record(seriesJSStoreMB, float64(j.Store)/1024/1024)
		h.store.Record(seriesJSMessages, float64(j.Messages))
	}
}

func fmtMB(bytes uint64) string {
	return fmt.Sprintf("%d MB", bytes/1024/1024)
}

func fmtNum(v int64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	}
	return fmt.Sprintf("%d", v)
}

// buildChartSections creates the chart section data from the time-series store.
func (h *MetricsHandler) buildChartSections() []metrics_components.ChartSection {
	s := h.snap
	return []metrics_components.ChartSection{
		{
			ID: "section-system", Title: "System", Icon: "\u2699", Color: "#06b6d4",
			Charts: []metrics_components.ChartData{
				h.chartFromSeries(seriesCPU, "cpu-chart", "CPU", "%", "#06b6d4"),
				h.chartFromSeries(seriesMemPercent, "mem-chart", "Memory", "%", "#06b6d4"),
				h.chartFromSeries(seriesLoadAvg, "load-chart", "Load Avg", "", "#06b6d4"),
			},
		},
		{
			ID: "section-runtime", Title: "Runtime", Icon: "\u25b6", Color: "#339970",
			Charts: []metrics_components.ChartData{
				h.chartFromSeries(seriesHeapMB, "heap-chart", "Heap", "MB", "#339970"),
				h.chartFromSeries(seriesGoroutines, "goroutines-chart", "Goroutines", "", "#339970"),
				h.chartFromSeries(seriesGCPause, "gc-chart", "GC Pause", "ms", "#339970"),
			},
			Stats: []metrics_components.StatItem{
				{Label: "Total Allocated", Value: fmtMB(s.memSys)},
				{Label: "In Use", Value: fmtMB(s.memAlloc)},
				{Label: "Heap Alloc", Value: fmtMB(s.memHeapAlloc)},
				{Label: "Heap Sys", Value: fmtMB(s.memHeapSys)},
				{Label: "Stack In Use", Value: fmtMB(s.memStackInUse)},
				{Label: "CPUs", Value: fmt.Sprintf("%d", s.numCPU)},
				{Label: "GC Cycles", Value: fmt.Sprintf("%d", s.numGC)},
			},
		},
		{
			ID: "section-http", Title: "HTTP", Icon: "\u21c4", Color: "#3b82f6",
			Charts: []metrics_components.ChartData{
				h.chartFromSeries(seriesHTTPRate, "http-rate-chart", "Req/s", "/s", "#3b82f6"),
				h.chartFromSeries(seriesHTTPInFlight, "http-inflight-chart", "In-Flight", "", "#3b82f6"),
			},
			Stats: []metrics_components.StatItem{
				{Label: "Total Requests", Value: fmtNum(int64(s.httpTotal))},
			},
		},
		{
			ID: "section-nats", Title: "NATS", Icon: "\u26a1", Color: "#8b5cf6",
			Charts: []metrics_components.ChartData{
				h.chartFromSeries(seriesNATSMsgIn, "nats-in-chart", "Msg In/s", "/s", "#8b5cf6"),
				h.chartFromSeries(seriesNATSMsgOut, "nats-out-chart", "Msg Out/s", "/s", "#8b5cf6"),
				h.chartFromSeries(seriesNATSConns, "nats-conns-chart", "Connections", "", "#8b5cf6"),
			},
			Stats: []metrics_components.StatItem{
				{Label: "Msgs In", Value: fmtNum(s.natsMsgsIn)},
				{Label: "Msgs Out", Value: fmtNum(s.natsMsgsOut)},
				{Label: "Slow Consumers", Value: fmt.Sprintf("%d", s.natsSlowConsumers)},
				{Label: "Uptime", Value: s.natsUptime},
			},
		},
		{
			ID: "section-jetstream", Title: "JetStream", Icon: "\u2601", Color: "#f59e0b",
			Charts: []metrics_components.ChartData{
				h.chartFromSeries(seriesJSMemMB, "js-mem-chart", "Memory", "MB", "#f59e0b"),
				h.chartFromSeries(seriesJSStoreMB, "js-store-chart", "Store", "MB", "#f59e0b"),
				h.chartFromSeries(seriesJSMessages, "js-msgs-chart", "Messages", "", "#f59e0b"),
			},
			Stats: []metrics_components.StatItem{
				{Label: "Streams", Value: fmt.Sprintf("%d", s.jsStreams)},
				{Label: "Consumers", Value: fmt.Sprintf("%d", s.jsConsumers)},
			},
		},
	}
}

// chartFromSeries builds a ChartData from a named time-series in the store.
func (h *MetricsHandler) chartFromSeries(seriesName, id, title, unit, color string) metrics_components.ChartData {
	cd := metrics_components.ChartData{
		ID:    id,
		Title: title,
		Unit:  unit,
		Color: color,
	}

	rb := h.store.Get(seriesName)
	if rb == nil || rb.Count() == 0 {
		return cd
	}

	cd.Points = rb.Values()
	cd.Current = rb.Last()
	cd.Min = rb.Min()
	cd.Max = rb.Max()
	cd.Avg = rb.Avg()

	return cd
}

// Convert collector detail types to component detail types.
func convertStreams(src []collectors.StreamDetail) []metrics_components.StreamDetail {
	dst := make([]metrics_components.StreamDetail, len(src))
	for i, s := range src {
		dst[i] = metrics_components.StreamDetail{
			Name:      s.Name,
			Messages:  s.Messages,
			Bytes:     s.Bytes,
			Consumers: s.Consumers,
			MsgRate:   s.MsgRate,
		}
	}
	return dst
}

func convertConsumers(src []collectors.ConsumerDetail) []metrics_components.ConsumerDetail {
	dst := make([]metrics_components.ConsumerDetail, len(src))
	for i, c := range src {
		dst[i] = metrics_components.ConsumerDetail{
			Stream:      c.Stream,
			Name:        c.Name,
			Pending:     c.Pending,
			AckPending:  c.AckPending,
			Redelivered: c.Redelivered,
			Delivered:   c.Delivered,
		}
	}
	return dst
}

func convertConnections(src []collectors.ConnectionDetail) []metrics_components.ConnectionDetail {
	dst := make([]metrics_components.ConnectionDetail, len(src))
	for i, c := range src {
		dst[i] = metrics_components.ConnectionDetail{
			CID:           c.CID,
			Name:          c.Name,
			IP:            c.IP,
			MsgsIn:        c.MsgsIn,
			MsgsOut:       c.MsgsOut,
			BytesIn:       c.BytesIn,
			BytesOut:      c.BytesOut,
			Uptime:        c.Uptime,
			Subscriptions: c.Subscriptions,
		}
	}
	return dst
}

// HandleMetricsPage renders the metrics dashboard page.
func (h *MetricsHandler) HandleMetricsPage(w http.ResponseWriter, r *http.Request) {
	metrics_pages.MetricsPage().Render(r.Context(), w)
}
