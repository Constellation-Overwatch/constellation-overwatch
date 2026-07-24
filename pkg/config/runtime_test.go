package config

import (
	"strings"
	"testing"
)

func TestLoadRuntimeDevelopmentDefaultsAreVisibleAndConvenient(t *testing.T) {
	clearRuntimeEnvironment(t)

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if cfg.Environment != EnvironmentDevelopment {
		t.Fatalf("environment = %q", cfg.Environment)
	}
	if cfg.BaseURL != "http://localhost:8080" || cfg.RPID != "localhost" {
		t.Fatalf("unexpected development RP config: base=%q rp=%q", cfg.BaseURL, cfg.RPID)
	}
	if cfg.SecureCookies {
		t.Fatal("development HTTP cookies unexpectedly marked Secure")
	}
	if !cfg.OriginAllowed("http://localhost:8080") {
		t.Fatal("development base origin not allowed")
	}
}

func TestLoadRuntimeProductionRejectsMissingExplicitSecurityValues(t *testing.T) {
	clearRuntimeEnvironment(t)
	t.Setenv("OVERWATCH_ENV", EnvironmentProduction)

	_, err := LoadRuntime()
	if err == nil {
		t.Fatal("expected production validation error")
	}
	for _, key := range []string{
		"HOST",
		"OVERWATCH_BASE_URL",
		"OVERWATCH_RPID",
		"ALLOWED_ORIGINS",
		"OVERWATCH_KEY_HASH_SECRET",
		"OVERWATCH_DATA_DIR",
		"OVERWATCH_BACKUP_DIR",
		"OVERWATCH_ADMIN_EMAIL",
		"OVERWATCH_BOOTSTRAP_FILE",
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}

func TestLoadRuntimeValidProductionSnapshot(t *testing.T) {
	setValidProductionEnvironment(t)

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if !cfg.IsProduction() || !cfg.SecureCookies {
		t.Fatalf("production security state: production=%v secure=%v", cfg.IsProduction(), cfg.SecureCookies)
	}
	if cfg.StrictTransport == "" || cfg.ContentSecurity == "" {
		t.Fatal("production headers are not configured")
	}
	if !cfg.OriginAllowed("https://hub.galaxyuas.com") {
		t.Fatal("exact configured origin denied")
	}
	if cfg.OriginAllowed("https://hub.galaxyuas.com.evil.test") {
		t.Fatal("lookalike origin allowed")
	}
	if !cfg.RemoteIsTrustedProxy("10.20.30.40:443") {
		t.Fatal("configured proxy CIDR denied")
	}
	if cfg.RemoteIsTrustedProxy("192.0.2.5:443") {
		t.Fatal("unconfigured proxy trusted")
	}
}

func TestLoadRuntimeProductionRejectsOriginAndCredentialDrift(t *testing.T) {
	setValidProductionEnvironment(t)
	t.Setenv("ALLOWED_ORIGINS", "https://other.galaxyuas.com")
	t.Setenv("OVERWATCH_KEY_HASH_SECRET", "changeme-development-secret-that-is-long")
	t.Setenv("OVERWATCH_ADMIN_EMAIL", "admin@example.com")

	_, err := LoadRuntime()
	if err == nil {
		t.Fatal("expected production validation error")
	}
	for _, fragment := range []string{
		"must include OVERWATCH_BASE_URL exactly",
		"non-demo bytes",
		"non-demo address",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error does not mention %q: %v", fragment, err)
		}
	}
}

func TestLoadRuntimeRejectsUntrustedProxySyntax(t *testing.T) {
	clearRuntimeEnvironment(t)
	t.Setenv("OVERWATCH_TRUSTED_PROXIES", "not-a-network")

	_, err := LoadRuntime()
	if err == nil || !strings.Contains(err.Error(), "not an IP or CIDR") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func setValidProductionEnvironment(t *testing.T) {
	t.Helper()
	clearRuntimeEnvironment(t)
	values := map[string]string{
		"OVERWATCH_ENV":             EnvironmentProduction,
		"HOST":                      "127.0.0.1",
		"PORT":                      "8080",
		"OVERWATCH_BASE_URL":        "https://hub.galaxyuas.com",
		"OVERWATCH_RPID":            "galaxyuas.com",
		"ALLOWED_ORIGINS":           "https://hub.galaxyuas.com",
		"OVERWATCH_KEY_HASH_SECRET": "0123456789abcdef0123456789abcdef",
		"OVERWATCH_DATA_DIR":        "/var/lib/constellation-overwatch",
		"OVERWATCH_BACKUP_DIR":      "/var/backups/constellation-overwatch",
		"OVERWATCH_ADMIN_EMAIL":     "operations@galaxyuas.com",
		"OVERWATCH_BOOTSTRAP_FILE":  "/run/constellation-overwatch/bootstrap.txt",
		"OVERWATCH_TRUSTED_PROXIES": "127.0.0.1,10.0.0.0/8",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func clearRuntimeEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OVERWATCH_ENV",
		"HOST",
		"PORT",
		"OVERWATCH_BASE_URL",
		"OVERWATCH_RPID",
		"ALLOWED_ORIGINS",
		"OVERWATCH_KEY_HASH_SECRET",
		"OVERWATCH_DATA_DIR",
		"OVERWATCH_BACKUP_DIR",
		"OVERWATCH_ADMIN_EMAIL",
		"OVERWATCH_BOOTSTRAP_FILE",
		"OVERWATCH_TRUSTED_PROXIES",
		"OVERWATCH_INSECURE",
	} {
		t.Setenv(key, "")
	}
}
