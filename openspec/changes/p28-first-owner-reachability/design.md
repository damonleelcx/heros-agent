# Design — first-owner reachability

## Context

P28's identity seam has five kinds. Four of them (`configured`, `oidc`, `saml`, `platform`) answer the
question *"how does a person prove who they are when they sign in?"*. The fifth, `password`, answers the
same question **and** brings with it a second family of surfaces that answer a different question
entirely: *"how does a person who cannot sign in obtain the ability to?"* — sign-up, address
confirmation, forgotten-password, and the first-owner bootstrap.

The defect is that both families were put behind the same switch. `passwordSignInEnabled()` reads
`PROVIDER === "password"` and is called by all four recovery pages. That reads as obviously correct —
these pages *are* the password feature — and it is wrong for one case, which happens to be the first
case every deployment meets.

**The evidence, from the deployed cluster:**

| Path | Result |
|---|---|
| `/reset-password?t=<valid token>` | `307 → /signin` |
| `/forgot-password` | `307 → /signin` |
| `/verify-email` | `307 → /signin` |
| `/create-account` | `307 → /signup` |
| `/signin` | `200` |

The owner row and the token were both written correctly (`usr_e2204f72f2ee3a237bd56f97`, `owner`,
`active`; two unconsumed `reset_password` tokens). Nothing in the platform failed. The link was
delivered and accepted by the relay. The reader still cannot use it.

---

## D1 — 🔴 OPEN DECISION: how the recovery pages stop depending on the seam

> Per the cost-escalation rule, a correct-but-costly path is **not** silently downgraded, and this
> decision is **not** taken here. Both paths are specified with their costs; the choice is the
> deployment owner's.

**What problem is being solved.** A person holding a valid single-use token cannot spend it, because the
page that spends it is gated on a deployment-wide sign-in setting that has nothing to do with the token.

### Path A — the token is the authority; recovery is ungated

`/reset-password` and `/verify-email` serve whenever a token is presented, under every seam. The seam
continues to govern `/signin`, `/signup` and `/create-account`.

- **Why it would be right.** These pages already refuse everything a bad token can be — expired, spent,
  unknown — with one message, and single-use is enforced at the store rather than by caller logic. An
  ungated page with no valid token is a page that renders "this link is no longer usable". The gate is
  therefore doing no security work; it is doing *feature-flag* work on a surface that is not the feature.
- **L-law reading.** Removes an L3 failure at no L1 cost: the authority checked is unchanged, and the
  set of things a holder of a valid token can do is unchanged.
- **Cost.** One line per page plus a test that the ungated page still refuses a bad token under a
  non-`password` seam. Small — and L8 is the lowest priority, so its smallness is not the argument.
- **Risk to state honestly.** An install that deliberately runs `oidc` now serves a
  `/reset-password` page that can, if somebody holds a token, mint a password credential. That is only
  reachable by a token the platform itself minted, so it is not a new authority — but it is a **new
  surface on an install that chose not to have one**, and an operator of an air-gapped bank install is
  entitled to be told rather than to discover it.

### Path B — the bootstrap opens the door it needs, and closes it

The seam gate stays. `HEROS_BOOTSTRAP_OWNER_EMAIL` additionally sets a deployment state — "a first owner
is pending" — which makes exactly `/reset-password` reachable, and which clears when the password is
set.

- **Why it would be right.** The exception is scoped to the one case that needs it, and self-closing. An
  `oidc` install that never names a bootstrap owner grows no new surface at all.
- **L-law reading.** Also removes the L3 failure, and scores better on L1 (no new surface where none was
  asked for).
- **Cost.** A new piece of deployment state that must be read by the console, which today knows nothing
  about the platform's bootstrap. That is a **new cross-process contract** — an L5 concern
  (evolvability), and contracts are one-way doors. It is the larger change by a wide margin.
- **Risk to state honestly.** State that "clears when the password is set" is a second thing that can be
  wrong. If it fails to clear, the install keeps a reachable reset page and nobody notices, because the
  symptom is a page that exists rather than one that errors.

### What is NOT a path

- **"Flip the seam first and accept the leap."** This is what the deployment must do *today* because
  nothing else is available, and it is precisely the unverified state the bootstrap owner exists to
  remove. Recording it as the design would be adopting an incident as an intention.
