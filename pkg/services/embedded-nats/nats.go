package embeddednats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/services/logger"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

const (
	defaultObjectStoreMaxBytes = 512 * 1024 * 1024
	mb                         = 1024 * 1024
)

type Config struct {
	Host                   string
	Port                   int
	DataDir                string
	MaxMemory              int64
	MaxFileStore           int64
	JetStreamDomain        string
	EnableTLS              bool
	TLSCert                string
	TLSKey                 string
	TLSCA                  string
	ExternalEnabled        bool
	ExternalURL            string
	Username               string
	Password               string
	Token                  string
	CredentialsFile        string
	NKeySeedFile           string
	StreamReplicas         int
	ObjectStoreBucket      string
	ObjectStoreMaxBytes    int64
	ObjectStoreCompression bool
}

type EmbeddedNATS struct {
	server     *server.Server
	nc         *nats.Conn
	js         nats.JetStreamContext
	kv         nats.KeyValue
	obj        nats.ObjectStore
	config     *Config
	streams    map[string]*StreamConfig
	mu         sync.Mutex      // protects serverOpts for NKey management
	serverOpts *server.Options // tracks current opts for ReloadOptions
	authToken  string          // internal auth token for embedded connections
}

var ErrExternalNKeyManagement = errors.New("NKey management not supported in external NATS mode")

// NKeyRecord is the minimal data needed to restore a NATS credential on startup.
type NKeyRecord struct {
	NATSPubKey string
	OrgID      string
	Scopes     []string
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

func getEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off":
			return false
		}
	}
	return fallback
}

func DefaultConfig() *Config {
	externalURL := shared.GetEnv("NATS_URL", "")
	externalFlag := getEnvBool("NATS_EXTERNAL_ENABLED", externalURL != "")
	externalEnabled := externalFlag || externalURL != ""
	domainDefault := "constellation"
	if externalEnabled {
		domainDefault = ""
	}

	return &Config{
		Host:                   shared.GetEnv("NATS_HOST", "127.0.0.1"), // Bind to localhost by default
		Port:                   getEnvInt("NATS_PORT", 4222),
		DataDir:                shared.GetEnv("OVERWATCH_DATA_DIR", "./data") + "/overwatch",
		MaxMemory:              getEnvInt64("NATS_MAX_MEMORY", 1024*1024*1024),       // 1GB
		MaxFileStore:           getEnvInt64("NATS_MAX_FILE_STORE", 2*1024*1024*1024), // 2GB
		JetStreamDomain:        shared.GetEnv("NATS_JETSTREAM_DOMAIN", domainDefault),
		EnableTLS:              getEnvBool("NATS_ENABLE_TLS", false),
		TLSCert:                shared.GetEnv("NATS_TLS_CERT", ""),
		TLSKey:                 shared.GetEnv("NATS_TLS_KEY", ""),
		TLSCA:                  shared.GetEnv("NATS_TLS_CA", ""),
		ExternalEnabled:        externalEnabled,
		ExternalURL:            externalURL,
		Username:               shared.GetEnv("NATS_USER", ""),
		Password:               shared.GetEnv("NATS_PASSWORD", ""),
		Token:                  shared.GetEnv("NATS_TOKEN", ""),
		CredentialsFile:        shared.GetEnv("NATS_CREDS", ""),
		NKeySeedFile:           shared.GetEnv("NATS_NKEY_SEED_FILE", ""),
		StreamReplicas:         getEnvInt("NATS_STREAM_REPLICAS", 1),
		ObjectStoreBucket:      shared.GetEnv("NATS_OBJECT_STORE_BUCKET", shared.ObjectStoreBucketConstellation),
		ObjectStoreMaxBytes:    getEnvInt64("NATS_OBJECT_STORE_MAX_BYTES", defaultObjectStoreMaxBytes),
		ObjectStoreCompression: getEnvBool("NATS_OBJECT_STORE_COMPRESSION", false),
	}
}

