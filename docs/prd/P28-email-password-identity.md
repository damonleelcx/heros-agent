# PRD — P28: Email & Password Identity (a person can obtain their own way in)

| Field | Value |
|---|---|
| Phase / Milestone | P28 / M18 (front-door usability; downstream of P22 identity and P27 account system) |
| Target window | Lands as one wave; blocks every self-serve claim P27 made |
| Lead role(s) | Product Designer + Backend (co-leads) |
| Supporting role(s) | System Designer, Frontend, DevOps, QA Engineer, Sales Operations |
| Status | **Deployed 2026-08-06** (image `a4b854c`) — ⚠️ **§12: the first-owner path does not work as shipped** |
| OpenSpec change | `p28-email-password-identity`; follow-up `p28-first-owner-reachability` |

> **The one-sentence job.** *Let a person create their own credential and use it* — an email address and a
> password they chose — **in the browser and in the terminal**, without anybody running a command against the
> production cluster on their behalf.

> **Scope discipline.** P28 adds **one identity mechanism** and the recovery paths that make it operable. It
> does not change what a session is, what a credential authorises, how seats are counted, or how anything is
> billed. Every surface above the identity seam — `session.ts`, `cookies.ts`, `middleware.ts`, `scope.ts`,
> `auth.Registry`'s contract — is unchanged, and that is asserted by test rather than assumed.

---

## 1. Summary

P27 gave the platform a durable person, a durable organization, and a sign-up service. What it did not give
anybody was **a way to hold a credential of their own**. Production runs the `configured` identity seam, whose
entire mechanism is a JSON object of `{assertion: tenant_id}` injected from a Kubernetes secret. To sign in, a
person must be handed one of those strings by whoever operates the cluster. The sign-in page says so in its own
copy — *"whoever runs it gives you this value"* — and the current onboarding instruction for a new user is,
literally, two AWS SSM commands:

```
aws ssm start-session --target i-05f4712279b04fac5 … kubectl -n heros get secret heros-console …
```

That is not a sign-in. It is an operator reading a shared secret out of a cluster and passing it to a human over
a chat client. It fails on four of the eight priority levels at once:

- **L1 security** — the credential is *shared*, *never rotated*, *not bound to a person*, and travels through
  whatever channel the operator happened to use. Nothing it does can be attributed, and revoking one person
  means revoking everybody who was ever given the same string.
- **L3 user complexity** — the front door requires cluster access. There is no number of users for which this
  works.
- **L4 operations** — every new person is an operator task, and an operator task on the production cluster.
- **L1 again, differently** — the same string is the *only* factor. There is no second one, and no way to add
  one, because there is no account for it to attach to.

`heros login` is in the same position from the other side. Its device flow is correct and complete — it mints a
credential that *names a person*, which is the thing that makes offboarding true in a terminal — but step 2 of
that flow is "sign in to the console", so it inherits the SSM command exactly.

P28 makes the person the source of their own credential. They give an address and choose a password; the
platform stores a memory-hard hash of the password and nothing else; they verify the address; they can reset it
without a human. The same pair works in the terminal. `configured`, `oidc`, `saml` and `platform` are all
untouched and all still selectable — an air-gapped install that federates with nobody keeps exactly what it has.

## 2. Problem & context

Six facts, each checked in the repository rather than remembered.

- **🔴 A person cannot obtain a credential.** The production console runs
  `CONSOLE_TENANT_IDENTITY=configured`. [`identity.ts`](../../web/console/src/lib/identity.ts) resolves a
  sign-in by `map[value]` against `CONSOLE_TENANT_ASSERTIONS`, a JSON object injected from a secret. There is
  no mint, no rotation, no per-person row, and no self-service anything. The page's own hint — added when
  somebody asked "how do my users know what to use?" — answers *ask whoever runs this install*, which is an
  accurate description of the defect rather than a fix for it.
- **🔴 The shared string is not a person, so P27's offboarding promise is void on this seam.**
  `tenancy.RemoveMember` is atomic across membership, sessions and personal credentials, and the removal
  preview discloses what it will *not* revoke. None of that reaches a `configured` assertion: the assertion is
  not a credential row, so removing the member leaves the string working for everybody who has it — including
  the person removed.
