package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentProduction  = "production"
)

// Runtime is the immutable, validated startup configuration shared by the
// network and authentication boundaries.
type Runtime struct {
	Environment       string
	Host              string
	Port              string
	BaseURL           string
	RPID              string
	AllowedOrigins    []string
	KeyHashSecret     string
	DataDir           string
	BackupDir         string
	AdminEmail        string
	BootstrapFile     string
	TrustedProxies    []netip.Prefix
	SecureCookies     bool
	ContentSecurity   string
	StrictTransport   string
	explicitlyDefined map[string]bool
}

// LoadRuntime reads configuration once after .env and CLI flag resolution.
// Development defaults remain convenient; production requires every
// security-sensitive value to be explicit.
func LoadRuntime() (*Runtime, error) {
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("OVERWATCH_ENV")))
	if environment == "" {
		environment = EnvironmentDevelopment
	}

	host := envOrDefault("HOST", "0.0.0.0")
	port := envOrDefault("PORT", "8080")
	baseURL := envOrDefault("OVERWATCH_BASE_URL", "http://localhost:"+port)
	rpID := envOrDefault("OVERWATCH_RPID", "localhost")
	dataDir := envOrDefault("OVERWATCH_DATA_DIR", "./data")

	originsValue := os.Getenv("ALLOWED_ORIGINS")
	if originsValue == "" {
		originsValue = baseURL
	}

	trustedProxies, proxyErr := parseTrustedProxies(os.Getenv("OVERWATCH_TRUSTED_PROXIES"))

	cfg := &Runtime{
		Environment:     environment,
		Host:            host,
		Port:            port,
		BaseURL:         baseURL,
		RPID:            strings.ToLower(strings.TrimSpace(rpID)),
		AllowedOrigins:  splitCSV(originsValue),
		KeyHashSecret:   os.Getenv("OVERWATCH_KEY_HASH_SECRET"),
		DataDir:         dataDir,
		BackupDir:       os.Getenv("OVERWATCH_BACKUP_DIR"),
		AdminEmail:      envOrDefault("OVERWATCH_ADMIN_EMAIL", "admin@localhost"),
		BootstrapFile:   os.Getenv("OVERWATCH_BOOTSTRAP_FILE"),
		TrustedProxies:  trustedProxies,
		ContentSecurity: compatibilityCSP(),
		explicitlyDefined: map[string]bool{
			"HOST":                      envDefined("HOST"),
			"OVERWATCH_BASE_URL":        envDefined("OVERWATCH_BASE_URL"),
			"OVERWATCH_RPID":            envDefined("OVERWATCH_RPID"),
			"ALLOWED_ORIGINS":           envDefined("ALLOWED_ORIGINS"),
			"OVERWATCH_KEY_HASH_SECRET": envDefined("OVERWATCH_KEY_HASH_SECRET"),
			"OVERWATCH_DATA_DIR":        envDefined("OVERWATCH_DATA_DIR"),
			"OVERWATCH_BACKUP_DIR":      envDefined("OVERWATCH_BACKUP_DIR"),
			"OVERWATCH_ADMIN_EMAIL":     envDefined("OVERWATCH_ADMIN_EMAIL"),
			"OVERWATCH_BOOTSTRAP_FILE":  envDefined("OVERWATCH_BOOTSTRAP_FILE"),
			"OVERWATCH_TRUSTED_PROXIES": envDefined("OVERWATCH_TRUSTED_PROXIES"),
		},
	}

	canonicalBase, baseErr := canonicalOrigin(cfg.BaseURL)
	if baseErr == nil {
		cfg.BaseURL = canonicalBase
	}
	for i, origin := range cfg.AllowedOrigins {
		if canonical, err := canonicalOrigin(origin); err == nil {
			cfg.AllowedOrigins[i] = canonical
		}
	}
	slices.Sort(cfg.AllowedOrigins)
	cfg.AllowedOrigins = slices.Compact(cfg.AllowedOrigins)

	cfg.SecureCookies = strings.HasPrefix(cfg.BaseURL, "https://") &&
		!strings.EqualFold(os.Getenv("OVERWATCH_INSECURE"), "true")
	if cfg.IsProduction() {
		cfg.StrictTransport = "max-age=31536000; includeSubDomains"
	}

	if proxyErr != nil {
		return nil, proxyErr
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Runtime) IsProduction() bool {
	return c != nil && c.Environment == EnvironmentProduction
}

// OriginAllowed performs exact-origin comparison against the startup snapshot.
func (c *Runtime) OriginAllowed(origin string) bool {
	if c == nil {
		return false
	}
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return false
	}
	return slices.Contains(c.AllowedOrigins, canonical)
}

