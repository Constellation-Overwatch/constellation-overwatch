package embeddednats

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

type Config struct {
	Port           int
	WSPort         int
	DataDir        string
	MaxMemory      int64
	MaxFileStore   int64
	JetStreamDomain string
	EnableTLS      bool
	TLSCert        string
	TLSKey         string
}

type EmbeddedNATS struct {
	server  *server.Server
	nc      *nats.Conn
	js      nats.JetStreamContext
	config  *Config
	streams map[string]*StreamConfig
}

type StreamConfig struct {
	Name             string
	Subjects         []string
	Retention        nats.RetentionPolicy
	MaxMsgs          int64
	MaxBytes         int64
	MaxAge           time.Duration
	MaxMsgSize       int32
	Replicas         int
	DuplicateWindow  time.Duration
	AllowRollup      bool
	AllowDirect      bool
	DiscardPolicy    nats.DiscardPolicy
}

func DefaultConfig() *Config {
	return &Config{
		Port:            4222,
		WSPort:          8222,
		DataDir:         "./data/nats",
		MaxMemory:       256 * 1024 * 1024, // 256MB
		MaxFileStore:    2 * 1024 * 1024 * 1024, // 2GB
		JetStreamDomain: "constellation",
		EnableTLS:       false,
	}
}

func New(cfg *Config) (*EmbeddedNATS, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	return &EmbeddedNATS{
		config:  cfg,
		streams: make(map[string]*StreamConfig),
	}, nil
}

func (en *EmbeddedNATS) Start() error {
	opts := &server.Options{
		Port:       en.config.Port,
		JetStream:  true,
		StoreDir:   en.config.DataDir,
	}
	
	// Only enable websocket if we have TLS
	if en.config.EnableTLS {
		opts.Websocket = server.WebsocketOpts{
			Port: en.config.WSPort,
			NoTLS: false,
		}
	}

	// Configure JetStream limits
	opts.JetStreamMaxMemory = en.config.MaxMemory
	opts.JetStreamMaxStore = en.config.MaxFileStore

	if en.config.JetStreamDomain != "" {
		opts.JetStreamDomain = en.config.JetStreamDomain
	}

	if en.config.EnableTLS && en.config.TLSCert != "" && en.config.TLSKey != "" {
		opts.TLSCert = en.config.TLSCert
		opts.TLSKey = en.config.TLSKey
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("failed to create NATS server: %w", err)
	}

	ns.ConfigureLogger()

	go ns.Start()

	if !ns.ReadyForConnections(10 * time.Second) {
		return fmt.Errorf("NATS server not ready for connections")
	}

	en.server = ns

	if err := en.connect(); err != nil {
		return fmt.Errorf("failed to connect to embedded NATS: %w", err)
	}

	log.Printf("Embedded NATS server started on port %d", en.config.Port)
	return nil
}

