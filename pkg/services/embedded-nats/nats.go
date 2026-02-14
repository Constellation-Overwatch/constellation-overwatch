package embeddednats

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type Config struct {
	Host             string
	Port             int
	WSPort           int
	WSAllowedOrigins []string // Empty = allow all
	DataDir          string
	MaxMemory        int64
	MaxFileStore     int64
	JetStreamDomain  string
	EnableTLS        bool
	TLSCert          string
	TLSKey           string
	EnableAuth       bool
	AuthToken        string
}

type EmbeddedNATS struct {
	server  *server.Server
	nc      *nats.Conn
	js      nats.JetStreamContext
	kv      nats.KeyValue
	config  *Config
	streams map[string]*StreamConfig
}

type StreamConfig struct {
	Name            string
	Subjects        []string
	Storage         nats.StorageType
	Retention       nats.RetentionPolicy
	MaxMsgs         int64
	MaxBytes        int64
	MaxAge          time.Duration
	MaxMsgSize      int32
	Replicas        int
	DuplicateWindow time.Duration
	AllowRollup     bool
	AllowDirect     bool
	DiscardPolicy   nats.DiscardPolicy
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

// getEnvStringSlice parses a comma-separated env var into a string slice
// Returns empty slice if value is "*" or empty (meaning "allow all")
func getEnvStringSlice(key string) []string {
	value := os.Getenv(key)
	if value == "" || value == "*" {
		return []string{} // Empty = allow all
	}
	var result []string
	for _, s := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func DefaultConfig() *Config {
	return &Config{
		Host:             getEnv("NATS_HOST", "0.0.0.0"), // Bind to all interfaces by default
		Port:             getEnvInt("NATS_PORT", 4222),
		WSPort:           getEnvInt("NATS_WS_PORT", 8222),
		WSAllowedOrigins: getEnvStringSlice("ALLOWED_ORIGINS"),
		DataDir:          getEnv("NATS_DATA_DIR", "./data/overwatch"),
		MaxMemory:        getEnvInt64("NATS_MAX_MEMORY", 1024*1024*1024),       // 1GB (increased for video streams)
		MaxFileStore:     getEnvInt64("NATS_MAX_FILE_STORE", 2*1024*1024*1024), // 2GB
		JetStreamDomain:  getEnv("NATS_JETSTREAM_DOMAIN", "constellation"),
		EnableTLS:        getEnv("NATS_ENABLE_TLS", "false") == "true",
		EnableAuth:       getEnv("OVERWATCH_TOKEN", "") != "", // Enable auth if OVERWATCH_TOKEN is set
		AuthToken:        getEnv("OVERWATCH_TOKEN", ""),
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

// NewService creates a new NATS service with default configuration
func NewService() (*EmbeddedNATS, error) {
	return New(DefaultConfig())
}

// Name returns the service name
func (en *EmbeddedNATS) Name() string {
	return "embedded-nats"
}

// Start initializes and starts the NATS service (implements Service interface)
func (en *EmbeddedNATS) Start(ctx context.Context) error {
	return en.StartEmbedded()
}

// Stop gracefully shuts down the NATS service (implements Service interface)
func (en *EmbeddedNATS) Stop(ctx context.Context) error {
	return en.Shutdown(ctx)
}

func (en *EmbeddedNATS) StartEmbedded() error {
	// Ensure the data directory exists before starting NATS
	if err := os.MkdirAll(en.config.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create NATS data directory %s: %w", en.config.DataDir, err)
	}

	opts := &server.Options{
		Host:      en.config.Host,
		Port:      en.config.Port,
		JetStream: true,
		StoreDir:  en.config.DataDir,

		// Connection limits optimized for video streaming
		MaxConn:        2000,
		MaxSubs:        0,                 // Unlimited subscriptions
		MaxPayload:     512 * 1024,        // 512KB max payload (matches video chunk size)
		MaxPending:     512 * 1024 * 1024, // 512MB pending data for video bursts
		MaxControlLine: 4096,
		WriteDeadline:  2 * time.Second, // Faster failure detection for real-time video

		// Ping settings for connection health
		PingInterval: 30 * time.Second, // More frequent pings for real-time apps
		MaxPingsOut:  3,

		// Disable debug logging by default
		Debug:   false,
		Trace:   false,
		Logtime: true,
		NoSigs:  true, // Disable built-in signal handlers
	}

	// Enable WebSocket only if WSPort > 0
	// Set NATS_WS_PORT=0 to disable (no longer needed since video uses MediaMTX WebRTC)
	if en.config.WSPort > 0 {
		opts.Websocket = server.WebsocketOpts{
			Host:             en.config.Host,
			Port:             en.config.WSPort,
			NoTLS:            !en.config.EnableTLS,
			SameOrigin:       false,
			AllowedOrigins:   en.config.WSAllowedOrigins,
			HandshakeTimeout: 10 * time.Second,
		}
	}

	// Configure Authentication
	if en.config.EnableAuth {
		opts.Authorization = en.config.AuthToken
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

	// Initialize streams and consumers
	if err := en.initializeStreamsAndConsumers(); err != nil {
		return fmt.Errorf("failed to initialize streams and consumers: %w", err)
	}

	logger.Info("Embedded NATS server started",
		zap.String("host", en.config.Host),
		zap.Int("port", en.config.Port),
		zap.Int("ws_port", en.config.WSPort))
	return nil
}

// initializeStreamsAndConsumers sets up all required streams and consumers
func (en *EmbeddedNATS) initializeStreamsAndConsumers() error {
	// Create constellation streams
	if err := en.CreateConstellationStreams(); err != nil {
		return fmt.Errorf("failed to create constellation streams: %w", err)
	}

	// Create global state KV bucket
	if err := en.CreateGlobalStateKV(shared.KVBucketGlobalState); err != nil {
		return fmt.Errorf("failed to create global state KV bucket: %w", err)
	}

	// Create durable consumers
	consumers := []struct {
		stream   string
		consumer string
		filter   string
	}{
		{shared.StreamEntities, shared.ConsumerEntityProcessor, shared.SubjectEntitiesAll},
		{shared.StreamCommands, shared.ConsumerCommandProcessor, shared.SubjectCommandsAll},
		{shared.StreamEvents, shared.ConsumerEventProcessor, shared.SubjectEventsAll},
		{shared.StreamTelemetry, shared.ConsumerTelemetryProcessor, shared.SubjectTelemetryAll},
		{shared.StreamVideoFrames, shared.ConsumerVideoProcessor, shared.SubjectVideoAll},
	}

	for _, c := range consumers {
		if err := en.CreateDurableConsumer(c.stream, c.consumer, c.filter); err != nil {
			return fmt.Errorf("failed to create consumer %s: %w", c.consumer, err)
		}
	}

	return nil
}

func (en *EmbeddedNATS) connect() error {
	url := fmt.Sprintf("nats://localhost:%d", en.config.Port)

	connectOpts := []nats.Option{
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
		nats.PingInterval(20 * time.Second),
		nats.MaxPingsOutstanding(5),
		nats.Timeout(5 * time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			logger.Error("NATS error", zap.Error(err))
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("NATS disconnected", zap.Error(err))
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			logger.Info("NATS reconnected")
		}),
	}

	if en.config.EnableAuth {
		connectOpts = append(connectOpts, nats.Token(en.config.AuthToken))
	}

	nc, err := nats.Connect(url, connectOpts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context with optimized settings for high-throughput telemetry
	js, err := nc.JetStream(
		nats.PublishAsyncMaxPending(256), // Allow more pending async publishes
		nats.MaxWait(3*time.Second),      // Reduced from default 5s for faster failures
	)
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
		Name:        streamConfig.Name,
		Subjects:    streamConfig.Subjects,
		Storage:     streamConfig.Storage,
		Retention:   streamConfig.Retention,
		MaxMsgs:     streamConfig.MaxMsgs,
		MaxBytes:    streamConfig.MaxBytes,
		MaxAge:      streamConfig.MaxAge,
		MaxMsgSize:  streamConfig.MaxMsgSize,
		Replicas:    streamConfig.Replicas,
		Duplicates:  streamConfig.DuplicateWindow,
		AllowRollup: streamConfig.AllowRollup,
		AllowDirect: streamConfig.AllowDirect,
		Discard:     streamConfig.DiscardPolicy,
	}

	// Try to update stream if it exists, otherwise create it
	_, err := en.js.StreamInfo(streamConfig.Name)
	if err == nil {
		// Stream exists, update it
		if _, err = en.js.UpdateStream(config); err != nil {
			return fmt.Errorf("failed to update stream %s: %w", streamConfig.Name, err)
		}
	} else {
		// Stream doesn't exist, create it
		if _, err = en.js.AddStream(config); err != nil {
			return fmt.Errorf("failed to add stream %s: %w", streamConfig.Name, err)
		}
		logger.Debug("Created stream", zap.String("stream", streamConfig.Name))
	}

	en.streams[streamConfig.Name] = streamConfig

	return nil
}

func (en *EmbeddedNATS) CreateConstellationStreams() error {
	streams := []StreamConfig{
		{
			Name:            "CONSTELLATION_ENTITIES",
			Subjects:        []string{"constellation.entities.>"},
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         100000,
			MaxBytes:        256 * 1024 * 1024,  // 256MB
			MaxAge:          7 * 24 * time.Hour, // 7 days
			MaxMsgSize:      1024 * 1024,        // 1MB
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
			Retention:       nats.LimitsPolicy, // Keep based on limits (not consumer interest)
			MaxMsgs:         100000,            // Increased for high-frequency telemetry
			MaxBytes:        256 * 1024 * 1024, // 256MB (increased from 64MB)
			MaxAge:          2 * time.Hour,     // Increased from 1 hour
			MaxMsgSize:      128 * 1024,        // 128KB (increased from 64KB for MAVLink messages)
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
			AllowDirect:     false,           // Commands must go through stream
			DiscardPolicy:   nats.DiscardNew, // Reject new commands if full
		},
		{
			Name:            "CONSTELLATION_VIDEO_FRAMES",
			Subjects:        []string{"constellation.video.>"},
			Storage:         nats.MemoryStorage, // Memory-based for fast access, no persistence
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         0,                  // Unlimited messages (bounded by MaxBytes)
			MaxBytes:        1024 * 1024 * 1024, // 1GB memory for larger swarms
			MaxAge:          10 * time.Second,   // Reduced - video is ephemeral, prevents stale frame buildup
			MaxMsgSize:      256 * 1024,         // 256KB per chunk (2-3 GOP packets, not full frames)
			Replicas:        1,
			DuplicateWindow: 500 * time.Millisecond, // Frame-level dedup (prevents duplicate MPEG-TS packets)
			AllowRollup:     true,                   // Support KV-style latest frame access
			AllowDirect:     true,                   // Enable direct get for latest frame
			DiscardPolicy:   nats.DiscardOld,        // Drop oldest frames when full
		},
	}

	for _, stream := range streams {
		if err := en.AddStream(&stream); err != nil {
			return err
		}
	}

	return nil
}

// CreateGlobalStateKV creates or retrieves the global state KV bucket
func (en *EmbeddedNATS) CreateGlobalStateKV(bucketName string) error {
	if en.js == nil {
		return fmt.Errorf("JetStream not initialized")
	}

	// Configure KV bucket
	config := &nats.KeyValueConfig{
		Bucket:      bucketName,
		Description: "Global state storage for fleets and swarms",
		MaxBytes:    512 * 1024 * 1024, // 512MB
		TTL:         0,                 // No TTL - data persists until deleted
		History:     10,                // Keep last 10 versions
		Replicas:    1,
	}

	// Try to get existing bucket
	kv, err := en.js.KeyValue(bucketName)
	if err != nil {
		// Bucket doesn't exist, create it
		kv, err = en.js.CreateKeyValue(config)
		if err != nil {
			return fmt.Errorf("failed to create KV bucket %s: %w", bucketName, err)
		}
		logger.Debug("Created KV bucket", zap.String("bucket", bucketName))
	}

	en.kv = kv
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
		Durable:       consumerName,
		FilterSubject: filterSubject,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    3,
		MaxAckPending: 1000,
		DeliverPolicy: nats.DeliverAllPolicy,
		ReplayPolicy:  nats.ReplayInstantPolicy,
	}

	// Try to get existing consumer
	_, err := en.js.ConsumerInfo(streamName, consumerName)
	if err == nil {
		// Consumer exists
		logger.Debug("Durable consumer already exists",
			zap.String("consumer", consumerName),
			zap.String("stream", streamName))
		return nil
	}

	// Create new consumer
	_, err = en.js.AddConsumer(streamName, config)
	if err != nil {
		return fmt.Errorf("failed to create consumer %s: %w", consumerName, err)
	}

	logger.Debug("Created durable consumer",
		zap.String("consumer", consumerName),
		zap.String("stream", streamName))
	return nil
}

func (en *EmbeddedNATS) Connection() *nats.Conn {
	return en.nc
}

func (en *EmbeddedNATS) JetStream() nats.JetStreamContext {
	return en.js
}

func (en *EmbeddedNATS) KeyValue() nats.KeyValue {
	return en.kv
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

func (en *EmbeddedNATS) GetConnection() *nats.Conn {
	return en.nc
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

// WatchKV watches for changes in the KV store and calls the callback for each change
func (en *EmbeddedNATS) WatchKV(ctx context.Context, callback func(key string, entry nats.KeyValueEntry) error) error {
	if en.kv == nil {
		return fmt.Errorf("KV store not initialized")
	}

	// Use a separate context for the watcher to isolate it from the HTTP request context's quirks
	// We will manually stop the watcher when the passed ctx is Done.
	watcher, err := en.kv.WatchAll()
	if err != nil {
		return fmt.Errorf("failed to watch KV: %w", err)
	}
	defer func() {
		// Ensure we stop the watcher to free resources
		if err := watcher.Stop(); err != nil {
			logger.Warnw("Failed to stop watcher", "error", err)
		}
	}()

	logger.Debugw("KV watcher started", "bucket", "CONSTELLATION_GLOBAL_STATE")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry, ok := <-watcher.Updates():
			if !ok {
				// Channel closed
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logger.Warn("KV watcher channel closed unexpectedly")
				return nil
			}

			if entry == nil {
				continue
			}

			if err := callback(entry.Key(), entry); err != nil {
				// If the context is canceled, return immediately
				if ctx.Err() != nil {
					return ctx.Err()
				}

				logger.Error("Error in KV watch callback",
					zap.String("key", entry.Key()),
					zap.Error(err))
				// Continue watching despite callback errors
			}
		}
	}
}

// GetAllKVEntries retrieves all entries from the KV store
func (en *EmbeddedNATS) GetAllKVEntries() ([]nats.KeyValueEntry, error) {
	if en.kv == nil {
		return nil, fmt.Errorf("KV store not initialized")
	}

	keys, err := en.kv.Keys()
	if err != nil {
		return nil, fmt.Errorf("failed to get keys: %w", err)
	}

	var entries []nats.KeyValueEntry
	for _, key := range keys {
		entry, err := en.kv.Get(key)
		if err != nil {
			logger.Error("Error getting key from KV store",
				zap.String("key", key),
				zap.Error(err))
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// UpdateKVWithRevision performs an optimistic lock update on a KV entry
// Returns the new revision number or error if the revision has changed
func (en *EmbeddedNATS) UpdateKVWithRevision(key string, data []byte, expectedRevision uint64) (uint64, error) {
	if en.kv == nil {
		return 0, fmt.Errorf("KV store not initialized")
	}

	// Use the Update method which checks the revision
	newRevision, err := en.kv.Update(key, data, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("failed to update key %s at revision %d: %w", key, expectedRevision, err)
	}

	return newRevision, nil
}

// GetKVEntry retrieves a single KV entry with its revision
func (en *EmbeddedNATS) GetKVEntry(key string) (nats.KeyValueEntry, error) {
	if en.kv == nil {
		return nil, fmt.Errorf("KV store not initialized")
	}

	entry, err := en.kv.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get key %s: %w", key, err)
	}

	return entry, nil
}
