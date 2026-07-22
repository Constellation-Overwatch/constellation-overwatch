package config

import "net/netip"

const (
	ModeDevelopment = "development"
	ModeProduction  = "production"
)

// Runtime is the validated deployment profile shared by the API and web
// services. Environment parsing stays at the executable boundary.
type Runtime struct {
	Mode                  string
	Host                  string
	Port                  string
	NATSHost              string
	NATSPort              string
	BaseURL               string
	RPID                  string
	AllowedOrigins        []string
	TrustedProxies        []netip.Prefix
	DataDir               string
	BackupDir             string
	AdminEmail            string
	BootstrapFile         string
	SecureCookies         bool
	HSTS                  bool
	ContentSecurityPolicy string
}

func (c Runtime) Production() bool { return c.Mode == ModeProduction }

// Development returns the explicit local-development profile. It is not
// suitable for a network-exposed deployment.
func Development() Runtime {
	return Runtime{
		Mode:                  ModeDevelopment,
		Host:                  "0.0.0.0",
		Port:                  "8080",
		NATSHost:              "127.0.0.1",
		NATSPort:              "4222",
		BaseURL:               "http://localhost:8080",
		RPID:                  "localhost",
		AllowedOrigins:        []string{"http://localhost:8080"},
		DataDir:               "./data",
		SecureCookies:         false,
		HSTS:                  false,
		ContentSecurityPolicy: DefaultContentSecurityPolicy,
	}
}

const DefaultContentSecurityPolicy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' https: http: wss: ws:; media-src 'self' https: http: blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"
