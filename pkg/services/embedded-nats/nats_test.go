package embeddednats

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

func TestBuildNATSPermissionsLeastPrivilege(t *testing.T) {
	t.Parallel()

	permissions := BuildNATSPermissions([]string{
		shared.ScopeNATSTelemetryWrite,
		shared.ScopeNATSCommandsRead,
		shared.ScopeNATSEventsWrite,
	}, "org-a")
	if permissions == nil {
		t.Fatal("BuildNATSPermissions() returned nil")
	}

	wantPublish := []string{
		"constellation.telemetry.org-a.>",
		"constellation.events.org-a.>",
		"constellation.events.*.org-a.>",
	}
	wantSubscribe := []string{"_INBOX.>", "constellation.commands.org-a.>"}
	if !reflect.DeepEqual(permissions.Publish.Allow, wantPublish) {
		t.Fatalf("publish allow = %#v, want %#v", permissions.Publish.Allow, wantPublish)
	}
	if !reflect.DeepEqual(permissions.Subscribe.Allow, wantSubscribe) {
		t.Fatalf("subscribe allow = %#v, want %#v", permissions.Subscribe.Allow, wantSubscribe)
	}

	for _, subject := range append(permissions.Publish.Allow, permissions.Subscribe.Allow...) {
		if subject == ">" || strings.HasPrefix(subject, "$JS.API") {
			t.Fatalf("privileged subject leaked into edge permissions: %q", subject)
		}
		if strings.Contains(subject, "org-b") {
			t.Fatalf("cross-organization subject leaked into edge permissions: %q", subject)
		}
	}
}

func TestBuildNATSPermissionsRejectsBroadOrInvalidScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		scopes []string
		orgID  string
	}{
		{name: "nats all", scopes: []string{"nats:all"}, orgID: "org-a"},
		{name: "ambiguous legacy telemetry", scopes: []string{"nats:telemetry"}, orgID: "org-a"},
		{name: "unknown", scopes: []string{"nats:unknown"}, orgID: "org-a"},
		{name: "empty organization", scopes: []string{shared.ScopeNATSTelemetryWrite}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := BuildNATSPermissions(tt.scopes, tt.orgID); got != nil {
				t.Fatalf("BuildNATSPermissions() = %#v, want nil", got)
			}
		})
	}
}

func TestNATSPermissionsConformance(t *testing.T) {
	t.Parallel()

	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	publicKey, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}

	permissions := BuildNATSPermissions([]string{
		shared.ScopeNATSTelemetryWrite,
		shared.ScopeNATSCommandsRead,
	}, "org-a")
	ns, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      server.RANDOM_PORT,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  t.TempDir(),
		Nkeys: []*server.NkeyUser{{
			Nkey:        publicKey,
			Permissions: permissions,
		}},
	})
	if err != nil {
		t.Fatalf("create NATS server: %v", err)
	}
	go ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server was not ready")
	}

	permissionErrors := make(chan error, 8)
	nc, err := nats.Connect(
		ns.ClientURL(),
		nats.Nkey(publicKey, kp.Sign),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			permissionErrors <- err
		}),
	)
	if err != nil {
		t.Fatalf("connect edge NKey: %v", err)
	}
	t.Cleanup(nc.Close)

	if err := nc.Publish("constellation.telemetry.org-a.entity-1", []byte("ok")); err != nil {
		t.Fatalf("publish allowed telemetry: %v", err)
	}
	if err := nc.FlushTimeout(time.Second); err != nil {
		t.Fatalf("flush allowed telemetry: %v", err)
	}
	assertNoNATSError(t, permissionErrors)

	assertNATSPermissionDenied(t, nc, permissionErrors, "constellation.telemetry.org-b.entity-1")
	assertNATSPermissionDenied(t, nc, permissionErrors, "$JS.API.STREAM.CREATE.ATTACK")
}

func assertNATSPermissionDenied(t *testing.T, nc *nats.Conn, errors <-chan error, subject string) {
	t.Helper()
	if err := nc.Publish(subject, []byte("denied")); err != nil {
		return
	}
	_ = nc.FlushTimeout(time.Second)
	select {
	case err := <-errors:
		if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") {
			t.Fatalf("publish %q produced unexpected error: %v", subject, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("publish %q did not fail with a permission violation", subject)
	}
}

func assertNoNATSError(t *testing.T, errors <-chan error) {
	t.Helper()
	select {
	case err := <-errors:
		t.Fatalf("allowed NATS operation failed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
