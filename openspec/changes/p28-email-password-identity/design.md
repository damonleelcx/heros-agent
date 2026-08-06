# Design — P28 Email & Password Identity

The arbitration for every decision below is in [ADR-012](../../../docs/adr/ADR-012-email-password-identity.md),
including the alternatives rejected and why. This file is the shape of what gets built.

## Context

Five identity seam kinds exist (`dev`, `configured`, `platform`, `oidc`, `saml`) and none of them lets a person
create their own credential. P27 built every row a password would attach to. What is added is one verifier, one
storage format, one token table, one mail seam, and the screens and CLI paths that use them.

## D1 — Where the verifier lives

**The platform (`agentd`) owns it. The console never sees a stored hash and never verifies a password.**

The console is a BFF holding a platform credential; it already does exactly this for the `platform` seam,
where `verifyPlatformToken` asks the platform whose token it is rather than consulting a second map. Password
verification follows that path, for the same reason: two places that can decide whether a password is correct
is two places a revocation has to reach.

```
browser ──POST /api/session {email,password}──▶ console BFF
                                                   │  (no password logic; forwards once, drops it)
                                                   ▼
                              POST /api/v1/auth/password/signin  ──▶ agentd
                                                   │                    argon2id verify
                                                   ◀── {tenant_id, user_id, token}
                                                   │
                              issueSession(principal) ──▶ HttpOnly cookie the browser cannot read
```

The password crosses the console process once, in a POST body, and is never stored, logged, or put in a
session record — the three rules `identity.ts` already binds every implementation to.

## D2 — Storage

### `user_password` — one row per person who has a password

| column | type | notes |
|---|---|---|
| `user_id` | TEXT PK → `platform_user` | one password per person |
| `encoded` | TEXT NOT NULL | `$argon2id$v=19$m=65536,t=3,p=4$<b64salt>$<b64hash>` — 🔴 algorithm-tagged |
| `updated_at` | TIMESTAMPTZ NOT NULL | |
| `failed_attempts` | INT NOT NULL DEFAULT 0 | cleared by success and by reset |
| `locked_until` | TIMESTAMPTZ NULL | NULL means not locked; never a zero time |

A CHECK enforces `encoded LIKE '$argon2id$%'`. That is the database's copy of "a password is never a bare
SHA-256", and it is the copy that cannot be bypassed by a code path that forgot.

### `identity_token` — single-use, expiring, purpose-bound

| column | type | notes |
|---|---|---|
| `token_hash` | TEXT PK | SHA-256 of a 256-bit minted value — correct here, see D3 |
| `user_id` | TEXT NOT NULL → `platform_user` | |
| `purpose` | TEXT NOT NULL CHECK IN (`verify_email`, `reset_password`) | closed set |
| `email` | TEXT NOT NULL | the address this token proves — so confirming a **changed** address is expressible |
| `created_at`, `expires_at` | TIMESTAMPTZ NOT NULL | |
| `consumed_at` | TIMESTAMPTZ NULL | 🔴 single-use is a database property, like `invitation.accepted_at` |

Consumption is `UPDATE … SET consumed_at = $2 WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > $2`
and a zero row count is the refusal. No read-then-write, so two concurrent clicks cannot both win.

### `platform_user.email_verified_at`

One nullable column. NULL means unverified, which is what every row that exists before this migration is —
honestly, since nobody ever verified them.

## D3 — Two hash functions, and why that is not a split truth source

`tenancy.HashSecret` (SHA-256) hashes **values we minted**: credentials, session tokens, device codes, and now
identity tokens. `password.Hash` (argon2id) hashes **values a human chose**. The rule is one line and it is
enforced by a test, not by discipline: *a value with 256 bits of `crypto/rand` behind it is looked up by SHA-256
hash; a value a person typed is verified by argon2id.* Applying the second to the first would put a 64 MiB
memory allocation on every authenticated request; applying the first to the second is the L1 failure ADR-012
Decision 2 refuses.

## D4 — Refusals are one answer with several causes

Extends `auth.RefusalCause`, which already keeps the pattern: the wire gets one generic answer, the operator's
log gets the distinction.

| cause (log) | wire |
|---|---|
| `unknown_address` | `that email and password did not match` |
| `wrong_password` | *(identical)* |
| `account_locked` | `too many attempts, try again in N minutes` — **deliberately distinguishable**, see below |
| `unverified_for_action` | names the action and offers a resend |

