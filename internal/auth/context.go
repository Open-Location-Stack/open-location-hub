package auth

import "context"

type contextKey string

const principalContextKey contextKey = "auth.principal"

type Principal struct {
	Subject        string
	Roles          []string
	OwnedResources map[string]map[string]struct{}
	Claims         map[string]any
}

func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(*Principal)
	return principal, ok && principal != nil
}
