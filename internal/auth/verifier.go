package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

// Authenticator verifies bearer tokens and returns the resulting principal.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*Principal, error)
}

type noneAuthenticator struct{}

func (noneAuthenticator) Authenticate(context.Context, string) (*Principal, error) {
	return &Principal{}, nil
}

type oidcAuthenticator struct {
	cfg          config.AuthConfig
	httpClient   *http.Client
	mu           sync.RWMutex
	keyfunc      jwt.Keyfunc
	lastRefresh  time.Time
	jwksURL      string
	refreshError error
}

type staticAuthenticator struct {
	cfg     config.AuthConfig
	parsers []jwt.Keyfunc
}

type hybridAuthenticator struct {
	authenticators []Authenticator
}

// NewAuthenticator builds the configured authentication strategy.
func NewAuthenticator(ctx context.Context, cfg config.AuthConfig) (Authenticator, error) {
	if !cfg.Enabled || cfg.Mode == "none" {
		return noneAuthenticator{}, nil
	}

	switch cfg.Mode {
	case "oidc":
		return newOIDCAuthenticator(ctx, cfg)
	case "static":
		return newStaticAuthenticator(cfg)
	case "hybrid":
		var authenticators []Authenticator
		if cfg.Issuer != "" {
			o, err := newOIDCAuthenticator(ctx, cfg)
			if err != nil {
				return nil, err
			}
			authenticators = append(authenticators, o)
		}
		if len(cfg.StaticPublicKeys) > 0 {
			s, err := newStaticAuthenticator(cfg)
			if err != nil {
				return nil, err
			}
			authenticators = append(authenticators, s)
		}
		if len(authenticators) == 0 {
			return nil, errors.New("hybrid mode has no valid authenticator configured")
		}
		return &hybridAuthenticator{authenticators: authenticators}, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode: %s", cfg.Mode)
	}
}

func newOIDCAuthenticator(ctx context.Context, cfg config.AuthConfig) (Authenticator, error) {
	a := &oidcAuthenticator{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	}
	if err := a.refreshVerifier(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *oidcAuthenticator) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if err := a.ensureFresh(ctx); err != nil {
		return nil, unauthorized("unable to refresh OIDC verifier")
	}

	a.mu.RLock()
	keyfn := a.keyfunc
	a.mu.RUnlock()
	if keyfn == nil {
		return nil, unauthorized("missing OIDC verifier")
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(cleanStrings(a.cfg.AllowedAlgs)),
		jwt.WithLeeway(a.cfg.ClockSkew),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(a.cfg.Issuer),
	)
	parsed, err := parser.ParseWithClaims(token, claims, keyfn)
	if err != nil {
		return nil, unauthorized("invalid bearer token")
	}
	if !parsed.Valid {
		return nil, unauthorized("invalid bearer token")
	}
	rawClaims := map[string]any{}
	for k, v := range claims {
		rawClaims[k] = v
	}
	if !audienceAllowed(extractAudience(rawClaims), a.cfg.Audience) {
		return nil, unauthorized("token audience is not accepted")
	}
	return principalFromClaims(rawClaims, a.cfg), nil
}

func (a *oidcAuthenticator) ensureFresh(ctx context.Context) error {
	a.mu.RLock()
	needsRefresh := a.keyfunc == nil || time.Since(a.lastRefresh) > a.cfg.OIDCJWKSRefreshTTL
	hasVerifier := a.keyfunc != nil
	a.mu.RUnlock()
	if !needsRefresh {
		return nil
	}
	if err := a.refreshVerifier(ctx); err != nil {
		if hasVerifier {
			return nil
		}
		return err
	}
	return nil
}

func (a *oidcAuthenticator) refreshVerifier(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keyfunc != nil && time.Since(a.lastRefresh) <= a.cfg.OIDCJWKSRefreshTTL {
		return nil
	}

	provider, err := oidc.NewProvider(withHTTPClient(ctx, a.httpClient), a.cfg.Issuer)
	if err != nil {
		a.refreshError = err
		return err
	}

	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		a.refreshError = err
		return err
	}
	if strings.TrimSpace(metadata.JWKSURI) == "" {
		err := errors.New("OIDC metadata missing jwks_uri")
		a.refreshError = err
		return err
	}
	jwks, err := keyfunc.NewDefaultCtx(context.Background(), []string{metadata.JWKSURI})
	if err != nil {
		a.refreshError = err
		return err
	}
	a.keyfunc = jwks.Keyfunc
	a.jwksURL = metadata.JWKSURI
	a.lastRefresh = time.Now()
	a.refreshError = nil
	return nil
}

