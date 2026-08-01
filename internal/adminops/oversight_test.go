package adminops_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// oversight_test.go covers P26 wave 26e.
//
// The property that matters most here is the one about ABSENCE. Three of these reads can be absent —
// the legal record, the readiness surface, the deployed version — and each absence has to be reported
// as itself rather than as an empty table, a `0`, or an `absent` state. "Nothing is configured" and
// "we did not ask" are opposite claims that render identically if nobody insists otherwise.

type testReadiness struct{}

func (testReadiness) Describe() string { return "test readiness surface" }

func (testReadiness) Integrations() []adminops.IntegrationRow {
	return []adminops.IntegrationRow{
		{Name: "metrics", State: adminops.IntegrationConfigured, Source: "platform readiness surface"},
		{Name: "error-monitoring", State: adminops.IntegrationAbsent, Source: "platform readiness surface"},
		{Name: "trace-export", State: adminops.IntegrationDegraded,
			FailureClass: "configured, endpoint unreachable for 14 consecutive flushes",
			Source:       "platform readiness surface"},
	}
}

func oversightView(t *testing.T, h *harness, cfg adminops.OversightConfig) adminops.OversightView {
	t.Helper()
	svc, err := adminops.NewOversightService(h.exec, cfg)
	if err != nil {
		t.Fatalf("NewOversightService: %v", err)
	}
	view, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	return view
}

// TestAnOperatorSessionShowsTheFactorThatAuthenticatedIt defends P26 task 6.1.
//
// It runs against the REAL verifier — the harness's sessions were issued by
// `adminidentity.Authenticator` after a real assertion and a real MFA check — and the surface renders
// what that verifier recorded. It also asserts the surface does NOT represent the test-mode fixture as
// a production identity provider.
func TestAnOperatorSessionShowsTheFactorThatAuthenticatedIt(t *testing.T) {
	h := newHarness(t)
	view := oversightView(t, h, adminops.OversightConfig{
		Sessions: h.sessions, Identity: h.identityInfo(),
	})

	if len(view.Sessions) == 0 {
		t.Fatal("no operator sessions are shown, though the fixture signed four principals in")
	}
	for _, s := range view.Sessions {
		if s.Factor == "" {
			t.Fatalf("session %s shows no factor — a reviewer would be left inferring authentication "+
				"strength, which is what this surface exists to stop", s.SessionID)
		}
		if s.VerifiedAt == "" {
			t.Fatalf("session %s shows no verification time", s.SessionID)
		}
		if !s.MultiFactor {
			t.Fatalf("session %s is not distinguishable as multi-factor, though the fixture verified one",
				s.SessionID)
		}
	}
	// 🔴 The surface claims no real IdP. The verifier is real; the ISSUER is the fixture, and saying so
	// is the difference between a demo that is honest and one that is misleading.
	if !view.IdentityProvider.TestMode {
		t.Fatal("the fixture IdP is not reported as test mode")
	}
	if !strings.Contains(view.IdentityProvider.Note, "TEST-MODE") {
		t.Fatalf("the surface does not say the identity provider is the fixture: %q", view.IdentityProvider.Note)
	}
	if !strings.Contains(view.IdentityProvider.Note, "real one") {
		t.Fatalf("the surface does not say the VERIFIER is real, which is what makes the factor "+
			"meaningful: %q", view.IdentityProvider.Note)
	}

	// And no session row can carry a token or a factor value — the type has nowhere to put one.
	encoded, err := json.Marshal(view.Sessions)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, token := range h.tokens {
		if strings.Contains(string(encoded), token) {
			t.Fatal("a session bearer token reached the oversight read model")
		}
	}
}

// TestAnObservabilityIntegrationHasThreeStatesAndNamesItsFailure defends P26 task 6.3.
func TestAnObservabilityIntegrationHasThreeStatesAndNamesItsFailure(t *testing.T) {
	if got := len(adminops.IntegrationStates()); got != 3 {
		t.Fatalf("IntegrationStates() has %d values, want 3 — a boolean would have to call 'not "+
			"configured' a fault or call a fault 'configured'", got)
	}
	h := newHarness(t)
	view := oversightView(t, h, adminops.OversightConfig{Readiness: testReadiness{}})

	if !view.IntegrationsKnown {
		t.Fatal("integrations report as unknown with a readiness source wired")
	}
	byState := map[adminops.IntegrationState]adminops.IntegrationRow{}
	for _, n := range view.Integrations {
		byState[n.State] = n
		if n.Source == "" {
			t.Fatalf("integration %s does not name where its state was read", n.Name)
		}
		// 🔴 Never a third party's dashboard, which is the least available part of the system during an
		// incident — which is exactly when this is read.
		for _, forbidden := range []string{"dashboard", "status page", "console.", ".com"} {
			if strings.Contains(strings.ToLower(n.Source), forbidden) {
				t.Fatalf("integration %s reads its state from %q — reporting health must come from the "+
					"platform's own readiness surface", n.Name, n.Source)
			}
		}
	}
	for _, want := range adminops.IntegrationStates() {
		if _, ok := byState[want]; !ok {
			t.Fatalf("no integration is in state %q — the three states are not all reachable", want)
		}
	}
	if byState[adminops.IntegrationDegraded].FailureClass == "" {
		t.Fatal("a degraded integration names no failure class — 'unreachable' and 'rejecting our " +
			"schema' send an operator to two different places")
	}
	if byState[adminops.IntegrationAbsent].FailureClass != "" {
		t.Fatal("an absent integration names a failure class — nothing has failed; nothing is configured")
	}
}

