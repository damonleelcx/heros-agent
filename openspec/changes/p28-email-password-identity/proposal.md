## Why

The production console runs the `configured` identity seam. Its entire mechanism is
`CONSOLE_TENANT_ASSERTIONS` — a JSON object of `{assertion: tenant_id}` injected from a Kubernetes secret — so
the only way for a person to obtain a way in is for an operator to read that secret out of the cluster and hand
it to them. The documented onboarding step is two `aws ssm` commands. The sign-in page states the situation in
its own copy: *"whoever runs it gives you this value."*

That shared string is not a person. It cannot be attributed, it cannot be rotated for one holder, and it is not
revoked by removing a member — so P27's central promise (*remove someone and their access ends at the next
request*) is **false on the seam production actually runs**. `heros login` inherits the same wall: its device
flow is correct, and step two of it is "sign in to the console".

P27 built the person, the membership, the revocable credential and the atomic sign-up service. What is missing
is the one thing that lets somebody use them: a credential they can create and recover themselves. P28 adds it,
as a fifth kind of the ADR-008 seam, leaving `configured`, `oidc`, `saml`, `platform` and `dev` untouched and
still selectable.

## What Changes

- **ADDED — `password` identity seam kind.** `CONSOLE_TENANT_IDENTITY=password` authenticates an email address
  and a password. The seam contract `verify → {tenantId, userId?}` is unchanged; a second entry point
  `verifyPassword(email, password)` is added beside it. The platform owns the verifier; the console holds no
  password logic and no password.
- **ADDED — argon2id password storage.** `$argon2id$v=19$m=…,t=…,p=…$salt$hash`, per-row salt,
  algorithm-tagged, re-hashed on sign-in when parameters change. 🔴 `tenancy.HashSecret` (SHA-256) may never
  receive a password, and a test enforces it.
- **ADDED — self-serve sign-up with an email and a password.** One act creates the person, the organization,
  the owner membership, the Free account and the password, or none of them. Still governed by
  `HEROS_SELF_SERVE_SIGNUP`, still off by default.
- **ADDED — email confirmation and password reset.** Single-use, one-hour, purpose-bound tokens in a new
  `identity_token` table. 🔴 Completing a reset revokes every session and every **personal** credential that
  person holds; machine credentials are untouched and the screen says so.
- **ADDED — `internal/mailer`.** One seam, an SMTP implementation, and an operator-visible fallback for a
  deployment that configures no SMTP. The fallback does **not** discard: it writes to the operator surface,
  logs WARN, and `/readyz` reports `mail: not configured`.
- **MODIFIED — `heros login` authenticates with an email and a password.** `--email` / `$HEROS_EMAIL` /
  prompt; `$HEROS_PASSWORD` / stdin / **hidden** prompt. 🚫 No `--password` flag. `--token` (machine) and
  `--device` (device flow) both remain.
- **MODIFIED — CLI non-interactivity.** A command may prompt **only** when a terminal is attached; with none
  it refuses, naming every non-interactive way to supply the value. It never blocks on an invisible prompt.
- **MODIFIED — account enumeration is closed on three surfaces.** Sign-in, sign-up and forgot-password answer
  identically whether or not the address is known, including on the timing profile.
- **MODIFIED — `auth` session-purpose check becomes an allowlist.** It refuses `PurposeConsole` **by name**
  today and would therefore have accepted a password-reset token as a platform API credential. **breaking for
  any caller that minted a non-upstream purpose expecting it to authenticate** — there are none.
- **ADDED — public platform routes live in the checked-in ingress manifests, with a fence.** The allowlist is
  hand-maintained; the two device paths were patched in by hand and the next `kubectl apply -f prod.yaml`
  deletes them. A test fails when a public platform route has no ingress entry.

## Impact

- **Affected capabilities:** `password-identity` (new), `email-delivery` (new), `platform-ingress` (new),
  `sso-identity` (modified), `cli` (modified), `self-serve-subscription` (modified)
- **Affected code/systems:** `internal/password` (new), `internal/mailer` (new), `internal/tenancy`
  (`UserPassword`, `IdentityToken`, purpose allowlist), `internal/auth`, `internal/signup`, `internal/api`,
  `internal/clilink`, `internal/cli`, `db/migrations/postgres/0041_*`, `web/console` (`/signin`, `/signup`,
  `/forgot-password`, `/reset-password`, `/verify-email`, `/app/settings/account`, `lib/idp/password.ts`),
  `deploy/` ingress manifests
- **New direct dependencies:** `golang.org/x/crypto` (argon2id), `golang.org/x/term` (hidden prompt) — both
  Go-team modules; `x/sys`, `x/text`, `x/sync` are already in the graph
- **Dependencies:** P22 (the seam), P27 (person, membership, credential, session, sign-up service), P21 (the
  paid-plan gate confirmation attaches to). Unblocks: any honest self-serve claim, and a second factor, which
  has nowhere to attach until an account exists
