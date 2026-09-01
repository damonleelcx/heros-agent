// Package tenancy carries who is acting, and is the only place that answers it.
//
// # 🔴 Why a principal is not a string
//
// A tenant id passed as a `string` parameter is a tenant id somebody can pass the wrong value for, and
// the wrong value is another customer's data. Every such call site is a place to get it right, and there
// is no such thing as a codebase where every call site is got right forever.
//
// So identity travels in the context, is put there by exactly one piece of middleware, and is read by
// code that cannot construct one for itself. A handler cannot invent a principal; it can only be given
// one or refuse.
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Principal is who is making a request.
type Principal struct {
	// Tenant owns the data. Never empty on a valid principal.
	Tenant string
	// Subject is the person or machine acting, for the audit trail. A principal with a tenant and no
	// subject is a service token; both are recorded.
	Subject string
	// SessionID identifies the credential used, so a specific session can be revoked without touching
	// the account.
	SessionID string
}

// Valid reports whether this principal may act.
func (p Principal) Valid() bool { return strings.TrimSpace(p.Tenant) != "" }

// String renders a principal for a log line. 🔴 Never includes the credential — only its id. A session
// token in a log is a session token that has leaked, and the habit of printing "just a prefix" is how
// the rest ends up beside it.
func (p Principal) String() string {
	if p.Subject == "" {
		return fmt.Sprintf("tenant=%s session=%s", p.Tenant, p.SessionID)
	}
	return fmt.Sprintf("tenant=%s subject=%s session=%s", p.Tenant, p.Subject, p.SessionID)
}

type key struct{}

// ErrNoPrincipal means the context carries no identity.
//
// 🔴 Returned rather than defaulting to anything. A default tenant is the bug this package exists to
// prevent: it turns "we forgot to authenticate this route" into "this route reads the default tenant's
// data", which is a silent success rather than a loud failure.
var ErrNoPrincipal = errors.New("tenancy: the request carries no identity")

// With returns a context carrying a principal. Called by authentication middleware and nowhere else.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// From returns the principal, or an error.
func From(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(key{}).(Principal)
	if !ok || !p.Valid() {
		return Principal{}, ErrNoPrincipal
	}
	return p, nil
}

// MustTenant returns the tenant or an error. The common case, named so a caller does not unpack a
// principal just to read one field.
func MustTenant(ctx context.Context) (string, error) {
	p, err := From(ctx)
	if err != nil {
		return "", err
	}
	return p.Tenant, nil
}

// System is the principal for work that has no requester: a worker draining a queue, a migration, a
// scheduled sweep.
//
// 🔴 It carries a REAL tenant rather than a wildcard. Background work still acts for exactly one
// customer, and a "system" principal that could read every tenant would be the single most valuable
// credential in the product — worth more than any customer's own login, and held by every worker
// process.
func System(tenant string) Principal {
	return Principal{Tenant: tenant, Subject: "system", SessionID: "system"}
}
