package auth

import (
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/tenancy"
)

// purpose_allowlist_test.go is the fence for the latent defect P28 found on its way past
// (ADR-012 Decision 6).
//
// `principalFromSession` refused the browser cookie BY NAME:
//
//	if sess.Purpose == tenancy.PurposeConsole { refuse }
//
// Correct by accident while exactly two purposes existed. Any third purpose would have been ACCEPTED as a
// platform API credential — silently, with no line of code looking wrong. P28 is the change that would have
// added one: had the password-reset token lived in `console_session` rather than in its own table, a reset
// link mailed to somebody would have authenticated against the entire platform API.
//
// The check is now `!= PurposeUpstream`. This test asserts the DIRECTION, not the current values — it
// constructs a session with a purpose nobody has defined and requires refusal, so reverting to a denylist
// fails here rather than in an incident.

// fictionalPurpose is not a real purpose and must never become one. It stands for "whatever purpose somebody
// adds next", which is exactly the case a denylist gets wrong.
const fictionalPurpose tenancy.Purpose = "some_future_purpose"

// purposeSource is a CredentialSource that hands back one session, whatever is asked for. It bypasses
// `tenancy.KnownPurpose` on purpose: the store refuses to WRITE an unknown purpose, and this test is about
// what `auth` does with one it is handed — from a future migration, a hand-edited row, or a replica running
// a newer build.
type purposeSource struct {
	sess   tenancy.Session
	tenant tenancy.Tenant
}

func (p purposeSource) ResolveCredential(string) (tenancy.Credential, error) {
	return tenancy.Credential{}, tenancy.ErrNotFound
}
func (p purposeSource) GetTenant(string) (tenancy.Tenant, error) { return p.tenant, nil }
func (p purposeSource) ResolveSession(string) (tenancy.Session, error) {
	return p.sess, nil
}

func TestOnlyAnUpstreamSessionAuthenticates(t *testing.T) {
	now := time.Now().UTC()
	tenant := tenancy.Tenant{TenantID: "acme", Name: "Acme", Status: tenancy.StatusActive}

	base := tenancy.Session{
		TokenHash: tenancy.HashSecret("a-token"),
		SessionID: "sess-1",
		TenantID:  "acme",
		UserID:    "usr-1",
		IssuedAt:  now.Add(-time.Minute).UnixMilli(),
		ExpiresAt: now.Add(time.Hour).UnixMilli(),
	}

	cases := []struct {
		name    string
		purpose tenancy.Purpose
		accept  bool
	}{
		{"the upstream token the console exchanges for", tenancy.PurposeUpstream, true},
		{"the browser's own cookie", tenancy.PurposeConsole, false},
		{"a purpose nobody has defined yet", fictionalPurpose, false},
		{"an empty purpose", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := base
			sess.Purpose = c.purpose
			r := (&Registry{keys: map[string]Principal{}}).WithSource(purposeSource{sess: sess, tenant: tenant})
			p, cause := r.LookupWithCause("a-token")
			if c.accept {
				if cause != RefusalNone || p.TenantID != "acme" {
					t.Fatalf("an upstream session was refused: cause=%q principal=%+v", cause, p)
				}
				return
			}
			if cause == RefusalNone {
				t.Fatalf("a %q session authenticated as a platform API credential. The purpose check must be "+
					"an ALLOWLIST (== PurposeUpstream); a denylist accepts every purpose added later, "+
					"silently — see ADR-012 Decision 6.", c.purpose)
			}
			// 🔴 And it is refused as UNKNOWN, not as its own cause. Whoever presented it learns nothing:
			// the only parties who could be are the console (a bug) or somebody holding a stolen cookie.
			if cause != RefusalUnknown {
				t.Errorf("a %q session was refused as %q — it must be indistinguishable from an unrecognised "+
					"credential", c.purpose, cause)
			}
		})
	}
}
