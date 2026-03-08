package components

// ChartData holds the data for a single sparkline chart card.
type ChartData struct {
	ID      string
	Title   string
	Unit    string // "MB", "%", "msg/s", "ms", ""
	Color   string // CSS color for the sparkline stroke
	Current float64
	Min     float64
	Max     float64
	Avg     float64
	Points  []float64 // normalized samples, oldest-first
}

// StatItem is a simple key-value metric displayed as a card.
type StatItem struct {
	Label string
	Value string
}

// ChartSection groups related charts under a titled section.
type ChartSection struct {
	ID     string
	Title  string
	Icon   string // emoji or unicode symbol for section header
	Color  string // accent color for the section
	Charts []ChartData
	Stats  []StatItem // flat key-value cards shown below sparklines
}

// StreamDetail holds NATS JetStream stream info for the streams table.
type StreamDetail struct {
	Name      string
	Messages  uint64
	Bytes     uint64
	Consumers int
	MsgRate   float64
}

// ConsumerDetail holds NATS JetStream consumer info for the consumers table.
type ConsumerDetail struct {
	Stream      string
	Name        string
	Pending     uint64
	AckPending  int
	Redelivered int
	Delivered   uint64
}

// ConnectionDetail holds NATS connection info for the connections table.
type ConnectionDetail struct {
	CID           uint64
	Name          string
	IP            string
	MsgsIn        int64
	MsgsOut       int64
	BytesIn       int64
	BytesOut      int64
	Uptime        string
	Subscriptions uint32
}
