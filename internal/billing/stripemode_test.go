package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/stripefake"
)

// stripemode_test.go is P21 section 3: the secret posture and the test/live separation.
//
// The property under test is the one that has no second chance. A live key resolving on a test surface
// is not a bug that fails a build — it is a real charge against a real card, discovered by the customer.
// So the separation is asserted at the layer that cannot be bypassed: the credential resolution path
// every single Stripe call goes through.

// TestStripeCredentialsComeFromTheSeamUnderTheP7ReservedNames is task 3.1.
func TestStripeCredentialsComeFromTheSeamUnderTheP7ReservedNames(t *testing.T) {
	const apiKey = "sk_test_p21_seam_placeholder_not_a_secret"
	rec := &recordingSecrets{inner: providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: apiKey},
		SecretBillingWebhookSigning: {APIKey: "webhook-signing-placeholder-not-a-secret"},
	}}
	secrets, err := NewManagedSecrets(rec)
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	f := newFakeStripe(t)
	p, err := NewStripeProvider(secrets, ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := p.EnsureCustomer(context.Background(), "cus_acme"); err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}

	// The credential was asked for BY THE RESERVED NAME, from the shared source — not read from an env
	// var, a config file, or a field on the provider.
	asked := strings.Join(rec.asked, ",")
	if !strings.Contains(asked, SecretBillingAPIKey) {
		t.Errorf("the Stripe key was not requested from the seam under %q (asked for: %q)", SecretBillingAPIKey, asked)
	}

	// It reached the wire as the bearer token, and nothing else did.
	var sawKey bool
	for _, h := range f.AuthHeaders() {
		if h == "Bearer "+apiKey {
			sawKey = true
		}
	}
	if !sawKey {
		t.Error("the key from the seam never reached Stripe's Authorization header")
	}

	// 🔴 The provider names itself for /readyz without naming what is behind it.
	if d := p.Describe(); strings.Contains(d, apiKey) {
		t.Errorf("Describe leaked the credential: %q", d)
	}
}

// TestStripeFailsClosedWithNoCredential is task 3.1's fail-closed half.
//
// With no key there is no fallback path: no unauthenticated call, no anonymous mode, no "continue and
// find out". A billing integration that degrades to unauthenticated is one that reports success for
// money that never moved.
func TestStripeFailsClosedWithNoCredential(t *testing.T) {
	f := newFakeStripe(t)
	secrets, err := NewManagedSecrets(providergateway.StaticSecrets{})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	p, err := NewStripeProvider(secrets, ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	_, err = p.EnsureCustomer(context.Background(), "cus_acme")
	if !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("a missing billing credential must fail closed, got %v", err)
	}
	if n := f.Calls("POST /v1/customers"); n != 0 {
		t.Errorf("stripe was called %d times with no credential — the refusal must happen BEFORE the call", n)
	}

	// A provider constructed with no secrets source at all is refused at construction, not discovered at
	// the first charge.
	if _, err := NewStripeProvider(nil, ModeTest, stripeClock); err == nil {
		t.Error("a provider with no secrets source was accepted — that failure must be loud at construction")
	}
}

// TestStripeModeZeroValueIsTestAndFollowsTheRolloutFlag is task 3.2's first half.
func TestStripeModeZeroValueIsTestAndFollowsTheRolloutFlag(t *testing.T) {
	secrets := stripeSecrets(t, stripefake.TestKey)

	// The ZERO value: a deployment that configured nothing charges nothing real.
	zero, err := NewStripeProvider(secrets, "", stripeClock)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if zero.Mode() != ModeTest {
		t.Errorf("the zero-value mode is %q, want test — a deployment that forgets to configure the mode must move no real money", zero.Mode())
	}

	// A nil rollout is the fully dark zero value too.
	dark, err := NewStripeProviderForRollout(secrets, nil, stripeClock)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if dark.Mode() != ModeTest {
		t.Errorf("with no rollout wired the mode is %q, want test", dark.Mode())
	}

	// A fresh rollout is dark: billing off, provider test mode.
	r := NewRollout()
	fromRollout, err := NewStripeProviderForRollout(secrets, r, stripeClock)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if fromRollout.Mode() != ModeTest {
		t.Errorf("a fresh rollout produced mode %q, want test", fromRollout.Mode())
	}

	// Live is reachable only by explicit configuration, and the provider follows the flag rather than
	// deriving the mode a second time.
	if err := r.Enable(ModeLive); err != nil {
		t.Fatalf("enable live: %v", err)
	}
	live, err := NewStripeProviderForRollout(stripeSecrets(t, stripefake.LiveKey), r, stripeClock)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if live.Mode() != ModeLive {
		t.Errorf("mode = %q, want live — the provider must follow the rollout flag, not a second opinion", live.Mode())
	}
	if got := live.Describe(); got != "stripe(live)" {
		t.Errorf("Describe = %q — /readyz must be able to tell a live box from a test one", got)
	}

	// An unknown mode is refused rather than defaulted. A default here would be a guess about money.
	if _, err := NewStripeProvider(secrets, Mode("sandbox"), stripeClock); err == nil {
		t.Error("an unknown billing mode was accepted")
	}
}

