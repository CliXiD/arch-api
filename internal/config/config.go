// Package config reads the process configuration from environment variables, once, at start-up.
package config

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Serve is the configuration of `arch-api serve`.
type Serve struct {
	DatabaseURL  string
	HTTPAddr     string
	MetricsAddr  string
	OIDCIssuer   string
	OIDCAudience string
	// OIDCJWKSURL is empty when the JWKS URL is discovered from the issuer.
	OIDCJWKSURL string
	LogLevel    slog.Level
	CORSOrigins []string
	// OTLPEndpoint is empty when tracing is off.
	OTLPEndpoint string
	OTLPProtocol string
}

// Migrate is the configuration of `arch-api migrate`.
type Migrate struct {
	DatabaseURL string
}

// Error lists every missing or invalid variable. It names the variables, never their values.
type Error struct {
	Missing []string
	Invalid []string
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Missing)+len(e.Invalid))
	for _, name := range e.Missing {
		parts = append(parts, name+" is required")
	}
	for _, name := range e.Invalid {
		parts = append(parts, name+" is invalid")
	}
	return "config: " + strings.Join(parts, ", ")
}

type reader struct {
	getenv func(string) string
	err    Error
}

func (r *reader) required(name string) string {
	v := strings.TrimSpace(r.getenv(name))
	if v == "" {
		r.err.Missing = append(r.err.Missing, name)
	}
	return v
}

func (r *reader) optional(name, def string) string {
	if v := strings.TrimSpace(r.getenv(name)); v != "" {
		return v
	}
	return def
}

func (r *reader) invalid(name string) {
	r.err.Invalid = append(r.err.Invalid, name)
}

func (r *reader) result() error {
	if len(r.err.Missing) == 0 && len(r.err.Invalid) == 0 {
		return nil
	}
	return &r.err
}

// LoadServe reads the configuration of `serve`. getenv is usually os.Getenv.
func LoadServe(getenv func(string) string) (Serve, error) {
	r := &reader{getenv: getenv}
	c := Serve{
		DatabaseURL:  r.required("DATABASE_URL"),
		HTTPAddr:     r.optional("HTTP_ADDR", ":8080"),
		MetricsAddr:  r.optional("METRICS_ADDR", ":9090"),
		OIDCIssuer:   r.required("OIDC_ISSUER"),
		OIDCAudience: r.required("OIDC_AUDIENCE"),
		OIDCJWKSURL:  r.optional("OIDC_JWKS_URL", ""),
		OTLPEndpoint: r.optional("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPProtocol: r.optional("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),
	}

	if c.DatabaseURL != "" && !isDatabaseURL(c.DatabaseURL) {
		r.invalid("DATABASE_URL")
	}
	if c.OIDCIssuer != "" && !isHTTPURL(c.OIDCIssuer) {
		r.invalid("OIDC_ISSUER")
	}
	if c.OIDCJWKSURL != "" && !isHTTPURL(c.OIDCJWKSURL) {
		r.invalid("OIDC_JWKS_URL")
	}

	switch level := strings.ToLower(r.optional("LOG_LEVEL", "info")); level {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		r.invalid("LOG_LEVEL")
	}

	for _, origin := range strings.Split(r.optional("CORS_ORIGINS", ""), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			c.CORSOrigins = append(c.CORSOrigins, origin)
		}
	}

	if c.OTLPEndpoint != "" && !isHTTPURL(c.OTLPEndpoint) {
		r.invalid("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if c.OTLPProtocol != "grpc" && c.OTLPProtocol != "http/protobuf" {
		r.invalid("OTEL_EXPORTER_OTLP_PROTOCOL")
	}

	return c, r.result()
}

// LoadMigrate reads the configuration of `migrate`: MIGRATE_DATABASE_URL, else DATABASE_URL.
func LoadMigrate(getenv func(string) string) (Migrate, error) {
	r := &reader{getenv: getenv}
	name := "MIGRATE_DATABASE_URL"
	dsn := r.optional(name, "")
	if dsn == "" {
		name = "DATABASE_URL"
		dsn = r.required(name)
	}
	if dsn != "" && !isDatabaseURL(dsn) {
		r.invalid(name)
	}
	return Migrate{DatabaseURL: dsn}, r.result()
}

func isDatabaseURL(s string) bool {
	_, err := pgconn.ParseConfig(s)
	return err == nil
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
