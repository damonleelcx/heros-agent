# P30 HEROS analysis agent — what we sell, and the five sentences that may not be said

- **Status:** Accepted (2026-08-12)
- **Audience:** anyone who writes a sentence about the analysis agent that a customer, a prospect or a
  security reviewer reads.
- **Source:** [`docs/prd/P30-heros-platform-agent.md`](../prd/P30-heros-platform-agent.md) §9.8.
- **Reads with:** [P11 CLI/CI claims](P11-cli-ci-integration-claims.md) for the egress boundary this
  phase reuses, and the customer-facing page it must not contradict:
  `/docs/concepts/inferred-structure`.

## 1. The one paragraph that is sayable on delivery

> For repositories in languages where static analysis cannot follow the call chain, HEROS reads the
> source and proposes the missing structure. Every fact it authors is marked as inferred and carries the
> exact agent version that produced it. It runs on your infrastructure with your own key if you require
> that.

Every clause of that is shipped and provable. Nothing beyond it is.

## 2. 🚫 The five sentences that may not be said

| Do not say | Because | Say instead |
|---|---|---|
| "HEROS understands your codebase" | It infers a graph on a pinned revision. Unfalsifiable claims are the ones that come back — there is no test a customer can run that would show it *not* understanding. | "It proposes the dependencies your language's parser could not establish." |
| "Automatically optimizes your agent" | Automation is Advisory. A proposal opens a **draft** PR; a person merges. Nothing is applied. | "It surfaces candidates. You review and merge them." |
| "Accurate graphs" | Precision and recall are **per-language and measured**. Quote the measured number or say nothing. | "On our Go fixture set it measures P ≥ 0.90 / R ≥ 0.70 — ask which language yours is." |
| "Deterministic / reproducible analysis" | It is **pinned**, not reproducible. Re-running the same revision reads a stored answer; it does not re-derive one, and a different model would answer differently. | "The same revision always shows you the same graph." |
| "We never see your code" | 🔴 **False under `platform` placement.** True under `customer`. | "Under `customer` placement your source never leaves your machine. Under `platform` we read it — which is why the default is neither." |

🔴 **The fifth is the one that ends a deal badly.** It is the sentence a rep reaches for in a security
review, it is true of the placement most enterprise buyers will choose, and it is false of the one that
is easier to demo. Never say it without naming the placement. If you do not know which the customer will
run, ask — it is one question.

## 3. What is provable, and where

| Claim | What it means | Where it is proven |
|---|---|---|
| **Every inferred fact is marked, and says who wrote it** | Each edge carries an author (`frontend`, `detector`, `heros`, `legacy`) and, when inferred, a confidence. The graph draws them differently and the table has a *How we know* column. | `internal/discovery/author.go`, `web/console/tests/p30-composition.test.mjs` |
| **An inferred fact never overrides a measured one** | The agent is not shown pairs a parser already answered, and an answer naming one is recorded as an abstention and discarded — at selection AND at the write boundary. | `TestAGoFixtureIRIsByteIdenticalWithTheAgentOnAndOff`, `TestIngestRefusesAnAgentEdgeOverAFrontendOne` |
| **The same revision always shows you the same graph** | The result is pinned on `(workflow, source_revision, agent_config_hash)`. A second read makes **zero** provider calls and returns a byte-identical body. | `TestACacheHitReturnsAByteIdenticalBody`; the unique index proved under contention in `p30_pgproof_test.go` |
| **Your provider key never reaches us** | Under `customer` placement the key is resolved on the customer's machine. There is **no field, column, log line or response** anywhere that could carry one, checked by auto-discovering fences over all four. | `p30_nokey_test.go`, `p30_nokey_surfaces_test.go` |
| **You can switch it off, and your data is not deleted** | Setting `disabled` marks stored inferences stale and keeps them attributed. Re-enabling clears the mark and re-runs nothing. | `TestDisablingMarksStaleRatherThanDeleting`, `TestTheStaleMarkSurvivesTheRealSchema` |
| **It is off until somebody turns it on** | Default placement is `disabled` fleet-wide. A freshly migrated deployment makes zero provider calls. | `TestAFreshlyMigratedDeploymentAnalysesNothing` |

## 4. The boundary to disclose unprompted

HEROS-inferred structure **feeds eval scope, proposals and cost attribution.** A customer relying on any
of those should know which of their nodes are inferred — and the console shows it, per node, per pattern,
with the total and the inferred portion side by side.

Say it before they find it. A customer who discovers that a number they quoted internally was partly
model-proposed discovers it in front of their own stakeholders.

## 5. 🔴 What is NOT yet true, and must not be implied

These are shipped as machinery and have **not** been exercised end to end on a real deployment. Saying
otherwise is the over-claim this file exists to prevent.

- **No definition has ever been activated, so no model has been measured.** The floors quoted in §2
  (P ≥ 0.90 / R ≥ 0.70) are a design starting point, not a measurement. What HAS been measured is the
  calibration *set*, against synthetic analysers. 🚫 Do not quote them as our agent's numbers.
- **The live acceptance has run three of its four layers.** The layer that writes a row through a real
  analysis needs a platform database and a live provider credential and has not been run. Until it has,
  "it works end to end" is not a sentence we own.
- **No price list is wired.** Every spend figure reads `unpriced`. 🚫 Do not quote a cost per analysis.
- **The rollout ladder is at `none`.** No tenant anywhere is placed for analysis.

## 6. If a security reviewer asks

Three answers, in the order they are usually asked:

1. **"Where does our source go?"** — Under `customer`, nowhere: the analysis runs on your machine and
   submits a result. Under `platform`, into our analysis, on our infrastructure. Neither is the default;
   an operator sets it per organization, deliberately, with a reason that is audited.
2. **"What does the model see?"** — The gap only: node identifiers, the pairs no parser connected, and
   which parsers ran. Not prompts, not source, not tool names. There is no field in the request that a
   repository could occupy.
3. **"What if our repository contains something adversarial?"** — Repository content is data, never
   instruction. The answer is validated against the identifiers already in your graph and the closed
   pattern taxonomy, so the worst a hostile instruction achieves is a wrong edge — which is marked
   inferred, carries a confidence, and never overrides anything a parser established.
