package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats.go"
)

const quarantineAfterDeliveries = 3

type QuarantineRecord struct {
	Worker          string    `json:"worker"`
	SourceStream    string    `json:"source_stream"`
	SourceConsumer  string    `json:"source_consumer"`
	OriginalSubject string    `json:"original_subject"`
	OriginalData    []byte    `json:"original_data"`
	Error           string    `json:"error"`
	NumDelivered    uint64    `json:"num_delivered"`
	StreamSequence  uint64    `json:"stream_sequence"`
	QuarantinedAt   time.Time `json:"quarantined_at"`
}

type Worker interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Name() string
}

type BaseWorker struct {
	name     string
	nc       *nats.Conn
	js       nats.JetStreamContext
	sub      *nats.Subscription
	consumer string
	stream   string
	subject  string
}

func NewBaseWorker(name string, nc *nats.Conn, js nats.JetStreamContext, stream, consumer, subject string) *BaseWorker {
	return &BaseWorker{
		name:     name,
		nc:       nc,
		js:       js,
		consumer: consumer,
		stream:   stream,
		subject:  subject,
	}
}

func (w *BaseWorker) Name() string {
	return w.name
}

func (w *BaseWorker) HealthCheck() error {
	if w.nc != nil && w.nc.IsConnected() {
		return nil
	}
	return nats.ErrConnectionClosed
}

func (w *BaseWorker) Stop(ctx context.Context) error {
	if w.sub != nil {
		// For pull subscriptions, unsubscribe instead of drain
		// Drain() is for push subscriptions and doesn't work properly with pull consumers
		return w.sub.Unsubscribe()
	}
	return nil
}

func (w *BaseWorker) processMessages(ctx context.Context, handler func(*nats.Msg) error) error {
	sub, err := w.js.PullSubscribe(w.subject, "",
		nats.Durable(w.consumer),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverAll(),
		nats.Bind(w.stream, w.consumer),
	)
	if err != nil {
		return err
	}
	w.sub = sub

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Check if subscription is still valid before attempting fetch
			if w.sub != nil && !w.sub.IsValid() {
				return nil
			}

			msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
			if err != nil {
				// Timeout is expected and normal - just continue
				if errors.Is(err, nats.ErrTimeout) {
					continue
				}
				// These errors indicate shutdown or connection closure - exit gracefully
				if errors.Is(err, nats.ErrBadSubscription) || errors.Is(err, nats.ErrConnectionClosed) {
					return nil
				}
				// For other errors, log and continue
				logger.Errorw("Error fetching messages", "worker", w.name, "error", err)
				continue
			}

			for _, msg := range msgs {
				if err := handler(msg); err != nil {
					w.handleFailure(msg, err)
				} else {
					// Handler succeeded - acknowledge the message
					if ackErr := msg.Ack(); ackErr != nil {
						logger.Errorw("Error acknowledging message", "worker", w.name, "error", ackErr)
					}
				}
			}
		}
	}
}

func (w *BaseWorker) handleFailure(msg *nats.Msg, handlerErr error) {
	metadata, metadataErr := msg.Metadata()
	if metadataErr == nil && metadata.NumDelivered >= quarantineAfterDeliveries {
		if err := w.quarantine(msg, handlerErr, metadata); err == nil {
			if err := msg.Term(); err != nil {
				logger.Errorw("Error terminating quarantined message", "worker", w.name, "subject", msg.Subject, "error", err)
			}
			return
		} else {
			logger.Errorw("Failed to quarantine poison message", "worker", w.name, "subject", msg.Subject, "error", err)
		}
	}

	if err := msg.Nak(); err != nil {
		logger.Errorw("Error sending NAK", "worker", w.name, "error", err)
	}
	logger.Errorw("Handler failed, message NAK'd for redelivery",
		"worker", w.name,
		"subject", msg.Subject,
		"error", handlerErr,
		"metadata_error", metadataErr)
}

func (w *BaseWorker) quarantine(msg *nats.Msg, handlerErr error, metadata *nats.MsgMetadata) error {
	record := QuarantineRecord{
		Worker:          w.name,
		SourceStream:    metadata.Stream,
		SourceConsumer:  metadata.Consumer,
		OriginalSubject: msg.Subject,
		OriginalData:    msg.Data,
		Error:           handlerErr.Error(),
		NumDelivered:    metadata.NumDelivered,
		StreamSequence:  metadata.Sequence.Stream,
		QuarantinedAt:   time.Now().UTC(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal quarantine record: %w", err)
	}

	quarantineMsg := nats.NewMsg(shared.SubjectQuarantine + "." + quarantineSubjectToken(w.name))
	quarantineMsg.Data = data
	quarantineMsg.Header.Set("Nats-Msg-Id", fmt.Sprintf("quarantine-%s-%d", metadata.Stream, metadata.Sequence.Stream))
	if _, err := w.js.PublishMsg(quarantineMsg); err != nil {
		return fmt.Errorf("publish quarantine record: %w", err)
	}
	logger.Errorw("Message quarantined after repeated handler failures",
		"worker", w.name,
		"subject", msg.Subject,
		"deliveries", metadata.NumDelivered,
		"stream_sequence", metadata.Sequence.Stream)
	return nil
}

func quarantineSubjectToken(name string) string {
	var token strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			token.WriteRune(r)
		} else {
			token.WriteByte('-')
		}
	}
	if token.Len() == 0 {
		return "worker"
	}
	return token.String()
}