🔴 **Lock is the one distinguishable state, and that is a considered exception.** It leaks "this address exists"
to somebody who has already made ten failed attempts against it — an attacker who has spent ten guesses is past
the point where the distinction helps them. Hiding it costs a real person the only information that explains why
a correct password is being refused, which is the exact "error with no next action" the copy rules forbid.

**Timing.** When no user exists, the verifier still runs a full argon2id verification against a fixed decoy
encoding, so an unknown address and a wrong password take the same time. Without this the enumeration oracle
closed on the response body is wide open on the clock.

## D5 — `internal/mailer`

```go
type Message struct{ To, Subject, TextBody string }   // no HTML, no attachments, no CC
type Mailer interface {
    Send(ctx context.Context, m Message) error
    Configured() bool          // for /readyz — never a guess
}
```

Two implementations. `SMTPMailer` (host, port, username, password, from, STARTTLS or implicit TLS) declares
**lane A, direct egress**, explicitly, per the two-lane rule — no bare client. `OperatorMailer` is the fallback:
it appends to an operator-readable record, logs at WARN naming the recipient and the purpose (never the link's
secret half in the log — the record holds it, the log does not), and reports `Configured() == false`.

🔴 The fallback exists so that "no SMTP" is a **visible** state rather than a silent discard. A deployment that
never configures mail still works; its operator has to hand the link over, and both the console health surface
and the log say that is what is happening.

**Mail never fails the act.** `Send` errors are logged and reported on the result; the sign-up, the reset
request and the invitation all stand. Rolling back an account because a mail server was down loses a customer
for an outage that is ours.

## D6 — The purpose allowlist (a latent defect fixed on the way past)

`auth/durable.go` today:

```go
if sess.Purpose == tenancy.PurposeConsole { return Principal{}, RefusalUnknown }
```

Denylist. Correct by accident while exactly two purposes exist. P28 is the change that adds more token kinds to
the identity domain, and the shape of the bug is that **nothing would look wrong** — a reset link would simply
also be a platform API credential.

```go
if sess.Purpose != tenancy.PurposeUpstream { return Principal{}, RefusalUnknown }
```

Fenced by a test that constructs a session with a fictional purpose and asserts refusal, so the fence fails if
somebody flips it back.

## D7 — Verification gates exactly two actions

Inviting a member, and moving to a plan that charges. Both **spend something the account has not proved it
owns** — an invitation puts mail in a third party's inbox under our name; an upgrade takes money.

Rejected: gating sign-in itself. If SMTP is misconfigured, nobody can ever get in, and the deployment's own
operator is locked out of the console they would fix it from. Rejected: gating nothing — an unverified account
that can invite is a spam relay with our SPF record on it.

## D8 — Bootstrapping the first owner without self-serve

An install with `HEROS_SELF_SERVE_SIGNUP=0` has no sign-up form, so the first owner cannot create themselves.
`tenancy.Seed` already writes configured credentials into the store at boot; it gains one more thing to seed —
`HEROS_BOOTSTRAP_OWNER_EMAIL`. On a deployment where that address has no password, boot mints a **single-use,
24-hour `reset_password` token**, hands it to the mailer (which, unconfigured, prints it to the operator
surface), and logs that it did. It is idempotent: an address that already has a password mints nothing.

This is deliberately the *existing* reset path rather than a bootstrap password. A printed temporary password
is a secret that lives in a log until somebody changes it; a single-use one-hour-class token is spent on first
use and worthless afterwards.

## Risks

| Risk | Mitigation |
|---|---|
| argon2id at 64 MiB × 4 lanes exhausts memory under a sign-in flood | Sign-in is not a hot path; lockout bounds per-account attempts; parameters are a tagged constant so lowering them is a deploy, not a migration |
| A deployment ships with no SMTP and nobody notices | `/readyz` reports `mail: not configured`; every send logs WARN; the operator surface holds the undelivered links |
| The ingress allowlist is edited by hand again | The fence test fails when a public platform route has no ingress entry — machine-enforced, not remembered |
| An existing `configured` deployment upgrades and finds sign-in changed | The kind is opt-in; `configured` is the default it already has; no migration touches its behaviour |