- **🔴 There is no password anywhere, and the one hash function that exists must not become one.**
  `tenancy.HashSecret` is SHA-256 and its comment explains precisely why that is correct: it hashes a 256-bit
  value *we* minted, so there is no dictionary to slow down. Every word of that reasoning inverts for a
  human-chosen password. Reusing it would be the textbook L1-for-L8 trade.
- **🔴 There is no mail sender in the repository.** Grep for `net/smtp` outside an import-fence allowlist and
  there are zero hits. Email verification, password reset and invitation delivery all assume a channel that
  does not exist — which is also why `tenancy.Invitation` has been a complete data model with no way to tell
  the invitee about it.
- **🔴 `/signup` cannot be the first screen, because it requires a session.**
  [`signup/page.tsx`](../../web/console/src/app/signup/page.tsx) reads the session and renders *"Sign in
  first"* when there is none — correct for the federated seams, where the identity provider vouches for you
  before you ever reach us, and circular for a self-serve product where signing up is *how* you get a session.
- **🔴 The session `Purpose` enum is a denylist at the point it matters.**
  [`auth/durable.go:210`](../../internal/auth/durable.go) refuses `PurposeConsole` by name. Any purpose added
  later is accepted as a platform API credential by default. Nothing exploits this today because there are
  exactly two purposes — and P28 is the change that would have added the third.

Those are not six problems. They are one: **the platform models people but issues them nothing.**

## 3. Goals / Non-goals

### Goals

| # | Goal | Done when |
|---|---|---|
| G1 | A person creates an account with an email and a password, unaided | `/signup` on a fresh browser produces a working session with no operator involved |
| G2 | A person signs in with that pair | `/signin` takes an address and a password; the SSM command appears in no runbook |
| G3 | The same pair works in the terminal | `heros login` authenticates by email + password and stores a **personal** credential |
| G4 | A forgotten password is recoverable without a human | `/forgot-password` → email → `/reset-password` → signed in |
| G5 | An address is proved before it can spend or send | Verification gates invitations and paid upgrades, and nothing else |
| G6 | Every existing seam still works | `configured`, `oidc`, `saml`, `platform` unchanged and still selectable |
| G7 | The password is unguessable in bulk if the database leaks | argon2id, per-row salt, algorithm-tagged, re-hashed on parameter change |

### Non-goals

- **TOTP / WebAuthn second factors.** The account row P28 creates is the thing a second factor attaches to;
  attaching one is a later phase, not a smuggled-in extra. *(Operator MFA already exists and is untouched —
  see [P22](P22-sso-identity.md).)*
- **Replacing SSO.** A customer whose IT organization federates keeps federating. P28 is what an install
  without an IdP has instead, and what the hosted product uses.
- **Social sign-in.** No Google/GitHub buttons. Each is a separate published contract with its own consent
  surface.
- **Changing the session, cookie, scope or entitlement model.** Untouched, and fenced.
- **A password policy engine.** One length floor and a breach-list check are the whole policy; a composition
  rule ("one symbol, one digit") measurably produces `Password1!` and is not proposed.

## 4. Scenario stories

> Written per [场景故事驱动设计](../../../aikeylabs-skills/senior-product-designer-workflow/references/场景故事驱动设计.md):
> who, in what situation, hit what wall, and what they do *instead* today.

### S1 — Priya evaluates the product on a Tuesday afternoon

Priya runs a four-person team and has ten minutes. She reads the marketing page, clicks *Get started*, and
wants an org she can invite two colleagues into before her next meeting.

**Today:** she reaches `/signup`, which says *Sign in first*. She clicks *Sign in*, which asks for a "Tenant
credential" and explains that whoever runs the install will give her one. There is nobody to ask. She leaves.

**After P28:** `/signup` asks for her work address, a password, and what to call the organization. She is
signed in on submit. A banner says her address is unverified and that invitations unlock once she confirms;
the mail arrives; she confirms; she invites two colleagues. Elapsed: under four minutes, no operator.

### S2 — Marco comes back after three weeks and has forgotten the password

**Today:** not expressible — there is no password.