func New(cfg *Config) (*EmbeddedNATS, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if cfg.StreamReplicas < 1 || cfg.StreamReplicas > 5 {
		return nil, fmt.Errorf("NATS_STREAM_REPLICAS must be between 1 and 5, got %d", cfg.StreamReplicas)
	}
	if cfg.ObjectStoreMaxBytes < 0 {
		return nil, fmt.Errorf("NATS_OBJECT_STORE_MAX_BYTES must be greater than or equal to 0, got %d", cfg.ObjectStoreMaxBytes)
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
	if en.IsExternal() {
		return en.StartExternal(ctx)
	}
	return en.StartEmbedded()
}

// Stop gracefully shuts down the NATS service (implements Service interface)
func (en *EmbeddedNATS) Stop(ctx context.Context) error {
	return en.Shutdown(ctx)
}

func (en *EmbeddedNATS) IsExternal() bool {
	return en.config.ExternalEnabled || strings.TrimSpace(en.config.ExternalURL) != ""
}

func (en *EmbeddedNATS) SupportsNKeyUserManagement() bool {
	return !en.IsExternal() && en.server != nil && en.serverOpts != nil
}

func (en *EmbeddedNATS) StartExternal(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(en.config.ExternalURL) == "" {
		return fmt.Errorf("NATS_URL is required when NATS_EXTERNAL_ENABLED is true")
	}

	if err := en.connect(en.config.ExternalURL); err != nil {
		return fmt.Errorf("failed to connect to external NATS: %w", err)
	}

	if err := en.initializeStreamsAndConsumers(); err != nil {
		return fmt.Errorf("failed to initialize streams and consumers: %w", err)
	}

	logger.Infow("External NATS connected",
		"servers", countNATSURLs(en.config.ExternalURL),
		"jetstream_domain", en.config.JetStreamDomain,
		"stream_replicas", en.config.StreamReplicas)
	return nil
}

func (en *EmbeddedNATS) StartEmbedded() error {
	// Ensure the data directory exists before starting NATS
	if err := os.MkdirAll(en.config.DataDir, 0700); err != nil {
		return fmt.Errorf("failed to create NATS data directory %s: %w", en.config.DataDir, err)
	}
	if err := en.validateEmbeddedStorageBudget(); err != nil {
		return err
	}

	// Generate internal auth token for embedded connections.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("failed to generate NATS auth token: %w", err)
	}
	en.authToken = hex.EncodeToString(tokenBytes)

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

		// Internal auth token for embedded connections
		Authorization: en.authToken,
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

	// Use a quiet logger that only surfaces warnings/errors through zap,
	// silencing the verbose NATS/JetStream boot info lines.
	ns.SetLogger(&quietLogger{}, false, false)

	go ns.Start()

	if !ns.ReadyForConnections(10 * time.Second) {
		return fmt.Errorf("NATS server not ready for connections")
	}

	en.server = ns
	en.serverOpts = opts

	if err := en.connect(fmt.Sprintf("nats://localhost:%d", en.config.Port)); err != nil {
		return fmt.Errorf("failed to connect to embedded NATS: %w", err)
	}

	// Initialize streams and consumers
	if err := en.initializeStreamsAndConsumers(); err != nil {
		return fmt.Errorf("failed to initialize streams and consumers: %w", err)
	}

	logger.Info("Embedded NATS server started",
		zap.String("host", en.config.Host),
		zap.Int("port", en.config.Port),
		zap.Int("stream_replicas", en.config.StreamReplicas))
	return nil
}

