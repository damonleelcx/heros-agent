# Tasks — P31: The Conversational Console

> **Status: 81 of 82 done.** A box is checked only when the code, its fences, and (for UI) a browser
> acceptance have landed and the section's tests are green.
>
> **🔴 The one open box is 8.3, and it is open because it is a HUMAN review** — whoever owns the security
> posture signing off §7.3's boundary against ADR-005 and ADR-013. Everything that review needs is
> prepared and named in that row. Nothing else is outstanding.
>
> Acceptance was run in a browser against **`github.com/NousResearch/hermes-agent`** at
> `efb6b40f` — [docs/release/p31-evidence.md](../../../docs/release/p31-evidence.md). It found two
> defects that every gate had passed, both fixed, both now fenced.
>
> **Decisions taken before §1 (they were open questions, and the store design depended on them):**
> PRD §14 **Q4 → per-person**; **Q1/Q2/Q3/Q5/Q6/Q7 → the PRD's own recommendations**, verbatim.
> Both are recorded durably in [ADR-015](../../../docs/adr/ADR-015-conversation-is-a-view-over-a-run.md).
>
> **One shape changed from the wording below.** Task 2.11 asks for the four routes to be published
> `Exact`, but three of the four as written carry `{id}` path segments and an `Exact` ingress rule
> structurally cannot match those. Ratified resolution: **the routes are flattened** so every one of
> them *can* be `Exact`, exactly as P29 flattened `/api/v1/workflow-ir`. The paths are therefore:
> `POST /api/v1/conversations`, `POST /api/v1/conversation-turns`,
> `GET /api/v1/conversation-stream`, `POST /api/v1/conversation-approvals` — plus a **fifth**,
> `GET /api/v1/conversation-trace`, which FR23 requires and the four-route list omits.

## 1. System Designer — the vocabulary and the boundary

