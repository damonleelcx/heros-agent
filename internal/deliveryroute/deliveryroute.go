// Package deliveryroute is the durable home for P12 delivery routes — the table behind
// forgedelivery.RouteRegistry, which has been an interface with no implementation since P12 landed.
//
// # What a route is, and what it is not
//
// A route says WHERE a tenant's verified proposals are delivered: the target repository, the forge, and
// the credential mode. It holds NO CREDENTIAL and there is no column for one. That is the whole shape of
// P12's design — in `ci` mode the customer's CI performs the delivery holding its own ephemeral token,
// and the platform never stores anything that can write to a customer's repository. A schema with
// nowhere to put a token is a stronger statement of that than a comment is.
//
// # Why a store package rather than a store inside internal/forgedelivery
//
// forgedelivery contains no `database/sql` reference of any kind, deliberately: it is the delivery
// DOMAIN — modes, targets, idempotency, the enforcement funnel — and every one of its rules is decided
// in Go, over values, testable without a database. Putting the store there would make the package that
// decides who may write to a customer's repository depend on a driver for persistence reasons alone.
// This package depends on forgedelivery; forgedelivery does not depend on it.
//
// # The one thing to read before changing the table
//
// A `forgedelivery.Route` is NOT reconstructible from the columns a reader would guess it needs. Its
// `Target.Base` — the branch a pull request lands in — is required by `Target.Validate` and is
// deliberately absent from `Target.Key()`, which is the `target` column. It has to be its own column,
// and migration 0026 removed it on the reasoning that "base_ref is not a field of Route", which is true
// of the struct and false of the type. TestARouteSurvivesTheRoundTripThroughItsColumns is the fence:
// it reduces a Route to exactly these columns and rebuilds it, so a column a valid Route needs cannot be
// dropped again by an argument that sounds right.
package deliveryroute

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// Errors this store returns.
var (
	// ErrUnscoped guards the mistake the database also refuses: a route with no tenant is a delivery
	// target nobody owns, and delivery is a WRITE into a repository.
	ErrUnscoped = errors.New("deliveryroute: a route must name its tenant")
	// ErrUnexplained mirrors 0025's delivery_route_lost_capability_is_explained. Caught here so the
	// caller gets a sentence rather than a constraint violation.
	ErrUnexplained = errors.New("deliveryroute: a lost capability must say what was lost")
)

// Capability kinds as the `capability_kind` column spells them. "" is intact — a third state rather
// than a boolean, because "configured and working", "configured but degraded" and "revoked" lead to
// three different next actions.
const (
	capIntact   = ""
	capDegraded = "degraded"
	capRevoked  = "revoked"
)

// PGStore is the durable route registry over migrations 0025, 0026 and 0027. It implements
// forgedelivery.RouteRegistry.
type PGStore struct{ db *sql.DB }

// Compile-time proof that this is the RouteRegistry forgedelivery.Service was written against. Without
// it the two only meet at the mounting call site, which is exactly where a missed method is discovered
// on a customer's deployment rather than here.
var _ fd.RouteRegistry = (*PGStore)(nil)

// NewPGStore returns a store over an open Postgres handle.
func NewPGStore(db *sql.DB) (*PGStore, error) {
	if db == nil {
		return nil, errors.New("deliveryroute: nil database")
	}
	return &PGStore{db: db}, nil
}