**After P28:** *Forgot your password?* on the sign-in page. He types his address and gets the same neutral
confirmation whether or not the address is known. The mail carries a single-use link valid for one hour. He
sets a new password, is signed in, and **every other session and every personal credential he holds is
revoked** — including the one on the laptop he lost, which is the reason he was resetting.

### S3 — Dana wires the CLI into CI at 11pm

**Today:** `heros login` prints a code and a URL; the URL is the console; the console wants the SSM string;
Dana files a ticket and stops.

**After P28:** `heros login` prompts for her address and password, mints a **personal** credential, stores it
`0600` and prints who it belongs to. For the CI job itself she uses `--token` with a machine credential from
`/app/settings/credentials`, because a personal credential in CI dies when she leaves the company — and the
CLI's own output says which kind it just stored, so the difference is not folklore.

### S4 — Ravi operates an air-gapped install for a bank

**Today:** he sets `CONSOLE_TENANT_ASSERTIONS` and hands out strings. It works, because there are eleven users
and he knows all of them.

**After P28:** *nothing changes for him unless he wants it to.* `configured` is still a supported kind. If he
does switch to `password`, sign-up stays off (`HEROS_SELF_SERVE_SIGNUP=0`, still the default) and he creates
the first owner with a one-time bootstrap credential the installer prints once; everyone else arrives by
invitation. He never configures SMTP if he does not want to — the platform surfaces the invitation and reset
links to him instead of dropping them, **loudly**, because a mail path that silently discards is how somebody
learns their reset never worked by never receiving it.

### S5 — Priya's colleague leaves

**Today (on `configured`):** she removes them from `/app/settings/members`, the screen says access has ended,
and the shared assertion string still signs them in. The screen is wrong and nothing says so.

**After P28:** removal revokes their sessions and their personal credentials at the next request, exactly as
P27 already implements — and now there is nothing else for them to hold. The removal preview still lists the
machine credentials it is *not* touching.

## 5. Noun dictionary

> Per [名词字典与界面 IA 规范](../../../aikeylabs-skills/senior-product-designer-workflow/references/名词字典与界面IA规范.md):
> three layers kept separate — interface copy, domain entity, code identifier. A word that means two things is
> a defect, not a synonym.

| Interface copy | Domain entity | Code identifier | Notes |
|---|---|---|---|
| Email address | `User.Email` | `email` | 🔴 On the `password` seam it is **also** the identity (`subject`). Everywhere else it stays a display attribute. §7.1 explains why that is not a contradiction. |
| Password | — | `password` (never stored) | Never persisted, never logged, never in a URL, never in a JSON response. |
| — (invisible) | `UserPassword` | `userpassword` | The stored argon2id encoding. It has no interface name because a user never sees or names it. |
| Sign in | — | `signin` | The act. Not "log in" — one verb, everywhere, including the CLI. |
| Sign up / Create your account | `signup.Request` | `signup` | "Create an organization" is what the *existing* federated flow does with a session already in hand. They are two different screens and keep two different names. |
| Confirm your email | `IdentityToken{Purpose: verify_email}` | `PurposeVerifyEmail` | Never "activate" — an unverified account is not deactivated, it is limited. |
| Reset your password | `IdentityToken{Purpose: reset_password}` | `PurposeResetPassword` | Never "recover" — nothing is recovered; the old password is gone forever. |
| Unverified | `User.EmailVerifiedAt == nil` | `email_verified_at` | Shown as a banner state, never as an error. |
| Personal credential | `Credential{UserID: set}` | `Kind() == "personal"` | Unchanged from P27. `heros login` mints one. |
| Machine credential | `Credential{UserID: ""}` | `Kind() == "machine"` | Unchanged. `heros login --token` uses one. |
| Sign-in method | `IdentityProviderKind` | `CONSOLE_TENANT_IDENTITY` | The five kinds. A customer never reads this word; an operator does. |

🚫 **Banned words**, with what to say instead: *login* (noun) → "sign-in"; *account activation* → "confirm your
email"; *credentials* (plural, meaning the pair) → "your email and password"; *token* on any customer-facing
password screen → nothing, the reader does not have one.

## 6. Information architecture

Five unauthenticated pages, all outside both the public and console shells for the reason `/signin` already
carries its own: a page whose whole job is that you are not signed in must not render a shell that assumes you
are.

