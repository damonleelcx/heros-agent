# ADR-012 — Email and password is a fifth seam kind; the platform owns the verifier, the console owns nothing

- **Status:** Accepted (2026-08-05)
- **Deciders:** System Design + Backend (proposed) + User (ratified — mail seam, argon2id, and the ingress
  fence were each put to the user and chosen explicitly)
- **Resolves:** PRD [P28](../prd/P28-email-password-identity.md) §7, and the open question ADR-008 deferred
  ("P7 has not named the tenant identity mechanism") for the **unfederated** case.
- **Relates to:** [ADR-008](ADR-008-console-tenant-identity-seam.md) (the seam this adds a kind to — its
  contract is unchanged), P22 (the federated kinds), P27 (the person, credential and session rows).
- **Owns:** phase **P28 — Email & Password Identity**.

## Context

Production runs `CONSOLE_TENANT_IDENTITY=configured`: a JSON object of `{assertion: tenant_id}` injected from
a Kubernetes secret, which a person can obtain only by an operator reading it out of the cluster and handing it
over. That is a shared, unrotatable, unattributable secret distributed over chat, and it is the documented
onboarding path — two `aws ssm` commands.

Six decisions follow, each arbitrated with the eight-level priority law. Where two options tied on a level, the
tie is stated rather than hidden.

---

## Decision 1 — `password` is a **fifth kind of the existing seam**, not a replacement for any of the four

`verify(assertion) → {tenantId, userId?}` is untouched. What the password kind adds is a second entry point on
the same seam — `verifyPassword(email, password) → {tenantId, userId}` — because a two-field credential cannot
be squeezed into a one-string assertion without inventing an encoding, and an encoding is a wire contract
nobody asked for.

**Rejected — replace `configured` outright.** It is the seam an air-gapped install uses, and *"a UI redesign
may not drop a capability"* applies to identity mechanisms with more force than to screens. Removing it would
break every install that federates with nobody and has eleven users its operator knows by name. L5 (evolvability)
and L2 (stability) both refuse it.

**Rejected — one kind that tries both.** A deployment that accepts a `configured` assertion *and* an
email/password pair has two doors and one lock; revoking a person on one path leaves the other open. L1.

## Decision 2 — argon2id via `golang.org/x/crypto`, algorithm-tagged in the stored value

`$argon2id$v=19$m=65536,t=3,p=4$<b64 salt>$<b64 hash>` — 64 MiB, 3 passes, 4 lanes, 16-byte salt, 32-byte tag.

**Why not reuse `tenancy.HashSecret`.** It is SHA-256, and its own comment gives the reason it is correct
there: it hashes a 256-bit value *we* minted, so there is no dictionary to slow down and no user-chosen entropy
to compensate for. **Every clause of that inverts for a password.** Reusing it would be the L1-for-L8 trade the
priority law names as its first example.

