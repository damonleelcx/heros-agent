package forgedelivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// p35_credential_test.go is `tasks.md` §6 — the credential posture P35's console default makes
// load-bearing.
//
// # Why these three are fenced HERE and not left to ADR-005's prose
//
// P35 makes the hosted Git App the DEFAULT on one surface (R3). Design D3 names the failure that
// exposes the phase to: *the default quietly widening into "the platform has write access to your
// account", which is a different product.* Nothing about that widening would be visible — the same
// route, the same code path, one broader grant — so the three properties that contain it are asserted
// as values rather than described.

// ── 6.1 the installation is scoped to what the customer selected ─────────────────────────────────

func TestInstallationScopedToSelectedRepositories(t *testing.T) {
	ok := Installation{
		InstallationID: "i1", TenantID: "ten_1",
		Repositories: []string{"nousresearch/hermes-agent"},
		Permissions:  LeastPrivilegePermissions(),
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a per-repository, least-privilege installation was refused: %v", err)
	}
	if !ok.Covers("nousresearch/hermes-agent") {
		// Covers() reads Active, which Install sets. A validated-but-not-installed value is inactive,
		// which is the correct default.
		installed := ok
		installed.Active = true
		if !installed.Covers("nousresearch/hermes-agent") {
			t.Fatal("an installation does not cover the repository it selected")
		}
	}
	if ok.Covers("nousresearch/some-other-repo") {
		t.Fatal("an installation covers a repository it did not select. Per-repository is the whole of " +
			"what makes the console default survivable")
	}
}

// TestABroaderInstallationIsRefused is the widening D3 names, in both of the shapes it can take.
func TestABroaderInstallationIsRefused(t *testing.T) {
	base := Installation{InstallationID: "i1", TenantID: "ten_1", Permissions: LeastPrivilegePermissions()}

	noRepos := base
	if err := noRepos.Validate(); err == nil {
		t.Fatal("an installation selecting NO repositories validated. 🚫 There is deliberately no " +
			"\"all repositories\" flag, so org-wide-by-default is not expressible — and an empty " +
			"selection must never be read as one")
	}

	for _, perm := range []PermissionSet{
		{"administration": "write"},
		{"actions": "write"},
		{"contents": "admin"},
		{"pull_requests": "admin"},
	} {
		wide := base
		wide.Repositories = []string{"nousresearch/hermes-agent"}
		wide.Permissions = perm
		if err := wide.Validate(); err == nil {
			t.Fatalf("the permission set %v validated. Broadening the App's permissions is a SPEC "+
				"change, not a configuration choice", perm)
		}
	}
}

// TestTheLeastPrivilegeSetIsTheNarrowestCredibleAsk asserts what the platform actually requests, so a
// widening is a visible diff on a named function rather than a config value somebody changed.
func TestTheLeastPrivilegeSetIsTheNarrowestCredibleAsk(t *testing.T) {
	got := LeastPrivilegePermissions()
	want := map[string]string{"pull_requests": "write", "contents": "write"}
	if len(got) != len(want) {
		t.Fatalf("the least-privilege set holds %d permissions: %v. The narrowest credible ask is what "+
			"gets an installation approved", len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("permission %q is %q, want %q", k, got[k], v)
		}
	}
}

// ── 6.2 revocation stops pushes IMMEDIATELY ──────────────────────────────────────────────────────

