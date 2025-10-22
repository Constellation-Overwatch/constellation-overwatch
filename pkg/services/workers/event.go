package workers

import (
	"context"
	"encoding/json"
	"log"

	"constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type EventWorker struct {
	*BaseWorker
}

func NewEventWorker(nc *nats.Conn, js nats.JetStreamContext) *EventWorker {
	return &EventWorker{
		BaseWorker: NewBaseWorker(
			"EventWorker",
			nc,
			js,
			shared.StreamEvents,
			shared.ConsumerEventProcessor,
			shared.SubjectEventsAll,
		),
	}
}

func (w *EventWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) {
		log.Printf("[%s] Received event message on subject: %s", w.Name(), msg.Subject)
		
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			log.Printf("[%s] Raw message data: %s", w.Name(), string(msg.Data))
		} else {
			prettyJSON, _ := json.MarshalIndent(data, "", "  ")
			log.Printf("[%s] Event data:\n%s", w.Name(), string(prettyJSON))
		}
	})
}