**Rejected — `crypto/pbkdf2` (stdlib in Go 1.24, zero new dependencies).** Genuinely attractive: this
repository has no direct `golang.org/x/crypto` today and has hand-written verifiers rather than pulled
libraries elsewhere (P22's OIDC and SAML). PBKDF2-HMAC-SHA256 at 600k iterations is OWASP-acceptable. It loses
on **L1**: it is not memory-hard, so a GPU or ASIC attacker gets orders of magnitude more guesses per dollar
against a leaked table. L2 says *once a higher level separates two options, do not go back for the lower-level
convenience* — and the convenience here is one dependency on a module maintained by the Go team, whose
siblings (`x/sys`, `x/text`, `x/sync`) are already in the module graph. **This was put to the user and chosen
explicitly.**

**Why the tag is in the stored value.** A bare hash makes the parameters a global constant, and raising the
cost then means either a migration nobody can run (the plaintexts are gone) or a permanently weak floor. Tagged,
a sign-in that verifies against stale parameters re-hashes on the spot, so raising cost is a deploy. This is
what keeps a one-way door from being one.

## Decision 3 — a new `identity_token` table; verification and reset are **not** sessions

Single-use, expiring, hashed, purpose-bound tokens for email verification and password reset.

**Rejected — reuse `console_session` with new `Purpose` values.** It is the closest existing shape (opaque
hashed token, purpose, user, expiry, revocation) and the reuse instinct is right in general. It fails on the
data: `console_session.tenant_id` is `NOT NULL REFERENCES tenant`, and a *password reset* is scoped to a
**person**, who may be a member of two organizations or — mid-signup — of none. Satisfying the column would
mean writing an arbitrary org onto a row where it means nothing, which is a lie in the schema. L1/L5.

**Rejected — columns on `user_password` (`reset_token_hash`, `reset_expires_at`, …).** No new table, which
`careful-table-creation` prefers. It cannot express two outstanding tokens, cannot express verifying a *new*
address while the old one still works, and puts a short-lived secret in the same row as the long-lived
credential so that every reset rewrites the password row. L6 (extensibility).

**Accepted, with its consumer named** — `careful-table-creation` forbids building a table for a future user.
`identity_token` has exactly two consumers on the day it lands (verification, reset) and one row shape.

## Decision 4 — `internal/mailer` is a seam with an SMTP implementation and an **operator-visible** fallback

**Rejected — no mail at all** (admin-issued reset links from the members screen). Zero new infrastructure, and
it makes "I forgot my password" require another human, which is the L3 failure this whole phase exists to
remove. It also leaves sign-up unverified forever, so anyone can register as anyone. Put to the user; rejected.

**Rejected — SMTP as a hard prerequisite** for self-serve. Strictly safest, and it makes an air-gapped install
un-bootable without a mail server it may not have. L4.

**🔴 The fallback is not a no-op.** A deployment with no SMTP configuration writes every message to the
operator surface and logs WARN, and `/readyz` reports `mail: not configured`. This follows
`logging-conventions` — a silent fall-back to a default is forbidden — and the failure mode it prevents is
specific: a person waiting for a reset mail cannot distinguish "discarded" from "the product is broken", and
neither can the operator.

## Decision 5 — on this seam the address **is** the subject, and the internal `user_id` is still the key

P27 wrote, in capitals, that email is a display attribute and never the identity, because an address gets
reassigned inside a company while an IdP's `sub` does not.

That reasoning is **about federation** and does not transfer. There is no IdP here and no `sub` to be stable
instead; the address is precisely what the person proved by receiving mail at it, and an invented opaque
subject would add no stability, only indirection. What survives unchanged is the part that was actually
load-bearing: `platform_user.user_id` remains the key every other table references, so an address change
rewrites one column. A `password` user and an `oidc` user sharing an address are two rows and two people —
`platform_user_federated_identity UNIQUE (issuer, subject)` makes that automatic, and linking them is a
deliberate act nobody has asked for.

## Decision 6 — `auth`'s session-purpose check becomes an **allowlist**

`auth/durable.go` refuses `PurposeConsole` by name and accepts everything else as a platform API credential.
With two purposes that is correct by accident. Adding a third — a password-reset token that authenticates a
person — would have made a reset link a **platform API credential**, silently, with no line of code looking
wrong.

The check is inverted to accept only `PurposeUpstream`. This is a **latent defect fixed on the way past**, in
scope because P28 is the change that would have detonated it, and it is fenced by a test that adds a fictional
purpose and asserts it is refused. Recorded here so the next person to add a purpose finds the reason rather
than the rule.

---

## Consequences

**Good.** A person obtains their own credential, bound to them, revocable with them, recoverable without a
human — in the browser and in the terminal. `heros login` stops depending on an operator. Every existing seam
is unchanged and still selectable. The purpose denylist is gone before anything sat on it.

**Costs, stated.** Two new direct dependencies (`golang.org/x/crypto` for argon2id, `golang.org/x/term` for the
hidden prompt — both Go-team modules). One new table and one new column set. A mail path to operate, with
deliverability as a new support surface. argon2id at 64 MiB × 4 lanes is real memory per concurrent sign-in;
sign-in is not a hot path, but a deployment sizing `agentd` must know this exists.

**What could make us revisit.** If a customer needs SSO *and* passwords simultaneously on one deployment, the
one-kind-at-a-time model becomes the constraint, and the answer is a kind list rather than a kind — an additive
change to `CONFIG`, deliberately left unbuilt because nobody has asked. If argon2id memory becomes a measured
problem, the tagged encoding is what makes lowering it safe.