// TestALiveKeyDoesNotResolveForATestSurface is task 3.2's load-bearing half.
//
// Both directions are asserted, because both are real incidents with different shapes: a live key on a
// test surface moves real money during a test run, and a test key on a live surface silently moves none
// while every log line says success.
func TestALiveKeyDoesNotResolveForATestSurface(t *testing.T) {
	f := newFakeStripe(t)
	ctx := context.Background()

	// LIVE key, TEST surface → refused before any call.
	testSurface, err := NewStripeProvider(stripeSecrets(t, stripefake.LiveKey), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	_, err = testSurface.EnsureCustomer(ctx, "cus_acme")
	if !errors.Is(err, ErrStripeKeyMode) {
		t.Fatalf("a LIVE key on a test surface must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "LIVE") {
		t.Errorf("the refusal does not name what went wrong: %q", err)
	}
	if n := f.Calls("POST /v1/customers") + f.Calls("GET /v1/customers/search"); n != 0 {
		t.Errorf("stripe was contacted %d times with a mismatched key — the refusal must precede the call, "+
			"because a call is the thing that moves money", n)
	}

	// TEST key, LIVE surface → refused too. Silent no-op billing is not a safe failure.
	liveSurface, err := NewStripeProvider(stripeSecrets(t, stripefake.TestKey), ModeLive, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := liveSurface.EnsureCustomer(ctx, "cus_acme"); !errors.Is(err, ErrStripeKeyMode) {
		t.Errorf("a TEST key on a live surface must be refused, got %v", err)
	}

	// An unrecognizable key is refused rather than assumed to be a test key. Assuming is how a live key
	// ends up on a test surface.
	unknown, err := NewStripeProvider(stripeSecrets(t, "some-opaque-value"), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := unknown.EnsureCustomer(ctx, "cus_acme"); !errors.Is(err, ErrStripeKeyMode) {
		t.Errorf("a key whose mode cannot be established must be refused, got %v", err)
	}

	// And the matched pair works, so the guard is a guard rather than a wall.
	ok, err := NewStripeProvider(stripeSecrets(t, stripefake.TestKey), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := ok.EnsureCustomer(ctx, "cus_acme"); err != nil {
		t.Errorf("a matched test key on a test surface must work: %v", err)
	}
}

// TestATestModeProviderMovesNoRealMoney is task 3.2's third clause, asserted against a Stripe that
// behaves like a LIVE account: it refuses the test key, so the test-mode provider provably cannot reach
// live data even if it were pointed at it.
func TestATestModeProviderMovesNoRealMoney(t *testing.T) {
	f := newFakeStripe(t)
	f.SetRequireLiveKey(true) // this fake is now a LIVE Stripe account
	ctx := context.Background()

	p, err := NewStripeProvider(stripeSecrets(t, stripefake.TestKey), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	_, err = p.EnsureCustomer(ctx, "cus_acme")
	if err == nil {
		t.Fatal("a test-mode provider reached a live Stripe account")
	}
	// Stripe's own refusal is a REJECTION, not an outage: retrying it would never succeed, and a retry
	// loop against a live account is the last thing anyone wants.
	if !errors.Is(err, ErrProviderRejected) {
		t.Errorf("a live account's refusal of a test key must be a rejection, got %v", err)
	}
}
