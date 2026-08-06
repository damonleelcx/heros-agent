# Tasks — first-owner reachability

> Grouped by role. 🔴 **§1 blocks everything below it**: D1 is an open decision and the shape of §3–§5
> depends on which path is taken. Nothing in §3 onward is started before it is answered.

## 1. Product Designer — the decision and the words

- [ ] 1.1 🔴 **Put D1 to the deployment owner** (`design.md`) — Path A (token is the authority) vs Path B
      (bootstrap opens a self-closing door). Present the L-law reading of each and the honest risk of
      each; do **not** pick one and present it as the plan.
- [ ] 1.2 Decide whether the hosted deployment keeps the `configured` tenant-credential form at all once
      the flip lands (PRD §12.2). It is not needed by a person after the flip; it remains the only way in
      for installs that federate with nobody.
- [ ] 1.3 Write the gated-page copy: what happened + one action, naming no seam, no variable, no
      mechanism. Anchor it against the existing "This install does not offer sign-up" line so the two
      read as one product.
- [ ] 1.4 Add the first-owner path to the noun dictionary — a person receiving that mail is the **first
      owner**, not an "admin" and not an "operator"; the operator is who runs the cluster.
- [ ] 1.5 Record the requirement in the PRD rather than only here, per requirement-spec-management: this
      affects a user path, so it is written down whether or not anybody asked.

## 2. System Designer — the seam boundary

- [ ] 2.1 State the boundary explicitly: the seam governs **who may sign in**; it does not govern **who
      may spend a token the platform itself minted**. Whichever path D1 takes, this sentence is the
      invariant it must satisfy.
- [ ] 2.2 Check the same conflation elsewhere — every caller of `passwordSignInEnabled()` is a candidate
      (`verify-email`, `forgot-password`, `create-account`, `reset-password`, `settings/account`,
      `signup`, `api/session`). Classify each as sign-in or recovery; the classification is declared, not
      inferred.
- [ ] 2.3 If Path B is chosen, record the new console↔platform contract as a one-way door with an ADR,
      because a published contract cannot be withdrawn.
- [ ] 2.4 Edition matrix for the change: state the effect on air-gapped, `oidc`, `saml`, `configured` and
      `password` installs, with a reason on every row — including the rows that are unaffected.

## 3. Backend — the platform half

- [ ] 3.1 Confirm by test that the token remains the only authority: a valid token consumed under a
      non-`password` seam produces exactly the credential and session it produces today, and an invalid
      one produces exactly the same refusal.
- [ ] 3.2 Keep the bootstrap log line as the operator's evidence, and assert it: the fact is logged, the
      token never is.
- [ ] 3.3 If Path B: the "first owner pending" state is derived from the store (a person with a
      membership and no password), **not** a second source of truth that can disagree with it.
- [ ] 3.4 Assert the re-mint contract that recovery depends on — a restart re-issues while no password
      exists, and stops the moment one does.

## 4. Frontend — the four pages

- [ ] 4.1 Apply D1's outcome to `(identity)/reset-password`, `verify-email`, `forgot-password`,
      `create-account`. All four in one change: three fixed and one left is the same defect with a
      smaller blast radius.
- [ ] 4.2 Replace the silent `redirect()` on a gated page with the §1.3 copy. A reader who arrived with a
      token is never shown a credential form that cannot accept them.
- [ ] 4.3 🔴 No improvised styling — the message uses the existing identity-shell anchors and design-system
      tokens; `npm run scan:tokens` must stay green.
- [ ] 4.4 Assert the pages render under a non-`password` seam. The existing console suite passes at 618
      and never loaded one, which is why this shipped.

## 5. AI Engineer

- [ ] 5.1 None. This change touches no model call, prompt, or detector. Recorded explicitly so the row is
      a decision rather than an omission.

## 6. QA — fences that can go red

- [ ] 6.1 🔴 Write the first-owner E2E whose **precondition is a non-`password` seam**: adopt an owner,
      mint the link, open it, set a password, sign in. Observe it **red** against today's code first — a
      test that could not have caught this is not a fence.
- [ ] 6.2 Assert the negative: under a non-`password` seam, `/signin` still refuses a password, so
      ungating recovery has not ungated sign-in.
- [ ] 6.3 Assert bad tokens under every seam — expired, spent, unknown — produce one message and store
      nothing.
- [ ] 6.4 🔴 End the test at the reader's eyes: the assertion is that the browser reaches a usable
      password form, not that a handler returned 200. HTTP 200 is not evidence of the thing the reader
      needs.
- [ ] 6.5 Add the regression entry: **the console suite was green at 618 while the first owner of every
      deployment was unreachable.** Name what the suite could not see — it drives handlers, and never a
      configured seam.

## 7. DevOps — the deploy that found it

- [ ] 7.1 🔴 Rewrite runbook §6 in an order that can be executed, per `deployment-topology`. Until D1
      lands, that order is flip-then-set-password, and the document must say plainly that the safety step
      is **not** in effect rather than presenting the flip as verified.
- [ ] 7.2 Record the drift-reconciliation procedure (design.md D4) in the runbook: the guarded atomic JSON
      patch, and that `--server-side --force-conflicts` does not substitute for it.
- [ ] 7.3 Add the egress check — a configured outbound port must be permitted by the same overlay's
      policy. This is the fence for the 587 defect; the fix has shipped, the fence has not.
- [ ] 7.4 Make `mail-proof` state where it ran, or run it in-cluster. A target that prints success from a
      build host is the artifact that hid this.
- [ ] 7.5 Add the admin-console image to a CI build. It was unbuildable for a day under a green main
      because no job builds that Dockerfile.
- [ ] 7.6 Note in the runbook that `HEROS_BOOTSTRAP_OWNER_EMAIL` is declared in the manifest and is
      therefore reset by the next apply; `HEROS_BOOTSTRAP_OWNER_TENANT` is not declared and survives. Set
      the pair **after** the apply.

## 8. Sales Operations — what may be said out loud

- [ ] 8.1 🔴 Self-serve sign-up is **not** claimable to strangers. SES production access was requested and
      **denied** (case `178599389200025`); mail reaches separately-verified addresses only, and an
      unverified stranger receives nothing **and sees no error**. Only "✅ delivered" may be promised, and
      this is not delivered.
- [ ] 8.2 The honest shape available now is a **private beta**: each address verified in advance, capped
      at 200 messages/day. State the cap rather than discovering it in front of a customer.
- [ ] 8.3 Do not describe the hosted deployment as offering account creation until the flip lands **and**
      D1 is resolved — today `/create-account` redirects away.
- [ ] 8.4 Feed back the first support question this will generate — *"I clicked the link and there was
      nowhere to set a password"* — as the acceptance criterion §1.3's copy has to answer. It is already
      the first one asked, by the deployment's own first owner.