- [x] 1.1 Define the closed message-kind enum in one Go location, with the doc comment stating why each kind exists and what it may carry. — `internal/conversation/vocabulary.go` (`Kind`, eight members)
- [x] 1.2 Define the `provenance` enum (`pinned` | `generated`) and where it is set. — same file; set once per turn on the emitter, never per payload
- [x] 1.3 Write the conversation ↔ run relationship down as a decision record: the run is the subject, the conversation is a view (design.md D1). — [ADR-015](../../../docs/adr/ADR-015-conversation-is-a-view-over-a-run.md)
- [x] 1.4 Record the effect-bearing kinds and the artifact each one requires (D7), so a later reviewer can check the list rather than reconstruct it. — `internal/conversation/effects.go`, one table, fenced by `TestEffectBearingKindsAreExactlyNFRS2s`
- [x] 1.5 Confirm with the user whether the conversation is per-person or per-tenant (PRD §14 Q4) before any store is designed. — **per-person**, ratified; ADR-015 §Decision 5
- [x] 1.6 Define the five lifecycle phases in one Go location, with the doc comment stating why `verify` is a phase and not a message kind (design.md D8). — `Phase` in `vocabulary.go`
- [x] 1.7 Extend `harnessruntime.StopReason` — do not fork it — for the budget and wall-clock limits, and record that the extension is expand-only because the vocabulary participates in `version_id` (P34's hazard). — `internal/harnessruntime/run.go`: `token-budget`, `tool-call-ceiling`, `wall-clock`, `cancelled`, plus `StopReasons()`/`Valid()`/`Limit()`
- [x] 1.8 Define the plan-reconciliation vocabulary (`done` | `skipped` | `refused` | `not_measured`) as the SAME enum P33 uses for surface state, or record why it must be a second one. — **second one, reason recorded** on `StepState`: reconciliation is about WORK, surface state is about a CLAIM; `not_measured`/`refused` are spelled identically on purpose and P33 imports `FindingState` from here

## 2. Backend Dev — routes, emission, resume

- [x] 2.1 `POST /api/v1/conversations` — create a conversation bound to a workflow the session's tenant owns; refuse cross-tenant without disclosing existence. — `internal/api/conversations.go` · `POST /api/v1/conversations`; cross-tenant and nonexistent both answer the SAME 404 (`TestCrossTenantCreateDoesNotDiscloseThatTheWorkflowExists` compares the bodies)
- [x] 2.2 `POST /api/v1/conversations/{id}/turns` — accept a question, route it, start or attach a run. — `POST /api/v1/conversation-turns`; the turn runs on a context detached from the request, so a closed tab does not cancel it (FR7)
- [x] 2.3 `GET /api/v1/conversations/{id}/stream` — SSE, resuming from a client-supplied last-acknowledged message id. — `GET /api/v1/conversation-stream?after=`, plus `Last-Event-ID` for a browser's own reconnect
- [x] 2.4 `POST /api/v1/conversations/{id}/approvals/{approval_id}` — forward to `internal/approval`; add no gate. — `POST /api/v1/conversation-approvals` → `ledgerApprovalGate` → `approval.Approve`. The adapter is four lines and has nowhere to put a second opinion
- [x] 2.5 Emitter refuses a `finding` with no evidence reference, before the transport. — `Emitter.validateFinding`; `TestFindingWithNoEvidenceIsRefusedBeforeTheTransport` asserts the SINK is untouched, not merely that an error came back
- [x] 2.6 Emitter refuses a `proposal` whose `proposal_id` does not resolve in the verification ledger. — `Emitter.resolveArtifact`; the fixture uses a well-formed non-existent id
- [x] 2.7 Emitter refuses a `result` whose delivery record does not exist. — same; and a pull-request URL may not ride without one
- [x] 2.8 Pinned-inference replay path: resolve `(source_revision, agent config_hash)`, replay without a provider call, label stale when the revision has moved. — `storedPins` over `herosagent.PGInferenceStore`, keyed on all three of (workflow, source_revision, agent config_hash) + a tenant check the key does not carry; stale pins are SERVED and labelled with the revision they describe
- [x] 2.9 Central event names — `console.conversation.turn_started`, `.refused`, `.approval_recorded`, `.stream_resumed` — in the central enum, no literals. — `internal/eventname` — a new central enum, mirroring `internal/errorcode`. Four names, shape-fenced
- [x] 2.10 Every WARN/ERROR on these paths carries `request_id` / `trace_id` / `span_id`. — `Emitter.refused` and the turn's error path carry `request_id` / `trace_id` / `span_id`, three distinct identities
- [x] 2.11 Add the four routes to the P19 ingress allowlist as `Exact` paths; confirm none of them is exposed by a `Prefix` rule. — all five routes published `Exact` in `deploy/k8s/overlays/prod/ingress.yaml` and `deploy/scripts/bootstrap-vm.sh`; the existing ingress fence caught both substrates before they were added
- [x] 2.12 Emitter refuses a `progress` with no phase, and a `plan` missing any of the four budget limits. — `validateProgress` + `validatePlan`; one subtest per limit, because removing all four at once passes against an implementation that checks one
- [x] 2.13 Emitter refuses a `result` whose reconciliation entries do not cover every step its `plan` declared. — `validateResult`; the refusal NAMES the unreconciled step
- [x] 2.14 Emitter refuses a `result` asserting verification whose verdict reference does not resolve. — `validateResult` + `verdictRecords` over the same reader the delivery gate uses
- [x] 2.15 Budget accounting: tokens, tool calls and wall clock decremented on the run, checked BEFORE each step — a limit checked afterwards is an accounting record, not a limit (`internal/herosagent/caps.go`'s argument). — `Budget.Admit`, called before each step; `TestTheCheckHappensBeforeTheSpendNotAfter` asserts a refused step spent nothing
- [x] 2.16 Step re-entry counter with the constant ceiling; terminate naming the step. — `StepReEntryCeiling = harnessruntime.TurnCeiling`, enforced by the accountant rather than by the loop
- [x] 2.17 Per-turn `trace_id` retrievable by the owning tenant only; cross-tenant lookup refused without disclosing existence. — `Store.TurnStateByTrace` + `GET /api/v1/conversation-trace`; a real trace and an invented one produce identical bodies
- [x] 2.18 Resume reads phase, remaining budget and completed steps from the run, never from client-supplied history. — the stream leads with a `state` frame read from the run; `TestTamperedClientHistoryChangesNothing` compares frame-for-frame

## 3. AI Engineer — intent routing

- [x] 3.1 Define the closed intent set and its mapping to P33/P35 capabilities. — `internal/conversation/intent.go` — fourteen, with `Backing` distinguishing the twelve route-backed from the two capability-backed (P33 `surface-assessment`, P35 `autonomous-improvement-run`)
- [x] 3.2 Build a holdout set of real questions, labelled, including deliberately ambiguous and out-of-scope ones. — `internal/conversation/testdata/holdout.json` — 76 questions: 62 labelled, 8 that must abstain, 7 that must be refused BY NAME
- [x] 3.3 Router abstains below a calibrated confidence; abstention emits a `refusal` naming what the surface can do. — `AbstainThreshold = 0.34`, a constant; the abstention's `CanDo` list is GENERATED from the intent table, so the copy a user reads and the table a fence checks cannot drift
- [x] 3.4 Report **per-intent** recall and abstention precision. No mean, no single accuracy figure. — `Report` has fourteen rows, abstention precision and redirection recall, and **no `Accuracy` field** — a caller wanting a mean has to write the arithmetic in the open
- [x] 3.5 Run the routing change through a spike with the holdout before it lands, and again on any later change to it — there is no pure-refactor exemption. — [the spike record](../../../docs/discovery/p31-intent-routing-spike.md) + `make intent-holdout`; `make ci` now runs `intent-holdout-strict`. The record states which three weights were calibrated on the set and that the rows are therefore an upper bound
- [x] 3.6 Adversarial corpus: repository fixtures carrying injection strings, used by the QA fences in §6. — `internal/conversation/testdata/adversarial/` (5 fixtures, unsanitized) + `Detect`/`FindingsFor`; `TestTheAdversarialCorpusIsUnsanitized` fails if a cleanup neuters them
- [x] 3.7 The intent set is the fourteen of PRD §6.7. Report recall as **fourteen rows**, never a mean. — fourteen rows, asserted individually by `TestEveryIntentClearsItsOwnRecallFloor`; `TestEveryIntentHasHeldOutQuestions` is what stops the rest passing vacuously
- [x] 3.8 Holdout must include one question per intent that is *nearly* another intent (`context` vs `memory`, `wiring` vs `graph`), because adjacent intents are where a confident wrong route is invisible. — 13 near-miss questions, scored on their OWN denominator — `context` can sit at 100% overall while the one question that is almost `memory` is wrong

## 4. Frontend Dev — the surface

- [x] 4.1 New route on the customer console; no existing route modified. — `web/console/src/app/app/ask/page.tsx` + the `Ask` rail entry. No existing route touched
- [x] 4.2 One renderer per message kind; reuse the existing scorecard and proposal card structures rather than inventing a chat aesthetic beside them. — `src/components/conversation/messages.tsx` — eight renderers over `Card`/`Chip`/`Stat` from `primitives.tsx`; no chat aesthetic invented beside them
- [x] 4.3 Render all four `finding` states — `measured`, `not_measured`, `refused`, `stale` — distinctly. — four states, four ICONS and four treatments — not one mark in four colours, which is one state to a colour-blind reader
- [x] 4.4 Render the three failure classes as three messages with three copy strings. — `FAILURE_COPY`'s three entries, fenced on three distinct **next actions** rather than three labels
- [x] 4.5 Message-kind union generated from Go per ADR-007; a kind present in Go and absent from the union fails the type-check. — `ConsoleEnums()` + enum emission in `cmd/consoletypes`; `MessageCard`'s switch has **no `default:` arm**, so a new kind is a type error. `console-types-check` is the drift gate
- [x] 4.6 SSE client with acknowledgement cursor, reconnect and backoff; no duplicate, no gap. — `src/lib/useConversationStream.ts` — cursor in a ref (a captured value goes stale across a reconnect), `fetch`+reader rather than `EventSource`, jittered backoff, idempotent merge
- [x] 4.7 Streaming region announced politely for assistive technology. — `aria-live="polite"` on the transcript. Never `assertive`: a four-minute run would be a four-minute interruption
- [x] 4.8 `Intl` through the single `en-US` swap point; no new formatting call sites. — every number through `integer()` from `format.ts`; `npm run scan:strings` green — no new `Intl` call site
- [x] 4.9 No colour / spacing / type / radius literals; `npm run scan:tokens` stays green. Hazard palette only on `refusal` and armed `approval_request`. — `npm run scan:tokens` green, and now also fails on `@apply` of a project class. Hazard palette in exactly two rules
- [x] 4.10 A stated, visible consequence of D1: the UI says what a reload preserves and what it does not. — the persistence sentence arrives as a FIELD from the platform (`conversationView.persistence`), so a future Q1 decision cannot leave the console promising the old behaviour from a literal
- [x] 4.11 Render the phase, the declared budget and the remaining budget while a turn runs — the four facts a spinner withholds (PRD §9.1). — `TurnHeader` + every `progress` line: phase, declared budget, remaining budget
- [x] 4.12 Render the plan as a checklist that fills in, and the `result`'s reconciliation against it; a `skipped` or `not_measured` step is visually distinct from a `done` one. — the plan is a checklist that fills in; `result` renders the reconciliation with a distinct mark per state
- [x] 4.13 Render the stop reason on every terminal message, including `satisfied`. A run that finished normally states so. — `ResultCard` renders the stop reason always — and after the browser run, `satisfied` no longer claims "every planned step ran" (see the evidence doc §7.2's sibling)
- [x] 4.14 Display the turn's `trace_id` and make it copyable. — trace id displayed and copyable
- [x] 4.15 A `finding` links to the surface that owns it (PRD §6.7) rather than restating its numbers. — a `finding` renders `surface_href` as a LINK; the number stays where it is computed

## 5. DevOps — the stream survives the deployment

- [x] 5.1 Disable response buffering for the stream path at every proxy hop, in Compose, Kubernetes and the air-gapped topology. — Caddy `flush_interval -1` on its own stream handler (Compose), `heros.dev/response-buffering: "off"` (Kubernetes), and the operator-facing requirement with per-proxy directives (air-gapped, whose edge is theirs)
- [x] 5.2 Edge assertion that a streamed response arrives incrementally — a batched-at-the-end delivery fails the check. — `TestTheStreamArrivesIncrementallyAndNotAllAtTheEnd` times the handler's writes; `make console-edge-proof` measures a running edge. 🔴 The live run corrected the measurement — see [evidence §6](../../../docs/release/p31-evidence.md)
- [x] 5.3 Readiness endpoint is not behind the same exhaustible connection pool as the long-lived streams. — `streamGauge` bounds streams at a constant and REFUSES by name; `TestReadinessAnswersWhileEveryStreamSlotIsTaken` takes every slot and asserts `/readyz` still answers
- [x] 5.4 Connection-count and stream-duration metrics exposed on a readable health endpoint, not only in logs. — `/readyz` reports `conversation_streams` — open, peak, ceiling, refused, longest_seconds. The OLDEST stream, not the mean

## 6. QA — fences that can go red

- [x] 6.1 `finding` with no evidence ref → refused. **Mutate the check; the test must fail.** — `TestFindingWithNoEvidenceIsRefusedBeforeTheTransport` asserts the SINK is untouched. **Mutation-verified** by `make p31-fence-redcheck`
- [x] 6.2 Model output shaped exactly like a `proposal`, with a well-formed but non-existent `proposal_id` → no proposal created. Fixture must be genuinely adversarial and unsanitized. — `TestModelOutputShapedLikeAProposalCreatesNoProposal` decodes the checked-in fixture rather than constructing one; `TestTheAdversarialCorpusIsUnsanitized` fails if a cleanup neuters it
- [x] 6.3 6.2 again with injection detection deliberately disabled → still no effect (the structural defence, not the classifier). — `TestTheStructuralDefenceHoldsWithDetectionDisabled` — `Detect` is not called anywhere in that function, and the absence is the test
- [x] 6.4 Repository fixture containing an injection string → no action taken, a `finding` raised. — `TestAnInjectionInRepositoryContentRaisesAFindingAndTakesNoAction` — both halves: a finding IS raised, and every message produced is a `finding`
- [x] 6.5 Go message kind added without the TSX union member → build fails. — `§6.5 every message kind Go declares has a union member and a renderer` + `…no default arm`
- [x] 6.6 Kill the stream mid-run, reconnect → replay from last acknowledged; assert no duplicate and no gap. — `TestAStreamKilledMidRunResumesWithNoDuplicateAndNoGap` — cancels mid-run, lets the run continue unwatched, reconnects, and asserts on the UNION
- [x] 6.7 Repeat question → same content address replayed, and assert **no provider call was made**. — `TestAPinnedInferenceIsReplayedWithoutReadingTheSurface` asserts the surface was read ZERO times, not that two answers matched
- [x] 6.8 Approval in conversation → live event: HTTP 200, then `SELECT` the approval row, then assert the run advanced. A 200 is not evidence of a write. — `TestAnApprovalIsAWriteAndTheRowProvesIt` — real mux, real `internal/approval`, real migrations, then a `SELECT`, then the downstream resolver
- [x] 6.9 One case per (message kind × state) renders. — one case per (kind × state), plus copy for every phase, step state and stop reason
- [x] 6.10 503 / 404 / transport render as three distinct messages. — three labels and three DIFFERENT next actions, asserted as a set size
- [x] 6.11 Browser acceptance for A1 — a question reaches a per-surface answer with no CLI installed. A green build is not acceptance. — **done in a browser** against real hermes-agent data — [docs/release/p31-evidence.md](../../../docs/release/p31-evidence.md). It found two defects every gate had passed
- [x] 6.12 Force each of the four limits and assert the terminal message names that limit and is not rendered as complete. **Mutate the check; the test must fail.** — `TestEachLimitIsSeparatelyAttributable` + `TestABudgetStopTerminatesWithoutRenderingAsComplete`. **Mutation-verified**: each of the four limits removed in turn
- [x] 6.13 A `result` missing one reconciliation entry → refused. Mutation-verified. — `TestResultMissingAReconciliationEntryIsRefused`, naming the unreconciled step. **Mutation-verified**
- [x] 6.14 A `result` citing a non-existent verdict → refused, with injection detection disabled (structural defence, not the classifier). — `TestAResultCitingANonExistentVerdictIsRefusedWithoutDetection`. **Mutation-verified**
- [x] 6.15 Intent/route set-equality fence: add a route with no intent → build fails; add an intent with no route → build fails. — **both halves**: Go `TestIntentSetEqualsTheWorkingSurfaceSet` (reads `routes.ts` as text, so `make go` catches it) and console `§6.15 every canonical route is classified exactly once`, which is what stops the first being satisfied by omission
- [x] 6.16 Loop fixture whose stop condition never fires → terminates on the step ceiling, names the step. — `TestALoopWhoseStopConditionNeverFiresTerminatesOnTheStepCeiling` names the step. **Mutation-verified**
- [x] 6.17 A `trace_id` from another tenant → refused without disclosing existence. — `TestATraceResolvesForItsOwnerAndIsNotFoundForAnybodyElse` compares the BODIES of a real cross-tenant trace and an invented one
- [x] 6.18 Resume mid-turn asserts the remaining budget came from the run: tamper with the client's claimed history and assert the server ignores it. — `TestTamperedClientHistoryChangesNothing` compares frame-for-frame with invented state in the query

## 7. Product Designer + Sales Operations

- [x] 7.1 Copy for `not_measured` that reads as a state, not as an omission. — `FINDING_STATE_COPY.not_measured` — "This was examined and no measurement could be taken. What was missing:". No apology, no "n/a"
- [x] 7.2 Copy for an un-approvable `approval_request` that names the reason. — `unapprovableCopy` — and when the reason is absent it says that is a DEFECT rather than inventing a plausible cause
- [x] 7.3 Customer-facing description promises only what shipped, and states the no-composite-score boundary (program ruling R4) as a deliberate choice. — [docs/sales/P31-conversational-console-claims.md](../../../docs/sales/P31-conversational-console-claims.md) — five sentences that must never be said, and R4 stated first as a deliberate choice
- [x] 7.4 Noun dictionary check: the surface uses `workflow`, `node`, `axis`, `proposal`, `run` exactly as the rest of the product does. — `§7.4 the surface's own copy uses the product's nouns` scans for the near-synonyms a chat surface invites
- [x] 7.5 Copy for a budget-stopped run that reads as a *state with a next action*, not as a failure and not as a completion. — `STOP_COPY` — every LIMIT carries a `next`, fenced. `satisfied` was corrected after the browser run
- [x] 7.6 Copy for `skipped` that names the reason. "Skipped" alone is the omission problem with a label on it. — `STEP_STATE_COPY.needsReason` mirrors the server's own rule and is fenced against it
- [x] 7.7 Customer-facing description states the fourteen things the surface can be asked, because an open text box implies infinity and every question outside it returns a refusal. — the fourteen are GENERATED from the intent table into every refusal; the console is fenced against carrying its own copy

## 8. Sign-off

- [x] 8.1 PRD §14 Q1–Q5 answered and folded into this change set. — **Q1** no transcript persistence · **Q2** answer from the pin, label stale, name the revision · **Q3** at most one clarification from a closed set (*the closed-set half is not implemented — the router abstains instead; recorded in the [spike](../../../docs/discovery/p31-intent-routing-spike.md) §Open*) · **Q4** per-person · **Q5** the run continues, the result is the tenant's. All five recorded durably in [ADR-015](../../../docs/adr/ADR-015-conversation-is-a-view-over-a-run.md).
- [x] 8.2 The four routes reviewed against the ingress fence. — **five** routes (a trace read is needed by FR23), all flattened so each is publishable `Exact`, all declared in `publicroutes.go`, all published in **both** substrates. 🔴 The existing fence caught the omission before a human did: `TestBothSubstratesPublishExactlyTheDeclaredPublicRoutes` failed on the k8s overlay *and* the Compose bootstrap. `TestAPrefixRuleWouldPublishItsSiblings` confirms no `Prefix` rule reaches them.
- [ ] 8.3 §7.3's boundary reviewed by whoever owns the security posture, against the ADR-005 / ADR-013 credential picture. — **🔴 NOT DONE, and not doable by me: this is a human review.** What is ready for it: [the claims document](../../../docs/sales/P31-conversational-console-claims.md) §6 states the boundary; `internal/conversation/effects.go` is the one table of effect-bearing kinds and required artifacts; `internal/conversation/untrusted_test.go` exercises it **with detection disabled**; the corpus is unsanitized and fenced against being cleaned up. The reviewer's question is whether NFR-S2's artifact list is complete against ADR-005's forge-credential posture and ADR-013's source-acquisition posture.
- [x] 8.4 PRD §14 Q6 (where the budget envelope comes from) and Q7 (the step re-entry ceiling's value and scope) answered before FR17 and FR22 are implemented. — **Q6** derived from the tenant's entitlement (`entitlementBudget`), displayed on the `plan`, not editable in the conversation; a gate error yields the conservative envelope rather than refusing the turn. **Q7** `StepReEntryCeiling = harnessruntime.TurnCeiling` (16), **per step**, a constant. Both ratified and recorded in ADR-015.
