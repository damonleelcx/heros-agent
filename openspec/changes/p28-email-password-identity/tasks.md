# Tasks — P28 Email & Password Identity

## 1. Product & design (product designer, system designer)

- [x] 1.1 PRD `docs/prd/P28-email-password-identity.md` — scenario stories, noun dictionary, IA, FR1–FR21, error copy, acceptance checklist
- [x] 1.2 ADR-012 — the six decisions with their rejected alternatives and the priority-law arbitration for each
- [x] 1.3 OpenSpec change — proposal, design, and delta specs for `password-identity`, `email-delivery`, `platform-ingress`, `sso-identity`, `cli`

## 2. Password primitive (backend)

- [x] 2.1 `internal/password` — argon2id `Hash` / `Verify` over the tagged encoding `$argon2id$v=…$m=…,t=…,p=…$salt$hash`
- [x] 2.2 `NeedsRehash` — a stored value tagged with stale parameters is re-hashed on successful sign-in
- [x] 2.3 Policy — 12-character floor, bundled common-password blocklist, refuse a password containing the person's own address
- [x] 2.4 A fixed decoy encoding, so a sign-in for an unknown address performs a real verification and does not leak existence on the clock
- [x] 2.5 🔴 Fence: no code path passes a password to `tenancy.HashSecret`; observed RED before green

## 3. Identity storage (backend)

- [x] 3.1 `tenancy.UserPassword` + `IdentityToken` types, `Purpose` values `verify_email` / `reset_password`
- [x] 3.2 `Store` gains `SetPassword`, `GetPassword`, `RecordPasswordFailure`, `ClearPasswordFailures`, `MintIdentityToken`, `ConsumeIdentityToken`, `MarkEmailVerified`, `FindUserByEmail`
- [x] 3.3 `MemStore` implementation
- [x] 3.4 `PGStore` implementation — consumption is one conditional `UPDATE`, so two concurrent clicks cannot both win
- [x] 3.5 Behavioural suite runs against both stores; an implementation that satisfies the interface and not the suite is not a second implementation
- [x] 3.6 Migration `0041_p28_password_identity` — `user_password`, `identity_token`, `platform_user.email_verified_at`; expand-only; a second apply is a no-op
- [x] 3.7 🔴 CHECK constraint `user_password.encoded LIKE '$argon2id$%'` — the database's copy of 2.5, which cannot be bypassed
- [x] 3.8 `.down.sql`

## 4. The purpose allowlist (backend)

- [x] 4.1 `auth/durable.go`: refuse every purpose that is not `PurposeUpstream`, replacing the by-name refusal of `PurposeConsole`
- [x] 4.2 🔴 Fence: a session with a fictional purpose is refused as an API credential; observed RED against the old denylist

## 5. Mail (backend)

- [x] 5.1 `internal/mailer` — `Message`, `Mailer`, `Configured()`
- [x] 5.2 `SMTPMailer` with declared egress lane, STARTTLS or implicit TLS, and a timeout
- [x] 5.3 `OperatorMailer` fallback — records the message, logs WARN naming recipient and purpose, never logs the token
- [x] 5.4 Message bodies: confirmation, reset, sign-up-attempt-on-existing-address, bootstrap
- [x] 5.5 `Configured()` reaches `/readyz`; a `password` deployment with no mail reports degraded rather than healthy
- [x] 5.6 🔴 Fence: the unconfigured path never returns success without recording the message

## 6. Platform API (backend)

- [x] 6.1 `POST /api/v1/auth/password/signup` — atomic through `signup.Service`, posture-gated, neutral on an existing address
- [x] 6.2 `POST /api/v1/auth/password/signin` — argon2id verify, lockout, rehash-on-success, personal credential or upstream session
- [x] 6.3 `POST /api/v1/auth/password/forgot` — always the same answer
- [x] 6.4 `POST /api/v1/auth/password/reset` — consume, set, revoke every session and personal credential, disclose machine credentials
- [x] 6.5 `POST /api/v1/auth/password/change` — authenticated, requires the current password, ends every other session
- [x] 6.6 `POST /api/v1/auth/password/verify` and `.../resend`
- [x] 6.7 Verification gate on invitation and on a paid plan change — those two and nothing else
- [x] 6.8 Bootstrap owner at boot: idempotent, mints a single-use token, logs the fact

## 7. Console (frontend)

- [x] 7.1 `lib/idp/password.ts` + the `password` kind in `lib/idp/config.ts` and `lib/identity.ts`
- [x] 7.2 `/signin` — email and password, no client JavaScript on the credential path, `Forgot your password?` and `Create an account`
- [x] 7.3 `/signup` — organization name, email, password; the session precondition does not apply on this kind
- [x] 7.4 `/forgot-password`, `/reset-password`, `/verify-email`
- [x] 7.5 `/app/settings/account` — change password, resend confirmation
- [x] 7.6 Unverified banner naming the address, with a resend
- [x] 7.7 Copy single-sourced in `content/identity.ts` / `lib/organizationCopy.ts`; design-system tokens only; no colour, spacing or radius literal
- [x] 7.8 🔴 Every other seam kind renders exactly what it rendered before

