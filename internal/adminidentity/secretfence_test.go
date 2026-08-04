package adminidentity_test

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// TestReservedSecretNamesCoverAdminIdentity is the drift fence over the two-place spelling of the
// operator-identity credential names — the same fence `internal/billing` carries, for the same bug.
//
// 🔴 The bug it exists to prevent shipped. `providergateway`'s PREFIX form built its key set from
// `SupportedProviders()` plus `ReservedSecretNames()`, and the admin names were in neither, so on a
// deployment configured with `HEROS_SECRETS_AWS_PREFIX` — which is what production runs — every one of
// them resolved to "no secret ID is mapped for provider". `internal/adminidentity/secrets.go` says these
// are "ordinary entries in the same source under reserved logical names": true of the explicit-IDs form
// and false of the prefix form, with nothing at boot able to tell the two apart.
//
// The consequence was not a visible error. `ProviderFromEnv` constructs fine, `/readyz` reports the
// manager healthy, and the failure lands at the moment of use: the operator completes SSO, presents a
// correct factor, and is told "that sign-in was not accepted". The signing key that could not be fetched
// is nowhere in that sentence.
func TestReservedSecretNamesCoverAdminIdentity(t *testing.T) {
	reserved := map[string]bool{}
	for _, n := range providergateway.ReservedSecretNames() {
		reserved[n] = true
	}
	for _, name := range adminidentity.SecretNames {
		if !reserved[name] {
			t.Errorf("adminidentity resolves %q, but providergateway.ReservedSecretNames() does not list it — "+
				"under HEROS_SECRETS_AWS_PREFIX that name expands to no secret ID at all, and operator "+
				"sign-in fails at the factor step rather than at boot", name)
		}
	}
	// The reverse, scoped to this package's namespace: a reserved `admin_` name nothing here consumes is
	// a secret an operator was told to provision for no reason.
	consumed := map[string]bool{}
	for _, n := range adminidentity.SecretNames {
		consumed[n] = true
	}
	for n := range reserved {
		if strings.HasPrefix(n, "admin_") && !consumed[n] {
			t.Errorf("providergateway reserves %q in adminidentity's namespace but no constant here resolves it", n)
		}
	}
}

// TestTOTPSeedNameIsNotReserved pins the one admin name that must NOT be in the reserved list.
//
// `TOTPSeedName` derives `admin_totp_seed/<admin_id>`, one per principal. It cannot be enumerated at
// compile time — the admin ids do not exist until operators do — so listing it would be listing a
// template as if it were a name, and the prefix expansion would map the literal string
// `admin_totp_seed/` to a secret nobody provisions. What serves it is the prefix fallback in
// `AWSSecretsManager.Credential`, and this test is here so that a later well-meaning "the admin names
// are incomplete" edit fails instead of silently breaking every TOTP verification.
func TestTOTPSeedNameIsNotReserved(t *testing.T) {
	for _, n := range providergateway.ReservedSecretNames() {
		if strings.HasPrefix(n, "admin_totp_seed") {
			t.Errorf("providergateway reserves %q — a per-principal seed name is resolved by the prefix "+
				"fallback, and reserving its template maps a literal that names no secret", n)
		}
	}
	if got := adminidentity.TOTPSeedName("adm-superadmin"); got != "admin_totp_seed/adm-superadmin" {
		t.Errorf("TOTPSeedName derived %q; the prefix fallback and the bootstrap command both assume "+
			"admin_totp_seed/<admin_id>", got)
	}
}
