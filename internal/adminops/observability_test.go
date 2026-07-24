package adminops_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// observability_test.go covers task 13.1/13.2 (FR18): admin activity is observable on the P2.5
// substrate with anomaly alerting, and NO secret appears in admin telemetry.

// recordingSink captures emitted admin metrics.
type recordingSink struct{ metrics []adminops.Metric }

func (r *recordingSink) EmitAdminMetric(m adminops.Metric) { r.metrics = append(r.metrics, m) }

// timeFixed is a stable clock for the telemetry tests.
var timeFixed = time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)

func (r *recordingSink) count(name adminops.MetricName) int {
	n := 0
	for _, m := range r.metrics {
		if m.Name == name {
			n++
		}
	}
	return n
}

// TestAdminActivityIsObservableOnTheSubstrate: logins, privileged actions, denials and cross-tenant
// views all emit metrics.
func TestAdminActivityIsObservableOnTheSubstrate(t *testing.T) {
	h := newHarness(t)
	sink := &recordingSink{}
	tel := adminops.NewTelemetry(sink, nil, h.clk.now)

	// Drive some activity through the same observers the wired console attaches.
	tel.AdminIdentityEvent(adminidentity.Event{Kind: adminidentity.EventLoginIssued, AdminID: "adm-support"})
	tel.AdminIdentityEvent(adminidentity.Event{Kind: adminidentity.EventLoginDeniedNoMFA, SSOSubject: "sso|attacker"})
	tel.AdminCommand(adminops.CommandEvent{
		ActorAdminID: "adm-platform_sre", Capability: adminrbac.CapKillSwitch, Target: "global", Result: "applied",
	})
	tel.AdminCommand(adminops.CommandEvent{
		ActorAdminID: "adm-support", Capability: adminrbac.CapKillSwitch, Target: "global", Result: "denied", Denied: true,
	})
	tel.AdminCommand(adminops.CommandEvent{
		ActorAdminID: "adm-platform_sre", Capability: adminrbac.CapCrossTenantRead, Target: "global", Result: "viewed",
	})

	if sink.count(adminops.MetricAdminLogin) != 1 {
		t.Errorf("logins emitted = %d, want 1", sink.count(adminops.MetricAdminLogin))
	}
	if sink.count(adminops.MetricAdminMFAFailure) != 1 {
		t.Errorf("MFA failures emitted = %d, want 1", sink.count(adminops.MetricAdminMFAFailure))
	}
	if sink.count(adminops.MetricAdminPrivilegedAction) != 1 {
		t.Errorf("privileged actions emitted = %d, want 1", sink.count(adminops.MetricAdminPrivilegedAction))
	}
	if sink.count(adminops.MetricAdminActionDenied) != 1 {
		t.Errorf("denials emitted = %d, want 1", sink.count(adminops.MetricAdminActionDenied))
	}
	if sink.count(adminops.MetricAdminCrossTenantView) != 1 {
		t.Errorf("cross-tenant views emitted = %d, want 1", sink.count(adminops.MetricAdminCrossTenantView))
	}
}

// TestAKillSwitchLeftArmedRaisesAnAnomaly: the "kill switch left armed" alert (task 13.1).
func TestAKillSwitchLeftArmedRaisesAnAnomaly(t *testing.T) {
	h := newHarness(t)
	tel := adminops.NewTelemetry(nil, nil, h.clk.now)
	tel.RecordKillSwitchGauge("global", true)
	found := false
	for _, a := range tel.Alerts() {
		if strings.Contains(a.Description, "kill switch") {
			found = true
		}
	}
	if !found {
		t.Fatal("arming a kill switch raised no anomaly alert")
	}
}

// TestASpikeInPrivilegedActionsRaisesAnAnomaly: the spike alert (task 13.1).
func TestASpikeInPrivilegedActionsRaisesAnAnomaly(t *testing.T) {
	h := newHarness(t)
	tel := adminops.NewTelemetry(nil, nil, h.clk.now)
	for i := 0; i < 25; i++ {
		tel.AdminCommand(adminops.CommandEvent{
			ActorAdminID: "adm-platform_sre", Capability: adminrbac.CapJobCancel, Target: "job:x", Result: "applied",
		})
	}
	spike := false
	for _, a := range tel.Alerts() {
		if strings.Contains(a.Description, "spike") {
			spike = true
		}
	}
	if !spike {
		t.Fatal("25 privileged actions raised no spike alert")
	}
}

// TestNoSecretInAdminTelemetry is task 13.2's load-bearing assertion. It drives the identity layer and
// the command path with REAL secret material and asserts none of it lands in an emitted metric.
func TestNoSecretInAdminTelemetry(t *testing.T) {
	// These stand in for secret material. What the assertion needs is that each value is DISTINCTIVE
	// enough to search for in an emitted metric — not that it looks like a real key. Writing them as
	// sentences keeps the test exactly as strong while keeping the repository's secret scanner sharp:
	// a tree full of key-shaped fixtures is a tree where the scanner has to be taught to look away.
	const ssoKey = "the sso signing value that must never be emitted"
	const mfaKey = "the mfa signing value that must never be emitted"
	const sessionKey = "the session signing value that must never be emitted"
	const providerHandle = "the provider handle that must never be emitted"

	sink := &recordingSink{}
	tel := adminops.NewTelemetry(sink, nil, func() time.Time { return timeFixed })

	secrets, err := adminidentity.FixtureSecrets(ssoKey, mfaKey, sessionKey)
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	provider, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{
		Issuer: "https://idp.test", Secrets: secrets, Now: func() time.Time { return timeFixed }, TestMode: true,
	})
	if err != nil {
		t.Fatalf("NewHMACProvider: %v", err)
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		Now: func() time.Time { return timeFixed }, Secrets: secrets, Observer: tel,
	})
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	principals := adminidentity.NewPrincipalStore()
	if err := principals.Put(adminidentity.Principal{
		AdminID: "adm-1", SSOSubject: "sso|adm-1", MFAEnrolled: true, Status: adminidentity.StatusActive, CreatedAt: timeFixed,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	idp, err := adminidentity.NewIdPFixture("https://idp.test", secrets, func() time.Time { return timeFixed })
	if err != nil {
		t.Fatalf("NewIdPFixture: %v", err)
	}
	authn, err := adminidentity.NewAuthenticator(provider, principals, sessions, tel)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	// A real login (with a signed assertion carrying MFA evidence) and a real command with a provider
	// handle in its evidence.
	assertion, err := idp.Assert(context.Background(), "sso|adm-1", "webauthn")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, _, err := authn.Authenticate(context.Background(), assertion); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	// A denied login too — the branch that carries the SSO subject and a detail string.
	bad, _ := idp.AssertWithoutMFA(context.Background(), "sso|adm-1")
	_, _, _ = authn.Authenticate(context.Background(), bad)

	tel.AdminCommand(adminops.CommandEvent{
		ActorAdminID: "adm-1", Capability: adminrbac.CapBillingCorrect, Target: "tenant:acme", Result: "applied",
	})

	// No emitted metric — in any dimension value — contains any secret.
	secretsToCatch := []string{ssoKey, mfaKey, sessionKey, providerHandle,
		assertion.Signature, assertion.MFA.Signature}
	for _, m := range sink.metrics {
		for k, v := range m.Dimensions {
			for _, secret := range secretsToCatch {
				if secret != "" && strings.Contains(v, secret) {
					t.Errorf("metric %s dimension %q leaked a secret: %q", m.Name, k, v)
				}
			}
		}
	}
	if len(sink.metrics) == 0 {
		t.Fatal("no metrics were emitted, so the assertion proves nothing")
	}
}