func (en *EmbeddedNATS) validateEmbeddedStorageBudget() error {
	if en.config.MaxFileStore <= 0 {
		return nil
	}

	required := int64(0)
	for _, stream := range en.constellationStreamConfigs() {
		if stream.Storage == nats.FileStorage && stream.MaxBytes > 0 {
			required += stream.MaxBytes
		}
	}
	required += 512 * mb // CONSTELLATION_GLOBAL_STATE KV bucket.
	if en.config.ObjectStoreBucket != "" && en.config.ObjectStoreMaxBytes > 0 {
		required += en.config.ObjectStoreMaxBytes
	}

	if required <= en.config.MaxFileStore {
		return nil
	}

	return fmt.Errorf(
		"configured JetStream storage reservations (%d bytes) exceed NATS_MAX_FILE_STORE (%d bytes); reduce NATS_OBJECT_STORE_MAX_BYTES or increase NATS_MAX_FILE_STORE",
		required,
		en.config.MaxFileStore,
	)
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

	if en.config.ObjectStoreBucket != "" {
		if err := en.CreateObjectStore(en.config.ObjectStoreBucket); err != nil {
			return fmt.Errorf("failed to create object store %s: %w", en.config.ObjectStoreBucket, err)
		}
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
		{shared.StreamAgentOps, shared.ConsumerAgentOpsProcessor, shared.SubjectAgentOpsAll},
	}

	for _, c := range consumers {
		if err := en.CreateDurableConsumer(c.stream, c.consumer, c.filter); err != nil {
			return fmt.Errorf("failed to create consumer %s: %w", c.consumer, err)
		}
	}

	return nil
}

// AuthToken returns the internal auth token for external clients that need
// to connect to the embedded NATS server.
func (en *EmbeddedNATS) AuthToken() string {
	return en.authToken
}

