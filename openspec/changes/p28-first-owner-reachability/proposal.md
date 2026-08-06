## Why

P28 shipped and was deployed to the hosted cluster on 2026-08-06. The deploy proved that **the first
owner of a deployment cannot obtain a password**, which is the one thing P28 exists to make possible.

`HEROS_BOOTSTRAP_OWNER_TENANT` was P28's answer to the lockout objection: it adopts a named address into
an existing organization as owner and mails them a single-use password-set link, so flipping
`CONSOLE_TENANT_IDENTITY` from `configured` to `password` "no longer strands the tenant holding the
data". On the deployed cluster it did adopt the owner, and it did mail the link. The link does not work:

```
GET https://heros-agent.space/reset-password?t=…   →  307  →  /signin
```

Every page that could consume that token is gated on the seam already being flipped —
`web/console/src/app/(identity)/reset-password/page.tsx:64`, and the same line in `forgot-password`,
`verify-email` and `create-account` — where `passwordSignInEnabled()` is `PROVIDER === "password"`.

So the safety step and the thing it protects are **mutually blocking**. The runbook's documented order
(`docs/runbooks/p28-smtp-setup.md` §6) is:

1. name the bootstrap owner
2. **sign in with the new password**
3. only then switch the console's seam

Step 2 cannot be performed before step 3, because step 2's page is unreachable until step 3 lands. **The
ordering is not merely wrong, it is unexecutable** — and it is the ordering the whole "this is not a
lockout" argument rests on. The flip is therefore a leap taken on the belief that the link will work
once the seam changes, which is exactly the unverified state the bootstrap owner was introduced to
remove.

Two further defects found by the same deploy, both of which had been green:

- **agentd was forbidden the port its own manifest configured.** The prod overlay set
  `HEROS_SMTP_PORT=587` and its egress allowlist opened 443 only, so the first real send failed with
  `dial tcp …:587: connect: connection refused`. `make mail-proof` runs on a build host and crosses no
  NetworkPolicy, so it could never have caught this. Fixed in the overlay; the *rule* that would have
  caught it does not exist yet.
- **A deployment that has drifted cannot be re-applied at all.** Where the live workload carries a
  literal `value` for a variable the manifest declares as `valueFrom`, `kubectl apply` is rejected
  outright (`may not be specified when value is not empty`), and `--server-side --force-conflicts` does
  not rescue it because `env` merges by `name` there too.

Under the eight-level priority law this is an **L3 (user complexity)** failure reached through an
**L2 (stability)** one, and it may not be traded against L8. The reader's experience is: they receive a
mail that says *"This install named you as its first owner"*, click it, and land on a sign-in form
titled **"That sign-in was not accepted"**. Nothing on the screen names the cause, and the cause is a
deployment setting they cannot see and could not change.

## What Changes

- **The credential-recovery pages stop depending on the sign-in seam.** Consuming a valid, unexpired,
  single-use identity token is not the same act as offering password sign-in, and it SHALL NOT be gated
  on it. Which of the two available shapes to adopt is an **open decision (D1, `design.md`)** — this
  change specifies the required *behaviour* and records both paths with their trade-offs rather than
  picking one unilaterally.
- **A gated identity page stops redirecting silently.** A reader arriving with a token on a deployment
  that cannot serve them is told what happened and what to do, per the error-copy rule that a message
  names *what happened* and *what to do next*.
- **The first-owner path gets an acceptance test that ends at the reader's eyes** — a token minted on a
  deployment in its pre-flip state must be consumable, asserted end to end rather than by unit tests
  that never load a seam.
- **Mail becomes provable from where the product runs, not from where it is built.** A proof executed on
  a build host is declared insufficient by spec.
- **The seam flip acquires a written, executable order**, replacing runbook §6's unexecutable one, and
  the drift trap that makes `kubectl apply` fail is recorded with the patch shape that works.
- 🚫 **Not in scope.** No change to what a session is, what a credential authorises, the password hash
  parameters, seat counting, or billing. `configured`, `oidc`, `saml`, `platform` and `dev` remain
  selectable and unchanged.

## Impact

- **Affected capabilities:** `password-identity` (recovery reachability, gated-page copy),
  `email-delivery` (proof location), `deployment-topology` (flip ordering, drift re-apply).
- **Affected code/systems:** `web/console/src/app/(identity)/*` (4 pages), `web/console/src/lib/identity.ts`,
  `internal/launch/ownerbootstrap.go` (unchanged behaviour; its log line is the operator's only evidence),
  `deploy/k8s/overlays/prod`, `docs/runbooks/p28-smtp-setup.md`.
- **Dependencies:** P28 must be deployed (it is, image `a4b854c`). This unblocks the
  `CONSOLE_TENANT_IDENTITY=password` flip on the hosted deployment, which every self-serve claim P27 and
  P28 made depends on.
- **Edition impact matrix.** Air-gapped and any install running `configured`, `oidc` or `saml` are
  affected **the same way** — the first owner of *any* of them hits this. An install already running
  `password` is not affected, because the gate it fails is already open. That is the whole population,
  and it is why this is a defect in the mechanism rather than a hosted-deployment configuration problem.