// TestRevocationStopsPushesImmediately is FR25, and the word that matters is "immediately".
//
// 🔴 The natural implementation caches an installation token for its lifetime — GitHub's are an hour —
// and refreshes on expiry. Under that design a revocation takes effect whenever the cached token
// happens to run out: somewhere between now and an hour from now, and not knowable from outside. A
// customer who revokes during an incident and watches a pull request appear four minutes later has
// learned that the control they were given is advisory.
//
// So this asserts the NEXT CALL fails, with no clock advanced and no token expired.
func TestRevocationStopsPushesImmediately(t *testing.T) {
	store := NewInstallationStore()
	if err := store.Install(Installation{
		InstallationID: "i1", TenantID: "ten_1",
		Repositories: []string{"nousresearch/hermes-agent"},
		Permissions:  LeastPrivilegePermissions(),
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.MayPush(ctx, "ten_1", "nousresearch/hermes-agent"); err != nil {
		t.Fatalf("an active installation refused a push: %v", err)
	}

	if err := store.Revoke("i1", "revoked by the customer from their forge settings"); err != nil {
		t.Fatal(err)
	}

	// 🔴 THE ASSERTION. No clock advanced. No token refreshed. The very next call.
	err := store.MayPush(ctx, "ten_1", "nousresearch/hermes-agent")
	if !errors.Is(err, ErrInstallationRevoked) {
		t.Fatalf("the push after a revocation returned %v. \"Immediately\" means the next call, not "+
			"the next token refresh — a revocation whose effect waits on a cached credential is a "+
			"control the customer cannot rely on during the incident they are revoking for", err)
	}
}

// TestARevokedInstallationHandsOutNoTokenAtAll asserts the check happens BEFORE the credential is
// requested, which is what makes the token cache irrelevant to revocation.
func TestARevokedInstallationHandsOutNoTokenAtAll(t *testing.T) {
	store := NewInstallationStore()
	secrets := NewMemSecretsManager()
	secrets.Put("i1", "ghs_the_token")
	if err := store.Install(Installation{
		InstallationID: "i1", TenantID: "ten_1",
		Repositories: []string{"nousresearch/hermes-agent"},
		Permissions:  LeastPrivilegePermissions(),
	}); err != nil {
		t.Fatal(err)
	}
	tokenUsed := false
	w := NewAppForgeWriter(store, secrets, "ten_1", func(string) ForgeWriter {
		tokenUsed = true
		return NewInMemForge(ForgeGitHub, false)
	})
	if err := store.Revoke("i1", "revoked"); err != nil {
		t.Fatal(err)
	}
	err := w.EnsureBranch(context.Background(),
		Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"}, "heros/opt/aaa-bbb")
	if !errors.Is(err, ErrInstallationRevoked) {
		t.Fatalf("a revoked installation attempted a write: %v", err)
	}
	if tokenUsed {
		t.Fatal("a token was requested for a revoked installation. The coverage check must run BEFORE " +
			"the credential is fetched, or the token cache becomes part of revocation's latency")
	}
}

// TestRevocationIsAReportedStateNotSilence — a customer who revoked is told what happened, and the
// sentence names which grant survived.
func TestRevocationIsAReportedStateNotSilence(t *testing.T) {
	for _, g := range GrantKinds() {
		eff, err := EffectOfRevoking(g)
		if err != nil {
			t.Fatalf("%s: %v", g, err)
		}
		if eff.Detail == "" {
			t.Fatalf("revoking %q reports nothing", g)
		}
		if eff.OtherGrantIntact == g {
			t.Fatalf("revoking %q names itself as the surviving grant", g)
		}
		if !strings.Contains(eff.Detail, "untouched") {
			t.Fatalf("revoking %q does not say the other grant survives: %q. A customer who is not told "+
				"will assume it did not, and act on that", g, eff.Detail)
		}
	}
	if _, err := EffectOfRevoking("invented"); err == nil {
		t.Fatal("an unknown grant kind described an effect")
	}
}

// ── 6.3 the write installation is structurally separate from the P32 read connection ─────────────

// TestReadConnectionAndWriteInstallationAreSeparateGrants is the requirement P32 made necessary: before
// it there was no second grant to confuse a write installation with.
func TestReadConnectionAndWriteInstallationAreSeparateGrants(t *testing.T) {
	const repo = "nousresearch/hermes-agent"

	// A WRITE installation for the repository.
	store := NewInstallationStore()
	if err := store.Install(Installation{
		InstallationID: "i1", TenantID: "ten_1",
		Repositories: []string{repo}, Permissions: LeastPrivilegePermissions(),
	}); err != nil {
		t.Fatal(err)
	}

	// Revoking the WRITE grant must not touch the read side, and vice versa. The two stores are
	// different types in different packages with different revocation methods — which is the structural
	// half — and the effect description is the half a customer reads.
	if err := store.Revoke("i1", "revoked"); err != nil {
		t.Fatal(err)
	}
	writeEffect, _ := EffectOfRevoking(GrantWrite)
	if writeEffect.StoppedReads {
		t.Fatal("revoking the write installation is described as stopping reads. A customer who lost " +
			"their assessments by revoking write access would have been given a control with a " +
			"consequence nobody warned them about — and the practical effect is that they would not revoke")
	}
	readEffect, _ := EffectOfRevoking(GrantRead)
	if readEffect.StoppedPushes {
		t.Fatal("revoking the read connection is described as stopping pushes")
	}
}

// TestNeitherGrantImpliesTheOther reads the enum rather than a hand-written list, so a third grant is
// covered the day it is added.
func TestNeitherGrantImpliesTheOther(t *testing.T) {
	for _, a := range GrantKinds() {
		for _, b := range GrantKinds() {
			if a == b {
				continue
			}
			if a.Implies(b) {
				t.Fatalf("%q implies %q. One credential that both reads source and opens pull requests "+
					"is the thing neither ADR-005 nor ADR-013 wants", a, b)
			}
		}
	}
}

// TestAReadConnectionCannotCarryAWriteScope asserts P32's own control still holds, from this side —
// because "the two grants are separate" is only true while the read grant cannot quietly become both.
//
// # 🔴 The gap this fence found, and the fix it forced
//
// When this test was written, `sourceingest` refused a write-capable scope with a DENYLIST of verbs —
// `write`, `push`, `admin`, `delete`, `maintain`, `manage`, `create`. **GitHub's classic `repo` scope
// grants full read AND write and contains no verb**, so it passed. The case below was written as a
// `t.Log` with an inverted assertion: it reported the gap and was built to FAIL the day it closed,
// rather than staying silent either way.
//
// It failed. `sourceingest` now decides with an ALLOWLIST — the scopes each forge's narrowest grant
// actually carries — so a spelling nobody wrote down is refused by construction rather than by
// somebody having thought of it. `repo` has been promoted into the fenced table below, beside two more
// instances of the same shape the inversion also closed: `public_repo` (write to public repositories)
// and GitLab's `api` (complete read/write API access). All three are NOUNS that confer every verb.
//
// The owning package fences the rule itself in `sourceingest/scopes_test.go`. This one is P35's, and it
// asserts the property from the side that depends on it: §6.3 requires the write installation to be
// structurally separate from the read connection, and that is only true while the read grant cannot
// write.
func TestAReadConnectionCannotCarryAWriteScope(t *testing.T) {
	// 🔴 `repo` heads the table because it is the one that got through. The three below it are the
	// verb-bearing spellings the old denylist did catch, kept so a future narrowing of the allowlist
	// cannot quietly stop refusing them.
	for _, scope := range []string{
		"repo", "public_repo", "api",
		"contents:write", "write_repository", "repository:write", "admin:org",
	} {
		a := sourceingest.Authorization{
			Forge: sourceingest.ForgeGitHub, GrantKind: sourceingest.GrantAppInstallation,
			Token: "tok", Covers: []string{"nousresearch/hermes-agent"}, Scopes: []string{scope},
		}
		if err := a.Validate("nousresearch/hermes-agent"); err == nil {
			t.Errorf("a read connection carrying the scope %q was admitted. The two grants stop being "+
				"separate the moment the read one can write", scope)
		}
	}

	// The control. Without it this table would still pass if `Validate` refused everything — including
	// the grant this platform actually asks for.
	ok := sourceingest.Authorization{
		Forge: sourceingest.ForgeGitHub, GrantKind: sourceingest.GrantAppInstallation,
		Token: "tok", Covers: []string{"nousresearch/hermes-agent"},
		Scopes: []string{"contents:read", "metadata:read"},
	}
	if err := ok.Validate("nousresearch/hermes-agent"); err != nil {
		t.Fatalf("the read grant this platform asks for was refused: %v", err)
	}
}
