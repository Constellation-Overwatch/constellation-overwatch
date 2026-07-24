package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	apiservices "github.com/Constellation-Overwatch/constellation-overwatch/api/services"
	"github.com/Constellation-Overwatch/constellation-overwatch/db"
	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
)

func TestRuntimeConfigProductionValid(t *testing.T) {
	env := validProductionEnv(t)
	cfg, err := runtimeConfigFromEnv(mapLookup(env))
	if err != nil {
		t.Fatalf("runtimeConfigFromEnv() error = %v", err)
	}
	if !cfg.Production() || !cfg.SecureCookies || !cfg.HSTS {
		t.Fatalf("production security flags = production:%v secure:%v hsts:%v", cfg.Production(), cfg.SecureCookies, cfg.HSTS)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != env["OVERWATCH_BASE_URL"] {
		t.Fatalf("allowed origins = %#v", cfg.AllowedOrigins)
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0].String() != "127.0.0.0/8" {
		t.Fatalf("trusted proxies = %#v", cfg.TrustedProxies)
	}
	if cfg.KeyHashSecret != env["OVERWATCH_KEY_HASH_SECRET"] {
		t.Fatal("validated API-key hash secret was not captured in runtime snapshot")
	}
}

func TestRuntimeConfigProductionAllowsLoopbackNATSBehindTLSProxy(t *testing.T) {
	env := validProductionEnv(t)
	env["NATS_HOST"] = "127.0.0.1"
	env["NATS_PORT"] = "4224"
	if _, err := runtimeConfigFromEnv(mapLookup(env)); err != nil {
		t.Fatalf("loopback NATS behind TLS proxy rejected: %v", err)
	}
}

func TestRuntimeConfigProductionFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{name: "missing required", mutate: func(env map[string]string) { delete(env, "OVERWATCH_KEY_HASH_SECRET") }, wantErr: "missing required"},
		{name: "plain HTTP", mutate: func(env map[string]string) { env["OVERWATCH_BASE_URL"] = "http://constellation.tailnet.example" }, wantErr: "absolute https origin"},
		{name: "origin path", mutate: func(env map[string]string) {
			env["OVERWATCH_ALLOWED_ORIGINS"] = "https://constellation.tailnet.example/operator"
		}, wantErr: "must not contain"},
		{name: "RP mismatch", mutate: func(env map[string]string) { env["OVERWATCH_RPID"] = "other.example" }, wantErr: "not a registrable suffix"},
		{name: "demo secret", mutate: func(env map[string]string) { env["OVERWATCH_KEY_HASH_SECRET"] = "changeme-changeme-changeme-changeme" }, wantErr: "non-demo"},
		{name: "packaged placeholder secret", mutate: func(env map[string]string) {
			env["OVERWATCH_KEY_HASH_SECRET"] = "REPLACE_WITH_AT_LEAST_32_RANDOM_CHARACTERS"
		}, wantErr: "non-demo"},
		{name: "wildcard bind", mutate: func(env map[string]string) { env["HOST"] = "0.0.0.0" }, wantErr: "explicit interface"},
		{name: "development routes", mutate: func(env map[string]string) { env["GO_ENV"] = "development" }, wantErr: "forbidden"},
		{name: "insecure cookies", mutate: func(env map[string]string) { env["OVERWATCH_INSECURE"] = "true" }, wantErr: "forbidden"},
		{name: "relative data", mutate: func(env map[string]string) { env["OVERWATCH_DATA_DIR"] = "./data" }, wantErr: "absolute persistent path"},
		{name: "nested backup", mutate: func(env map[string]string) {
			env["OVERWATCH_BACKUP_DIR"] = filepath.Join(env["OVERWATCH_DATA_DIR"], "backups")
		}, wantErr: "outside"},
		{name: "bootstrap under data", mutate: func(env map[string]string) {
			env["OVERWATCH_BOOTSTRAP_FILE"] = filepath.Join(env["OVERWATCH_DATA_DIR"], "bootstrap.txt")
		}, wantErr: "parent must be separate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validProductionEnv(t)
			tt.mutate(env)
			_, err := runtimeConfigFromEnv(mapLookup(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRuntimeConfigDevelopmentIsVisiblyInsecure(t *testing.T) {
	cfg, err := runtimeConfigFromEnv(mapLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("runtimeConfigFromEnv() error = %v", err)
	}
	if cfg.Production() || cfg.SecureCookies || cfg.HSTS {
		t.Fatalf("development profile unexpectedly secure/exposable: %#v", cfg)
	}
}

func TestLoadRuntimeEnvRejectsProduction(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("OVERWATCH_ENV=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERWATCH_ENV", "")
	_ = os.Unsetenv("OVERWATCH_ENV")
	if _, err := loadRuntimeEnv(envPath); err == nil || !strings.Contains(err.Error(), "must not be enabled") {
		t.Fatalf("loadRuntimeEnv() error = %v, want production .env rejection", err)
	}

	t.Setenv("OVERWATCH_ENV", "production")
	if _, err := loadRuntimeEnv(envPath); err == nil || !strings.Contains(err.Error(), "forbids application .env") {
		t.Fatalf("loadRuntimeEnv() error = %v, want existing production rejection", err)
	}
}

func TestWriteBootstrapFileIsCreateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.txt")
	secretURL := "https://constellation.tailnet.example/invite/secret"
	if err := writeBootstrapFile(path, "operator@galaxyuas.com", secretURL); err != nil {
		t.Fatalf("writeBootstrapFile() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), secretURL) {
		t.Fatalf("bootstrap file does not contain setup URL")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("bootstrap mode = %o, want 600", got)
		}
	}
	if exists, err := secureBootstrapFileExists(path); err != nil || !exists {
		t.Fatalf("secureBootstrapFileExists() = %v, %v", exists, err)
	}
	if err := writeBootstrapFile(path, "attacker@example.net", "https://evil.invalid"); err == nil {
		t.Fatal("second write unexpectedly overwrote create-once bootstrap file")
	}
}

func TestBootstrapAdminSelectsOnlyIncompleteDefaultOrganizationAdmin(t *testing.T) {
	dbSvc, err := db.New(&db.Config{
		DBPath:         filepath.Join(t.TempDir(), "bootstrap.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if err := dbSvc.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if _, err := dbSvc.DB.Exec(
		`INSERT INTO organizations (org_id, name, org_type)
		 VALUES ('default', 'Default Organization', 'commercial'),
		        ('org-b', 'Organization B', 'commercial')`,
	); err != nil {
		t.Fatalf("insert organizations: %v", err)
	}

	userSvc := apiservices.NewUserService(dbSvc.DB)
	oldCreatedAt := time.Now().Add(-time.Hour).Format(time.RFC3339)
	for _, user := range []*apiservices.User{
		{
			UserID:            "admin-b",
			OrgID:             "org-b",
			Username:          "admin-b",
			Email:             "admin-b@example.test",
			Role:              "admin",
			NeedsPasskeySetup: true,
			CreatedAt:         oldCreatedAt,
			UpdatedAt:         oldCreatedAt,
		},
		{
			UserID:            "admin-default",
			OrgID:             "default",
			Username:          "admin-default",
			Email:             "admin-default@example.test",
			Role:              "admin",
			NeedsPasskeySetup: true,
		},
	} {
		if err := userSvc.CreateUser(user); err != nil {
			t.Fatalf("create user %s: %v", user.UserID, err)
		}
	}

	cfg := runtimeconfig.Development()
	cfg.BaseURL = "http://localhost:8080"
	if err := bootstrapAdmin(dbSvc, cfg); err != nil {
		t.Fatalf("bootstrapAdmin() error = %v", err)
	}

	var defaultInvites, otherInvites int
	if err := dbSvc.DB.QueryRow(
		`SELECT COUNT(*) FROM invites
		 WHERE org_id = 'default' AND email = 'admin-default@example.test'
		   AND purpose = 'initial_setup' AND status = 'pending'`,
	).Scan(&defaultInvites); err != nil {
		t.Fatalf("count default invites: %v", err)
	}
	if err := dbSvc.DB.QueryRow(
		`SELECT COUNT(*) FROM invites WHERE org_id = 'org-b'`,
	).Scan(&otherInvites); err != nil {
		t.Fatalf("count other-org invites: %v", err)
	}
	if defaultInvites != 1 || otherInvites != 0 {
		t.Fatalf("invite counts default/other = %d/%d, want 1/0", defaultInvites, otherInvites)
	}
}

func TestBootstrapAdminIgnoresIncompleteAdminWhenDefaultOrganizationHasNone(t *testing.T) {
	dbSvc, err := db.New(&db.Config{
		DBPath:         filepath.Join(t.TempDir(), "bootstrap-no-default-admin.db"),
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		AutoInitialize: true,
	})
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if err := dbSvc.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if _, err := dbSvc.DB.Exec(
		`INSERT INTO organizations (org_id, name, org_type)
		 VALUES ('default', 'Default Organization', 'commercial'),
		        ('org-b', 'Organization B', 'commercial')`,
	); err != nil {
		t.Fatalf("insert organizations: %v", err)
	}
	if err := apiservices.NewUserService(dbSvc.DB).CreateUser(&apiservices.User{
		UserID:            "admin-b",
		OrgID:             "org-b",
		Username:          "admin-b",
		Email:             "admin-b@example.test",
		Role:              "admin",
		NeedsPasskeySetup: true,
	}); err != nil {
		t.Fatalf("create non-default admin: %v", err)
	}

	cfg := runtimeconfig.Development()
	cfg.BaseURL = "http://localhost:8080"
	if err := bootstrapAdmin(dbSvc, cfg); err != nil {
		t.Fatalf("bootstrapAdmin() error = %v, want nil", err)
	}

	var count int
	if err := dbSvc.DB.QueryRow(`SELECT COUNT(*) FROM invites`).Scan(&count); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if count != 0 {
		t.Fatalf("invite count = %d, want 0", count)
	}
}

func validProductionEnv(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"OVERWATCH_ENV":             "production",
		"HOST":                      "127.0.0.1",
		"PORT":                      "8090",
		"NATS_HOST":                 "100.64.0.10",
		"NATS_PORT":                 "4223",
		"OVERWATCH_BASE_URL":        "https://constellation.tailnet.example",
		"OVERWATCH_RPID":            "constellation.tailnet.example",
		"OVERWATCH_ALLOWED_ORIGINS": "https://constellation.tailnet.example",
		"OVERWATCH_KEY_HASH_SECRET": "correct-horse-battery-staple-with-extra-entropy",
		"OVERWATCH_DATA_DIR":        filepath.Join(root, "data"),
		"OVERWATCH_BACKUP_DIR":      filepath.Join(root, "backups"),
		"OVERWATCH_ADMIN_EMAIL":     "operator@galaxyuas.com",
		"OVERWATCH_BOOTSTRAP_FILE":  filepath.Join(root, "secrets", "bootstrap.txt"),
		"OVERWATCH_TRUSTED_PROXIES": "127.0.0.0/8",
	}
}

func mapLookup(values map[string]string) envLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
