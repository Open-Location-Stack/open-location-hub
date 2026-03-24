package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains the hub runtime configuration loaded from environment
// variables.
type Config struct {
	HTTPListenAddr                        string
	LogLevel                              string
	PostgresURL                           string
	ValkeyURL                             string
	MQTTBrokerURL                         string
	StateLocationTTL                      time.Duration
	StateProximityTTL                     time.Duration
	StateDedupTTL                         time.Duration
	RPCTimeout                            time.Duration
	ProximityResolutionEntryConfidenceMin float64
	ProximityResolutionExitGraceDuration  time.Duration
	ProximityResolutionBoundaryGrace      float64
	ProximityResolutionMinDwellDuration   time.Duration
	ProximityResolutionPositionMode       string
	ProximityResolutionFallbackRadius     float64
	ProximityResolutionStaleStateTTL      time.Duration
	Auth                                  AuthConfig
}

// AuthConfig contains authentication and authorization configuration.
type AuthConfig struct {
	Mode                string
	Audience            []string
	Issuer              string
	AllowedAlgs         []string
	ClockSkew           time.Duration
	StaticPublicKeys    []string
	OIDCJWKSRefreshTTL  time.Duration
	HTTPTimeout         time.Duration
	PermissionsFile     string
	RolesClaim          string
	OwnedResourcesClaim string
	Enabled             bool
}

// FromEnv loads Config from environment variables and validates the result.
func FromEnv() (Config, error) {
	cfg := Config{
		HTTPListenAddr:                        env("HTTP_LISTEN_ADDR", ":8080"),
		LogLevel:                              env("LOG_LEVEL", "info"),
		PostgresURL:                           env("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable"),
		ValkeyURL:                             env("VALKEY_URL", "redis://localhost:6379/0"),
		MQTTBrokerURL:                         env("MQTT_BROKER_URL", "tcp://localhost:1883"),
		StateLocationTTL:                      durationEnv("STATE_LOCATION_TTL", 10*time.Minute),
		StateProximityTTL:                     durationEnv("STATE_PROXIMITY_TTL", 5*time.Minute),
		StateDedupTTL:                         durationEnv("STATE_DEDUP_TTL", 2*time.Minute),
		RPCTimeout:                            durationEnv("RPC_TIMEOUT", 5*time.Second),
		ProximityResolutionEntryConfidenceMin: floatEnv("PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN", 0),
		ProximityResolutionExitGraceDuration:  durationEnv("PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION", 15*time.Second),
		ProximityResolutionBoundaryGrace:      floatEnv("PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE", 2),
		ProximityResolutionMinDwellDuration:   durationEnv("PROXIMITY_RESOLUTION_MIN_DWELL_DURATION", 5*time.Second),
		ProximityResolutionPositionMode:       env("PROXIMITY_RESOLUTION_POSITION_MODE", "zone_position"),
		ProximityResolutionFallbackRadius:     floatEnv("PROXIMITY_RESOLUTION_FALLBACK_RADIUS", 0),
		ProximityResolutionStaleStateTTL:      durationEnv("PROXIMITY_RESOLUTION_STALE_STATE_TTL", 10*time.Minute),
		Auth: AuthConfig{
			Mode:                env("AUTH_MODE", "none"),
			Audience:            csvEnv("AUTH_AUDIENCE", "open-rtls-hub"),
			Issuer:              env("AUTH_ISSUER", ""),
			AllowedAlgs:         csvEnv("AUTH_ALLOWED_ALGS", "RS256"),
			ClockSkew:           durationEnv("AUTH_CLOCK_SKEW", 30*time.Second),
			StaticPublicKeys:    csvEnv("AUTH_STATIC_PUBLIC_KEYS", ""),
			OIDCJWKSRefreshTTL:  durationEnv("AUTH_OIDC_REFRESH_TTL", 10*time.Minute),
			HTTPTimeout:         durationEnv("AUTH_HTTP_TIMEOUT", 5*time.Second),
			PermissionsFile:     env("AUTH_PERMISSIONS_FILE", "config/auth/permissions.yaml"),
			RolesClaim:          env("AUTH_ROLES_CLAIM", "groups"),
			OwnedResourcesClaim: env("AUTH_OWNED_RESOURCES_CLAIM", "owned_resources"),
			Enabled:             boolEnv("AUTH_ENABLED", true),
		},
	}

	if err := cfg.Auth.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.StateLocationTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_LOCATION_TTL must be > 0")
	}
	if cfg.StateProximityTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_PROXIMITY_TTL must be > 0")
	}
	if cfg.StateDedupTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_DEDUP_TTL must be > 0")
	}
	if cfg.RPCTimeout <= 0 {
		return Config{}, fmt.Errorf("RPC_TIMEOUT must be > 0")
	}
	if cfg.ProximityResolutionEntryConfidenceMin < 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN must be >= 0")
	}
	if cfg.ProximityResolutionExitGraceDuration <= 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION must be > 0")
	}
	if cfg.ProximityResolutionBoundaryGrace < 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE must be >= 0")
	}
	if cfg.ProximityResolutionMinDwellDuration < 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_MIN_DWELL_DURATION must be >= 0")
	}
	if cfg.ProximityResolutionPositionMode != "zone_position" {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_POSITION_MODE must be zone_position")
	}
	if cfg.ProximityResolutionFallbackRadius < 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_FALLBACK_RADIUS must be >= 0")
	}
	if cfg.ProximityResolutionStaleStateTTL <= 0 {
		return Config{}, fmt.Errorf("PROXIMITY_RESOLUTION_STALE_STATE_TTL must be > 0")
	}
	return cfg, nil
}

// Validate checks that the authentication settings are internally consistent.
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
	if a.PermissionsFile == "" {
		return fmt.Errorf("AUTH_PERMISSIONS_FILE is required when auth is enabled")
	}
	if a.RolesClaim == "" {
		return fmt.Errorf("AUTH_ROLES_CLAIM is required when auth is enabled")
	}
	if a.OwnedResourcesClaim == "" {
		return fmt.Errorf("AUTH_OWNED_RESOURCES_CLAIM is required when auth is enabled")
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

func floatEnv(k string, d float64) float64 {
	v := env(k, "")
	if v == "" {
		return d
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return d
	}
	return x
}