func (s *PGStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

const routeColumns = `target, base_ref, forge, mode`

// Put records a route for a tenant, replacing any route already configured for that target.
//
// # What is validated here, and what deliberately is not
//
// Not `Route.Validate`. That method also requires `ForgeKind.Supported()`, which only github satisfies —
// and refusing to STORE a gitlab route would be this store overruling a decision that belongs one layer
// up. Migration 0026 states the split explicitly: gitlab and bitbucket are declared-but-unimplemented,
// storing one is legitimate configuration, and DELIVERING to one is refused in Go where the reason can
// be spoken. The CHECK constraint keeps a typo out; it does not re-decide what is implemented.
//
// So this validates exactly what makes the row RECONSTRUCTIBLE — a known mode, a target complete enough
// to name a pull request's destination and base, a forge the column will accept.
//
// ⚠️ A stored gitlab route is refused later by Deliverer.Prepare, and Service.Pending turns that refusal
// into an absent entry rather than a message. A tenant who configures gitlab therefore sees an empty
// pending list, not "gitlab is not implemented". That silence is forgedelivery's, not this store's, and
// it is worth fixing where the `continue` is.
func (s *PGStore) Put(parent context.Context, tenantID string, r fd.Route) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrUnscoped
	}
	if !r.Mode.Valid() {
		return fmt.Errorf("deliveryroute: %q is not a delivery mode (ci, app)", r.Mode)
	}
	// Target.Validate is what requires Base — the field 0026 dropped the column for. Calling it here
	// means a route that could not be read back is refused at the moment it is written.
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("deliveryroute: %w", err)
	}
	if !knownForge(r.ForgeKind) {
		return fmt.Errorf("deliveryroute: %q is not a known forge (github, gitlab, bitbucket)", r.ForgeKind)
	}

	ctx, cancel := s.ctx(parent)
	defer cancel()

	// Capability is NOT touched on conflict. A re-Put is a reconfiguration of where deliveries go; it is
	// not a claim that a revoked App installation has come back. Clearing a lost capability as a side
	// effect of an unrelated edit would silently re-enable delivery through a credential that no longer
	// exists — ClearCapability is the deliberate way to say it was restored.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, base_ref, forge, mode)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (tenant_id, target) DO UPDATE
		   SET base_ref = EXCLUDED.base_ref,
		       forge    = EXCLUDED.forge,
		       mode     = EXCLUDED.mode`,
		tenantID, r.Target.Key(), r.Target.Base, string(r.ForgeKind), string(r.Mode)); err != nil {
		return fmt.Errorf("deliveryroute: put %s/%s: %w", tenantID, r.Target.Key(), err)
	}
	return nil
}

// RouteFor returns the configured route for a tenant+target, or nil if none is configured. A missing
// route is not an error — it is the RouteAbsent condition the console reports with a next action.
//
// 🔴 AN EMPTY TARGET IS THE "HAS ANY ROUTE" PROBE, NOT A LOOKUP OF THE TARGET NAMED "".
//
// forgedelivery.Service.anyRoute calls RouteFor(ctx, tenantID, "") and reads a non-nil result as "this
// tenant has at least one route configured". That contract lived only in a comment on the CALLER, and an
// implementation that took it literally would find nothing (the CHECK forbidding an empty `target` guarantees no such
// row can exist), return nil, and make Service report RouteAbsent to every tenant who HAS routes —
// telling them to configure a route they already configured, on the surface whose entire job is to say
// what to do next. It is stated on the interface now as well as here.
//
// The probe cannot be reached by accident: a Target complete enough to pass Validate always has an owner
// and a repo, so Target.Key() is never "" for any route a caller could deliver to.
func (s *PGStore) RouteFor(parent context.Context, tenantID, target string) (*fd.Route, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrUnscoped
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()

	query := `SELECT ` + routeColumns + ` FROM delivery_route WHERE tenant_id = $1 AND target = $2`
	args := []any{tenantID, target}
	if target == "" {
		// Oldest first so the probe is deterministic across calls. Which route comes back does not
		// matter to anyRoute, but a query that returns a different row each time is one whose behaviour
		// nobody can write a test for.
		query = `SELECT ` + routeColumns + ` FROM delivery_route WHERE tenant_id = $1
		          ORDER BY created_at ASC, target ASC LIMIT 1`
		args = []any{tenantID}
	}

	r, err := scanRoute(s.db.QueryRowContext(ctx, query, args...))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("deliveryroute: route for %s/%s: %w", tenantID, target, err)
	}
	return &r, nil
}

// Capability reports a lost-capability condition for a tenant, or Kind=="" when capability is intact.
//
// Revoked wins over degraded when a tenant has both. They are different sentences with different next
// actions — a revoked App installation must be re-installed by the customer, a degraded CI credential
// must be rotated — and reporting the milder one would send an operator to rotate a token for an
// installation that no longer exists.
func (s *PGStore) Capability(parent context.Context, tenantID string) (fd.RouteConditionKind, string, error) {
	if strings.TrimSpace(tenantID) == "" {
		return "", "", ErrUnscoped
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()

	var kind, detail string
	err := s.db.QueryRowContext(ctx,
		`SELECT capability_kind, capability_detail FROM delivery_route
		  WHERE tenant_id = $1 AND capability_kind <> ''
		  ORDER BY CASE capability_kind WHEN 'revoked' THEN 0 ELSE 1 END, created_at ASC, target ASC
		  LIMIT 1`, tenantID).Scan(&kind, &detail)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Intact. Reported as the empty kind, which is what Service.RouteConditionFor reads to mean
		// "capability is fine, now ask the other question" — never as an error.
		return "", "", nil
	case err != nil:
		return "", "", fmt.Errorf("deliveryroute: capability for %s: %w", tenantID, err)
	}
	return conditionKind(kind), detail, nil
}

// SetCapability records that delivery capability was lost for one route: a CI credential that expired or
// rotated (degraded), or a hosted App installation the customer removed (revoked).
//
// detail is REQUIRED and the database says so too. "Delivery is degraded" with no detail is a banner
// nobody can act on, and this surface exists to state a next action rather than a mood.
func (s *PGStore) SetCapability(parent context.Context, tenantID, target string, kind fd.RouteConditionKind, detail string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrUnscoped
	}
	col, err := capabilityColumn(kind)
	if err != nil {
		return err
	}
	if col == capIntact {
		return fmt.Errorf("deliveryroute: use ClearCapability to record that capability was restored")
	}
	if strings.TrimSpace(detail) == "" {
		return fmt.Errorf("%w: %s on %s", ErrUnexplained, kind, target)
	}

	ctx, cancel := s.ctx(parent)
	defer cancel()

	res, err := s.db.ExecContext(ctx,
		`UPDATE delivery_route SET capability_kind = $3, capability_detail = $4
		  WHERE tenant_id = $1 AND target = $2`, tenantID, target, col, detail)
	if err != nil {
		return fmt.Errorf("deliveryroute: set capability on %s/%s: %w", tenantID, target, err)
	}
	// A no-op UPDATE is reported, not shrugged off. This is called from a webhook or a credential-check
	// job, and "we recorded that the token is dead" silently matching zero rows is how a tenant keeps
	// being offered deliveries through a credential that cannot perform them.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("deliveryroute: no route %s for tenant %s to mark %s", target, tenantID, kind)
	}
	return nil
}

// ClearCapability records that capability was restored — the deliberate counterpart to SetCapability,
// and the only way back to intact. Put does not clear it (see Put).
func (s *PGStore) ClearCapability(parent context.Context, tenantID, target string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrUnscoped
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()

	if _, err := s.db.ExecContext(ctx,
		`UPDATE delivery_route SET capability_kind = '', capability_detail = ''
		  WHERE tenant_id = $1 AND target = $2`, tenantID, target); err != nil {
		return fmt.Errorf("deliveryroute: clear capability on %s/%s: %w", tenantID, target, err)
	}
	return nil
}

// List returns a tenant's configured routes, oldest first — the console's route list.
func (s *PGStore) List(parent context.Context, tenantID string) ([]fd.Route, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrUnscoped
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+routeColumns+` FROM delivery_route WHERE tenant_id = $1
		  ORDER BY created_at ASC, target ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("deliveryroute: list for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []fd.Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("deliveryroute: list for %s: %w", tenantID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deliveryroute: list for %s: %w", tenantID, err)
	}
	return out, nil
}

