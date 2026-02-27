package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPListenAddr string
	LogLevel       string
	PostgresURL    string
	ValkeyURL      string
	MQTTBrokerURL  string
	Auth           AuthConfig
}

type AuthConfig struct {
	Mode               string
	Audience           []string
	Issuer             string
	AllowedAlgs        []string
	ClockSkew          time.Duration
	StaticPublicKeys   []string
	OIDCJWKSRefreshTTL time.Duration
	Enabled            bool
}

func FromEnv() (Config, error) {
	cfg := Config{
		HTTPListenAddr: env("HTTP_LISTEN_ADDR", ":8080"),
		LogLevel:       env("LOG_LEVEL", "info"),
		PostgresURL:    env("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable"),
		ValkeyURL:      env("VALKEY_URL", "redis://localhost:6379/0"),
		MQTTBrokerURL:  env("MQTT_BROKER_URL", "tcp://localhost:1883"),
		Auth: AuthConfig{
			Mode:               env("AUTH_MODE", "none"),
			Audience:           csvEnv("AUTH_AUDIENCE", "open-rtls-hub"),
			Issuer:             env("AUTH_ISSUER", ""),
			AllowedAlgs:        csvEnv("AUTH_ALLOWED_ALGS", "RS256"),
			ClockSkew:          durationEnv("AUTH_CLOCK_SKEW", 30*time.Second),
			StaticPublicKeys:   csvEnv("AUTH_STATIC_PUBLIC_KEYS", ""),
			OIDCJWKSRefreshTTL: durationEnv("AUTH_OIDC_REFRESH_TTL", 10*time.Minute),
			Enabled:            boolEnv("AUTH_ENABLED", true),
		},
	}

	if err := cfg.Auth.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (a AuthConfig) Validate() error {
	if !a.Enabled {
		return nil
	}
	switch a.Mode {
	case "none":
		return nil
	case "oidc":
		if a.Issuer == "" {
			return fmt.Errorf("AUTH_ISSUER is required for AUTH_MODE=oidc")
		}
	case "static":
		if len(clean(a.StaticPublicKeys)) == 0 {
			return fmt.Errorf("AUTH_STATIC_PUBLIC_KEYS is required for AUTH_MODE=static")
		}
	case "hybrid":
		if a.Issuer == "" && len(clean(a.StaticPublicKeys)) == 0 {
			return fmt.Errorf("AUTH_MODE=hybrid requires AUTH_ISSUER and/or AUTH_STATIC_PUBLIC_KEYS")
		}
	default:
		return fmt.Errorf("unsupported AUTH_MODE=%q", a.Mode)
	}
	return nil
}

func clean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func env(k, d string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return d
}

func csvEnv(k, d string) []string {
	v := env(k, d)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func durationEnv(k string, d time.Duration) time.Duration {
	v := env(k, "")
	if v == "" {
		return d
	}
	x, err := time.ParseDuration(v)
	if err != nil {
		return d
	}
	return x
}

func boolEnv(k string, d bool) bool {
	v := env(k, "")
	if v == "" {
		return d
	}
	x, err := strconv.ParseBool(v)
	if err != nil {
		return d
	}
	return x
}
