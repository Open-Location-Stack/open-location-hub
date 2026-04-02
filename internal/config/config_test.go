package config

import "testing"

func configFromMap(values map[string]string) (Config, error) {
	return fromLookupEnv(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := configFromMap(map[string]string{
		"AUTH_MODE": "none",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPListenAddr != ":8080" {
		t.Fatalf("unexpected listen addr: %s", cfg.HTTPListenAddr)
	}
	if cfg.HTTPRequestBodyLimitBytes != 4*1024*1024 {
		t.Fatalf("unexpected request body limit: %d", cfg.HTTPRequestBodyLimitBytes)
	}
	if cfg.Auth.PermissionsFile != "config/auth/permissions.yaml" {
		t.Fatalf("unexpected permissions file: %s", cfg.Auth.PermissionsFile)
	}
	if cfg.Auth.RolesClaim != "groups" {
		t.Fatalf("unexpected roles claim: %s", cfg.Auth.RolesClaim)
	}
	if cfg.Auth.OwnedResourcesClaim != "owned_resources" {
		t.Fatalf("unexpected owned resources claim: %s", cfg.Auth.OwnedResourcesClaim)
	}
	if cfg.StateLocationTTL <= 0 || cfg.StateProximityTTL <= 0 || cfg.StateDedupTTL <= 0 {
		t.Fatal("expected positive transient state TTL defaults")
	}
	if cfg.RPCTimeout <= 0 {
		t.Fatal("expected positive rpc timeout")
	}
	if cfg.WebSocketWriteTimeout <= 0 || cfg.WebSocketReadTimeout <= 0 || cfg.WebSocketPingInterval <= 0 || cfg.WebSocketOutboundBuffer <= 0 {
		t.Fatal("expected positive websocket settings")
	}
	if cfg.EventBusSubscriberBuffer <= 0 || cfg.NativeLocationBuffer <= 0 {
		t.Fatal("expected positive async buffer settings")
	}
	if cfg.DerivedLocationBuffer <= 0 {
		t.Fatal("expected positive derived location buffer")
	}
	if cfg.WebSocketReadTimeout <= cfg.WebSocketPingInterval {
		t.Fatal("expected websocket read timeout to exceed ping interval")
	}
	if cfg.CollisionsEnabled {
		t.Fatal("expected collisions to default to disabled")
	}
}

func TestOIDCRequiresIssuer(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":   "oidc",
		"AUTH_ISSUER": "",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEnabledAuthRequiresPermissionsFile(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":               "static",
		"AUTH_STATIC_PUBLIC_KEYS": "key",
		"AUTH_PERMISSIONS_FILE":   "",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestTransientStateSettingsMustBePositive(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":          "none",
		"STATE_LOCATION_TTL": "0s",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRequestBodyLimitMustBePositive(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":                     "none",
		"HTTP_REQUEST_BODY_LIMIT_BYTES": "0",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestWebSocketReadTimeoutMustExceedPingInterval(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":               "none",
		"WEBSOCKET_READ_TIMEOUT":  "30s",
		"WEBSOCKET_PING_INTERVAL": "30s",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDerivedLocationBufferMustBePositive(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":               "none",
		"DERIVED_LOCATION_BUFFER": "0",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNativeLocationBufferMustBePositive(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":              "none",
		"NATIVE_LOCATION_BUFFER": "0",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEventBusSubscriberBufferMustBePositive(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE":                   "none",
		"EVENT_BUS_SUBSCRIBER_BUFFER": "0",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidHubIDLoadsSuccessfully(t *testing.T) {
	t.Parallel()

	cfg, err := configFromMap(map[string]string{
		"AUTH_MODE": "none",
		"HUB_ID":    "4f630dd4-e5f2-4398-9970-c63cad9bc109",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HubID != "4f630dd4-e5f2-4398-9970-c63cad9bc109" {
		t.Fatalf("unexpected hub id: %s", cfg.HubID)
	}
}

func TestInvalidHubIDFailsValidation(t *testing.T) {
	t.Parallel()

	_, err := configFromMap(map[string]string{
		"AUTH_MODE": "none",
		"HUB_ID":    "not-a-uuid",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