## 8. CLI

- [x] 8.1 `heros login` — `--email` / `$HEROS_EMAIL` / prompt; `$HEROS_PASSWORD` / stdin / hidden prompt; 🚫 no `--password` flag
- [x] 8.2 Hidden prompt via `golang.org/x/term`; no TTY → refuse, naming all three ways
- [x] 8.3 Stores a personal credential `0600`; envelope carries `credential_kind` and no token
- [x] 8.4 `--token` and `--device` unchanged
- [x] 8.5 `heros login --help` and the CLI reference name the new inputs

## 9. Delivery (devops)

- [x] 9.1 Every public platform route in the checked-in ingress manifests, including the two device paths patched in by hand
- [x] 9.2 🔴 Fence: a public platform route with no ingress entry fails a test, naming the route and the manifest
- [x] 9.3 SMTP configuration surface + env parity across the deployment overlays
- [x] 9.4 `HEROS_BOOTSTRAP_OWNER_EMAIL` documented where the other posture variables are

## 10. Verification (QA)

- [x] 10.1 Unit: password primitive, policy, rehash, decoy timing path
- [x] 10.2 Store suite: both stores, single-use under concurrency, purpose binding
- [x] 10.3 API: enumeration parity on all three surfaces, lockout, reset revocation, verification gates
- [x] 10.4 Console: sign-in, sign-up, reset, seam-parity regression
- [x] 10.5 CLI: TTY and no-TTY paths, personal-credential kind, no password in argv or envelope
- [x] 10.6 🔴 The four fences (2.5, 3.7, 4.2, 5.6) each observed RED before being made green
- [ ] 10.7 ⚠️ Three-environment integration — pre-production cluster, local macOS, Windows — **not run**; see §11

## 12. 🔴 Found by running it, after every test was green

- [x] 12.1 **`POST /api/v1/auth/password/signin` was refused by the auth middleware on every real deployment.** `auth.PublicPaths` listed the two device paths and not this one, so with `auth_mode=required` the gate answered a flat 401 and the handler never ran — `heros login` could not sign in at all. Added to `PublicPaths`; only `signin` (the rest of the family is reached by the console holding the BFF credential).
- [x] 12.2 🔴 **Why 12 green tests could not see it**: `passwordauth_test.go` drives `s.Mux`, and `auth.Compose` wraps `s.Handler` one layer further out. `middleware.go` already carried a comment describing this exact trap from P27's device flow — **a comment did not stop it happening again**. New fence `internal/api/authgate_fence_test.go` drives the COMPOSED handler, both directions, observed RED against the pre-fix state.
- [x] 12.3 **The first version of that fence was wrong** and would have failed on correct code: it treated any 401 as "the gate refused", but `handlePasswordSignIn` legitimately answers 401 for an unknown address. It now sends a MALFORMED body — 400 means the handler ran, 401 means the gate refused — which depends on no error prose.
- [x] 12.4 **Whole flow proven locally** against a real `agentd` with `auth_mode=required` and the real SES relay: sign-up → confirmation mail sent → sign-in with NO credential → personal credential minted → console `/api/session` returns a `303` and an `HttpOnly` cookie.

## 11. Not done, and why

- [ ] 11.1 ⚠️ **Pre-production cluster deploy.** Requires cluster credentials this session does not have.
- [ ] 11.2 ⚠️ **Windows leg.** The hidden password prompt is the most platform-dependent thing in this change and Windows is where it is most likely to differ. Not run.
- [x] 11.3 **Live SMTP send — DONE.** Amazon SES in `us-east-1`, domain verified with Easy DKIM, SPF and DMARC (`p=none`) published in Route 53, a send-only IAM user locked to `ses:FromAddress = support@heros-agent.space`, credentials in `heros/platform` and projected into the cluster. A real `ResetPassword` message was sent through `internal/mailer` and accepted by the relay (`make mail-proof`).
- [ ] 11.4 🔴 **The SES sandbox is NOT lifted — the request was submitted and DENIED** (immediate/automated, case `178599389200025`). 200/day, verified recipients only: a stranger signing up receives nothing AND gets no error, because SES accepts the message and drops it. Cannot retry via the API (`ConflictException`); the Support API needs a paid plan. Route is Support Center — read the denial email, reply to the case. `docs/runbooks/p28-smtp-setup.md` §5.1.
- [ ] 11.5 ⚠️ **The P28 code is not deployed.** The overlay pins image `2eafe12`, which predates all of this — mail is configured and the product cannot yet use it. Shipping is a release, not a config apply.
- [ ] 11.6 ⚠️ **`support@heros-agent.space` is send-only** — the domain has no MX, so replies bounce. Nothing we send invites a reply, so no promise is broken; somebody will reply anyway.
