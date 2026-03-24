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
