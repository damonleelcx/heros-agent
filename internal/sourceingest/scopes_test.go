package sourceingest

import (
	"strings"
	"testing"
)

// scopes_test.go fences the read-only scope ALLOWLIST — the control that keeps a read connection from
// quietly being a write one.
//
// # What it replaced, and the defect that forced the inversion
//
// The check used to be a DENYLIST of write verbs (`write`, `push`, `admin`, …). **GitHub's classic
// `repo` scope grants full read AND write and contains no verb**, so it passed. It is not alone:
// `public_repo` grants write to public repositories, and GitLab's `api` grants complete read/write API
// access. All three are NOUNS that confer every verb, which is the exact shape a verb denylist cannot
// see — and adding them would have left the list missing the fourth.
//
// ADR-005 and ADR-013 each independently refuse one credential that both reads source and writes to a
// repository, and P35 `tasks.md` §6.3 depends on that being structurally impossible rather than merely
// unlikely. These are the tests that make it structural.

// TestEveryForgeDeclaresItsReadOnlyScopes is the anti-vacuity check, and it has two halves because a
// list can be useless in two different ways.
func TestEveryForgeDeclaresItsReadOnlyScopes(t *testing.T) {
	for _, forge := range Forges() {
		allowed, err := ReadOnlyScopesFor(forge)
		if err != nil {
			t.Fatalf("%s has no read-scope declaration: %v", forge, err)
		}
		// 🔴 EMPTY is the dangerous shape, not a permissive one: with no allowlist every scope is
		// refused, so a customer on that forge cannot connect at all while the fences below still pass.
		if len(allowed) == 0 {
			t.Fatalf("%s declares no read-only scopes. Every scope it reports would be refused, and a "+
				"forge nobody can connect to is a broken forge rather than a safe one", forge)
		}
		// … and each declared scope must actually be ADMITTED. A list whose own entries are refused is
		// the same outage with more code in front of it.
		kind, err := ExpectedGrantKind(forge)
		if err != nil {
			t.Fatal(err)
		}
		a := Authorization{
			Forge: forge, GrantKind: kind, Token: "t",
			Covers: []string{"acme/api"}, Scopes: allowed,
		}
		if err := a.Validate("acme/api"); err != nil {
			t.Fatalf("%s declares %v as its read scopes and Validate refuses them: %v", forge, allowed, err)
		}
	}
}

// TestTheDeclaredReadScopesAreWhatTheConsentScreenStates keeps the two halves of one fact together.
//
// 🔴 The drift it catches runs both ways and both are bad: a scope ENFORCED but not DISCLOSED means the
// consent screen understates what the platform will accept; a scope DISCLOSED but not ENFORCED means it
// overstates what the platform asks for. `permission` is prose read by a person and its exact wording
// is asserted by the console, so the comparison is made after normalisation rather than by deriving one
// from the other — which would push a formatting decision into a security control.
func TestTheDeclaredReadScopesAreWhatTheConsentScreenStates(t *testing.T) {
	normalise := func(s string) string {
		return strings.ReplaceAll(strings.ToLower(s), " ", "")
	}
	for _, d := range DescribeForges() {
		allowed, err := ReadOnlyScopesFor(d.Forge)
		if err != nil {
			t.Fatal(err)
		}
		prose := normalise(d.Permission)
		for _, scope := range allowed {
			if !strings.Contains(prose, normalise(scope)) {
				t.Errorf("%s enforces the scope %q and its consent screen says %q. A scope this platform "+
					"accepts and does not disclose is a permission the customer never agreed to",
					d.Forge, scope, d.Permission)
			}
		}
		// The reverse: every colon-bearing token the prose names must be enforced. Split on commas
		// because that is how the prose lists them.
		for _, part := range strings.Split(prose, ",") {
			if part == "" || !strings.Contains(part, ":") {
				continue
			}
			var found bool
			for _, scope := range allowed {
				if normalise(scope) == part {
					found = true
				}
			}
			if !found {
				t.Errorf("%s's consent screen says %q, which names %q, and Validate does not accept it. "+
					"The screen promises a narrower grant than the code takes", d.Forge, d.Permission, part)
			}
		}
	}
}

