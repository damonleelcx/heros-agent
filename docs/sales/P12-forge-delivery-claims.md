# P12 Forge Delivery — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-07-25)
- **Audience:** anyone who describes P12 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review.
- **Rule:** the honest version of this feature is a **stronger** sale than the inflated one. The thing
  that stalls source-tooling deals is repository write access; P12's default answer to it is unusually
  good, and overstating the rest only invites the security question the default already answers.

## 10.1 Lead with the default posture — you grant us nothing

**The opening line, and it is true:**

> **You grant us nothing. Your CI opens the pull request with a token it already has.**

In the default (CI-mediated) mode the platform **holds no forge credential** for the customer's
repository. Their own continuous-integration job fetches the verified diff and its evidence over an
authenticated call, applies it on a branch, and opens the pull request using the ephemeral,
repository-scoped token the CI environment already issues to its own jobs. There is no standing write
key on our side to request, store, rotate, audit, or lose.

Say it plainly in a security review: *"What happens if you're breached?" — "We never had write access to
your source."* That is the answer repository-write objections are looking for, and it is the default,
not an upgrade. It is provable, not just asserted: the platform's delivery path has **no
forge-credential store and no code path that reads one** (asserted structurally in the test suite).

## 10.2 Present the hosted App honestly — it is real standing write access

For customers who cannot or will not run delivery in CI, a hosted Git App is available. Describe it as
**what it is**, never as equivalent to the default:

- It **is** standing write access to the selected repositories. Do not soften this.
- It is **contained**: per-repository (never org-wide by default), a **least-privilege** permission set
  no broader than opening and updating pull requests, and the installation token held in a secrets
  manager that never logs it or lets it leave the platform.
- It is **customer-revocable from their own forge settings without contacting us**, and revocation is a
  reported state, not a silent stop.

A customer who chooses it should be choosing it **knowingly**. Presenting it as "the same thing, easier"
is both false and self-defeating — it re-opens the exact objection the default closes.

## 10.3 Do not oversell the automation level — a human merges

Two hard limits. Contradicting either loses trust the moment the customer opens the screen, because the
product enforces both server-side:

- **Delivery requires Team or above.** Do not promise delivery on Free. It is refused server-side,
  named, with the plan that lifts it.
- **The platform never merges below the Autonomous level (Enterprise).** Below it, the platform
  **opens and updates a pull request; a human reviews and merges.** Auto-merge exists only at
  Enterprise, and even there only for a change that passed the verification gate.

The positioning is **"a human merges."** That is a feature, not a limitation, in every source-tooling
conversation: the change arrives as a normal pull request on the customer's repository, under their own
branch protections and required checks, reviewable the way their team already reviews. Overstating
autonomy trades that away for a claim the screen contradicts.

## 10.4 Frame gainshare on merges, not on proposals opened

Gainshare is billed on **merged** deltas — a change that shipped — never on pull requests opened. A
proposal that a reviewer declines, or that is superseded, or that is closed without merging, contributes
**zero**. Only a merge into the customer's target branch, observed and recorded, is billable.

State it that way: *"You pay a share of the savings on changes you chose to merge and keep. Nothing you
declined, nothing merely suggested."* It is the honest frame and the easy one to defend — the invoice
traces to a merge commit and the verified delta behind it, and a change that was merged and later
reverted is answerable from the record as a sequence, not a disputed number.

## The one-paragraph version

> heros-agent delivers each verified optimization as a pull request on your own repository, with the
> evidence in the description — the diff, the held-out delta with its confidence interval, and a link to
> the full record. By default **your CI opens it with a token it already holds; we never receive a
> credential to your source.** A hosted Git App is available if you'd rather we open it — per-repository,
> least-privilege, and revocable by you at any time. A human merges; we don't, below Enterprise. You pay
> a share only of the savings on changes you actually merge.