func (en *EmbeddedNATS) connect(connectURL string) error {
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

	if en.IsExternal() {
		authOpts, err := en.externalAuthOptions()
		if err != nil {
			return err
		}
		connectOpts = append(connectOpts, authOpts...)
	} else {
		connectOpts = append(connectOpts, nats.Token(en.authToken))
	}
	if en.config.EnableTLS {
		connectOpts = append(connectOpts, nats.Secure())
	}
	if en.config.TLSCA != "" {
		connectOpts = append(connectOpts, nats.RootCAs(en.config.TLSCA))
	}
	if en.config.TLSCert != "" && en.config.TLSKey != "" && en.IsExternal() {
		connectOpts = append(connectOpts, nats.ClientCert(en.config.TLSCert, en.config.TLSKey))
	}

	nc, err := nats.Connect(connectURL, connectOpts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	jsOpts := []nats.JSOpt{
		nats.PublishAsyncMaxPending(256), // Allow more pending async publishes
		nats.MaxWait(3 * time.Second),    // Reduced from default 5s for faster failures
	}
	if en.config.JetStreamDomain != "" {
		jsOpts = append(jsOpts, nats.Domain(en.config.JetStreamDomain))
	}

	// Create JetStream context with optimized settings for high-throughput telemetry
	js, err := nc.JetStream(jsOpts...)
	if err != nil {
		nc.Close()
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	en.nc = nc
	en.js = js
	return nil
}

func (en *EmbeddedNATS) externalAuthOptions() ([]nats.Option, error) {
	opts := make([]nats.Option, 0, 3)
	if en.config.CredentialsFile != "" {
		opts = append(opts, nats.UserCredentials(en.config.CredentialsFile))
		return opts, nil
	}
	if en.config.NKeySeedFile != "" {
		nkeyOpt, err := nats.NkeyOptionFromSeed(en.config.NKeySeedFile)
		if err != nil {
			return nil, fmt.Errorf("failed to configure NATS NKey seed file: %w", err)
		}
		opts = append(opts, nkeyOpt)
	}
	if en.config.Token != "" {
		opts = append(opts, nats.Token(en.config.Token))
	}
	if en.config.Username != "" || en.config.Password != "" {
		opts = append(opts, nats.UserInfo(en.config.Username, en.config.Password))
	}
	return opts, nil
}

func countNATSURLs(raw string) int {
	count := 0
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
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
	} else if errors.Is(err, nats.ErrStreamNotFound) {
		// Stream doesn't exist, create it
		if _, err = en.js.AddStream(config); err != nil {
			return fmt.Errorf("failed to add stream %s: %w", streamConfig.Name, err)
		}
		logger.Debug("Created stream", zap.String("stream", streamConfig.Name))
	} else {
		return fmt.Errorf("failed to get stream %s: %w", streamConfig.Name, err)
	}

	en.streams[streamConfig.Name] = streamConfig

	return nil
}

func (en *EmbeddedNATS) CreateConstellationStreams() error {
	streams := en.constellationStreamConfigs()

	for _, stream := range streams {
		if err := en.AddStream(&stream); err != nil {
			return err
		}
	}

	return nil
}

func (en *EmbeddedNATS) constellationStreamConfigs() []StreamConfig {
	replicas := en.config.StreamReplicas
	return []StreamConfig{
		{
			Name:            "CONSTELLATION_ENTITIES",
			Subjects:        []string{"constellation.entities.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         100000,
			MaxBytes:        256 * 1024 * 1024,  // 256MB
			MaxAge:          7 * 24 * time.Hour, // 7 days
			MaxMsgSize:      1024 * 1024,        // 1MB
			Replicas:        replicas,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     true,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_EVENTS",
			Subjects:        []string{"constellation.events.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.WorkQueuePolicy, // Events consumed once
			MaxMsgs:         50000,
			MaxBytes:        128 * 1024 * 1024, // 128MB
			MaxAge:          24 * time.Hour,
			MaxMsgSize:      256 * 1024, // 256KB
			Replicas:        replicas,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_TELEMETRY",
			Subjects:        []string{"constellation.telemetry.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.LimitsPolicy, // Keep based on limits (not consumer interest)
			MaxMsgs:         100000,            // Increased for high-frequency telemetry
			MaxBytes:        256 * 1024 * 1024, // 256MB (increased from 64MB)
			MaxAge:          2 * time.Hour,     // Increased from 1 hour
			MaxMsgSize:      128 * 1024,        // 128KB (increased from 64KB for MAVLink messages)
			Replicas:        replicas,
			DuplicateWindow: 30 * time.Second,
			AllowRollup:     true,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_ORGANIZATIONS",
			Subjects:        []string{"constellation.organizations.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         10000,
			MaxBytes:        32 * 1024 * 1024,   // 32MB
			MaxAge:          7 * 24 * time.Hour, // 7 days
			MaxMsgSize:      256 * 1024,         // 256KB
			Replicas:        replicas,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
		{
			Name:            "CONSTELLATION_COMMANDS",
			Subjects:        []string{"constellation.commands.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.WorkQueuePolicy,
			MaxMsgs:         10000,
			MaxBytes:        32 * 1024 * 1024, // 32MB
			MaxAge:          15 * time.Minute,
			MaxMsgSize:      32 * 1024, // 32KB
			Replicas:        replicas,
			DuplicateWindow: 1 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     false,           // Commands must go through stream
			DiscardPolicy:   nats.DiscardNew, // Reject new commands if full
		},
		{
			Name:            "CONSTELLATION_AGENTOPS",
			Subjects:        []string{"constellation.agentops.>"},
			Storage:         nats.FileStorage,
			Retention:       nats.LimitsPolicy,
			MaxMsgs:         50000,
			MaxBytes:        128 * 1024 * 1024,
			MaxAge:          7 * 24 * time.Hour,
			MaxMsgSize:      256 * 1024,
			Replicas:        replicas,
			DuplicateWindow: 2 * time.Minute,
			AllowRollup:     false,
			AllowDirect:     true,
			DiscardPolicy:   nats.DiscardOld,
		},
	}
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
		Replicas:    en.config.StreamReplicas,
		Storage:     nats.FileStorage,
	}

	// Try to get existing bucket
	kv, err := en.js.KeyValue(bucketName)
	if err != nil {
		// Bucket doesn't exist, create it
		if !errors.Is(err, nats.ErrBucketNotFound) {
			return fmt.Errorf("failed to get KV bucket %s: %w", bucketName, err)
		}
		kv, err = en.js.CreateKeyValue(config)
		if err != nil {
			return fmt.Errorf("failed to create KV bucket %s: %w", bucketName, err)
		}
		logger.Debug("Created KV bucket", zap.String("bucket", bucketName))
	}

	en.kv = kv
	return nil
}

func (en *EmbeddedNATS) CreateObjectStore(bucketName string) error {
	if en.js == nil {
		return fmt.Errorf("JetStream not initialized")
	}
	if bucketName == "" {
		return fmt.Errorf("object store bucket name is required")
	}

	config := &nats.ObjectStoreConfig{
		Bucket:      bucketName,
		Description: "Object storage for Constellation artifacts and payloads",
		MaxBytes:    en.config.ObjectStoreMaxBytes,
		Storage:     nats.FileStorage,
		Replicas:    en.config.StreamReplicas,
		Compression: en.config.ObjectStoreCompression,
	}

	obj, err := en.js.ObjectStore(bucketName)
	if err != nil {
		if !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("failed to get object store %s: %w", bucketName, err)
		}
		obj, err = en.js.CreateObjectStore(config)
		if err != nil {
			return fmt.Errorf("failed to create object store %s: %w", bucketName, err)
		}
		logger.Debug("Created object store", zap.String("bucket", bucketName))
	}

	if status, err := obj.Status(); err == nil && status.Replicas() != en.config.StreamReplicas {
		logger.Warnw("Object store replica count differs from configuration",
			"bucket", bucketName,
			"configured_replicas", en.config.StreamReplicas,
			"actual_replicas", status.Replicas())
	}

	en.obj = obj
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
		Replicas:      en.config.StreamReplicas,
	}

	// Try to get existing consumer
	_, err := en.js.ConsumerInfo(streamName, consumerName)
	if err == nil {
		if _, err := en.js.UpdateConsumer(streamName, config); err != nil {
			return fmt.Errorf("failed to update consumer %s: %w", consumerName, err)
		}
		logger.Debug("Updated durable consumer",
			zap.String("consumer", consumerName),
			zap.String("stream", streamName))
		return nil
	} else if !errors.Is(err, nats.ErrConsumerNotFound) {
		return fmt.Errorf("failed to get consumer %s: %w", consumerName, err)
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

func (en *EmbeddedNATS) ObjectStore() nats.ObjectStore {
	return en.obj
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

// AddNKeyUser registers a new NKey user with scoped permissions on the running server.
func (en *EmbeddedNATS) AddNKeyUser(publicKey string, perms *server.Permissions) error {
	en.mu.Lock()
	defer en.mu.Unlock()

	if en.IsExternal() || en.server == nil {
		return ErrExternalNKeyManagement
	}

	if en.serverOpts == nil {
		return fmt.Errorf("server options not initialized")
	}

	newOpts := en.serverOpts.Clone()

	// Check for duplicate
	for _, nk := range newOpts.Nkeys {
		if nk.Nkey == publicKey {
			return nil
		}
	}

	newOpts.Nkeys = append(newOpts.Nkeys, &server.NkeyUser{
		Nkey:        publicKey,
		Permissions: perms,
	})

	if err := en.server.ReloadOptions(newOpts); err != nil {
		return fmt.Errorf("failed to reload NATS options: %w", err)
	}

	en.serverOpts = newOpts
	logger.Infow("Added NATS NKey user", "public_key", publicKey[:12]+"...")
	return nil
}

// RemoveNKeyUser removes an NKey user and reloads server options.
func (en *EmbeddedNATS) RemoveNKeyUser(publicKey string) error {
	en.mu.Lock()
	defer en.mu.Unlock()

	if en.IsExternal() || en.server == nil {
		return ErrExternalNKeyManagement
	}

	if en.serverOpts == nil {
		return fmt.Errorf("server options not initialized")
	}

	newOpts := en.serverOpts.Clone()
	filtered := make([]*server.NkeyUser, 0, len(newOpts.Nkeys))
	found := false
	for _, nk := range newOpts.Nkeys {
		if nk.Nkey == publicKey {
			found = true
			continue
		}
		filtered = append(filtered, nk)
	}
	if !found {
		return nil
	}
	newOpts.Nkeys = filtered

	if err := en.server.ReloadOptions(newOpts); err != nil {
		return fmt.Errorf("failed to reload NATS options: %w", err)
	}

	en.serverOpts = newOpts
	logger.Infow("Removed NATS NKey user", "public_key", publicKey[:12]+"...")
	return nil
}

// RestoreNKeyUsers re-registers NKey users from stored records on startup.
func (en *EmbeddedNATS) RestoreNKeyUsers(keys []NKeyRecord) error {
	en.mu.Lock()
	defer en.mu.Unlock()

	if en.IsExternal() || en.server == nil {
		return ErrExternalNKeyManagement
	}

	if en.serverOpts == nil {
		return fmt.Errorf("server options not initialized")
	}

	newOpts := en.serverOpts.Clone()
	for _, key := range keys {
		if key.NATSPubKey == "" {
			continue
		}
		perms := BuildNATSPermissions(key.Scopes, key.OrgID)
		if perms == nil {
			continue
		}
		newOpts.Nkeys = append(newOpts.Nkeys, &server.NkeyUser{
			Nkey:        key.NATSPubKey,
			Permissions: perms,
		})
	}

	if err := en.server.ReloadOptions(newOpts); err != nil {
		return fmt.Errorf("failed to restore NATS NKey users: %w", err)
	}

	en.serverOpts = newOpts
	logger.Infow("Restored NATS NKey users", "count", len(keys))
	return nil
}

// quietLogger implements server.Logger, routing only warnings/errors/fatals
// through our zap logger and silencing the verbose NATS boot info.
type quietLogger struct{}

func (q *quietLogger) Noticef(format string, v ...any) {}
func (q *quietLogger) Debugf(format string, v ...any)  {}
func (q *quietLogger) Tracef(format string, v ...any)  {}
func (q *quietLogger) Warnf(format string, v ...any) {
	logger.Warnw(fmt.Sprintf(format, v...), "component", "nats")
}
func (q *quietLogger) Errorf(format string, v ...any) {
	logger.Errorw(fmt.Sprintf(format, v...), "component", "nats")
}
func (q *quietLogger) Fatalf(format string, v ...any) {
	logger.Errorw(fmt.Sprintf(format, v...), "component", "nats")
}

// BuildNATSPermissions converts scope strings to NATS subject permissions.
func BuildNATSPermissions(scopes []string, orgID string) *server.Permissions {
	var pubAllow, subAllow []string

	baseAllow := []string{"$JS.API.>", "_INBOX.>"}

	for _, scope := range scopes {
		switch scope {
		case "nats:all":
			return &server.Permissions{
				Publish:   &server.SubjectPermission{Allow: []string{">"}},
				Subscribe: &server.SubjectPermission{Allow: []string{">"}},
			}
		case "nats:telemetry":
			pubAllow = append(pubAllow, fmt.Sprintf("constellation.telemetry.%s.>", orgID))
			subAllow = append(subAllow, fmt.Sprintf("constellation.telemetry.%s.>", orgID))
		case "nats:commands":
			subAllow = append(subAllow, fmt.Sprintf("constellation.commands.%s.>", orgID))
		case "nats:commands:write":
			pubAllow = append(pubAllow, fmt.Sprintf("constellation.commands.%s.>", orgID))
		case "nats:entities":
			pubAllow = append(pubAllow, fmt.Sprintf("constellation.entities.%s.>", orgID))
			subAllow = append(subAllow, fmt.Sprintf("constellation.entities.%s.>", orgID))
		case "nats:events":
			subAllow = append(subAllow, "constellation.events.>")
		case "nats:agentops":
			pubAllow = append(pubAllow, "constellation.agentops.>")
			subAllow = append(subAllow, "constellation.agentops.>")
		}
	}

	if len(pubAllow) == 0 && len(subAllow) == 0 {
		return nil
	}

	return &server.Permissions{
		Publish:   &server.SubjectPermission{Allow: append(baseAllow, pubAllow...)},
		Subscribe: &server.SubjectPermission{Allow: append(baseAllow, subAllow...)},
	}
}
