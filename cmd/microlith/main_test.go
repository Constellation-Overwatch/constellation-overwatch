package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
)

func TestBootstrapAdminWritesProductionSecretToMode0600File(t *testing.T) {
	root := t.TempDir()
	dbService := newBootstrapTestDB(t, root)
	outputPath := filepath.Join(root, "secrets", "bootstrap.txt")
	cfg := &runtimeconfig.Runtime{
		Environment:   runtimeconfig.EnvironmentProduction,
		BaseURL:       "https://hub.galaxyuas.com",
		AdminEmail:    "operations@galaxyuas.com",
		BootstrapFile: outputPath,
	}

	if err := bootstrapAdmin(dbService, cfg); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat bootstrap output: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("bootstrap output mode = %o", perm)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read bootstrap output: %v", err)
	}
	if !strings.Contains(string(body), "https://hub.galaxyuas.com/invite/") {
		t.Fatalf("bootstrap output missing HTTPS setup URL: %q", body)
	}

	var users, invites int
	if err := dbService.GetDB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := dbService.GetDB().QueryRow(`SELECT COUNT(*) FROM invites`).Scan(&invites); err != nil {
		t.Fatal(err)
	}
	if users != 1 || invites != 1 {
		t.Fatalf("bootstrap rows: users=%d invites=%d", users, invites)
	}
}

func TestBootstrapAdminRollsBackWhenSecureOutputCannotBeCreated(t *testing.T) {
	root := t.TempDir()
	dbService := newBootstrapTestDB(t, root)
	outputPath := filepath.Join(root, "bootstrap.txt")
	if err := os.WriteFile(outputPath, []byte("operator-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &runtimeconfig.Runtime{
		Environment:   runtimeconfig.EnvironmentProduction,
		BaseURL:       "https://hub.galaxyuas.com",
		AdminEmail:    "operations@galaxyuas.com",
		BootstrapFile: outputPath,
	}

	if err := bootstrapAdmin(dbService, cfg); err == nil {
		t.Fatal("expected exclusive bootstrap output failure")
	}

	var users int
	if err := dbService.GetDB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatalf("bootstrap mutated database after output failure: users=%d", users)
	}
}

func newBootstrapTestDB(t *testing.T, root string) *db.Service {
	t.Helper()
	service, err := db.New(&db.Config{
		DBPath:         filepath.Join(root, "db", "constellation.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}
