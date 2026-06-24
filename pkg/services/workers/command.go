package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/agentops"
	embeddednats "github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/embedded-nats"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats.go"
)

type CommandWorker struct {
	*BaseWorker
	agentOpsLaunch *agentops.LaunchExecutor
}

func NewCommandWorker(nc *nats.Conn, js nats.JetStreamContext, db *sql.DB, natsEmbedded *embeddednats.EmbeddedNATS) *CommandWorker {
	return &CommandWorker{
		BaseWorker: NewBaseWorker(
			"CommandWorker",
			nc,
			js,
			shared.StreamCommands,
			shared.ConsumerCommandProcessor,
			shared.SubjectCommandsAll,
		),
		agentOpsLaunch: agentops.NewLaunchExecutor(db, natsEmbedded, agentops.DefaultLaunchExecutorConfig()),
	}
}

func (w *CommandWorker) Start(ctx context.Context) error {
	return w.processMessages(ctx, func(msg *nats.Msg) error {
		logger.Infow("Received command message", "worker", w.Name(), "subject", msg.Subject)

		if w.agentOpsLaunch != nil {
			handled, err := w.agentOpsLaunch.HandleCommand(ctx, msg.Subject, msg.Data)
			if handled {
				if err != nil {
					logger.Errorw("Agent Ops launch command failed", "worker", w.Name(), "subject", msg.Subject, "error", err)
				}
				return nil
			}
		}

		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			logger.Errorw("Failed to unmarshal command", "worker", w.Name(), "error", err, "data", string(msg.Data))
			return fmt.Errorf("invalid JSON: %w", err)
		}

		prettyJSON, _ := json.MarshalIndent(data, "", "  ")
		logger.Debugw("Command data", "worker", w.Name(), "json", string(prettyJSON))

		// TODO: Process the command here

		return nil
	})
}
