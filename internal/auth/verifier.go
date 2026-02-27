package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

type Verifier interface {
	Verify(ctx context.Context, token string) error
}

type noneVerifier struct{}

func (noneVerifier) Verify(context.Context, string) error { return nil }

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
}

func (o *oidcVerifier) Verify(ctx context.Context, token string) error {
	_, err := o.verifier.Verify(ctx, token)
	return err
}

type staticVerifier struct {
	parsers []jwt.Keyfunc
	algs    map[string]struct{}
}

func (s *staticVerifier) Verify(_ context.Context, token string) error {
	for _, keyfn := range s.parsers {
		_, err := jwt.Parse(token, keyfn, jwt.WithValidMethods(s.validMethods()))
		if err == nil {
			return nil
		}
	}
	return errors.New("token verification failed for all configured static keys")
}

func (s *staticVerifier) validMethods() []string {
	if len(s.algs) == 0 {
		return []string{"RS256"}
	}
	m := make([]string, 0, len(s.algs))
	for k := range s.algs {
		m = append(m, k)
	}
	return m
}

type hybridVerifier struct {
	verifiers []Verifier
}

func (h *hybridVerifier) Verify(ctx context.Context, token string) error {
	var errs []string
	for _, v := range h.verifiers {
		if err := v.Verify(ctx, token); err == nil {
			return nil
		} else {
			errs = append(errs, err.Error())
		}
	}
	return fmt.Errorf("all verifiers failed: %s", strings.Join(errs, "; "))
}

func NewVerifier(ctx context.Context, cfg config.AuthConfig) (Verifier, error) {
	if !cfg.Enabled || cfg.Mode == "none" {
		return noneVerifier{}, nil
	}

	switch cfg.Mode {
	case "oidc":
		return newOIDCVerifier(ctx, cfg)
	case "static":
		return newStaticVerifier(cfg)
	case "hybrid":
		var v []Verifier
		if cfg.Issuer != "" {
			o, err := newOIDCVerifier(ctx, cfg)
			if err != nil {
				return nil, err
			}
			v = append(v, o)
		}
		if len(cfg.StaticPublicKeys) > 0 {
			s, err := newStaticVerifier(cfg)
			if err != nil {
				return nil, err
			}
			v = append(v, s)
		}
		if len(v) == 0 {
			return nil, errors.New("hybrid mode has no valid verifier configured")
		}
		return &hybridVerifier{verifiers: v}, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode: %s", cfg.Mode)
	}
}

func newOIDCVerifier(ctx context.Context, cfg config.AuthConfig) (Verifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	oidcCfg := &oidc.Config{ClientID: ""}
	if len(cfg.Audience) > 0 {
		oidcCfg.ClientID = cfg.Audience[0]
	}
	return &oidcVerifier{verifier: provider.Verifier(oidcCfg)}, nil
}

func newStaticVerifier(cfg config.AuthConfig) (Verifier, error) {
	algs := make(map[string]struct{}, len(cfg.AllowedAlgs))
	for _, alg := range cfg.AllowedAlgs {
		if alg != "" {
			algs[strings.TrimSpace(alg)] = struct{}{}
		}
	}

	parsers := make([]jwt.Keyfunc, 0, len(cfg.StaticPublicKeys))
	for _, raw := range cfg.StaticPublicKeys {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			jwks, err := keyfunc.NewDefaultCtx(context.Background(), []string{raw})
			if err != nil {
				return nil, err
			}
			parsers = append(parsers, jwks.Keyfunc)
			continue
		}
		pub, err := parseRSAPublicKey(raw)
		if err != nil {
			return nil, err
		}
		parsers = append(parsers, func(t *jwt.Token) (any, error) {
			return pub, nil
		})
	}
	if len(parsers) == 0 {
		return nil, errors.New("no static keys configured")
	}
	return &staticVerifier{parsers: parsers, algs: algs}, nil
}

func parseRSAPublicKey(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid PEM block")
	}
	pk, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		cert, certErr := x509.ParseCertificate(block.Bytes)
		if certErr != nil {
			return nil, err
		}
		if rsaPub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, errors.New("certificate key is not RSA")
	}
	rsaPub, ok := pk.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("key is not RSA")
	}
	return rsaPub, nil
}

func Middleware(v Verifier, cfg config.AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled || cfg.Mode == "none" {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			hdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(strings.ToLower(hdr), "bearer ") {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			token := strings.TrimSpace(hdr[len("Bearer "):])
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err := v.Verify(ctx, token); err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
