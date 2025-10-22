package workers

import (
	"context"
	"encoding/json"
	"log"

	"constellation-api/pkg/shared"
	"github.com/nats-io/nats.go"
)

type EntityWorker struct {
	*BaseWorker
}

func NewEntityWorker(nc *nats.Conn, js nats.JetStreamContext) *EntityWorker {
	return &EntityWorker{
		BaseWorker: NewBaseWorker(
			"EntityWorker",
			nc,
			js,
			shared.StreamEntities,
			shared.ConsumerEntityProcessor,
			shared.SubjectEntitiesAll,
		),
	}
}

func (w *EntityWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) {
		log.Printf("[%s] Received entity message on subject: %s", w.Name(), msg.Subject)
		
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			log.Printf("[%s] Raw message data: %s", w.Name(), string(msg.Data))
		} else {
			prettyJSON, _ := json.MarshalIndent(data, "", "  ")
			log.Printf("[%s] Entity data:\n%s", w.Name(), string(prettyJSON))
		}
	})
}