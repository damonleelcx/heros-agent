package auth

import "context"

type ctxKey int

const principalKey ctxKey = 1

// Principal is an authenticated caller (multi-tenant).
type Principal struct {
	TenantID string
	Role     string // "admin" | "member"
	APIKeyID string // optional label from config
}

func (p Principal) IsAdmin() bool {
	return p.Role == "admin"
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	v := ctx.Value(principalKey)
	if v == nil {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}
