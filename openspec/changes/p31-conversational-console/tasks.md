# Tasks — P31: The Conversational Console

> **Nothing here is implemented.** This change set is documents only, as the whole GEHA program is.
> Every box is unchecked and stays unchecked until the code lands.

## 1. System Designer — the vocabulary and the boundary

- [ ] 1.1 Define the closed message-kind enum in one Go location, with the doc comment stating why each kind exists and what it may carry.
- [ ] 1.2 Define the `provenance` enum (`pinned` | `generated`) and where it is set.
- [ ] 1.3 Write the conversation ↔ run relationship down as a decision record: the run is the subject, the conversation is a view (design.md D1).
- [ ] 1.4 Record the effect-bearing kinds and the artifact each one requires (D7), so a later reviewer can check the list rather than reconstruct it.
- [ ] 1.5 Confirm with the user whether the conversation is per-person or per-tenant (PRD §14 Q4) before any store is designed.
- [ ] 1.6 Define the five lifecycle phases in one Go location, with the doc comment stating why `verify` is a phase and not a message kind (design.md D8).
- [ ] 1.7 Extend `harnessruntime.StopReason` — do not fork it — for the budget and wall-clock limits, and record that the extension is expand-only because the vocabulary participates in `version_id` (P34's hazard).
- [ ] 1.8 Define the plan-reconciliation vocabulary (`done` | `skipped` | `refused` | `not_measured`) as the SAME enum P33 uses for surface state, or record why it must be a second one.

## 2. Backend Dev — routes, emission, resume

- [ ] 2.1 `POST /api/v1/conversations` — create a conversation bound to a workflow the session's tenant owns; refuse cross-tenant without disclosing existence.
- [ ] 2.2 `POST /api/v1/conversations/{id}/turns` — accept a question, route it, start or attach a run.
- [ ] 2.3 `GET /api/v1/conversations/{id}/stream` — SSE, resuming from a client-supplied last-acknowledged message id.
- [ ] 2.4 `POST /api/v1/conversations/{id}/approvals/{approval_id}` — forward to `internal/approval`; add no gate.
- [ ] 2.5 Emitter refuses a `finding` with no evidence reference, before the transport.
- [ ] 2.6 Emitter refuses a `proposal` whose `proposal_id` does not resolve in the verification ledger.
- [ ] 2.7 Emitter refuses a `result` whose delivery record does not exist.
- [ ] 2.8 Pinned-inference replay path: resolve `(source_revision, agent config_hash)`, replay without a provider call, label stale when the revision has moved.
- [ ] 2.9 Central event names — `console.conversation.turn_started`, `.refused`, `.approval_recorded`, `.stream_resumed` — in the central enum, no literals.
- [ ] 2.10 Every WARN/ERROR on these paths carries `request_id` / `trace_id` / `span_id`.
- [ ] 2.11 Add the four routes to the P19 ingress allowlist as `Exact` paths; confirm none of them is exposed by a `Prefix` rule.
- [ ] 2.12 Emitter refuses a `progress` with no phase, and a `plan` missing any of the four budget limits.
- [ ] 2.13 Emitter refuses a `result` whose reconciliation entries do not cover every step its `plan` declared.
- [ ] 2.14 Emitter refuses a `result` asserting verification whose verdict reference does not resolve.
- [ ] 2.15 Budget accounting: tokens, tool calls and wall clock decremented on the run, checked BEFORE each step — a limit checked afterwards is an accounting record, not a limit (`internal/herosagent/caps.go`'s argument).
- [ ] 2.16 Step re-entry counter with the constant ceiling; terminate naming the step.
- [ ] 2.17 Per-turn `trace_id` retrievable by the owning tenant only; cross-tenant lookup refused without disclosing existence.
- [ ] 2.18 Resume reads phase, remaining budget and completed steps from the run, never from client-supplied history.

## 3. AI Engineer — intent routing

- [ ] 3.1 Define the closed intent set and its mapping to P33/P35 capabilities.
- [ ] 3.2 Build a holdout set of real questions, labelled, including deliberately ambiguous and out-of-scope ones.
- [ ] 3.3 Router abstains below a calibrated confidence; abstention emits a `refusal` naming what the surface can do.
- [ ] 3.4 Report **per-intent** recall and abstention precision. No mean, no single accuracy figure.
- [ ] 3.5 Run the routing change through a spike with the holdout before it lands, and again on any later change to it — there is no pure-refactor exemption.
- [ ] 3.6 Adversarial corpus: repository fixtures carrying injection strings, used by the QA fences in §6.
- [ ] 3.7 The intent set is the fourteen of PRD §6.7. Report recall as **fourteen rows**, never a mean.
- [ ] 3.8 Holdout must include one question per intent that is *nearly* another intent (`context` vs `memory`, `wiring` vs `graph`), because adjacent intents are where a confident wrong route is invisible.

## 4. Frontend Dev — the surface

- [ ] 4.1 New route on the customer console; no existing route modified.
- [ ] 4.2 One renderer per message kind; reuse the existing scorecard and proposal card structures rather than inventing a chat aesthetic beside them.
- [ ] 4.3 Render all four `finding` states — `measured`, `not_measured`, `refused`, `stale` — distinctly.
- [ ] 4.4 Render the three failure classes as three messages with three copy strings.
- [ ] 4.5 Message-kind union generated from Go per ADR-007; a kind present in Go and absent from the union fails the type-check.
- [ ] 4.6 SSE client with acknowledgement cursor, reconnect and backoff; no duplicate, no gap.
- [ ] 4.7 Streaming region announced politely for assistive technology.
- [ ] 4.8 `Intl` through the single `en-US` swap point; no new formatting call sites.
- [ ] 4.9 No colour / spacing / type / radius literals; `npm run scan:tokens` stays green. Hazard palette only on `refusal` and armed `approval_request`.
- [ ] 4.10 A stated, visible consequence of D1: the UI says what a reload preserves and what it does not.
- [ ] 4.11 Render the phase, the declared budget and the remaining budget while a turn runs — the four facts a spinner withholds (PRD §9.1).
- [ ] 4.12 Render the plan as a checklist that fills in, and the `result`'s reconciliation against it; a `skipped` or `not_measured` step is visually distinct from a `done` one.
- [ ] 4.13 Render the stop reason on every terminal message, including `satisfied`. A run that finished normally states so.
- [ ] 4.14 Display the turn's `trace_id` and make it copyable.
- [ ] 4.15 A `finding` links to the surface that owns it (PRD §6.7) rather than restating its numbers.

## 5. DevOps — the stream survives the deployment

- [ ] 5.1 Disable response buffering for the stream path at every proxy hop, in Compose, Kubernetes and the air-gapped topology.
- [ ] 5.2 Edge assertion that a streamed response arrives incrementally — a batched-at-the-end delivery fails the check.
- [ ] 5.3 Readiness endpoint is not behind the same exhaustible connection pool as the long-lived streams.
- [ ] 5.4 Connection-count and stream-duration metrics exposed on a readable health endpoint, not only in logs.

## 6. QA — fences that can go red

- [ ] 6.1 `finding` with no evidence ref → refused. **Mutate the check; the test must fail.**
- [ ] 6.2 Model output shaped exactly like a `proposal`, with a well-formed but non-existent `proposal_id` → no proposal created. Fixture must be genuinely adversarial and unsanitized.
- [ ] 6.3 6.2 again with injection detection deliberately disabled → still no effect (the structural defence, not the classifier).
- [ ] 6.4 Repository fixture containing an injection string → no action taken, a `finding` raised.
- [ ] 6.5 Go message kind added without the TSX union member → build fails.
- [ ] 6.6 Kill the stream mid-run, reconnect → replay from last acknowledged; assert no duplicate and no gap.
- [ ] 6.7 Repeat question → same content address replayed, and assert **no provider call was made**.
- [ ] 6.8 Approval in conversation → live event: HTTP 200, then `SELECT` the approval row, then assert the run advanced. A 200 is not evidence of a write.
- [ ] 6.9 One case per (message kind × state) renders.
- [ ] 6.10 503 / 404 / transport render as three distinct messages.
- [ ] 6.11 Browser acceptance for A1 — a question reaches a per-surface answer with no CLI installed. A green build is not acceptance.
- [ ] 6.12 Force each of the four limits and assert the terminal message names that limit and is not rendered as complete. **Mutate the check; the test must fail.**
- [ ] 6.13 A `result` missing one reconciliation entry → refused. Mutation-verified.
- [ ] 6.14 A `result` citing a non-existent verdict → refused, with injection detection disabled (structural defence, not the classifier).
- [ ] 6.15 Intent/route set-equality fence: add a route with no intent → build fails; add an intent with no route → build fails.
- [ ] 6.16 Loop fixture whose stop condition never fires → terminates on the step ceiling, names the step.
- [ ] 6.17 A `trace_id` from another tenant → refused without disclosing existence.
- [ ] 6.18 Resume mid-turn asserts the remaining budget came from the run: tamper with the client's claimed history and assert the server ignores it.

## 7. Product Designer + Sales Operations

- [ ] 7.1 Copy for `not_measured` that reads as a state, not as an omission.
- [ ] 7.2 Copy for an un-approvable `approval_request` that names the reason.
- [ ] 7.3 Customer-facing description promises only what shipped, and states the no-composite-score boundary (program ruling R4) as a deliberate choice.
- [ ] 7.4 Noun dictionary check: the surface uses `workflow`, `node`, `axis`, `proposal`, `run` exactly as the rest of the product does.
- [ ] 7.5 Copy for a budget-stopped run that reads as a *state with a next action*, not as a failure and not as a completion.
- [ ] 7.6 Copy for `skipped` that names the reason. "Skipped" alone is the omission problem with a label on it.
- [ ] 7.7 Customer-facing description states the fourteen things the surface can be asked, because an open text box implies infinity and every question outside it returns a refusal.

## 8. Sign-off

- [ ] 8.1 PRD §14 Q1–Q5 answered and folded into this change set.
- [ ] 8.2 The four routes reviewed against the ingress fence.
- [ ] 8.3 §7.3's boundary reviewed by whoever owns the security posture, against the ADR-005 / ADR-013 credential picture.
- [ ] 8.4 PRD §14 Q6 (where the budget envelope comes from) and Q7 (the step re-entry ceiling's value and scope) answered before FR17 and FR22 are implemented.
