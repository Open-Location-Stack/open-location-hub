package auth

import "context"

type contextKey string

const principalContextKey contextKey = "auth.principal"

// Principal captures the authenticated caller identity and derived
// authorization context extracted from a token.
type Principal struct {
	Subject        string
	Roles          []string
	OwnedResources map[string]map[string]struct{}
	Claims         map[string]any
}

// WithPrincipal stores the authenticated principal on the request context.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

// PrincipalFromContext returns the authenticated principal stored on the
// context, if one is present.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(*Principal)
	return principal, ok && principal != nil
}
