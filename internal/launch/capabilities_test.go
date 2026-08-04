package launch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
)

// This file is the fence behind P19 FR27. What it protects is narrow and easy to lose: that the process
// the DEPLOYMENT runs registers every capability surface, so an uninstalled capability answers 503 and
// never 404.
//
// It is worth a fence because losing it is invisible. Before mountCapabilities existed, agentd served
// six routes and every console page fell through to 404 — the build was green, every unit test passed,
// and the only symptom was a console telling users their workflow did not exist.

// capabilityRoutes is one representative route per capability surface, with the method the handler
// registers. A 404 here means the surface was never registered on the deployed path.
//
// `authGated` marks a route whose auth middleware runs BEFORE the not-mounted check, so an anonymous
// request gets 401 rather than 503. That order is correct and deliberate — an unauthenticated caller
// learns nothing about which capabilities this deployment has — so the 503 assertion skips those two.
// The 404 assertion does not: an unregistered route answers 404 to everyone, authenticated or not, and
// that is the failure this file exists to catch.
var capabilityRoutes = []struct {
	method, path, capability string
	authGated                bool
}{
	{method: "GET", path: "/api/v1/runs/run-1", capability: "p2"},
	{method: "GET", path: "/api/v1/workflows/wf-1/eval-board", capability: "p4"},
	{method: "GET", path: "/api/v1/variants/v-1/scorecard", capability: "p4.5"},
	{method: "GET", path: "/api/v1/workflows/wf-1/ir", capability: "p5"},
	{method: "GET", path: "/api/v1/workflows/wf-1/proposals", capability: "p5.5"},
	{method: "GET", path: "/api/v1/runs/run-1/monitor", capability: "p6"},
	{method: "GET", path: "/api/v1/customers/c-1/billing", capability: "p7"},
	{method: "GET", path: "/api/v1/prompts", capability: "p10"},
	{method: "GET", path: "/api/v1/models", capability: "p10 matrix"},
	{method: "GET", path: "/api/v1/whoami", capability: "p11", authGated: true},
	{method: "GET", path: "/api/v1/deliveries", capability: "p12"},
	{method: "POST", path: "/api/v1/authoring/preflight", capability: "p13 authoring"},
	{method: "GET", path: "/api/v1/customers/c-1/payment", capability: "p21"},
	{method: "GET", path: "/api/v1/workflows/wf-1/pattern-graph", capability: "p3.5"},
	{method: "GET", path: "/api/v1/runs/run-1/monitor", capability: "p2.5 monitor"},
	{method: "GET", path: "/api/v1/legal/acceptances", capability: "p23", authGated: true},
}

// mountedServer builds the server the deployed path builds, with no platform database — the state a
// single-binary or open-core install is actually in.
func mountedServer(t *testing.T) *api.Server {
	t.Helper()
	srv := api.New(nil, config.Config{})
	if _, err := mountCapabilities(srv, nil, t.TempDir(), "", nil); err != nil {
		t.Fatalf("mountCapabilities: %v", err)
	}
	return srv
}

func TestEveryCapabilityRouteIsRegistered(t *testing.T) {
	srv := mountedServer(t)
	for _, rt := range capabilityRoutes {
		t.Run(rt.capability, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s returned 404: the %s surface is not registered on the deployed path.\n"+
					"A 404 tells a client the IDENTIFIER does not resolve; the truth is that the CAPABILITY is "+
					"not installed here. The console renders the first as \"no such workflow\" for a workflow "+
					"that plainly exists. Register it in mountCapabilities — with a nil source if it has no "+
					"durable store — so it answers 503 instead.", rt.method, rt.path, rt.capability)
			}
		})
	}
}

func TestUnsourcedCapabilitiesAnswerNotMounted(t *testing.T) {
	srv := mountedServer(t)
	// With no platform database, every one of these has no source, so each must say so with a 503 —
	// not a 200 over an empty in-memory store, which would be "installed and quietly lossy".
	for _, rt := range capabilityRoutes {
		if rt.authGated {
			continue // 401 first, by design — see the table's comment.
		}
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s (%s) returned %d with no source wired; want 503 not-mounted",
				rt.method, rt.path, rt.capability, rec.Code)
		}
	}
}

// The Stripe webhook is the ONE deliberate exception: the single inbound-from-internet path is mounted
// only where a deployment collects payments, so it stays unregistered rather than published to answer
// 503 on every install, including air-gapped ones. Asserted so the exception stays deliberate — if
// somebody later registers it by default, this fails and they have to mean it.
func TestBillingWebhookIsNotPublishedWithoutBilling(t *testing.T) {
	srv := mountedServer(t)
	req := httptest.NewRequest("POST", "/billing/webhook", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /billing/webhook returned %d on a deployment with no billing; want 404 (unregistered). "+
			"internal/api/p21.go states the path is mounted only where a deployment exposes it — publishing "+
			"an internet-facing route on every install to answer 503 is a different decision, and it needs to "+
			"be made on purpose.", rec.Code)
	}
}

// A capability added to internal/api but never wired into mountCapabilities is the exact regression this
// change fixed, and nothing else would catch it: the build stays green and the surface simply 404s.
func TestEveryMountFunctionIsCalledByTheDeployedPath(t *testing.T) {
	apiDir := filepath.Join("..", "api")
	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read %s: %v", apiDir, err)
	}
	// Mount* and Register* are the two names internal/api uses for "attach a capability surface".
	decl := regexp.MustCompile(`func \(s \*Server\) ((?:Mount|Register)[A-Za-z0-9]*)\(`)

	wiring, err := os.ReadFile("capabilities.go")
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}

	// MountBillingWebhook is the documented exception (see the test above).
	exempt := map[string]bool{"MountBillingWebhook": true}

	var found int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(apiDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(body), -1) {
			name := m[1]
			if exempt[name] {
				continue
			}
			found++
			if !strings.Contains(string(wiring), "."+name+"(") {
				t.Errorf("api.Server.%s (declared in internal/api/%s) is never called by "+
					"internal/launch/capabilities.go.\n"+
					"A capability the deployed process does not register answers 404 rather than 503, and "+
					"nothing else fails: the build is green and the surface just looks like a bad identifier. "+
					"Call it — with a nil source if it has no durable store yet.", name, e.Name())
			}
		}
	}
	if found == 0 {
		t.Fatal("found no Mount*/Register* declarations in internal/api — the scan did not match the " +
			"source's shape, so this fence was about to pass without checking anything")
	}
}
