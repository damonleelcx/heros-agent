package api

import (
	"strings"
	"testing"
)

// P30 tasks 1.8–1.10 — the generation action is addressed by a FLAT path, that path is published, and
// removing the Ingress rule turns this file red.
//
// The edge-reach fence in ingress_fence_test.go derives its path set from `internal/runlink/transport`,
// which is the only code that speaks to the platform from outside the cluster. This route has no
// transport caller yet — the console's BFF reaches it in-cluster — so the derived fence cannot see it,
// and "cannot see it" is precisely how the P27 device routes came to be patched into a running cluster
// by hand. These assertions are the extension: they cover the route by name, on both substrates, and
// they are wired to the same manifests the derived fence reads.

// generationPath is the one path this file is about. Written once so a rename fails here loudly rather
// than silently narrowing every assertion below to a route that no longer exists.
const generationPath = "/api/v1/proposal-generations"

// The route registers under the flat path AND — for one release, expand-contract — under the
// parameterised one a console built before this change addresses.
func TestGenerationRegistersBothShapes(t *testing.T) {
	registered := registeredRoutes(t)
	if !registered[generationPath] {
		t.Errorf("%s is not registered. It is the shape that can be published Exact; without it the "+
			"only way to reach generation from the edge is a Prefix rule under /api/v1/workflows/.",
			generationPath)
	}
	const old = "/api/v1/workflows/{workflow_id}/proposals/generate"
	if !registered[old] {
		t.Errorf("%s is no longer registered. Expand-contract: it serves a console built before the flat "+
			"path existed, and it is removed when that floor moves — not as a cleanup.", old)
	}
}

// 🔴 THE FENCE, and the one that must go red when the Ingress rule is deleted.
//
// Proved red by removing the `- path: /api/v1/proposal-generations` block from
// deploy/k8s/overlays/prod/ingress.yaml: this test then reports the route as unroutable, and
// TestNothingIsPublishedThatIsNotDeclaredPublic stays green — which is the point. Nothing else in the
// tree notices a missing ingress rule. The deployment is healthy, the handler is registered, the build
// is green, and the only symptom is a 404 at the edge.
func TestTheGenerationPathIsPublishedExactOnEverySubstrate(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}
	if !declared[generationPath] {
		t.Fatalf("publicroutes.go does not declare %s public, so no substrate is required to publish it "+
			"and every assertion below is vacuous", generationPath)
	}

	pathType, routed := ingressPaths(t)[generationPath]
	switch {
	case !routed:
		t.Errorf("deploy/k8s/overlays/prod/ingress.yaml does not route %s to agentd.\n"+
			"  It answers 404 in production the moment that manifest is applied — a green build, a healthy "+
			"deployment, and an action nobody can trigger. Add:\n"+
			"          - path: %s\n            pathType: Exact\n"+
			"            backend: { service: { name: agentd, port: { number: 4321 } } }",
			generationPath, generationPath)
	case pathType != "Exact":
		t.Errorf("%s is published with pathType %q. Exact, never Prefix: a Prefix rule publishes every "+
			"route beneath it, which is the whole reason this path is flat.", generationPath, pathType)
	}

	compose := setOf(composePlatformPaths(t))
	if !compose[generationPath] {
		t.Errorf("deploy/scripts/bootstrap-vm.sh does not publish %s.\n"+
			"  The product ships on two substrates and a route published on one of them is a route that "+
			"404s for every self-hosting customer.", generationPath)
	}
}

// 🚫 No Prefix rule under /api/v1/workflows/ was introduced to reach the OLD shape instead. That would
// have been the cheap alternative to flattening the route, and it publishes eight console-only siblings.
func TestNoPrefixRuleWasAddedUnderWorkflows(t *testing.T) {
	// The assertion reads the RULES, not the file's text: the manifest's comments name
	// `/api/v1/workflows/` at length to explain why nothing under it is published, and a substring check
	// over the whole file would fail on the explanation. See the standing rule that a fence must not
	// trip on prose.
	const manifest = "deploy/k8s/overlays/prod/ingress.yaml"
	for path, pathType := range ingressPaths(t) {
		if strings.HasPrefix(path, "/api/v1/workflows") {
			t.Errorf("%s routes %s (%s) to agentd. Every route under /api/v1/workflows/ carries a "+
				"caller-supplied identifier mid-path; publishing any of them needs a Prefix rule, which "+
				"publishes them all.", manifest, path, pathType)
		}
	}
}
