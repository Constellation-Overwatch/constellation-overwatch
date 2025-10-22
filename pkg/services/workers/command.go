package workers

import (
	"context"
	"encoding/json"
	"log"

	"constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type CommandWorker struct {
	*BaseWorker
}

func NewCommandWorker(nc *nats.Conn, js nats.JetStreamContext) *CommandWorker {
	return &CommandWorker{
		BaseWorker: NewBaseWorker(
			"CommandWorker",
			nc,
			js,
			shared.StreamCommands,
			shared.ConsumerCommandProcessor,
			shared.SubjectCommandsAll,
		),
	}
}

func (w *CommandWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) {
		log.Printf("[%s] Received command message on subject: %s", w.Name(), msg.Subject)
		
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			log.Printf("[%s] Raw message data: %s", w.Name(), string(msg.Data))
		} else {
			prettyJSON, _ := json.MarshalIndent(data, "", "  ")
			log.Printf("[%s] Command data:\n%s", w.Name(), string(prettyJSON))
		}
	})
}