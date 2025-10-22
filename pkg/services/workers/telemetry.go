package workers

import (
	"context"
	"encoding/json"
	"log"

	"constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type TelemetryWorker struct {
	*BaseWorker
}

func NewTelemetryWorker(nc *nats.Conn, js nats.JetStreamContext) *TelemetryWorker {
	return &TelemetryWorker{
		BaseWorker: NewBaseWorker(
			"TelemetryWorker",
			nc,
			js,
			shared.StreamTelemetry,
			shared.ConsumerTelemetryProcessor,
			shared.SubjectTelemetryAll,
		),
	}
}

func (w *TelemetryWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) {
		log.Printf("[%s] Received telemetry message on subject: %s", w.Name(), msg.Subject)
		
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			log.Printf("[%s] Raw message data: %s", w.Name(), string(msg.Data))
		} else {
			prettyJSON, _ := json.MarshalIndent(data, "", "  ")
			log.Printf("[%s] Telemetry data:\n%s", w.Name(), string(prettyJSON))
		}
	})
}