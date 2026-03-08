// Package signals provides typed signal structs for Datastar SSE communication.
// Using typed structs instead of map[string]interface{} provides:
// - Compile-time validation of signal keys
// - IDE autocomplete and refactoring support
// - Clear documentation of signal structure
// - Reduced runtime errors from typos
package signals

// ConnectionSignal represents the connection state sent to the frontend.
type ConnectionSignal struct {
	IsConnected bool `json:"_isConnected"`
}

// MetricsSignals represents runtime and system metrics pushed to the frontend.
type MetricsSignals struct {
	// Runtime metrics
	MemTotal      uint64 `json:"memTotal"`
	MemAlloc      uint64 `json:"memAlloc"`
	MemHeapAlloc  uint64 `json:"memHeapAlloc"`
	MemHeapSys    uint64 `json:"memHeapSys"`
	MemStackInUse uint64 `json:"memStackInUse"`
	NumGoroutines int    `json:"numGoroutines"`
	NumCPU        int    `json:"numCPU"`
	NumGC         uint32 `json:"numGC"`
	GCPauseNs     uint64 `json:"gcPauseNs"`

	// HTTP metrics
	HTTPRequestsTotal    float64 `json:"httpRequestsTotal"`
	HTTPRequestsInFlight float64 `json:"httpRequestsInFlight"`

	// NATS server metrics (from native server.Varz / server.Jsz)
	NATSConnections   int    `json:"natsConnections"`
	NATSMsgsIn        int64  `json:"natsMsgsIn"`
	NATSMsgsOut       int64  `json:"natsMsgsOut"`
	NATSSlowConsumers int64  `json:"natsSlowConsumers"`
	NATSUptime        string `json:"natsUptime"`

	// JetStream metrics
	JSStreams    int    `json:"jsStreams"`
	JSConsumers int    `json:"jsConsumers"`
	JSMessages  uint64 `json:"jsMessages"`
	JSMemUsed   uint64 `json:"jsMemUsed"`
	JSStoreUsed uint64 `json:"jsStoreUsed"`

	// Metadata
	Timestamp   string `json:"timestamp"`
	IsConnected bool   `json:"_isConnected,omitempty"`
}

// EntitySignal represents the minimal entity state pushed to the frontend.
// This is used for signal updates, not full entity rendering (which is server-side).
type EntitySignal struct {
	EntityID   string `json:"entityId"`
	OrgID      string `json:"orgId"`
	Name       string `json:"name,omitempty"`
	EntityType string `json:"entityType,omitempty"`
	Status     string `json:"status,omitempty"`
	IsLive     bool   `json:"isLive"`

	// Position (for map integration)
	// Using pointers to distinguish between "not set" and "set to zero" (equator/prime meridian)
	Lat *float64 `json:"lat,omitempty"`
	Lng *float64 `json:"lng,omitempty"`
	Alt *float64 `json:"alt,omitempty"`

	// Heading (for map marker rotation)
	// Using pointer to distinguish between "not set" and "heading 0 (north)"
	Heading *int16 `json:"heading,omitempty"`

	// Video (for per-entity WHEP playback)
	WebRTCURL string `json:"webrtcUrl,omitempty"`
}

// AnalyticsSignals represents aggregated analytics data.
type AnalyticsSignals struct {
	TypeCounts   map[string]int `json:"typeCounts,omitempty"`
	StatusCounts map[string]int `json:"statusCounts,omitempty"`
	Threats      ThreatSignals  `json:"threats,omitempty"`
	Vision       VisionSignals  `json:"vision,omitempty"`
}

// ThreatSignals represents threat-related signals.
type ThreatSignals struct {
	Active   int                `json:"active"`
	Priority ThreatPriorityData `json:"priority,omitempty"`
}

// ThreatPriorityData breaks down threats by priority level.
type ThreatPriorityData struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
}

// VisionSignals represents computer vision signals.
type VisionSignals struct {
	Tracked    int `json:"tracked"`
	Detections int `json:"detections"`
}

// DashboardSignals combines overwatch state with analytics for full dashboard updates.
type DashboardSignals struct {
	LastUpdate    string           `json:"lastUpdate,omitempty"`
	TotalEntities int              `json:"totalEntities"`
	TotalOrgs     int              `json:"totalOrgs"`
	IsConnected   bool             `json:"_isConnected"`
	Analytics     AnalyticsSignals `json:"analytics,omitempty"`
}

// MapSignals represents map-specific dashboard signals.
type MapSignals struct {
	TotalEntities int    `json:"totalEntities"`
	IsConnected   bool   `json:"_isConnected"`
	LastUpdate    string `json:"lastUpdate,omitempty"`
}

// OrgSignal represents an organization container signal.
type OrgSignal struct {
	OrgID    string                  `json:"orgId"`
	Entities map[string]EntitySignal `json:"entities,omitempty"`
}
