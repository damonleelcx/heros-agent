## Why

This is where the conversation does something. P33 reports findings; P35 carries the rest of the original
request — propose, wait to be told yes, apply, re-measure, and when the change proved itself, commit, push
and open the pull request.

Almost every component exists. `internal/optimizer/loop.go` runs enumerate → verify → gate → merge, and its
tests read like the platform's conscience: `TestLoop_GateFailingHighScorerNotMerged`,
`TestLoop_UnverifiedNotMerged`, `TestLoop_ContractViolationNotMerged`. `internal/proposal` generates
candidates, `internal/verification` decides, `internal/transform` produces the diff, and
`internal/forgedelivery` already codes **both** delivery modes.

Two things are genuinely missing.

**A question cannot become a run.** There is no translation from "improve this" to an `Enumerator` scoped
to an intent and a budget somebody agreed to. An unbounded search started by a sentence is a search nobody
can predict the cost of.

**The console has no way to deliver.** [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md)
made the customer's own CI the default so the platform holds no write credential, and that argument is
correct — but it answers "what should happen when we do not know how the customer works." On the console we
do know: they arrived with no CI integration and no CLI. Program ruling **R3** amends the default for that
one surface.

P30 also left a warning this phase must not repeat: the generate route was mounted, had no button, was not
published on the ingress so a button would have 404'd, and the surface **discarded the reason** when there
was nothing to propose — `proposalgen` returns five distinct states and the screen showed none of them.

## What Changes

- **ADDED** the improvement run: a question becomes a **bounded plan** — workflow, revision, axes in scope,
  candidate cap, spend budget, stopping condition — shown before anything executes, and a question that
  cannot be bounded is **refused** rather than run with defaults.
- **ADDED** per-proposal approval bound to `(config_hash, source_revision)`, routed through
  `internal/approval`. Declining one proposal does not cancel the run; a moved revision voids an approval.
- **ADDED** re-measurement after apply, with the teeth: a change that fails to reproduce its verified delta
  is **withdrawn before delivery**, and both measurements are reported.
- **ADDED** the requirement that "nothing to propose" names which of the five closed states applies.
- **ADDED** run bounds as first-class outcomes — budget, candidate cap, stopping condition, kill switch —
  each reported by name, and cancellation that leaves nothing partial on the customer's repository.
- **MODIFIED** the delivery default **per surface** (R3): console-driven runs use the hosted Git App;
  CLI- and CI-originated runs keep CI-mediated delivery and the platform receives no forge credential.
- **ADDED** two requirements the P32 read connection makes necessary: the read connection and the write
  installation are **separate grants with independent revocations**, and revoking a write installation
  stops pushes **immediately** rather than at the next token refresh.
- **NOT RESTATED, deliberately:** every other property that makes delivery safe — downstream of
  verification, idempotent per `(config_hash, source_revision, target)`, never merging below Autonomous, a
  merge observed rather than inferred, evidence in the pull request body, append-only records with
  `transform` immutable. Those are **already folded** into `specs/forge-delivery/`, and re-declaring them
  as `ADDED` would make this delta claim to introduce behaviour that already exists. What this change adds
  instead is a **fence obligation**: `tasks.md` §7 re-runs each of them through the *conversational* path,
  because a requirement holding for one caller says nothing about a new one.
- **NOT ADDED:** a second verification gate. The request lists "if everything is good" as a step; in this
  codebase that is the P5.5 gate, and a second one is a second place for the first to be bypassed.
- **NOT ADDED:** merging. Auto-merge is P6's Autonomous level and Enterprise-only.

## Impact

- **Affected capabilities:** `improvement-run` (new), `forge-delivery` (modified — one requirement
  changed, two added), and by reference `proposal-engine`, `verification`, `autonomous-optimizer`,
  `delivery-record`, `change-delivery`
- **Affected code/systems:** `internal/optimizer` (a bounded enumerator driven by a plan),
  `internal/proposal` (unchanged, new caller), `internal/approval` (new caller, no new gate),
  `internal/forgedelivery` (surface-scoped default, `withheld` path reused for staged rollout),
  `internal/worktree`, `internal/api`, `web/console`
- **Dependencies:** upstream — [P31](../../../docs/prd/P31-conversational-console.md),
  [P32](../../../docs/prd/P32-repo-intake.md), [P33](../../../docs/prd/P33-surface-assessment.md), P5.5, P6, P7
  (entitlements gate by plan **and** automation level), P12, ADR-001, ADR-005.
- **Amends ADR-005's default for one surface.** The amendment is recorded here rather than left implicit,
  and every other constraint of that ADR is restated as a requirement.
- **Documents only in this program.** Every task is unchecked.
