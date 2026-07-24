package main

import (
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	runtimeconfig "github.com/Constellation-Overwatch/constellation-overwatch/pkg/config"
)

type envLookup func(string) (string, bool)

func loadRuntimeConfig() (runtimeconfig.Runtime, error) {
	return runtimeConfigFromEnv(os.LookupEnv)
}

func runtimeConfigFromEnv(lookup envLookup) (runtimeconfig.Runtime, error) {
	cfg := runtimeconfig.Development()
	mode := strings.ToLower(strings.TrimSpace(envValue(lookup, "OVERWATCH_ENV", runtimeconfig.ModeDevelopment)))
	if mode != runtimeconfig.ModeDevelopment && mode != runtimeconfig.ModeProduction {
		return cfg, fmt.Errorf("OVERWATCH_ENV must be %q or %q", runtimeconfig.ModeDevelopment, runtimeconfig.ModeProduction)
	}
	cfg.Mode = mode

	cfg.Host = envValue(lookup, "HOST", cfg.Host)
	cfg.Port = envValue(lookup, "PORT", cfg.Port)
	cfg.NATSHost = envValue(lookup, "NATS_HOST", cfg.NATSHost)
	cfg.NATSPort = envValue(lookup, "NATS_PORT", cfg.NATSPort)
	cfg.BaseURL = strings.TrimRight(envValue(lookup, "OVERWATCH_BASE_URL", cfg.BaseURL), "/")
	cfg.RPID = envValue(lookup, "OVERWATCH_RPID", cfg.RPID)
	cfg.AllowedOrigins = splitCSV(envValue(lookup, "OVERWATCH_ALLOWED_ORIGINS", envValue(lookup, "ALLOWED_ORIGINS", strings.Join(cfg.AllowedOrigins, ","))))
	cfg.KeyHashSecret = envValue(lookup, "OVERWATCH_KEY_HASH_SECRET", "")
	cfg.DataDir = envValue(lookup, "OVERWATCH_DATA_DIR", cfg.DataDir)
	cfg.BackupDir = envValue(lookup, "OVERWATCH_BACKUP_DIR", "")
	cfg.AdminEmail = envValue(lookup, "OVERWATCH_ADMIN_EMAIL", "admin@localhost")
	cfg.BootstrapFile = envValue(lookup, "OVERWATCH_BOOTSTRAP_FILE", "")

	trusted, err := parseTrustedProxies(envValue(lookup, "OVERWATCH_TRUSTED_PROXIES", ""))
	if err != nil {
		return cfg, err
	}
	cfg.TrustedProxies = trusted
	cfg.SecureCookies = strings.HasPrefix(cfg.BaseURL, "https://")
	cfg.HSTS = cfg.Production()

	if cfg.Production() {
		if err := validateProductionConfig(cfg, lookup); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

func validateProductionConfig(cfg runtimeconfig.Runtime, lookup envLookup) error {
	required := []string{
		"HOST", "PORT", "NATS_HOST", "NATS_PORT", "OVERWATCH_BASE_URL",
		"OVERWATCH_RPID", "OVERWATCH_ALLOWED_ORIGINS", "OVERWATCH_KEY_HASH_SECRET",
		"OVERWATCH_DATA_DIR", "OVERWATCH_BACKUP_DIR", "OVERWATCH_ADMIN_EMAIL",
		"OVERWATCH_BOOTSTRAP_FILE",
	}
	var missing []string
	for _, key := range required {
		if value, ok := lookup(key); !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("production configuration missing required values: %s", strings.Join(missing, ", "))
	}

	if envValue(lookup, "OVERWATCH_INSECURE", "false") == "true" {
		return fmt.Errorf("OVERWATCH_INSECURE is forbidden in production")
	}
	if goEnv := strings.ToLower(envValue(lookup, "GO_ENV", "")); goEnv == "dev" || goEnv == "development" {
		return fmt.Errorf("GO_ENV=%s is forbidden in production", goEnv)
	}
	if err := validateBindHost("HOST", cfg.Host); err != nil {
		return err
	}
	if err := validateBindHost("NATS_HOST", cfg.NATSHost); err != nil {
		return err
	}
	if err := validatePort("PORT", cfg.Port); err != nil {
		return err
	}
	if err := validatePort("NATS_PORT", cfg.NATSPort); err != nil {
		return err
	}

	baseURL, err := exactHTTPSOrigin(cfg.BaseURL)
	if err != nil {
		return fmt.Errorf("OVERWATCH_BASE_URL: %w", err)
	}
	if net.ParseIP(cfg.RPID) != nil || strings.Contains(cfg.RPID, ":") || cfg.RPID == "localhost" {
		return fmt.Errorf("OVERWATCH_RPID must be a production DNS name")
	}
	host := baseURL.Hostname()
	if host != cfg.RPID && !strings.HasSuffix(host, "."+cfg.RPID) {
		return fmt.Errorf("OVERWATCH_RPID %q is not a registrable suffix of base URL host %q", cfg.RPID, host)
	}
	baseAllowed := false
	for _, origin := range cfg.AllowedOrigins {
		u, err := exactHTTPSOrigin(origin)
		if err != nil {
			return fmt.Errorf("OVERWATCH_ALLOWED_ORIGINS entry %q: %w", origin, err)
		}
		if u.String() == baseURL.String() {
			baseAllowed = true
		}
	}
	if !baseAllowed {
		return fmt.Errorf("OVERWATCH_ALLOWED_ORIGINS must include exact OVERWATCH_BASE_URL origin %q", cfg.BaseURL)
	}

	secret, _ := lookup("OVERWATCH_KEY_HASH_SECRET")
	secretLower := strings.ToLower(secret)
	if len(secret) < 32 || strings.Contains(secretLower, "changeme") || strings.Contains(secretLower, "example") {
		return fmt.Errorf("OVERWATCH_KEY_HASH_SECRET must be at least 32 characters and non-demo")
	}
	parsedEmail, err := mail.ParseAddress(cfg.AdminEmail)
	emailLower := strings.ToLower(cfg.AdminEmail)
	if err != nil || parsedEmail.Address != cfg.AdminEmail || strings.HasSuffix(emailLower, "@localhost") || strings.HasSuffix(emailLower, ".example") || strings.HasSuffix(emailLower, ".invalid") || strings.HasSuffix(emailLower, ".test") {
		return fmt.Errorf("OVERWATCH_ADMIN_EMAIL must be a non-demo address")
	}
	if err := validatePersistentPath("OVERWATCH_DATA_DIR", cfg.DataDir); err != nil {
		return err
	}
	if err := validatePersistentPath("OVERWATCH_BACKUP_DIR", cfg.BackupDir); err != nil {
		return err
	}
	if pathsOverlap(cfg.DataDir, cfg.BackupDir) {
		return fmt.Errorf("OVERWATCH_BACKUP_DIR must be separate from and outside OVERWATCH_DATA_DIR")
	}
	if !filepath.IsAbs(cfg.BootstrapFile) {
		return fmt.Errorf("OVERWATCH_BOOTSTRAP_FILE must be an absolute path")
	}
	bootstrapDir := filepath.Dir(cfg.BootstrapFile)
	if pathsOverlap(cfg.DataDir, bootstrapDir) || pathsOverlap(cfg.BackupDir, bootstrapDir) {
		return fmt.Errorf("OVERWATCH_BOOTSTRAP_FILE parent must be separate from data and backup directories")
	}

	return nil
}

func prepareRuntimePaths(cfg runtimeconfig.Runtime) error {
	mode := os.FileMode(0o755)
	if cfg.Production() {
		mode = 0o700
	}
	for name, path := range map[string]string{"data": cfg.DataDir, "backup": cfg.BackupDir} {
		if path == "" {
			continue
		}
		if err := os.MkdirAll(path, mode); err != nil {
			return fmt.Errorf("prepare %s directory: %w", name, err)
		}
		if cfg.Production() {
			if err := requirePrivateDirectory(path); err != nil {
				return fmt.Errorf("validate %s directory: %w", name, err)
			}
		}
	}
	if cfg.Production() {
		bootstrapDir := filepath.Dir(cfg.BootstrapFile)
		if err := os.MkdirAll(bootstrapDir, 0o700); err != nil {
			return fmt.Errorf("prepare bootstrap directory: %w", err)
		}
		if err := requirePrivateDirectory(bootstrapDir); err != nil {
			return fmt.Errorf("validate bootstrap directory: %w", err)
		}
	}
	return nil
}

func envValue(lookup envLookup, key, fallback string) string {
	if value, ok := lookup(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func splitCSV(value string) []string {
	var values []string
	for _, part := range strings.Split(value, ",") {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, item := range splitCSV(value) {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			addr, addrErr := netip.ParseAddr(item)
			if addrErr != nil {
				return nil, fmt.Errorf("OVERWATCH_TRUSTED_PROXIES entry %q is not an IP or CIDR", item)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func exactHTTPSOrigin(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute https origin")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, fmt.Errorf("must not contain credentials, path, query, or fragment")
	}
	u.Path = ""
	return u, nil
}

func validateBindHost(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if ip := net.ParseIP(value); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("%s must bind an explicit interface, not %s", name, value)
	}
	return nil
}

func validatePort(name, value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s must be an integer from 1 to 65535", name)
	}
	return nil
}

func validatePersistentPath(name, value string) error {
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute persistent path", name)
	}
	return nil
}

func pathsOverlap(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if runtime.GOOS == "windows" {
		first = strings.ToLower(first)
		second = strings.ToLower(second)
	}
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func requirePrivateDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil // NTFS ACL validation is an installation responsibility.
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("permissions %o expose production state; require 0700", info.Mode().Perm())
	}
	return nil
}