```
/signin              email + password  ·  "Forgot your password?"  ·  "Create an account"
/signup              organization name + email + password          ·  "Sign in"
/forgot-password     email                                          → neutral confirmation, always
/reset-password?t=   new password (token in the query, spent on submit)
/verify-email?t=     no fields — one confirmation, then onward
```

and one authenticated addition:

```
/app/settings/account   change password · resend confirmation · sessions and personal credentials
```

**🔴 `/app/settings/account` is where a password is changed, not `/app/settings/members`.** Members is *other
people*; the thing you are changing is yours. Putting a self-service action on the administration screen is how
an admin ends up changing their own password from a row that looks like it belongs to somebody else.

**Left navigation gains nothing.** Per the IA rule that the left rail holds task domains and never actions,
"Account" appears under Settings, which already exists.

## 7. Functional requirements

### 7.1 Identity

**FR1 — The `password` sign-in method is a fifth seam kind, not a replacement.**
`CONSOLE_TENANT_IDENTITY=password` selects it. `configured`, `oidc`, `saml`, `platform` and `dev` behave
exactly as they do today, and the seam contract (`verify → {tenantId, userId?}`) is unchanged.

**FR2 — On the `password` seam, the person is `(issuer="password", subject=<normalised address>)`.**

> 🔴 This looks like it contradicts the rule P27 wrote in capitals — *email is a DISPLAY attribute and never
> the identity, because an address is reassigned inside a company and a subject is not.* It does not, and the
> distinction is worth stating once, properly.
>
> That rule is about **federated** identity, where the IdP owns the address and can reassign it underneath us:
> the new hire who inherits `sales@` would inherit the previous holder's account, and the IdP's `sub` is the
> thing that does not move. On the `password` seam **there is no IdP and no `sub`** — the address *is* what
> the person proved, by receiving mail at it, and there is no other stable identifier in existence. Inventing
> an opaque subject would not add stability; it would only hide which address it stood for.
>
> What we keep from the rule is its actual content: the **internal `user_id` is still the key** every other
> table references, so an address change rewrites one column and nothing else, and a `password` user and an
> `oidc` user with the same address are two different people until somebody deliberately links them.

**FR3 — Passwords are stored as argon2id, algorithm-tagged, and never anything else.**
Encoding: `$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`. Parameters live in one constant. A sign-in whose
stored encoding does not match the current parameters re-hashes on success, so raising the cost is a deploy and
not a migration. 🔴 `tenancy.HashSecret` (SHA-256) is used for minted tokens and **may never** receive a
password; this is fenced by test.

**FR4 — Password policy is a floor and a blocklist, and it is stated before the field is submitted.**
Minimum 12 characters. Rejected if it appears in the bundled common-password list, or if it contains the user's
own address. No composition rules. The rule is rendered next to the field, not discovered on submit.

**FR5 — Sign-in refuses unknown address and wrong password identically.**
One message, one status, one timing profile: the comparison always performs a full argon2id verification,
including against a fixed decoy encoding when no user exists, so response time does not disclose whether the
address is registered. This mirrors `auth.RefusalCause`, which already keeps the distinction server-side.

**FR6 — Repeated failures lock the *account*, and say so plainly.**
Ten consecutive failures within fifteen minutes locks sign-in for that user for fifteen minutes. The message
names the lock and its duration — a person who mistyped four times must not be left thinking the product is
broken. A successful sign-in and a successful reset both clear the counter. Locking is per-user and not per-IP:
an IP lock is a denial-of-service surface pointed at a shared office.

### 7.2 Sign-up

**FR7 — `/signup` on the `password` seam creates the person, the organization, the owner membership, the Free
account and the password in ONE act, or none of them.**
It reuses `signup.Service.Create`, which is already atomic across the identity and billing stores; the password
write joins the same transaction. A half-created account is unrecoverable through the product.

**FR8 — Sign-up remains governed by the declared posture.**
`HEROS_SELF_SERVE_SIGNUP` stays off by default. `password` + self-serve off = invitation-only, which is exactly
what an air-gapped install wants.

