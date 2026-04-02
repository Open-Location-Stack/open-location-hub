package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type lookupEnvFunc func(string) (string, bool)

// Config contains the hub runtime configuration loaded from environment
// variables.
type Config struct {
	HTTPListenAddr                        string
	HTTPRequestBodyLimitBytes             int64
	LogLevel                              string
	HubID                                 string
	HubLabel                              string
	ResetHubID                            bool
	PostgresURL                           string
	MQTTBrokerURL                         string
	WebSocketWriteTimeout                 time.Duration
	WebSocketReadTimeout                  time.Duration
	WebSocketPingInterval                 time.Duration
	WebSocketOutboundBuffer               int
	EventBusSubscriberBuffer              int
	NativeLocationBuffer                  int
	DerivedLocationBuffer                 int
	StateLocationTTL                      time.Duration
	StateProximityTTL                     time.Duration
	StateDedupTTL                         time.Duration
	MetadataReconcileInterval             time.Duration
	RPCTimeout                            time.Duration
	RPCAnnouncementInterval               time.Duration
	RPCHandlerID                          string
	CollisionsEnabled                     bool
	CollisionStateTTL                     time.Duration
	CollisionCollidingDebounce            time.Duration
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
	return fromLookupEnv(os.LookupEnv)
}

func fromLookupEnv(lookup lookupEnvFunc) (Config, error) {
	cfg := Config{
		HTTPListenAddr:                        envWithLookup(lookup, "HTTP_LISTEN_ADDR", ":8080"),
		HTTPRequestBodyLimitBytes:             int64EnvWithLookup(lookup, "HTTP_REQUEST_BODY_LIMIT_BYTES", 4*1024*1024),
		LogLevel:                              envWithLookup(lookup, "LOG_LEVEL", "info"),
		HubID:                                 strings.TrimSpace(envWithLookup(lookup, "HUB_ID", "")),
		HubLabel:                              strings.TrimSpace(envWithLookup(lookup, "HUB_LABEL", "")),
		ResetHubID:                            boolEnvWithLookup(lookup, "RESET_HUB_ID", false),
		PostgresURL:                           envWithLookup(lookup, "POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/openrtls?sslmode=disable"),
		MQTTBrokerURL:                         envWithLookup(lookup, "MQTT_BROKER_URL", "tcp://localhost:1883"),
		WebSocketWriteTimeout:                 durationEnvWithLookup(lookup, "WEBSOCKET_WRITE_TIMEOUT", 5*time.Second),
		WebSocketReadTimeout:                  durationEnvWithLookup(lookup, "WEBSOCKET_READ_TIMEOUT", time.Minute),
		WebSocketPingInterval:                 durationEnvWithLookup(lookup, "WEBSOCKET_PING_INTERVAL", 30*time.Second),
		WebSocketOutboundBuffer:               intEnvWithLookup(lookup, "WEBSOCKET_OUTBOUND_BUFFER", 256),
		EventBusSubscriberBuffer:              intEnvWithLookup(lookup, "EVENT_BUS_SUBSCRIBER_BUFFER", 1024),
		NativeLocationBuffer:                  intEnvWithLookup(lookup, "NATIVE_LOCATION_BUFFER", 2048),
		DerivedLocationBuffer:                 intEnvWithLookup(lookup, "DERIVED_LOCATION_BUFFER", 1024),
		StateLocationTTL:                      durationEnvWithLookup(lookup, "STATE_LOCATION_TTL", 10*time.Minute),
		StateProximityTTL:                     durationEnvWithLookup(lookup, "STATE_PROXIMITY_TTL", 5*time.Minute),
		StateDedupTTL:                         durationEnvWithLookup(lookup, "STATE_DEDUP_TTL", 2*time.Minute),
		MetadataReconcileInterval:             durationEnvWithLookup(lookup, "METADATA_RECONCILE_INTERVAL", 30*time.Second),
		RPCTimeout:                            durationEnvWithLookup(lookup, "RPC_TIMEOUT", 5*time.Second),
		RPCAnnouncementInterval:               durationEnvWithLookup(lookup, "RPC_ANNOUNCEMENT_INTERVAL", time.Minute),
		RPCHandlerID:                          envWithLookup(lookup, "RPC_HANDLER_ID", "open-rtls-hub"),
		CollisionsEnabled:                     boolEnvWithLookup(lookup, "COLLISIONS_ENABLED", false),
		CollisionStateTTL:                     durationEnvWithLookup(lookup, "COLLISION_STATE_TTL", 2*time.Minute),
		CollisionCollidingDebounce:            durationEnvWithLookup(lookup, "COLLISION_COLLIDING_DEBOUNCE", 5*time.Second),
		ProximityResolutionEntryConfidenceMin: floatEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_ENTRY_CONFIDENCE_MIN", 0),
		ProximityResolutionExitGraceDuration:  durationEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_EXIT_GRACE_DURATION", 15*time.Second),
		ProximityResolutionBoundaryGrace:      floatEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_BOUNDARY_GRACE_DISTANCE", 2),
		ProximityResolutionMinDwellDuration:   durationEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_MIN_DWELL_DURATION", 5*time.Second),
		ProximityResolutionPositionMode:       envWithLookup(lookup, "PROXIMITY_RESOLUTION_POSITION_MODE", "zone_position"),
		ProximityResolutionFallbackRadius:     floatEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_FALLBACK_RADIUS", 0),
		ProximityResolutionStaleStateTTL:      durationEnvWithLookup(lookup, "PROXIMITY_RESOLUTION_STALE_STATE_TTL", 10*time.Minute),
		Auth: AuthConfig{
			Mode:                envWithLookup(lookup, "AUTH_MODE", "none"),
			Audience:            csvEnvWithLookup(lookup, "AUTH_AUDIENCE", "open-rtls-hub"),
			Issuer:              envWithLookup(lookup, "AUTH_ISSUER", ""),
			AllowedAlgs:         csvEnvWithLookup(lookup, "AUTH_ALLOWED_ALGS", "RS256"),
			ClockSkew:           durationEnvWithLookup(lookup, "AUTH_CLOCK_SKEW", 30*time.Second),
			StaticPublicKeys:    csvEnvWithLookup(lookup, "AUTH_STATIC_PUBLIC_KEYS", ""),
			OIDCJWKSRefreshTTL:  durationEnvWithLookup(lookup, "AUTH_OIDC_REFRESH_TTL", 10*time.Minute),
			HTTPTimeout:         durationEnvWithLookup(lookup, "AUTH_HTTP_TIMEOUT", 5*time.Second),
			PermissionsFile:     envWithLookup(lookup, "AUTH_PERMISSIONS_FILE", "config/auth/permissions.yaml"),
			RolesClaim:          envWithLookup(lookup, "AUTH_ROLES_CLAIM", "groups"),
			OwnedResourcesClaim: envWithLookup(lookup, "AUTH_OWNED_RESOURCES_CLAIM", "owned_resources"),
			Enabled:             boolEnvWithLookup(lookup, "AUTH_ENABLED", true),
		},
	}

	if err := cfg.Auth.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.StateLocationTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_LOCATION_TTL must be > 0")
	}
	if cfg.WebSocketWriteTimeout <= 0 {
		return Config{}, fmt.Errorf("WEBSOCKET_WRITE_TIMEOUT must be > 0")
	}
	if cfg.WebSocketReadTimeout <= 0 {
		return Config{}, fmt.Errorf("WEBSOCKET_READ_TIMEOUT must be > 0")
	}
	if cfg.WebSocketPingInterval <= 0 {
		return Config{}, fmt.Errorf("WEBSOCKET_PING_INTERVAL must be > 0")
	}
	if cfg.WebSocketReadTimeout <= cfg.WebSocketPingInterval {
		return Config{}, fmt.Errorf("WEBSOCKET_READ_TIMEOUT must be greater than WEBSOCKET_PING_INTERVAL")
	}
	if cfg.HTTPRequestBodyLimitBytes <= 0 {
		return Config{}, fmt.Errorf("HTTP_REQUEST_BODY_LIMIT_BYTES must be > 0")
	}
	if cfg.HubID != "" {
		if _, err := uuid.Parse(cfg.HubID); err != nil {
			return Config{}, fmt.Errorf("HUB_ID must be a valid UUID: %w", err)
		}
	}
	if cfg.WebSocketOutboundBuffer <= 0 {
		return Config{}, fmt.Errorf("WEBSOCKET_OUTBOUND_BUFFER must be > 0")
	}
	if cfg.EventBusSubscriberBuffer <= 0 {
		return Config{}, fmt.Errorf("EVENT_BUS_SUBSCRIBER_BUFFER must be > 0")
	}
	if cfg.NativeLocationBuffer <= 0 {
		return Config{}, fmt.Errorf("NATIVE_LOCATION_BUFFER must be > 0")
	}
	if cfg.DerivedLocationBuffer <= 0 {
		return Config{}, fmt.Errorf("DERIVED_LOCATION_BUFFER must be > 0")
	}
	if cfg.StateProximityTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_PROXIMITY_TTL must be > 0")
	}
	if cfg.StateDedupTTL <= 0 {
		return Config{}, fmt.Errorf("STATE_DEDUP_TTL must be > 0")
	}
	if cfg.MetadataReconcileInterval <= 0 {
		return Config{}, fmt.Errorf("METADATA_RECONCILE_INTERVAL must be > 0")
	}
	if cfg.RPCTimeout <= 0 {
		return Config{}, fmt.Errorf("RPC_TIMEOUT must be > 0")
	}
	if cfg.RPCAnnouncementInterval <= 0 {
		return Config{}, fmt.Errorf("RPC_ANNOUNCEMENT_INTERVAL must be > 0")
	}
	if strings.TrimSpace(cfg.RPCHandlerID) == "" {
		return Config{}, fmt.Errorf("RPC_HANDLER_ID must not be empty")
	}
	if cfg.CollisionStateTTL <= 0 {
		return Config{}, fmt.Errorf("COLLISION_STATE_TTL must be > 0")
	}
	if cfg.CollisionCollidingDebounce < 0 {
		return Config{}, fmt.Errorf("COLLISION_COLLIDING_DEBOUNCE must be >= 0")
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

func envWithLookup(lookup lookupEnvFunc, k, d string) string {
	if v, ok := lookup(k); ok {
		return v
	}
	return d
}

func csvEnvWithLookup(lookup lookupEnvFunc, k, d string) []string {
	v := envWithLookup(lookup, k, d)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func durationEnvWithLookup(lookup lookupEnvFunc, k string, d time.Duration) time.Duration {
	v := envWithLookup(lookup, k, "")
	if v == "" {
		return d
	}
	x, err := time.ParseDuration(v)
	if err != nil {
		return d
	}
	return x
}

func boolEnvWithLookup(lookup lookupEnvFunc, k string, d bool) bool {
	v := envWithLookup(lookup, k, "")
	if v == "" {
		return d
	}
	x, err := strconv.ParseBool(v)
	if err != nil {
		return d
	}
	return x
}

func floatEnvWithLookup(lookup lookupEnvFunc, k string, d float64) float64 {
	v := envWithLookup(lookup, k, "")
	if v == "" {
		return d
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return d
	}
	return x
}

func intEnvWithLookup(lookup lookupEnvFunc, k string, d int) int {
	v := envWithLookup(lookup, k, "")
	if v == "" {
		return d
	}
	x, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return x
}

func int64EnvWithLookup(lookup lookupEnvFunc, k string, d int64) int64 {
	v := envWithLookup(lookup, k, "")
	if v == "" {
		return d
	}
	x, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return d
	}
	return x
}
