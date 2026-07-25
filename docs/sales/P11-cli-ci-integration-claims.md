# P11 CLI & CI Integration — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-07-25)
- **Audience:** anyone who describes P11 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review.
- **Rule:** every clause here is a **commitment a customer can verify themselves**. That is what makes
  this the strongest thing we can say — and it is only true while we do not overstate it. The dry-run,
  the signed release, and the written allowlist are the evidence; lead with them.

## 10.1 The capability, with its verifiable clauses

**What P11 gives a customer:**

- A **complete CLI** — `discover`, `apply` (Variant Spec → reviewable diff, worktree-isolated), `eval`
  (multi-seed, scored, with confidence intervals from the same P4 harness the platform runs), plus
  `login`, `link`, `status`, `version`.
- A **CI integration** — a published, versioned GitHub action / reusable workflow that runs discover +
  eval on a PR, posts a check, and uploads the IR and run report as artifacts.
- **Opt-in linking** — an explicit, authenticated act that makes a run's metrics and structure legible
  in the dashboard.

**The three clauses — each demonstrable, so say them plainly:**

1. **Free on every plan, including Free.** Every capability in P11 is available on the Free plan (P7
   entitlements: CLI and discovery are the plan floor). *Verify:* it runs with no account.
2. **Works offline, with no account.** `discover`, `apply` and `eval` complete with the network denied
   and no login. *Verify:* run it on an air-gapped machine; there is an automated test that runs the
   whole local workflow with networking denied.
3. **Never transmits source, prompts, or provider keys.** The linked payload is **constructed from an
   allowlist** (metrics, IR structure, config hashes, scores, run metadata) — prompt text, source code,
   file contents, diffs, environment values and provider credentials are not fields in it and no code
   path adds them. *Verify:* run `heros link --dry-run` and read the exact bytes; they are byte-identical
   to what a real link sends.

The upgrade conversation is not "unlock the CLI" — the CLI is free. It is: **"link your runs and the
results become a dashboard your team can act on, with pull requests that arrive verified"** (Team+, per
P7). Linking is a reward (your results become comparable and shareable), never a toll.

## 10.2 🚫 Never present SUM as total spend

SUM (spend under management) is derived from **linked runs only**. The platform does **not** infer,
extrapolate, or estimate unlinked spend. So:

- Do **not** say "the platform shows your total LLM spend." Say "the platform shows the spend of the
  runs you linked, and tells you what fraction of your activity that is."
- **Link coverage is visible in the product** (beside every SUM figure) and distinguishes *complete*
  from *unknown*. Never quote a SUM figure without acknowledging its coverage — a number that reflects a
  fraction of activity, presented as the whole, is what a billing dispute is made of.
- "We inferred the rest" is not a sentence that belongs in a conversation about a customer's bill. The
  honest version — "we bill only what we observed, and we show you how much that is" — is also the more
  defensible one.

## 10.3 The security-review path is a first-class part of the funnel

This product asks to run **inside CI with repository access**. The security review is where the deal
lives or dies, so treat it as a stage of the funnel, not an obstacle — and offer the evidence **early
and unprompted**:

- **The dry-run.** `heros link --dry-run <run>` prints the exact payload without sending it. Hand a
  reviewer this before they ask. "We only send metrics" is a claim; the bytes are evidence.
- **The written allowlist.** [`docs/decisions/p11-contracts.md`](../decisions/p11-contracts.md) §1 is
  the field-by-field list, with the single source of truth in
  [`internal/runlink/allowlist.go`](../../internal/runlink/allowlist.go). It is a document a reviewer
  can read and check the payload against.
- **The signed release.** Releases are checksummed, ed25519-signed, and reproducible, with a documented
  verification step ([`docs/release/cli-verification.md`](../release/cli-verification.md)). A binary
  that runs in CI with repo access is a distribution target; offer the verification path up front.
- **The endpoint pin.** Linking transmits to **`https://heros-agent.space` and nowhere else** — a fixed,
  named destination a reviewer can allowlist at their egress boundary.
- **Build-safety.** Our availability cannot break their build: platform unavailability is reported and
  the build continues (bounded timeout); only a customer-configured gate fails it.

## 10.4 Do not promise 11b capabilities before they ship

- The **CI integration** (checks, artifacts, the action) is 11b. Do not present it as available until it
  has shipped for the customer's forge. GitHub is first-class at M14; GitLab and Bitbucket are a
  documented invocation of the same binary — say which the customer is getting.
- **Opening pull requests** is **P12**, not P11. P11's CI integration *carries* the delivery step but
  does not define it. Do not promise "verified PRs land automatically" as a P11 capability.
- Anything not in §10.1 above is not a P11 claim. When unsure, demonstrate it live or do not say it.

## The one-line version

> The CLI is **free on every plan**, runs **offline with no account**, and **never sends your source,
> prompts, or provider keys** — and you can prove all three yourself with a dry-run. Link the runs worth
> keeping, and only those, metered exactly as observed with coverage shown.