**FR9 — A sign-up whose address already has a password is refused with the same neutral message as a
successful one is confirmed**, and an email is sent to the existing address saying somebody tried. Registration
must not be an oracle for "does this person have an account here".

**FR10 — An unverified account may sign in and use the product. It may not invite anybody, and it may not move
to a plan that charges.**
Both gates exist because both spend something the account has not proved it owns: an invitation puts mail in a
third party's inbox under our name, and an upgrade takes money. Everything else is available, because a
product that blocks the door until an email arrives fails for every recipient whose mail is slow.

### 7.3 Recovery

**FR11 — `/forgot-password` answers identically for every address**, known or not, well-formed or not.

**FR12 — A reset link is single-use, expires in one hour, and is spent at the store.**
Single-use is a database property, exactly as `invitation.accepted_at` already is — not caller logic.

**FR13 — 🔴 Completing a reset revokes every session and every personal credential that person holds.**
This is the whole point of a reset: the common reason to reset is that something was compromised, and a reset
that leaves the attacker's session live has done nothing. Machine credentials are untouched and the completion
screen says so, in the same shape as the removal preview.

**FR14 — Changing a password from `/app/settings/account` requires the current one**, and revokes every *other*
session, keeping the one in use.

### 7.4 Email delivery

**FR15 — Mail goes through one seam with two implementations: SMTP, and an operator-visible fallback.**
No deployment is required to configure SMTP. A deployment that has not configured it does **not** silently
discard: every message is written to the operator surface with a WARN log, and the console's own health surface
reports `mail: not configured`. 🔴 Silently dropping a reset mail is indistinguishable, to the person waiting,
from a product that does not work.

**FR16 — A message body carries a link and no secret beyond it**, and the link is a single-use token bound to
one purpose and one user.

**FR17 — Mail failure never fails the act that triggered it.** A sign-up whose confirmation mail bounces is
still a sign-up; the person can resend from `/app/settings/account`. The reverse — rolling back an account
because a mail server was down — loses the customer for an outage that is ours.

### 7.5 CLI

**FR18 — `heros login` authenticates with an email and a password.**
Address from `--email`, `$HEROS_EMAIL`, or a prompt. Password from `$HEROS_PASSWORD`, stdin, or a **hidden**
prompt. 🚫 There is no `--password` flag: a password in `argv` is a password in the shell history and in every
`ps` on the machine.

**FR19 — The non-interactive contract survives.** With no TTY and no supplied values, the command **refuses
with a message naming the three ways to supply them**. It does not hang on a prompt nobody can see. This is the
P11 contract — machine output on stdout, narration on stderr, no TTY required — kept rather than eroded.

**FR20 — The credential minted is PERSONAL**, carries the device label, and is revoked when the person is
removed. `--token` remains the machine path and is unchanged. The device flow remains available at
`heros login --device` for a terminal where typing a password is not wanted.

**FR21 — `heros status` continues to name the person and the organization and to print no secret.** Unchanged.

## 8. Error copy

> Per the error-message rule: an error names *what happened* and *what to do next*. A message that only names
> the failure is a dead end wearing a red border.

| Situation | Copy | Why this wording |
|---|---|---|
| Wrong password / unknown address | **That email and password did not match.** Check them and try again, or reset your password. | One message for both. Naming which half was wrong is an account-enumeration oracle. |
| Locked | **Too many attempts. Try again in 15 minutes**, or reset your password to sign in sooner. | The duration and an escape hatch. Without the escape hatch a locked user has nothing to do but wait. |
| Password too short | **Use at least 12 characters.** A short sentence works well. | Rendered *before* submit, and the advice is actionable. |
| Password too common | **That password appears in public breach lists.** Choose one you have not used elsewhere. | Says why, without implying we saw their other accounts. |
| Address already registered | *(none — the neutral confirmation)* | FR9. The information goes to the address, not to the screen. |
| Unverified, inviting | **Confirm your email before inviting people.** We sent a link to `p@example.com` — resend it. | Names the address so a typo is visible, and offers the fix inline. |
| Unverified, upgrading | **Confirm your email before changing to a paid plan.** | Same shape. |
| Reset link expired/used | **This link is no longer usable.** Request a new one. | One message for expired, spent, and unknown — same reasoning as `tenancy.ErrDeviceCode`. |
| Mail not configured (operator) | **`mail: not configured` — this deployment cannot send confirmation or reset links. Links are being written to the operator log instead.** | An operator reads this. It is allowed to name the mechanism, and it must. |
| Sign-up disabled | **This install does not offer sign-up.** Ask whoever runs it for an invitation. | Existing copy, kept. |
| CLI, no TTY | **`login: no terminal to prompt on.` Supply `--email` and set `$HEROS_PASSWORD`, or pipe `email\npassword` on stdin.** | FR19 — the three ways, named. |

