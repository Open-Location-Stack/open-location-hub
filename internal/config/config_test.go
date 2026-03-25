package config

import "testing"

func TestDefaults(t *testing.T) {
	t.Setenv("AUTH_MODE", "none")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPListenAddr != ":8080" {
		t.Fatalf("unexpected listen addr: %s", cfg.HTTPListenAddr)
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
	if cfg.WebSocketWriteTimeout <= 0 || cfg.WebSocketOutboundBuffer <= 0 {
		t.Fatal("expected positive websocket settings")
	}
	if cfg.CollisionsEnabled {
		t.Fatal("expected collisions to default to disabled")
	}
}

func TestOIDCRequiresIssuer(t *testing.T) {
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("AUTH_ISSUER", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEnabledAuthRequiresPermissionsFile(t *testing.T) {
	t.Setenv("AUTH_MODE", "static")
	t.Setenv("AUTH_STATIC_PUBLIC_KEYS", "key")
	t.Setenv("AUTH_PERMISSIONS_FILE", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestTransientStateSettingsMustBePositive(t *testing.T) {
	t.Setenv("AUTH_MODE", "none")
	t.Setenv("STATE_LOCATION_TTL", "0s")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected validation error")
	}
}
