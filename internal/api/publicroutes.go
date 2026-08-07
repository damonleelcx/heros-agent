package api

// publicroutes.go is the DECLARED exposure of every route this package registers.
//
// # The failure this exists to prevent, which has already happened twice
//
// The production ingress routes a hand-maintained list of paths to `agentd`. It carried three. P27 added
// two device-authorization endpoints, the CLI's login 404'd against production even though the code was
// deployed and correct, and the fix was applied to the running cluster BY HAND — so the next
// `kubectl apply` of the checked-in manifest deletes it and breaks `heros login` again. Nothing in the
// repository knew, because a route's reachability lived in a YAML file and its existence lived in Go, and
// no test compared them.
//
// 🔴 The shape of that bug is the important part: everything passes. The handler is registered, the tests
// are green, the deployment is healthy, and the only symptom is a 404 that reads to a customer as "wrong
// URL" and to us as "works on my machine". A code review cannot catch it, because the two halves are in
// different languages in different directories.
//
// # What is classified here, and what deliberately is not
//
// The two CREDENTIAL-FREE families — `/api/v1/device/*` and `/api/v1/auth/*` — plus the three routes that
// were already public. Those are the routes where "should this be on the internet?" is a real question
// with a wrong answer, and `TestEveryUnauthenticatedFamilyRouteIsClassified` makes adding one to either
// family a decision somebody makes rather than a default they inherit.
//
// The ~70 console-only routes are NOT listed. Every one of them has the same answer, a list nobody reads
// is a list nobody maintains, and the fence that actually catches the failure does not need them: it
// derives the set of externally-called paths from `internal/runlink/transport`'s source, which is the only
// code in the product that speaks to the platform from outside the cluster. See ingress_fence_test.go.
//
// # What `Public` means, precisely
//
// Reachable from the internet on the CUSTOMER hostname, by something that is not the console. In practice
// that is the CLI, which transmits to a compiled-in constant, and the payment provider's webhook.
//
// It is NOT the same as "unauthenticated". `POST /api/v1/auth/password/reset` takes no credential and is
// `Internal`, because only the console's server side calls it — the browser posts to the console's own
// origin and the BFF forwards inside the cluster. Publishing it would add an internet-facing surface for
// no gain, which is the review this classification forces somebody to have.
type RouteExposure map[string]Exposure

// Exposure is where a route can be reached from.
type Exposure string

const (
	// ExposurePublic is reachable from the internet on the customer hostname, and therefore REQUIRES an
	// entry in the checked-in ingress manifest.
	ExposurePublic Exposure = "public"
	// ExposureInternal is reached only from inside the cluster — by the console's server side, or by
	// another component. It must NOT have an ingress entry.
	ExposureInternal Exposure = "internal"
)

// PublicRoutes are the routes that must appear in the production ingress.
//
// 🔴 Each is `Exact` in the manifest, never `Prefix`, and never a blanket `/api/v1/*` rule. The rest of
// port 4321 is a console-only surface reached with the BFF's own credential; these are a bearer-token and
// unauthenticated surface built for machines. The NetworkPolicy cannot help — it matches L3/L4, so
// admitting the proxy to 4321 admits it to all of 4321. This list is the entire restriction.
func PublicRoutes() []string {
	out := make([]string, 0, 8)
	for route, exposure := range routeExposure {
		if exposure == ExposurePublic {
			out = append(out, route)
		}
	}
	return out
}

// routeExposure classifies every route `internal/api` registers on the platform mux.
//
// Keyed by PATH, not by "METHOD PATH", because an ingress rule matches a path and knows nothing about
// methods — so two methods on one path are one ingress decision, and keying by method would let somebody
// publish `GET /x` while believing they had not published `POST /x`.
var routeExposure = RouteExposure{
	// ── the CLI's compiled-in surface (internal/runlink/allowlist.go) ─────────────────────────────
	// `heros login --token` validates against whoami; `heros link` posts a run.
	"/api/v1/whoami":    ExposurePublic,
	"/api/v1/run-links": ExposurePublic,

	// ── the payment provider's callback (internal/api/p21.go) ─────────────────────────────────────
	// The only route that accepts UNSOLICITED internet traffic. Safe to publish for exactly one reason:
	// the handler verifies the provider's signature over the RAW BODY before any side effect.
	"/billing/webhook": ExposurePublic,

	// ── device authorization (P27 §13) ────────────────────────────────────────────────────────────
	// 🔴 These are the two that were patched into the running cluster by hand. The CLI has no credential
	// when it calls them — that is the point of the flow — so they cannot be reached through the console.
	"/api/v1/device/authorize": ExposurePublic,
	"/api/v1/device/token":     ExposurePublic,
	// The approval is made by a signed-in person FROM the console, which calls it inside the cluster.
	// It correctly 404s publicly: it is the step that mints a credential naming a person.
	"/api/v1/device/approve": ExposureInternal,

	// ── the machine-addressed structure surface (P29 §1) ──────────────────────────────────────────
	// 🔴 These are the routes the edge fence was BLIND to, and the blindness was structural rather than an
	// omission: they were addressed as `/api/v1/workflows/{id}/ir` and `/api/v1/proposals/{id}/verdict`,
	// so an `Exact` ingress rule could not match them, and the fence skipped anything it could not publish
	// Exact. Three commands 404'd at the edge behind a green build.
	//
	// The remedy is the flat shape, not a wider fence. A `Prefix` rule under `/api/v1/workflows/` would
	// publish `commit`, `orderings`, `orderings/stream`, `validate`, `proposals/generate`,
	// `proposals/{id}/open-pr`, `pattern-graph`, `eval-board` and `nodes` beside the two wanted routes —
	// see TestAPrefixRuleWouldPublishItsSiblings, which names every one of them.
	"/api/v1/workflow-ir":               ExposurePublic,
	"/api/v1/workflow-source":           ExposurePublic,
	"/api/v1/workflow-source-discovery": ExposurePublic,
	"/api/v1/proposal-verdicts":         ExposurePublic,
	// P29 §2.9 · the transform receipt. It has no parameterised predecessor — it is new, and it was born
	// flat, which is the pattern this family now establishes for every machine route.
	"/api/v1/transform-receipts": ExposurePublic,

	// The parameterised originals. EXPAND-CONTRACT: they stay registered for one release so a CLI built
	// before this change still works from INSIDE a cluster, and they are never published — which is the
	// status quo, stated rather than inherited. They are removed when the CLI floor moves.
	"/api/v1/workflows/{workflow_id}/ir":                                ExposureInternal,
	"/api/v1/workflows/{workflow_id}/source/{source_revision}":          ExposureInternal,
	"/api/v1/workflows/{workflow_id}/source/{source_revision}/discover": ExposureInternal,
	"/api/v1/proposals/{proposal_id}/verdict":                           ExposureInternal,

	// ── password identity (P28) ───────────────────────────────────────────────────────────────────
	// 🔴 Sign-in is the ONE password route that must be public, because `heros login` calls it with no
	// credential from a customer's laptop. Everything else on this surface is reached by the console's
	// server side: the browser posts to the console's own origin and the BFF forwards inside the cluster.
	"/api/v1/auth/password/signin": ExposurePublic,
	"/api/v1/auth/password/signup": ExposureInternal,
	"/api/v1/auth/password/forgot": ExposureInternal,
	"/api/v1/auth/password/reset":  ExposureInternal,
	"/api/v1/auth/password/verify": ExposureInternal,
	"/api/v1/auth/password/resend": ExposureInternal,
	"/api/v1/auth/password/change": ExposureInternal,
}
