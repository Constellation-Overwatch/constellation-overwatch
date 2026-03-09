package datasource

import "time"

// MetricsSnapshot contains system metrics
type MetricsSnapshot struct {
	MemTotal       uint64
	MemAlloc       uint64
	HeapAlloc      uint64
	NumGoroutines  int
	NumCPU         int
	NumGC          uint32
	CPUPercent     float64
	MemUsedPercent float64
	LoadAvg1       float64
}

// WorkerStatus represents a worker's status
type WorkerStatus struct {
	Name    string
	Healthy bool
}

// EntitySummary is a condensed entity view for the TUI
type EntitySummary struct {
	ID         string
	Name       string
	EntityType string
	Status     string
	IsLive     bool
	OrgID      string
}

// StreamStat represents NATS stream statistics
type StreamStat struct {
	Name     string
	Messages uint64
	Bytes    uint64
}

// LogEntry represents a log entry
type LogEntry struct {
	Time    time.Time
	Level   string
	Message string
	Fields  map[string]interface{}
}
