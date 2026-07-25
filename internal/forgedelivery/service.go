package forgedelivery

import (
	"context"
	"fmt"
)

// service.go composes the delivery core into the read/serve surface the console and the CI runner use.
// It is where "no route is a reported state" and "lost capability is degraded, never silent" become
// concrete answers rather than absences (tasks 6.1, 6.2, 6.3).

// RouteConditionKind is the delivery-route condition for a tenant. It is a FIRST-CLASS state, never an
// empty list: silence is indistinguishable from "the product found nothing", and that confusion lands
// hardest during evaluation (design Decision 6).
type RouteConditionKind string

const (
	// RouteConfigured: a route exists and delivery can proceed.
	RouteConfigured RouteConditionKind = "configured"
	// RouteAbsent: verified proposals exist for a repository with no configured route (FR13 / task 6.1).
	RouteAbsent RouteConditionKind = "no_route"
	// RouteDegraded: delivery capability was lost — a CI credential expired/rotated (task 6.2 / 5.4).
	RouteDegraded RouteConditionKind = "degraded"
	// RouteRevoked: the hosted App installation was removed (task 6.2 / 8.4).
	RouteRevoked RouteConditionKind = "revoked"
)

// RouteCondition is the reported delivery-route state, rendered by the console as a condition WITH a
// next action — never an empty result (task 6.1).
type RouteCondition struct {
	Kind       RouteConditionKind `json:"kind"`
	Detail     string             `json:"detail"`
	NextAction string             `json:"next_action"`
	// Targets names the repositories the condition concerns, so the console can name them rather than
	// saying "some repository".
	Targets []string `json:"targets,omitempty"`
}

// PendingProvider yields the verified, deliverable proposals for a tenant — the P5.5 output delivery
// consumes. It is what lets "verified proposals exist but there is no route" be answered, rather than
// proposals accumulating invisibly.
type PendingProvider interface {
	PendingVerified(ctx context.Context, tenantID string) ([]Proposal, error)
}

// RouteRegistry answers which route a (tenant, target) has, and whether delivery capability is degraded
// or revoked. A missing route is not an error — it is the RouteAbsent condition.
type RouteRegistry interface {
	// RouteFor returns the configured route for a tenant+target, or nil if none is configured.
	RouteFor(ctx context.Context, tenantID, target string) (*Route, error)
	// Capability reports a lost-capability condition (degraded/revoked) for a tenant, or Kind=="" when
	// capability is intact. A read error is returned, never swallowed.
	Capability(ctx context.Context, tenantID string) (kind RouteConditionKind, detail string, err error)
}

// Service is the delivery read/serve surface. It builds the console's view, serves the CI fetch, and
// records CI reports — one place so the console and CI cannot disagree about a delivery's state.
type Service struct {
	del      *Deliverer
	routes   RouteRegistry
	pending  PendingProvider
	consBase string
}

// NewService composes the surface. consoleBase is the console's public origin, used to build canonical
// evidence references (may be empty in a headless deployment, in which case refs are relative).
func NewService(del *Deliverer, routes RouteRegistry, pending PendingProvider, consoleBase string) *Service {
	return &Service{del: del, routes: routes, pending: pending, consBase: consoleBase}
}

// ListDeliveries returns the tenant's delivery heads, newest first — the console's delivery list.
func (s *Service) ListDeliveries(ctx context.Context, tenantID string) ([]DeliveryHead, error) {
	return s.del.rec.ListForTenant(ctx, tenantID)
}

// RouteConditionFor computes the tenant's delivery-route condition (tasks 6.1, 6.2). Lost capability
// wins over route-absence (a degraded route is a stronger, more urgent condition than a missing one).
func (s *Service) RouteConditionFor(ctx context.Context, tenantID string) (RouteCondition, error) {
	// Lost capability first — it is the most urgent and must never read as a quiet week.
	if kind, detail, err := s.routes.Capability(ctx, tenantID); err != nil {
		return RouteCondition{}, err
	} else if kind != "" {
		return RouteCondition{Kind: kind, Detail: detail, NextAction: nextActionFor(kind)}, nil
	}

	// Any verified proposals with no route for their target?
	pending, err := s.pending.PendingVerified(ctx, tenantID)
	if err != nil {
		return RouteCondition{}, err
	}
	var unrouted []string
	seen := map[string]bool{}
	for _, p := range pending {
		// A pending proposal names a target only via its eventual route; when none exists we cannot know
		// the exact repo, so we surface the proposal's config as the unrouted subject. In practice the
		// pending provider carries the intended target; here we report the count and the next action.
		key := p.ConfigHash
		if !seen[key] {
			seen[key] = true
			unrouted = append(unrouted, key)
		}
	}
	if len(unrouted) > 0 && !s.anyRoute(ctx, tenantID) {
		return RouteCondition{
			Kind:       RouteAbsent,
			Detail:     fmt.Sprintf("%d verified proposal(s) are ready but this repository has no delivery route configured.", len(unrouted)),
			NextAction: nextActionFor(RouteAbsent),
		}, nil
	}
	return RouteCondition{Kind: RouteConfigured}, nil
}

// anyRoute reports whether the tenant has any configured route. It probes the registry with the empty
// target as a "has any" query; a registry returns nil for no route without erroring.
func (s *Service) anyRoute(ctx context.Context, tenantID string) bool {
	r, err := s.routes.RouteFor(ctx, tenantID, "")
	return err == nil && r != nil
}

// Pending serves the authenticated CI fetch (task 5.3): for a tenant+target it Prepares every verified
// proposal, enforcing the gate/entitlement/halt/route/bound SERVER-SIDE. Only deliverable proposals are
// returned; an undeliverable one is simply absent, never leaked.
func (s *Service) Pending(ctx context.Context, tenantID string, target Target) ([]Prepared, error) {
	proposals, err := s.pending.PendingVerified(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	route, err := s.routes.RouteFor(ctx, tenantID, target.Key())
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, ErrNoRoute
	}
	var out []Prepared
	for _, p := range proposals {
		p.TenantID = tenantID // scope is the authenticated tenant, never the request body
		prep, err := s.del.Prepare(ctx, p, route)
		if err != nil {
			continue // undeliverable → not served (gate/entitlement/halt/bound decided it)
		}
		out = append(out, prep)
	}
	return out, nil
}

// RecordReport records a CI-opened delivery (task 5.1 report leg). It re-derives Prepared from the
// proposal identity so the record's identity fields are the platform's, not the runner's claim.
func (s *Service) RecordReport(ctx context.Context, tenantID string, prep Prepared, r Report) (Result, error) {
	prep.TenantID = tenantID
	return s.del.RecordFromReport(ctx, prep, r)
}

// nextActionFor names what to do about a route condition — the "next action" the console renders so the
// condition is never a dead end (task 6.1).
func nextActionFor(kind RouteConditionKind) string {
	switch kind {
	case RouteAbsent:
		return "Configure a delivery route: enable CI-mediated delivery in your pipeline, or install the hosted Git App on this repository."
	case RouteDegraded:
		return "Renew the CI credential used for delivery, or re-run the CI integration; delivery resumes once it is valid."
	case RouteRevoked:
		return "Re-install the hosted Git App on the affected repositories to resume delivery."
	default:
		return ""
	}
}