// TestNoReadinessSourceIsNotReportedAsAbsent defends the distinction the three states exist for.
func TestNoReadinessSourceIsNotReportedAsAbsent(t *testing.T) {
	h := newHarness(t)
	view := oversightView(t, h, adminops.OversightConfig{})

	if view.IntegrationsKnown {
		t.Fatal("integrations report as known with no readiness source")
	}
	if len(view.Integrations) != 0 {
		t.Fatalf("with no readiness source the surface invented %d integration rows. 'Nothing is "+
			"configured' and 'we did not ask' are different answers, and rendering the second as the "+
			"first is a claim nobody made.", len(view.Integrations))
	}
}

// TestAnUnknownDeployedVersionIsStatedAndNamesTheMissingCollection defends P26 task 6.4.
func TestAnUnknownDeployedVersionIsStatedAndNamesTheMissingCollection(t *testing.T) {
	h := newHarness(t)
	view := oversightView(t, h, adminops.OversightConfig{
		Tenants: func() []string { return []string{tenantAcme, tenantBoreal} },
	})

	if len(view.Deployments) != 2 {
		t.Fatalf("the surface shows %d deployments, want 2", len(view.Deployments))
	}
	for _, d := range view.Deployments {
		if !d.Unknown {
			t.Fatalf("tenant %s reports a deployed version with no source — no version may be inferred "+
				"from an API contract version, a feature probe, or any other proxy", d.TenantID)
		}
		if d.Version != "" {
			t.Fatalf("tenant %s reports version %q while also reporting unknown", d.TenantID, d.Version)
		}
		if d.MissingCollection != adminops.MissingDeploymentHeartbeat {
			t.Fatalf("tenant %s's unknown names %q as the missing collection, want %q — an unnamed "+
				"missing input is a wish, not a task", d.TenantID, d.MissingCollection,
				adminops.MissingDeploymentHeartbeat)
		}
	}
}

// TestPaymentsAreStatedAsNotYetReadable defends P26 task 6.5.
//
// 🔴 The failure it prevents is a page that renders an empty payments table and reads as "no failed
// payments" — which is the most dangerous possible reading of a subsystem that does not exist.
func TestPaymentsAreStatedAsNotYetReadable(t *testing.T) {
	h := newHarness(t)
	view := oversightView(t, h, adminops.OversightConfig{})

	var payments *adminops.NotYetReadable
	for i := range view.NotYetReadable {
		if strings.Contains(view.NotYetReadable[i].Subject, "payment") {
			payments = &view.NotYetReadable[i]
		}
	}
	if payments == nil {
		t.Fatal("the payments gap is not stated at all")
	}
	if payments.Requires == "" {
		t.Fatal("the payments gap names no collection — an unnamed missing input is a wish")
	}
	if !strings.Contains(payments.Requires, "webhook") || !strings.Contains(payments.Requires, "dunning") {
		t.Fatalf("the payments gap does not specify the webhook and dunning surface: %q", payments.Requires)
	}
	if !strings.Contains(payments.Statement, "not shipped") {
		t.Fatalf("the statement does not say the subsystem has not shipped: %q", payments.Statement)
	}
	// It must not offer a zero, and it must say so.
	if !strings.Contains(payments.Statement, "nothing below is a zero") {
		t.Fatalf("the statement does not refuse a zero: %q", payments.Statement)
	}
	// And the deployed-version gap is stated in the same shape.
	var sawVersion bool
	for _, n := range view.NotYetReadable {
		if strings.Contains(n.Subject, "deployed version") {
			sawVersion = true
			if n.Requires != adminops.MissingDeploymentHeartbeat {
				t.Fatalf("the deployed-version gap names %q", n.Requires)
			}
		}
	}
	if !sawVersion {
		t.Fatal("the deployed-version gap is not stated")
	}
}

// TestTheOversightSurfaceIsReadOnly defends the phase's boundary on this surface too.
func TestTheOversightSurfaceIsReadOnly(t *testing.T) {
	allowed := map[string]bool{"View": true}
	typ := reflect.TypeOf(&adminops.OversightService{})
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; !allowed[name] {
			t.Fatalf("OversightService exposes %q — this surface reads and does nothing", name)
		}
	}
	h := newHarness(t)
	if !oversightView(t, h, adminops.OversightConfig{}).ReadOnly {
		t.Fatal("the oversight read model does not declare itself read-only")
	}
}