// TestAFullAccessScopeIsRefusedOnEveryForge is the regression this whole change exists for.
//
// 🔴 These three are the instances that defeated a verb denylist. They are asserted on EVERY forge
// rather than only on the one that spells them, because the failure being prevented is an
// authorization built by a confused adapter — and a `repo` scope arriving on a Bitbucket grant is more
// alarming, not less.
func TestAFullAccessScopeIsRefusedOnEveryForge(t *testing.T) {
	for _, forge := range Forges() {
		kind, err := ExpectedGrantKind(forge)
		if err != nil {
			t.Fatal(err)
		}
		for _, scope := range []string{"repo", "public_repo", "api"} {
			a := Authorization{
				Forge: forge, GrantKind: kind, Token: "t",
				Covers: []string{"acme/api"}, Scopes: []string{scope},
			}
			err := a.Validate("acme/api")
			if err == nil {
				t.Fatalf("%s admitted the scope %q as a READ connection. It grants write, so this "+
					"connection both reads source and can push — the one credential ADR-005 and ADR-013 "+
					"each independently refuse", forge, scope)
			}
			// The refusal must be actionable. A customer pasting a classic personal access token needs
			// to be told the scope grants write, not merely that it is unrecognised.
			if !strings.Contains(err.Error(), "write") {
				t.Errorf("%s refused %q without saying it can WRITE: %v", forge, scope, err)
			}
		}
	}
}

// TestTheExplainerDecidesNothing proves the surviving verb list is diagnostic rather than a second
// security control.
//
// 🔴 If the two lists could disagree in a way that admitted something, keeping both would be a defect.
// They cannot: the allowlist decides, so a write-capable scope the explainer has never heard of is
// still refused — with a less helpful sentence, which is the only thing a stale explainer can cost.
func TestTheExplainerDecidesNothing(t *testing.T) {
	// A plausible future spelling that carries no known verb and is in no table.
	const invented = "repository:mutate"
	if explainScope(invented) != "" {
		t.Fatalf("%q is recognised by the explainer; this test needs a scope it does NOT know", invented)
	}
	for _, forge := range Forges() {
		kind, _ := ExpectedGrantKind(forge)
		a := Authorization{
			Forge: forge, GrantKind: kind, Token: "t",
			Covers: []string{"acme/api"}, Scopes: []string{invented},
		}
		if err := a.Validate("acme/api"); err == nil {
			t.Fatalf("%s admitted %q, which no list recognises. The allowlist must decide, or a scope "+
				"nobody wrote down becomes a scope nobody refuses", forge, invented)
		}
	}
}

// TestAScopeFromAnotherForgeIsRefused is the per-forge half.
//
// A GitHub grant reporting GitLab's `read_repository` is not a harmless spelling difference — it is
// evidence that whatever built the authorization is confused about which forge it is talking to, and
// admitting it would record that confusion as a connection.
func TestAScopeFromAnotherForgeIsRefused(t *testing.T) {
	for _, forge := range Forges() {
		mine, err := ReadOnlyScopesFor(forge)
		if err != nil {
			t.Fatal(err)
		}
		kind, _ := ExpectedGrantKind(forge)
		for _, other := range Forges() {
			if other == forge {
				continue
			}
			theirs, _ := ReadOnlyScopesFor(other)
			for _, scope := range theirs {
				if scopeAdmitted(scope, mine) {
					continue // a spelling the two forges genuinely share
				}
				a := Authorization{
					Forge: forge, GrantKind: kind, Token: "t",
					Covers: []string{"acme/api"}, Scopes: []string{scope},
				}
				if err := a.Validate("acme/api"); err == nil {
					t.Errorf("%s admitted %q, which is %s's spelling", forge, scope, other)
				}
			}
		}
	}
}

// TestAnEmptyScopeListIsAdmittedAndTheLimitIsStated pins the one thing this change deliberately did NOT
// alter, so a later reader does not mistake it for an oversight.
//
// ⚠️ Not every forge reports a scope list on every grant kind, so an absent list means "the forge said
// nothing" rather than "the grant permits nothing". What bounds a grant's REACH regardless is
// `Covers` / `AccountWide`, and the second half of this test is what makes that claim checkable rather
// than reassuring.
func TestAnEmptyScopeListIsAdmittedAndTheLimitIsStated(t *testing.T) {
	for _, forge := range Forges() {
		kind, _ := ExpectedGrantKind(forge)
		base := Authorization{Forge: forge, GrantKind: kind, Token: "t", Covers: []string{"acme/api"}}
		if err := base.Validate("acme/api"); err != nil {
			t.Fatalf("%s refused an authorization reporting no scopes: %v. That would reject connections "+
				"that work today, for a claim the forge never made", forge, err)
		}
		// 🔴 The compensating control, asserted here rather than assumed: a grant that reports no scopes
		// AND reaches further than the named repository is still refused.
		wide := base
		wide.AccountWide = true
		if err := wide.Validate("acme/api"); err == nil {
			t.Fatalf("%s admitted an account-wide grant that reported no scopes. The scope list being "+
				"optional is only safe because reach is bounded separately", forge)
		}
	}
}
