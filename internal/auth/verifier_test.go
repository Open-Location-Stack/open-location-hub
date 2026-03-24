package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

func TestStaticAuthenticatorAcceptsToken(t *testing.T) {
	priv, pubPEM := makeKeypair(t)
	cfg := config.AuthConfig{
		Mode:                "static",
		Enabled:             true,
		Issuer:              "issuer",
		Audience:            []string{"open-rtls-hub"},
		AllowedAlgs:         []string{"RS256"},
		StaticPublicKeys:    []string{pubPEM},
		RolesClaim:          "groups",
		OwnedResourcesClaim: "owned_resources",
	}
	a, err := NewAuthenticator(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new authenticator failed: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub":    "test",
		"iss":    "issuer",
		"aud":    []string{"open-rtls-hub"},
		"exp":    time.Now().Add(time.Hour).Unix(),
		"groups": []string{"omlox-api-admin"},
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	principal, err := a.Authenticate(context.Background(), raw)
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if principal.Subject != "test" {
		t.Fatalf("unexpected subject %q", principal.Subject)
	}
}

func TestPrincipalFromClaimsExtractsOwnedResources(t *testing.T) {
	principal := principalFromClaims(map[string]any{
		"sub":    "test",
		"groups": []any{"reader"},
		"owned_resources": map[string]any{
			"provider_ids": []any{"provider-1"},
		},
	}, config.AuthConfig{
		RolesClaim:          "groups",
		OwnedResourcesClaim: "owned_resources",
	})

	if _, ok := principal.OwnedResources["provider_ids"]["provider-1"]; !ok {
		t.Fatal("expected provider ownership claim")
	}
}

func makeKeypair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("keygen failed: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub key failed: %v", err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return priv, string(pub)
}
