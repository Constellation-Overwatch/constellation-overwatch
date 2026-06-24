package embeddednats

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestBuildNATSPermissionsAgentOpsScope(t *testing.T) {
	perms := BuildNATSPermissions([]string{"nats:agentops"}, "org-1")
	if perms == nil || perms.Publish == nil || perms.Subscribe == nil {
		t.Fatalf("expected publish and subscribe permissions, got %#v", perms)
	}

	if !containsSubject(perms.Publish.Allow, "constellation.agentops.>") {
		t.Fatalf("publish allow = %#v, want constellation.agentops.>", perms.Publish.Allow)
	}
	if !containsSubject(perms.Subscribe.Allow, "constellation.agentops.>") {
		t.Fatalf("subscribe allow = %#v, want constellation.agentops.>", perms.Subscribe.Allow)
	}
}

func TestDefaultConfigExternalURL(t *testing.T) {
	t.Setenv("NATS_URL", "nats://nats-1.example.com:4222,nats://nats-2.example.com:4222")
	t.Setenv("NATS_EXTERNAL_ENABLED", "")
	t.Setenv("NATS_JETSTREAM_DOMAIN", "")
	t.Setenv("NATS_STREAM_REPLICAS", "3")

	cfg := DefaultConfig()
	if !cfg.ExternalEnabled {
		t.Fatal("expected external mode when NATS_URL is set")
	}
	if cfg.ExternalURL == "" {
		t.Fatal("expected external URL to be set")
	}
	if cfg.JetStreamDomain != "" {
		t.Fatalf("external JetStream domain default = %q, want empty", cfg.JetStreamDomain)
	}
	if cfg.StreamReplicas != 3 {
		t.Fatalf("stream replicas = %d, want 3", cfg.StreamReplicas)
	}
}

func TestNewRejectsInvalidReplicaCounts(t *testing.T) {
	for _, replicas := range []int{0, 6} {
		_, err := New(&Config{StreamReplicas: replicas})
		if err == nil {
			t.Fatalf("expected error for replicas=%d", replicas)
		}
	}
}

func TestExternalModeRejectsNKeyManagement(t *testing.T) {
	en, err := New(&Config{
		ExternalEnabled: true,
		ExternalURL:     "nats://nats.example.com:4222",
		StreamReplicas:  1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := en.AddNKeyUser("UABC", nil); !errors.Is(err, ErrExternalNKeyManagement) {
		t.Fatalf("AddNKeyUser() error = %v, want ErrExternalNKeyManagement", err)
	}
	if err := en.RemoveNKeyUser("UABC"); !errors.Is(err, ErrExternalNKeyManagement) {
		t.Fatalf("RemoveNKeyUser() error = %v, want ErrExternalNKeyManagement", err)
	}
	if err := en.RestoreNKeyUsers(nil); !errors.Is(err, ErrExternalNKeyManagement) {
		t.Fatalf("RestoreNKeyUsers() error = %v, want ErrExternalNKeyManagement", err)
	}
}

func TestEmbeddedStorageBudgetRejectsOversizedObjectStore(t *testing.T) {
	en, err := New(&Config{
		StreamReplicas:      1,
		MaxFileStore:        2 * 1024 * 1024 * 1024,
		ObjectStoreBucket:   "TEST_OBJECTS",
		ObjectStoreMaxBytes: 2 * 1024 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := en.validateEmbeddedStorageBudget(); err == nil {
		t.Fatal("expected oversized object store budget to fail")
	}
}

func TestEmbeddedStartInitializesJetStreamPrimitives(t *testing.T) {
	en, err := New(&Config{
		Host:                "127.0.0.1",
		Port:                freeTCPPort(t),
		DataDir:             t.TempDir(),
		MaxMemory:           64 * 1024 * 1024,
		MaxFileStore:        2 * 1024 * 1024 * 1024,
		StreamReplicas:      1,
		ObjectStoreBucket:   "TEST_OBJECTS",
		ObjectStoreMaxBytes: 32 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := en.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		if err := en.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	}()

	if en.KeyValue() == nil {
		t.Fatal("expected KV bucket to be initialized")
	}
	if en.ObjectStore() == nil {
		t.Fatal("expected object store to be initialized")
	}
	if _, err := en.JetStream().StreamInfo(shared.StreamTelemetry); err != nil {
		t.Fatalf("telemetry stream info error = %v", err)
	}
	status, err := en.ObjectStore().Status()
	if err != nil {
		t.Fatalf("object store status error = %v", err)
	}
	if status.Replicas() != 1 {
		t.Fatalf("object store replicas = %d, want 1", status.Replicas())
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on random port: %v", err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

func containsSubject(subjects []string, want string) bool {
	for _, subject := range subjects {
		if subject == want {
			return true
		}
	}
	return false
}
