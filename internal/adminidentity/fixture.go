package adminidentity

import (
	"context"
	"errors"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// fixture.go is the TEST-MODE admin identity provider (task 14.1's "admin IdP (SSO+MFA, test mode)").
//
// It is a non-test file because three callers need it: the package's own tests, every other P8
// package's tests, and cmd/proof/operatorconsole — which has to log an operator in to demonstrate anything at all.
// A fixture that only exists under _test.go forces the demo to grow a second, drifting login path,
// and a second login path on this surface is exactly the thing worth not having.
//
// What it is NOT: a way to bypass MFA. IdPFixture mints assertions that the REAL verifier
// (HMACProvider) checks with the REAL keys. Its Deny* helpers produce the malformed inputs a test
// needs, and every one of them is refused by production code, not by a test-only branch.

// IdPFixture mints signed assertions the way a real admin IdP would, using keys drawn from the same
// secrets source the verifier reads.
type IdPFixture struct {
	issuer  string
	secrets Secrets
	now     Clock
}

// NewIdPFixture builds a fixture IdP for issuer, signing with the keys in secrets.
func NewIdPFixture(issuer string, secrets Secrets, now Clock) (*IdPFixture, error) {
	if secrets == nil {
		return nil, errors.New("adminidentity: the fixture IdP needs the same secrets source the verifier reads")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &IdPFixture{issuer: issuer, secrets: secrets, now: now}, nil
}

// Assert mints a complete SSO + MFA assertion for subject.
func (f *IdPFixture) Assert(ctx context.Context, subject, factor string) (Assertion, error) {
	now := f.now()
	a := Assertion{
		Issuer:   f.issuer,
		Subject:  subject,
		IssuedAt: now,
		MFA:      MFAEvidence{Factor: factor, VerifiedAt: now},
	}
	ssoKey, err := f.secrets.SSOSigningKey(ctx)
	if err != nil {
		return Assertion{}, err
	}
	mfaKey, err := f.secrets.MFASigningKey(ctx)
	if err != nil {
		return Assertion{}, err
	}
	a.Signature = Sign(ssoKey, AssertionPayload(a))
	a.MFA.Signature = Sign(mfaKey, MFAPayload(subject, a.MFA))
	return a, nil
}

// AssertWithoutMFA mints an SSO-only assertion — a valid single-factor login. This is the input FR1's
// "a session without MFA is denied" scenario presents.
func (f *IdPFixture) AssertWithoutMFA(ctx context.Context, subject string) (Assertion, error) {
	a, err := f.Assert(ctx, subject, "")
	if err != nil {
		return Assertion{}, err
	}
	a.MFA = MFAEvidence{}
	return a, nil
}

// FixtureSecrets builds an in-process secrets source holding the three admin signing keys.
//
// It routes through providergateway.StaticSecrets rather than inventing a source type, so the fixture
// exercises the same ManagedSecrets adapter production uses and reports itself as "static" on the
// readiness surface — a deployment that accidentally shipped with it is visible, not silent.
func FixtureSecrets(ssoKey, mfaKey, sessionKey string) (Secrets, error) {
	return NewManagedSecrets(providergateway.StaticSecrets{
		SecretAdminSSOSigning:     {APIKey: ssoKey},
		SecretAdminMFASigning:     {APIKey: mfaKey},
		SecretAdminSessionSigning: {APIKey: sessionKey},
	})
}