// RemoteIsTrustedProxy reports whether the direct peer may supply forwarding
// headers. An empty trusted-proxy list means those headers are never trusted.
func (c *Runtime) RemoteIsTrustedProxy(remoteAddr string) bool {
	if c == nil {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range c.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (c *Runtime) Validate() error {
	var problems []error

	switch c.Environment {
	case EnvironmentDevelopment:
	case EnvironmentProduction:
	default:
		problems = append(problems, fmt.Errorf(
			"OVERWATCH_ENV must be %q or %q",
			EnvironmentDevelopment,
			EnvironmentProduction,
		))
	}

	baseOrigin, err := canonicalOrigin(c.BaseURL)
	if err != nil {
		problems = append(problems, fmt.Errorf("OVERWATCH_BASE_URL: %w", err))
	}

	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			if c.IsProduction() {
				problems = append(problems, errors.New("ALLOWED_ORIGINS cannot contain '*' in production"))
			}
			continue
		}
		if _, err := canonicalOrigin(origin); err != nil {
			problems = append(problems, fmt.Errorf("ALLOWED_ORIGINS entry %q: %w", origin, err))
		}
	}
	if strings.TrimSpace(c.Host) == "" || strings.ContainsAny(c.Host, " \t\r\n/") {
		problems = append(problems, errors.New("HOST must be a bind hostname or IP address"))
	}
	port, portErr := strconv.Atoi(c.Port)
	if portErr != nil || port < 1 || port > 65535 {
		problems = append(problems, errors.New("PORT must be an integer from 1 through 65535"))
	}

	if c.IsProduction() {
		required := []string{
			"HOST",
			"OVERWATCH_BASE_URL",
			"OVERWATCH_RPID",
			"ALLOWED_ORIGINS",
			"OVERWATCH_KEY_HASH_SECRET",
			"OVERWATCH_DATA_DIR",
			"OVERWATCH_BACKUP_DIR",
			"OVERWATCH_ADMIN_EMAIL",
			"OVERWATCH_BOOTSTRAP_FILE",
		}
		for _, key := range required {
			if !c.explicitlyDefined[key] {
				problems = append(problems, fmt.Errorf("%s must be explicitly configured in production", key))
			}
		}

		if err == nil && !strings.HasPrefix(baseOrigin, "https://") {
			problems = append(problems, errors.New("OVERWATCH_BASE_URL must use https in production"))
		}
		if !c.SecureCookies {
			problems = append(problems, errors.New("production requires Secure cookies; remove OVERWATCH_INSECURE and use HTTPS"))
		}
		if baseOrigin != "" && !slices.Contains(c.AllowedOrigins, baseOrigin) {
			problems = append(problems, errors.New("ALLOWED_ORIGINS must include OVERWATCH_BASE_URL exactly"))
		}
		if net.ParseIP(c.RPID) != nil || c.RPID == "" || strings.Contains(c.RPID, ":") {
			problems = append(problems, errors.New("OVERWATCH_RPID must be a production DNS name"))
		} else if err == nil {
			baseHost, parseErr := url.Parse(baseOrigin)
			if parseErr == nil {
				hostname := strings.ToLower(baseHost.Hostname())
				if hostname != c.RPID && !strings.HasSuffix(hostname, "."+c.RPID) {
					problems = append(problems, errors.New("OVERWATCH_RPID must equal or be a registrable suffix of the base URL hostname"))
				}
			}
		}
		if insecureSecret(c.KeyHashSecret) {
			problems = append(problems, errors.New("OVERWATCH_KEY_HASH_SECRET must contain at least 32 non-demo bytes"))
		}
		if err := validatePersistentPaths(c.DataDir, c.BackupDir); err != nil {
			problems = append(problems, err)
		}
		if !filepath.IsAbs(c.BootstrapFile) {
			problems = append(problems, errors.New("OVERWATCH_BOOTSTRAP_FILE must be an absolute path in production"))
		}
		if demoEmail(c.AdminEmail) {
			problems = append(problems, errors.New("OVERWATCH_ADMIN_EMAIL must be a non-demo address in production"))
		}
	}

	return errors.Join(problems...)
}

func compatibilityCSP() string {
	// Datastar and the current templates evaluate expressions and contain
	// inline handlers/styles. Keep those explicit until nonce/hash migration;
	// all other resource classes remain same-origin and deny by default.
	return "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdnjs.cloudflare.com; " +
		"style-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com; " +
		"img-src 'self' data: blob: https://*.cartocdn.com; " +
		"connect-src 'self' https://*.cartocdn.com https://fonts.openmaptiles.org; " +
		"font-src 'self' data: https://fonts.openmaptiles.org; " +
		"media-src 'self' blob:; worker-src 'self' blob:; object-src 'none'; " +
		"base-uri 'self'; frame-ancestors 'none'; form-action 'self'"
}

func canonicalOrigin(raw string) (string, error) {
	if raw == "*" {
		return raw, nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid origin: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("origin scheme must be http or https")
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("value must be an exact origin without credentials, path, query, or fragment")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, value := range splitCSV(raw) {
		if value == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("OVERWATCH_TRUSTED_PROXIES entry %q is not an IP or CIDR", value)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

func validatePersistentPaths(dataDir, backupDir string) error {
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(backupDir) {
		return errors.New("OVERWATCH_DATA_DIR and OVERWATCH_BACKUP_DIR must be absolute in production")
	}
	data := filepath.Clean(dataDir)
	backup := filepath.Clean(backupDir)
	if data == backup {
		return errors.New("OVERWATCH_DATA_DIR and OVERWATCH_BACKUP_DIR must be distinct")
	}
	if rel, err := filepath.Rel(data, backup); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("OVERWATCH_BACKUP_DIR must not be nested inside OVERWATCH_DATA_DIR")
	}
	if rel, err := filepath.Rel(backup, data); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("OVERWATCH_DATA_DIR must not be nested inside OVERWATCH_BACKUP_DIR")
	}
	return nil
}

func insecureSecret(secret string) bool {
	if len(secret) < 32 {
		return true
	}
	lower := strings.ToLower(secret)
	for _, marker := range []string{"changeme", "password", "example", "dev_only", "development"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func demoEmail(raw string) bool {
	address, err := mail.ParseAddress(raw)
	if err != nil || address.Address != raw {
		return true
	}
	parts := strings.Split(strings.ToLower(raw), "@")
	if len(parts) != 2 {
		return true
	}
	domain := parts[1]
	return domain == "localhost" ||
		domain == "example.com" ||
		strings.HasSuffix(domain, ".example") ||
		strings.HasSuffix(domain, ".invalid") ||
		strings.HasSuffix(domain, ".test")
}

func envDefined(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(raw string) []string {
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
