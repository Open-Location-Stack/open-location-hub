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

func TestStaticVerifierAcceptsToken(t *testing.T) {
	priv, pubPEM := makeKeypair(t)
	cfg := config.AuthConfig{Mode: "static", Enabled: true, AllowedAlgs: []string{"RS256"}, StaticPublicKeys: []string{pubPEM}}
	v, err := NewVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new verifier failed: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "test", "exp": time.Now().Add(time.Hour).Unix()})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	if err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestHybridVerifierAcceptsStaticPath(t *testing.T) {
	priv, pubPEM := makeKeypair(t)
	cfg := config.AuthConfig{Mode: "hybrid", Enabled: true, AllowedAlgs: []string{"RS256"}, StaticPublicKeys: []string{pubPEM}}
	v, err := NewVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new verifier failed: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "test", "exp": time.Now().Add(time.Hour).Unix()})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	if err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("verify failed: %v", err)
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
