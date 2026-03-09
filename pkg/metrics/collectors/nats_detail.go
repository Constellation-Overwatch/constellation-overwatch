package collectors

import (
	"time"

	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/nats-io/nats-server/v2/server"
)

// StreamDetail holds per-stream JetStream statistics.
type StreamDetail struct {
	Name      string
	Messages  uint64
	Bytes     uint64
	Consumers int
	MsgRate   float64 // msgs/sec delta
}

// ConsumerDetail holds per-consumer JetStream statistics.
type ConsumerDetail struct {
	Stream      string
	Name        string
	Pending     uint64
	AckPending  int
	Redelivered int
	Delivered   uint64
}

// ConnectionDetail holds per-connection NATS statistics.
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

// NATSDetails aggregates all NATS detail metrics.
type NATSDetails struct {
	Streams     []StreamDetail
	Consumers   []ConsumerDetail
	Connections []ConnectionDetail
}

// NATSDetailCollector gathers detailed NATS JetStream and connection metrics.
type NATSDetailCollector struct {
	nats     *embeddednats.EmbeddedNATS
	prevMsgs map[string]uint64
	prevTime time.Time
	hasPrev  bool
}

// NewNATSDetailCollector creates a new NATSDetailCollector.
func NewNATSDetailCollector(nats *embeddednats.EmbeddedNATS) *NATSDetailCollector {
	return &NATSDetailCollector{
		nats:     nats,
		prevMsgs: make(map[string]uint64),
	}
}

// Collect gathers detailed NATS metrics. Errors are handled gracefully per-item.
func (c *NATSDetailCollector) Collect() NATSDetails {
	var details NATSDetails

	js := c.nats.JetStream()
	if js == nil {
		return details
	}

	now := time.Now()
	elapsed := float64(0)
	if c.hasPrev {
		elapsed = now.Sub(c.prevTime).Seconds()
	}

	// Dynamically discover all streams instead of hardcoding names
	for name := range js.StreamNames() {
		info, err := js.StreamInfo(name)
		if err != nil {
			continue
		}

		var msgRate float64
		if c.hasPrev && elapsed > 0 {
			if prev, ok := c.prevMsgs[name]; ok {
				msgRate = float64(info.State.Msgs-prev) / elapsed
			}
		}
		c.prevMsgs[name] = info.State.Msgs

		details.Streams = append(details.Streams, StreamDetail{
			Name:      name,
			Messages:  info.State.Msgs,
			Bytes:     info.State.Bytes,
			Consumers: info.State.Consumers,
			MsgRate:   msgRate,
		})

		// Enumerate consumers for this stream
		for consumerName := range js.ConsumerNames(name) {
			ci, err := js.ConsumerInfo(name, consumerName)
			if err != nil {
				continue
			}
			details.Consumers = append(details.Consumers, ConsumerDetail{
				Stream:      name,
				Name:        ci.Name,
				Pending:     ci.NumPending,
				AckPending:  ci.NumAckPending,
				Redelivered: ci.NumRedelivered,
				Delivered:   ci.Delivered.Stream,
			})
		}
	}

	c.prevTime = now
	c.hasPrev = true

	// Connection details via server Connz
	details.Connections = collectConnections(c.nats.Connz())

	return details
}

func collectConnections(connz *server.Connz) []ConnectionDetail {
	if connz == nil {
		return nil
	}

	conns := make([]ConnectionDetail, 0, len(connz.Conns))
	for _, ci := range connz.Conns {
		conns = append(conns, ConnectionDetail{
			CID:           ci.Cid,
			Name:          ci.Name,
			IP:            ci.IP,
			MsgsIn:        ci.InMsgs,
			MsgsOut:       ci.OutMsgs,
			BytesIn:       ci.InBytes,
			BytesOut:      ci.OutBytes,
			Uptime:        ci.Uptime,
			Subscriptions: ci.NumSubs,
		})
	}
	return conns
}
