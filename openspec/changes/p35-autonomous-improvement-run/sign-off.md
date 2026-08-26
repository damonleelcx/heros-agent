# P35 §11 — Sign-off

## 11.1 — PRD §14 Q1–Q5, answered and folded in

All five are settled in [`decisions.md`](decisions.md) with their rejected alternatives and the
cost-and-complexity level each was rejected on, and folded into
[PRD §14](../../../docs/prd/P35-autonomous-improvement-run.md#14-open-questions--answered), whose header
now reads **answered** and whose status line reads **Ratified**. A sixth decision was settled beside
them (D-35.6) because the phase surfaced a **contradiction between two shipped rules** that could not be
left to whoever reached it first.

| # | Answer | Where it is now enforced |
|---|---|---|
| Q1 | **Offer both, App preselected, choice recorded per repository** | `forgedelivery.Route.Mode` is per (repository, workflow); a console run with no installation is `WithheldNoInstallation` with the diff retained — never a silent downgrade to a mode the customer cannot use |
| Q2 | **Bot as author, approving person as `Co-authored-by`** | the person is read back from the approval row, never from the request; `approval.Approve` already refuses an empty actor, and `Service.Approve` refuses one before the binding check so a nameless decision costs nothing |
| Q3 | **Scheduled runs stop at proposals** | `RunOrigin.MayDeliver()` is false for `scheduled`, checked in `Deliver` **before entitlement** — so "may this deliver" is not answerable by buying a plan |
| Q4 | **Charged for compute, not billable**, reported separately | `Run.WithdrawnSpendUSD` and `Health.WithdrawnSpendUSD` beside the totals; P7 reads neither, because it bills merged deliveries |
| Q5 | **Bounded in-run retry, then reconciliation** | `KindDeliveryPendingForge` is what `Ledger.Unresolved` looks for, and the pass that reads it runs every cycle rather than after a failure |
| **Q6** | **Neither rule moves: never push before the last safe point** | the cancellation and brake checks sit immediately before `OpenFromPrepared`; `ForgeWriter` still has no delete method and `StaleBranchPolicy.MayDelete` still returns false |

---

## 11.2 — The ADR-005 amendment, reviewed against what was built

> The task asks specifically whether **the default changed the MODE and not the SCOPE**. That is the one
> sentence design D3 says this phase is exposed to losing, and it is re-derived here from the code
> rather than cited.

### What ADR-005 decided, and what R3 changed

ADR-005 made the customer's own CI the default so that **no write-scoped forge credential is held by the
platform**, because pushing to a customer's repository is the highest-blast-radius action in the system.
R3 amends that default **for console-driven runs only**, because the CI-mediated path requires an
integration a console customer does not have — and defaulting to a mode the customer cannot use is a
default that means *this feature is off*.

### The mode changed. Here is the check that the scope did not.

| ADR-005 required of Mode 2 | Still true? | What makes it true, in code |
|---|---|---|
| **Per-repository** | ✅ | `Installation.Repositories` is a selected list; an installation selecting **none** is refused rather than read as org-wide. There is no `AllRepositories` field, so org-wide-by-default is **not expressible** |
| **Least-privilege** | ✅ | `LeastPrivilegePermissions()` is `{pull_requests: write, contents: write}` and nothing else. `WithinLeastPrivilege` refuses a grant naming a permission outside the set or a level above it, so broadening is a **spec change** visible in review |
| **Customer-revocable** | ✅ | `InstallationStore.Revoke` models the customer revoking from their own side; the platform needs no involvement |
| **Revocation immediate and complete** (FR25) | ✅ **and this is new** | `AppForgeWriter.withDelegate` resolves the installation **before a token is requested**, on every single call. The token cache is therefore irrelevant to revocation, which is the property being claimed |
| **CLI/CI hold no platform credential** | ✅ | `Surface.HoldsPlatformCredential()` is true only for `console`, asserted across the whole closed set; the CI-mediated route hands **no writer** at all |

### 🔴 What was found while checking this, and what it changed

**The origin is read from the TRANSPORT, never from a request body**, and that turned out to be
load-bearing twice rather than once:

1. `api.originFor` reads the user agent. A body field would let any caller claim `console` and reach the
   write-credential path — which is the widening D3 names, arriving through the door nobody was watching.
2. **A run reconstructed from the ledger had no origin at all**, so `surfaceFor("")` resolved to the safe
   CLI default and every console run completed from the record silently became CI-mediated. It failed
   loudly in a browser only by luck; on a deployment configured for both modes it would have delivered a
   console customer's change down a path they have no CI integration for, and reported success. Found by
   opening the page. Closed by putting `Origin` on the ledger entry and refusing an entry without one.

The second is the more instructive finding, and the rule it produced is written where it happened: **a
ledger entry carries what the repair path needs, not what the writing path happened to have in scope.**

### The slope, and the one thing that keeps a customer in control regardless

Every future convenience request pushes toward per-account. The property that survives that pressure is
not the scope — it is **FR25**: revocation is immediate and complete, checked on the write path rather
than on the token's lifecycle. A customer who can stop the platform pushing *on the next call*, from
their own settings, without contacting us, is in control of a grant whatever its scope drifts to.

---

## 11.3 — The gate inventory, walked end to end

[`gate-inventory.md`](gate-inventory.md) is the artifact, and it is a **checklist with a machine behind
it** rather than a paragraph:

- `TestGateInventoryIsComplete` parses the file and fails when a row names a fence that does not exist,
  or when a gate `improvementrun.Gates()` declares has no row. The checklist cannot rot into a
  description of an older system.
- `make p35-fence-redcheck` breaks each gate and asserts the named fence goes **red** — 20 mutations,
  each written as a change somebody could plausibly ship, each required to **compile** before its result
  is trusted.

### 🔴 The walk found four fences that were not fencing

That is the finding, and it is worth more than the twenty that passed:

| What the drill found | Why it mattered |
|---|---|
| A cap-over-stopping-condition override that **could not be made to fail** | It was **unreachable** — `CandidateCap` maps onto `MaxIterations` and the loop consumes one candidate per iteration, so `max_iter` always fires first. It was **deleted rather than excused**: if it ever became reachable it would relabel a genuinely exhausted search as a truncated one, and send somebody to raise a cap that was not the constraint |
| Two G2 mutations passed because a **second guard** caught the same case | Defence in depth working, and a drill that could prove neither guard. Fixed by giving each guard a case only it catches: `MergeReady()` uniquely catches a candidate that passed the gate and did **not build**; `Validate()` uniquely catches a proposal read back from a **store**, which never ran the constructor's checks |
| One G2 mutation still passed after that | Because the guard it broke prevents a proposal **row** from being written, and the fence only asserted the card was not shown. The assertion it needed was that `ProposalRecorder` was never called — a gate-failing candidate must not produce a row that P12's delivery surface can later offer |
| A G4 mutation **did not compile** | The drill refused it rather than counting it, which is the harness working: a mutation that does not build exits non-zero for a reason unrelated to the fence, and accepting it would report a gate as proven when it was never run |

### What a reviewer who did not implement this should do

1. Read `gate-inventory.md` top to bottom. Every row names a fence.
2. Run `make p35-fence-redcheck`. It should print **20 gate(s) proven capable of failing**.
3. Pick two rows and break them **by hand**, differently from how the drill breaks them. A drill proves
   the mutations somebody thought of.
4. Read [`findings.md`](findings.md). Two things were found and deliberately **not fixed in this
   change**; confirm you agree with both calls.

   ✅ **F1 has since been closed in its own change**, in `internal/sourceingest`, where its reviewers
   are the people who should evaluate a scope allowlist. It closed by **inverting** the control rather
   than extending it: `repo` turned out to be the first instance of a class a verb denylist cannot see —
   scope names that are NOUNS conferring every verb — so the check is now an allowlist of the scopes
   each forge's narrowest grant carries. The write-up also **corrects its own reachability claim**: on
   GitHub a classic PAT never reached the scope check through `Connect`, and the reachable instance was
   GitLab's `api`.

   🔴 That fence did the job it was built for. It was written as a `t.Log` with an **inverted**
   assertion — reporting the gap and failing the day it closed — and it failed on the first run after
   the fix rather than staying silent in either direction.

---

## What is NOT signed off, stated plainly

🔴 **The live four-step (7.14 / A13) has never been run against a real forge in this change.**
`TestLiveFourStep_ApproveSelectSelectFetch` is written, compiles under `-tags live`, and `make
p35-live-four-step` runs it — but it needs a token and a repository somebody is willing to have a pull
request opened on, and it opens one. The four boxes in the inventory are ticked for *the step exists and
is asserted*. **Nobody should read them as "a pull request has been observed."** It is a
release-checklist item, not something a build can do.

⚠️ **The hosted deployment is at PRD §12 stage 1 — plan only.** The verification gate runs the customer's
eval harness, which by design does not execute on the platform, so `POST /api/v1/improvement-runs`
answers **503 naming exactly that**. Everything downstream of verification — approval, re-measurement,
withdrawal, delivery, reconciliation — is built, fenced and proven able to fail, and is reachable today
only from a deployment that supplies a verifier. That is a rollout stage rather than a gap, and it is
recorded in `deploy/README.md` as a capability with its reason, so nobody discovers it from a 503.