func (en *EmbeddedNATS) connect() error {
	url := fmt.Sprintf("nats://localhost:%d", en.config.Port)
	
	nc, err := nats.Connect(url,
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Printf("NATS error: %v", err)
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Printf("NATS disconnected: %v", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			log.Printf("NATS reconnected")
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	en.nc = nc
	en.js = js
	return nil
}

func (en *EmbeddedNATS) AddStream(streamConfig *StreamConfig) error {
	if en.js == nil {
		return fmt.Errorf("JetStream not initialized")
	}

	config := &nats.StreamConfig{
		Name:            streamConfig.Name,
		Subjects:        streamConfig.Subjects,
		Retention:       streamConfig.Retention,
		MaxMsgs:         streamConfig.MaxMsgs,
		MaxBytes:        streamConfig.MaxBytes,
		MaxAge:          streamConfig.MaxAge,
		MaxMsgSize:      streamConfig.MaxMsgSize,
		Replicas:        streamConfig.Replicas,
		Duplicates:      streamConfig.DuplicateWindow,
		AllowRollup:     streamConfig.AllowRollup,
		AllowDirect:     streamConfig.AllowDirect,
		Discard:         streamConfig.DiscardPolicy,
	}

	// Try to update stream if it exists, otherwise create it
	stream, err := en.js.StreamInfo(streamConfig.Name)
	if err == nil {
		// Stream exists, update it
		stream, err = en.js.UpdateStream(config)
		if err != nil {
			return fmt.Errorf("failed to update stream %s: %w", streamConfig.Name, err)
		}
		log.Printf("Updated existing stream: %s", streamConfig.Name)
	} else {
		// Stream doesn't exist, create it
		stream, err = en.js.AddStream(config)
		if err != nil {
			return fmt.Errorf("failed to add stream %s: %w", streamConfig.Name, err)
		}
		log.Printf("Created new stream: %s", streamConfig.Name)
	}

	en.streams[streamConfig.Name] = streamConfig
	log.Printf("Created stream: %s with subjects: %v", stream.Config.Name, stream.Config.Subjects)
	
	return nil
}

func (en *EmbeddedNATS) CreateConstellationStreams() error {
	streams := []StreamConfig{
		{
			Name:            "CONSTELLATION_ENTITIES",
			Subjects:        []string{"constellation.entities.>"},
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         100000,
			MaxBytes:        256 * 1024 * 1024, // 256MB
			MaxAge:          7 * 24 * time.Hour,  // 7 days
			MaxMsgSize:      1024 * 1024,         // 1MB
			Replicas:        1,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     true,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_EVENTS",
			Subjects:        []string{"constellation.events.>"},
			Retention:       nats.WorkQueuePolicy, // Events consumed once
			MaxMsgs:         50000,
			MaxBytes:        128 * 1024 * 1024, // 128MB
			MaxAge:          24 * time.Hour,
			MaxMsgSize:      256 * 1024, // 256KB
			Replicas:        1,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_TELEMETRY",
			Subjects:        []string{"constellation.telemetry.>"},
			Retention:       nats.InterestPolicy, // Keep while there are consumers
			MaxMsgs:         25000,
			MaxBytes:        64 * 1024 * 1024, // 64MB
			MaxAge:          1 * time.Hour,
			MaxMsgSize:      64 * 1024, // 64KB
			Replicas:        1,
			DuplicateWindow: 30 * time.Second,
			AllowRollup:     true,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_COMMANDS",
			Subjects:        []string{"constellation.commands.>"},
			Retention:       nats.WorkQueuePolicy,
			MaxMsgs:         10000,
			MaxBytes:        32 * 1024 * 1024, // 32MB
			MaxAge:          15 * time.Minute,
			MaxMsgSize:      32 * 1024, // 32KB
			Replicas:        1,
			DuplicateWindow: 1 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     false, // Commands must go through stream
			DiscardPolicy:   nats.DiscardNew, // Reject new commands if full
		},
	}

	for _, stream := range streams {
		if err := en.AddStream(&stream); err != nil {
			return err
		}
	}

	return nil
}

func (en *EmbeddedNATS) PublishWithDedup(subject string, data []byte, msgID string) error {
	msg := nats.NewMsg(subject)
	msg.Data = data
	msg.Header.Set("Nats-Msg-Id", msgID)
	
	_, err := en.js.PublishMsg(msg)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}
	
	return nil
}

func (en *EmbeddedNATS) CreateDurableConsumer(streamName, consumerName string, filterSubject string) error {
	config := &nats.ConsumerConfig{
		Durable:         consumerName,
		FilterSubject:   filterSubject,
		AckPolicy:       nats.AckExplicitPolicy,
		AckWait:         30 * time.Second,
		MaxDeliver:      3,
		MaxAckPending:   1000,
		DeliverPolicy:   nats.DeliverAllPolicy,
		ReplayPolicy:    nats.ReplayInstantPolicy,
	}

	// Try to get existing consumer
	_, err := en.js.ConsumerInfo(streamName, consumerName)
	if err == nil {
		// Consumer exists
		log.Printf("Durable consumer already exists: %s on stream: %s", consumerName, streamName)
		return nil
	}

	// Create new consumer
	_, err = en.js.AddConsumer(streamName, config)
	if err != nil {
		return fmt.Errorf("failed to create consumer %s: %w", consumerName, err)
	}

	log.Printf("Created durable consumer: %s on stream: %s", consumerName, streamName)
	return nil
}

func (en *EmbeddedNATS) Connection() *nats.Conn {
	return en.nc
}

func (en *EmbeddedNATS) JetStream() nats.JetStreamContext {
	return en.js
}

func (en *EmbeddedNATS) Shutdown(ctx context.Context) error {
	if en.nc != nil {
		en.nc.Close()
	}

	if en.server != nil {
		en.server.Shutdown()
		en.server.WaitForShutdown()
	}

	return nil
}

func (en *EmbeddedNATS) HealthCheck() error {
	if en.nc == nil {
		return fmt.Errorf("NATS connection not initialized")
	}

	if !en.nc.IsConnected() {
		return fmt.Errorf("NATS not connected")
	}

	if en.server != nil && !en.server.Running() {
		return fmt.Errorf("NATS server not running")
	}

	return nil
}