func newStaticAuthenticator(cfg config.AuthConfig) (Authenticator, error) {
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
		if _, err := os.Stat(raw); err == nil {
			data, readErr := os.ReadFile(raw)
			if readErr != nil {
				return nil, readErr
			}
			raw = string(data)
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
	return &staticAuthenticator{cfg: cfg, parsers: parsers}, nil
}

func (a *staticAuthenticator) Authenticate(_ context.Context, token string) (*Principal, error) {
	var lastErr error
	for _, keyfn := range a.parsers {
		claims := jwt.MapClaims{}
		options := []jwt.ParserOption{
			jwt.WithValidMethods(cleanStrings(a.cfg.AllowedAlgs)),
			jwt.WithLeeway(a.cfg.ClockSkew),
			jwt.WithExpirationRequired(),
		}
		if issuer := strings.TrimSpace(a.cfg.Issuer); issuer != "" {
			options = append(options, jwt.WithIssuer(issuer))
		}
		if aud := audienceOrDefault(a.cfg.Audience); aud != "" {
			options = append(options, jwt.WithAudience(aud))
		}
		parser := jwt.NewParser(options...)
		parsed, err := parser.ParseWithClaims(token, claims, keyfn)
		if err != nil {
			lastErr = err
			continue
		}
		if !parsed.Valid {
			lastErr = errors.New("invalid token")
			continue
		}
		rawClaims := map[string]any{}
		for k, v := range claims {
			rawClaims[k] = v
		}
		if !audienceAllowed(extractAudience(rawClaims), a.cfg.Audience) {
			lastErr = errors.New("audience mismatch")
			continue
		}
		return principalFromClaims(rawClaims, a.cfg), nil
	}
	if lastErr == nil {
		lastErr = errors.New("token verification failed")
	}
	return nil, unauthorized("invalid bearer token")
}

func (a *hybridAuthenticator) Authenticate(ctx context.Context, token string) (*Principal, error) {
	for _, authenticator := range a.authenticators {
		principal, err := authenticator.Authenticate(ctx, token)
		if err == nil {
			return principal, nil
		}
	}
	return nil, unauthorized("invalid bearer token")
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

// Middleware authenticates bearer tokens and enforces the configured route
// authorization registry on incoming HTTP requests.
func Middleware(authenticator Authenticator, cfg config.AuthConfig, registry *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled || cfg.Mode == "none" || r.URL.Path == "/healthz" || r.URL.Path == "/v2/ws/socket" {
				next.ServeHTTP(w, r)
				return
			}

			hdr := strings.TrimSpace(r.Header.Get("Authorization"))
			if !strings.HasPrefix(strings.ToLower(hdr), "bearer ") {
				writeAuthError(w, unauthorized("missing bearer token"))
				return
			}
			token := strings.TrimSpace(hdr[len("Bearer "):])
			if token == "" {
				writeAuthError(w, unauthorized("missing bearer token"))
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), cfg.HTTPTimeout)
			defer cancel()
			principal, err := authenticator.Authenticate(ctx, token)
			if err != nil {
				writeAuthError(w, err)
				return
			}
			if registry == nil {
				writeAuthError(w, forbidden("authorization registry is not configured"))
				return
			}
			if err := registry.Authorize(r, principal); err != nil {
				writeAuthError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

func principalFromClaims(claims map[string]any, cfg config.AuthConfig) *Principal {
	return &Principal{
		Subject:        stringClaim(claims["sub"]),
		Roles:          extractStringValues(claims[cfg.RolesClaim]),
		OwnedResources: extractOwnedResources(claims[cfg.OwnedResourcesClaim]),
		Claims:         claims,
	}
}

func extractOwnedResources(v any) map[string]map[string]struct{} {
	if v == nil {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		if typed, ok := v.(map[string]interface{}); ok {
			raw = map[string]any(typed)
		} else {
			return nil
		}
	}
	out := make(map[string]map[string]struct{}, len(raw))
	for k, values := range raw {
		normalizedKey := normalizeOwnedKey(k)
		set := make(map[string]struct{})
		for _, item := range extractStringValues(values) {
			set[item] = struct{}{}
		}
		if len(set) > 0 {
			out[normalizedKey] = set
		}
	}
	return out
}

func normalizeOwnedKey(k string) string {
	k = strings.TrimSpace(k)
	if strings.Contains(k, ".") {
		k = k[strings.LastIndex(k, ".")+1:]
	}
	k = strings.ReplaceAll(k, "-", "_")
	if strings.Contains(k, "/") {
		k = k[strings.LastIndex(k, "/")+1:]
	}
	if strings.Contains(k, ":") {
		k = k[strings.LastIndex(k, ":")+1:]
	}
	return strings.ToLower(k)
}

func extractStringValues(v any) []string {
	switch value := v.(type) {
	case nil:
		return nil
	case string:
		fields := strings.Fields(value)
		if len(fields) > 1 {
			return fields
		}
		if value == "" {
			return nil
		}
		return []string{value}
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func stringClaim(v any) string {
	s, _ := v.(string)
	return s
}

func extractAudience(claims map[string]any) []string {
	return extractStringValues(claims["aud"])
}

func audienceAllowed(tokenAudience, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, item := range allowed {
		allowedSet[item] = struct{}{}
	}
	for _, item := range tokenAudience {
		if _, ok := allowedSet[item]; ok {
			return true
		}
	}
	return false
}

func audienceOrDefault(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func withHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return oidc.ClientContext(ctx, client)
}
