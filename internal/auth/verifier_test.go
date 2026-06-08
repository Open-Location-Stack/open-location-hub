package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

func TestStaticAuthenticatorAcceptsToken(t *testing.T) {
	t.Parallel()

	priv, pubPEM := makeKeypair(t)
	cfg := config.AuthConfig{
		Mode:                "static",
		Enabled:             true,
		Issuer:              "issuer",
		Audience:            []string{"open-location-hub"},
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
		"aud":    []string{"open-location-hub"},
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
	t.Parallel()

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

func TestMiddlewareSkipsAuthForUnknownPaths(t *testing.T) {
	t.Parallel()

	cfg := config.AuthConfig{Enabled: true, Mode: "static"}
	router := chi.NewRouter()
	router.Use(Middleware(noneAuthenticator{}, cfg, nil, router))
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	})

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown route, got %d", rec.Code)
	}
}

func TestMiddlewareStillAuthenticatesKnownPaths(t *testing.T) {
	t.Parallel()

	cfg := config.AuthConfig{Enabled: true, Mode: "static"}
	router := chi.NewRouter()
	router.Use(Middleware(noneAuthenticator{}, cfg, nil, router))
	router.Get("/v2/providers", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v2/providers", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for known protected route without bearer token, got %d", rec.Code)
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
