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
}

func TestOIDCRequiresIssuer(t *testing.T) {
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("AUTH_ISSUER", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected validation error")
	}
}