type scanner interface{ Scan(...any) error }

// scanRoute rebuilds a Route from the four columns that carry it. It is the read half of the round-trip
// the package doc names, and the reason parseTarget is a function rather than three lines inline: the
// test that fences 0026's mistake calls exactly this path.
func scanRoute(sc scanner) (fd.Route, error) {
	var target, base, forge, mode string
	if err := sc.Scan(&target, &base, &forge, &mode); err != nil {
		return fd.Route{}, err
	}
	t, err := parseTarget(target, base)
	if err != nil {
		return fd.Route{}, err
	}
	return fd.Route{Mode: fd.Mode(mode), Target: t, ForgeKind: fd.ForgeKind(forge)}, nil
}

// parseTarget inverts Target.Key(): "owner/repo" or "owner/repo#workflow", plus the base branch that
// Key() deliberately omits and the base_ref column carries.
func parseTarget(key, base string) (fd.Target, error) {
	repoPart, workflow := key, ""
	if i := strings.Index(key, "#"); i >= 0 {
		repoPart, workflow = key[:i], key[i+1:]
	}
	owner, repo, ok := strings.Cut(repoPart, "/")
	if !ok || owner == "" || repo == "" {
		// The `target <> ''` CHECK cannot express "looks like owner/repo", so this is where a row written
		// by something other than Put is caught. Returned as an error rather than a zero Target: a zero
		// Target reaches Prepare and is refused there as "no base branch", which names the wrong problem.
		return fd.Target{}, fmt.Errorf("deliveryroute: %q is not a target key (owner/repo[#workflow])", key)
	}
	return fd.Target{Owner: owner, Repo: repo, Base: base, Workflow: workflow}, nil
}

// knownForge accepts the three forges the column's CHECK accepts — which is a wider set than
// ForgeKind.Supported(). See Put for why the two differ on purpose.
func knownForge(k fd.ForgeKind) bool {
	return k == fd.ForgeGitHub || k == fd.ForgeGitLab || k == fd.ForgeBitbucket
}

// capabilityColumn maps a condition kind to its column value. RouteConfigured and RouteAbsent are
// deliberately refused: neither is a stored fact. "Configured" is the absence of a lost capability, and
// "no route" is the absence of a ROW — writing either into capability_kind would be recording a derived
// state next to the facts it is derived from, where the two can then disagree.
func capabilityColumn(k fd.RouteConditionKind) (string, error) {
	switch k {
	case fd.RouteDegraded:
		return capDegraded, nil
	case fd.RouteRevoked:
		return capRevoked, nil
	case "":
		return capIntact, nil
	default:
		return "", fmt.Errorf("deliveryroute: %q is not a storable capability condition (degraded, revoked)", k)
	}
}

// conditionKind maps the column back. An unrecognised value is returned as-is rather than defaulted to
// intact: the CHECK makes it unreachable, and if it ever is reached, surfacing an odd condition is
// safer than reporting that delivery is fine.
func conditionKind(col string) fd.RouteConditionKind {
	switch col {
	case capDegraded:
		return fd.RouteDegraded
	case capRevoked:
		return fd.RouteRevoked
	default:
		return fd.RouteConditionKind(col)
	}
}
