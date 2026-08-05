package auth

import "context"

type ctxKey int

const principalKey ctxKey = 1

// Principal is an authenticated caller (multi-tenant).
//
// It is a PRINCIPAL, not a user: a machine credential authenticates and names nobody. That distinction
// is the whole of `UserID` below, and it is what lets removing a person revoke their keys without
// breaking the customer's build pipeline.
type Principal struct {
	TenantID string
	Role     string // "owner" | "admin" | "member"
	APIKeyID string // credential id (durable path) or the optional label from configuration
	// UserID is the person this credential belongs to, when there is one.
	//
	// 🔴 Empty means MACHINE CREDENTIAL — not "unknown". Nothing may put a placeholder here: an audit
	// entry that names a person who did not act is worse than one that names none, because it is
	// believed. ADR-008 deferred a user model "because the platform cannot currently prove one"; P22
	// made it provable and P27 is where the field appears.
	UserID string
}

// Personal reports whether this principal is a person rather than a machine.
func (p Principal) Personal() bool { return p.UserID != "" }

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