- **"Document the order as flip-then-set-password."** Same objection, plus it makes the safety claim in
  P28's proposal false in writing rather than in fact.

---

## D2 — A gated identity page states its reason (no open decision)

**What problem is being solved.** `redirect("/signin")` discards the fact that the reader arrived with a
token. They land on a form for a credential they do not have, and — if they submit it empty, as the first
reader did — on `That sign-in was not accepted. The credential your browser presented did not verify.`
Every word of that is false about their situation.

**Why this design.** The error-copy rule requires a message to name what happened and what to do next,
and the visibility rule requires that "configured but not in effect" be visible where the reader passes.
A silent 307 fails both.

**Why it is appropriate.** The reader cannot fix this themselves, so per the visibility rule
(`fixable → insistent; not fixable → dismissible`) it is a stated reason on arrival, not a permanent
banner. It names no internal mechanism: the commercial-leak rule forbids naming
`CONSOLE_TENANT_IDENTITY`, the seam, or the deploy to a person who is not an operator.

**Alternative rejected.** Rendering the reset form anyway and failing on submit — that spends the
reader's chosen password against a page that was never going to work, and teaches them the product is
broken rather than not yet switched on.

**Outcome.** One read, one action: the reader learns the install has not enabled password sign-in and is
told to contact whoever runs it. The operator learns the same fact from `/readyz`, which already reports
`self_serve_signup` and `mail_configured` and is the correct place for the mechanism's name.

---

## D3 — A mail proof that runs where the product does not run is not a proof

**What problem is being solved.** `make mail-proof` exercised the SMTP credential, the relay, and
`internal/mailer` — and passed — while every send from the actual workload failed, because the proof ran
on a build host and the failure was a NetworkPolicy the build host is not subject to.

**Why this design.** The rule generalises past SMTP: a dependency's reachability is a property of the
*place the process runs*, and any proof executed elsewhere is evidence about a different system. The
existing runbook already said the inbox is "the layer people skip"; this is the layer before it, and it
was skipped by a green check.

**Why it is appropriate.** It costs nothing to state and converts a class of false green into a red one.
The four-layer verification in the runbook keeps its shape; only the *location* of layer 1 changes.

**Alternative rejected.** Adding port 587 to a checklist. A checklist entry is re-read by whoever
remembers to; the deploy that broke this had a green proof and a reviewed manifest.

**Outcome.** The health surface answers the question the proof used to be asked: `mail_configured` is
reported by the workload itself, from inside the pod, and a send failure is a WARN on that workload.

---

## D4 — The drift trap, recorded because it makes a deploy unrunnable

Where the live workload carries a literal `value` for a variable the checked-in manifest declares as
`valueFrom`, a strategic merge produces an entry with both, and the API **rejects the whole Deployment**:

```
spec.template.spec.containers[0].env[33].valueFrom: Invalid value: "":
  may not be specified when `value` is not empty
```

⚠️ `kubectl apply --server-side --force-conflicts` does **not** resolve this — `env` merges by `name`
there too, and the same rejection is returned for the same entries.

The shape that works is a **single JSON patch** replacing each drifted entry with the manifest's, guarded
by `test` ops on the index:

```json
[{"op":"test","path":"/spec/…/env/21/name","value":"ADMIN_IDP_ISSUER"},
 {"op":"replace","path":"/spec/…/env/21","value":{"name":"ADMIN_IDP_ISSUER","valueFrom":{…}}}]
```

**Atomic is the point, not the style.** Clearing the literals in one step and applying in a second leaves
an intermediate pod template with `ADMIN_IDENTITY_MODE=oidc` and an empty issuer, and `adminlaunch`
refuses that boot — on a single-replica deployment, a surface that does not come back.

---

## Risks

| Risk | Handling |
|---|---|
| D1 stays open and the flip is taken anyway | That is the current state, and it is stated as such in the runbook rather than presented as safe. The token is re-minted on every restart until a password exists, so a failed flip is recoverable by flipping back — the assertions secret is untouched by the flip. **This rollback has not been exercised**, and is recorded as a claim, not a proof. |
| Path A widens the surface on installs that chose not to have one | Named in the air-gapped install documentation, not left to discovery. |
| The acceptance test is written against the post-flip state and passes vacuously | The test's precondition is a **non-`password` seam**; a test that cannot fail before the fix is not a fence. It is required to be observed red first. |