## 9. Acceptance checklist

- [ ] A browser with no cookies reaches `/signup`, creates an account, and lands on `/app` — no operator action
- [ ] The confirmation mail arrives (SMTP configured) or appears on the operator surface with a WARN (not)
- [ ] Sign out; sign in with the same pair; land on `/app`
- [ ] `/forgot-password` with a known and an unknown address are byte-identical responses
- [ ] A reset link used twice fails the second time
- [ ] After a reset, a session captured beforehand is refused **at the next request**
- [ ] An unverified account is refused an invitation and a paid upgrade, and allowed everything else
- [ ] `heros login` with `--email` + `$HEROS_PASSWORD` stores a credential whose `credential_kind` is `personal`
- [ ] `heros login` with no TTY and no values refuses, naming the three ways
- [ ] `heros status` names the person and organization and prints no secret
- [ ] Removing that person makes the next CLI request fail with no restart
- [ ] `CONSOLE_TENANT_IDENTITY=configured` still signs in exactly as before
- [ ] The SSM command appears in no runbook, README, or onboarding document
- [ ] `kubectl apply -f prod.yaml` leaves every public platform route reachable (§10)
- [ ] 🔴 **The first owner of a deployment still running `configured` can open the mailed link and set a
      password** — the one item this list was missing, and the one that fails (§12.1)
- [ ] 🔴 **The mail that proves delivery was sent by the deployed workload**, not by a build host (§12.3)

## 10. Delivery

**🔴 The ingress allowlist is part of this change, not adjacent to it.** The production ingress routes a
hand-maintained list of platform paths to `agentd`. It carried three; the two device-authorization paths were
added by hand, out of band, and the next `kubectl apply -f prod.yaml` deletes them. P28 adds six more public
paths to that list. Shipping them into a manifest that will delete them is shipping a sign-in that stops working
on the next deploy, so P28 puts the list in the checked-in manifests and adds a test that fails when a public
platform route has no ingress entry.

**Environments.** Per the three-environment rule, integration is not passed until the flow is exercised on the
pre-production cluster, on a local macOS install, and on Windows. The Windows leg is not ceremonial here: the
CLI's hidden password prompt is the single most platform-dependent thing in this change.

## 11. Cross-references

- [P22 — SSO & Identity](P22-sso-identity.md) — the seam P28 adds a kind to; ADR-008 is its record
- [P27 — Account System](P27-account-system.md) — the person, membership, credential and session rows P28 issues against
- [P21 — Stripe Payments](P21-stripe-payments.md) — the paid-plan gate FR10 attaches to
- [P11 — CLI & CI Integration](P11-cli-ci-integration.md) — the non-interactive contract FR19 preserves
- ADR-012 — email/password as a first-class identity seam (decisions and rejected alternatives)

## 12. What the deployment found (2026-08-06)

P28 was deployed to the hosted cluster on 2026-08-06 as image `a4b854c`. The platform reports
`mail_configured: true`, `self_serve_signup: true`, `store: postgres`, `status: ready`. Three things this
document asserted turned out not to hold, and they are recorded here rather than quietly repaired,
because two of them are requirements this PRD never wrote down.

### 12.1 🔴 The first owner cannot obtain a password — FR22, new

**FR22 — Spending a valid identity token does not depend on the sign-in seam.**

The bootstrap owner was P28's answer to the lockout objection: naming an address adopts them as owner of
an existing organization and mails them a single-use password-set link, so flipping the seam "no longer
strands the tenant holding the data". On the deployed cluster the adoption worked and the mail was
delivered. **The link does not work:**

```
GET /reset-password?t=<valid token>   →  307  →  /signin
```

Every page that could spend that token is gated on the seam *already* being `password`
(`(identity)/reset-password/page.tsx:64` and the same line in `forgot-password`, `verify-email`,
`create-account`, where `passwordSignInEnabled()` is `PROVIDER === "password"`).

So the safety step and the thing it protects block each other. The delivery order in
`docs/runbooks/p28-smtp-setup.md` §6 — set the password, *then* flip — **cannot be executed**, and it is
the order the entire "this is not a lockout" argument rests on. The flip is available only as a leap.

This is an L3 failure reached through an L2 one, and it may not be traded against implementation cost.
What the reader experiences: a mail saying *"This install named you as its first owner"*, a click, and a
form headed **"That sign-in was not accepted."** — a sentence that is false about their situation, on a
page they were not trying to reach, about a credential they never presented.

> **How it passed.** The console suite was green at 618 tests. It drives handlers and never loads a
> non-`password` seam, so the redirect that makes the feature unreachable is invisible to all of them.
> The acceptance list above asserted that `configured` *still signs in as before* — it never asked
> whether a person could **arrive** at `password` from `configured`, which is the only path any real
> deployment takes.

Resolution and the open design fork are in `openspec/changes/p28-first-owner-reachability` (**D1**, which
is a decision for the deployment owner and is deliberately not taken there).

### 12.2 Is the tenant-credential form still needed?

Asked directly during the deploy, and worth answering in the PRD because it is a product question, not a
configuration one.

- **On the hosted deployment — no, once the flip lands.** `configured` exists so that somebody can be let
  in before there is an identity system. After the flip there is one, and the shared string is strictly
  worse than it on every count P28 already listed: it is not a person, it cannot be attributed, and
  removing a member does not revoke it. Keeping both means keeping a way in that survives offboarding.
- **On other installs — yes, and it is not deprecated.** An air-gapped install that federates with nobody
  and wants no password store is exactly who `configured` is for. P28 added a fifth kind; it did not
  retire the other four, and this PRD's scope discipline says so.
- **Therefore:** the tenant-credential form should disappear from *this* deployment when the seam flips,
  and remain shipped. That is a per-deployment fact, which is where it already lives. It is listed as
  task 1.2 rather than settled here, because retiring a way in is a one-way door for whoever is holding
  that string today.

### 12.3 The mail proof proved the wrong system — FR23, new

**FR23 — A deployment's mail proof is executed from the deployed workload.**

FR15 required mail to go through one seam with an operator-visible fallback, and it does. What no
requirement stated is *where the proof runs*. `make mail-proof` runs on a build host; it exercised the
credential, the relay and `internal/mailer`, and passed — while every send from the workload failed:

```
dial tcp 52.206.145.59:587: connect: connection refused
```

The production overlay set `HEROS_SMTP_PORT=587` and its egress allowlist, in the same file, opened
**443 only**. The build host is subject to no NetworkPolicy, so the proof could never have crossed the
thing that was broken. Fixed in the overlay; the fence that would catch the next one is task 7.3.

> The runbook already called the inbox "the layer people skip". This is the layer before it, and it was
> skipped by a green check — which is worse, because a green check is read as evidence.

### 12.4 Delivery notes that belong in §10

- **An out-of-band `kubectl set env` makes the next apply fail outright**, not merely revert it: where the
  live workload holds a literal `value` for a variable the manifest declares `valueFrom`, the API rejects
  the whole Deployment. `--server-side --force-conflicts` does not help — `env` merges by name there too.
- **`HEROS_BOOTSTRAP_OWNER_EMAIL` is declared in the manifest** with an empty value, so an apply silently
  clears it and no link is minted. `HEROS_BOOTSTRAP_OWNER_TENANT` is not declared and survives. Set the
  pair **after** the apply.
- **Self-serve sign-up cannot be offered to strangers yet.** SES production access was requested and
  **denied** (case `178599389200025`). Mail reaches separately-verified addresses only; an unverified
  stranger receives nothing **and sees no error**, because SES accepts the message and drops it. S1 —
  *"Priya evaluates the product on a Tuesday afternoon"* — is not deliverable on the hosted deployment
  today, and no sales material may claim it.
