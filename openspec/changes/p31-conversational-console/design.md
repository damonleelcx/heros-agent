# Design — P31: The Conversational Console

## Context

A chat surface over a platform whose founding console rule is *the browser derives nothing*, and whose
founding product rule is *diagnosis proposes, verification decides*. Both rules are in tension with the
thing a chat surface naturally is: a place where a model writes prose and a reader decides what it meant.

The design's job is to get the conversational **feel** — sequencing, streaming, follow-ups, approval in
place — without letting a model into the result position or into the UI.

## D1 — A conversation is a view over a run, not a durable subject

**Decision.** The run is the record. The conversation is a sequence of messages *about* a run, and (until
open question Q1 is answered) is not persisted as an independent, queryable, exportable object.

**Why.** A persisted transcript holds the customer's own prose about their own source. That is a new data
class: retention, export, deletion, and disclosure all attach to it, and P23's data inventory would gain a
row. The product value of persistence — scrolling back through last week's question — is real but level-3
(UX); the cost is level-1 and level-5 (a new data class is a one-way door). The eight-level rule decides
this without argument.

**Rejected.** *Persist transcripts from day one.* Better UX, and it means the first version of the
retention rule is written by whoever is closest to a deadline.

**Consequence.** A reload resumes the *run* and replays its messages from the run's own record; it does
not restore a chat history spanning runs. Users will ask for the latter. That is Q1.

## D2 — Typed messages, not prose

**Decision.** Eight kinds, closed. Every kind that makes a claim carries the reference that supports it.
`answer` — free prose — is admissible only for questions that assert nothing about the repository.

**Why.** The alternative is prose plus a client-side parser that finds the actionable parts, which puts an
interpreter of statistical claims in the browser. P9's rule already forbids the browser recomputing a
score; letting it *extract* one from text is the same failure with worse ergonomics.

There is a second reason that matters more. A typed message is **falsifiable**: a `finding` either has an
evidence reference or it does not, and a fence can check it. Prose is not falsifiable, so nothing can be
built to keep it honest.

**Rejected.** *Prose with structured annotations attached.* Sounds like both; behaves like prose, because
the prose is what the reader reads and the annotation is what nobody checks.

## D3 — Reuse the SSE substrate; do not add a socket

**Decision.** `text/event-stream`, on the transport `internal/api/monitor.go` already uses.

**Why.** The browser's writes in this surface are ordinary request/response — submit a turn, submit an
approval. Bidirectionality buys nothing and costs a new ingress concern in Compose, Kubernetes and the
air-gapped topology. SSE already crosses the P19 ingress and is already exercised.

**Consequence, and it is a real one.** SSE dies quietly behind a buffering proxy: the stream still
completes, so nothing errors, and every message arrives at the end in one burst. That failure mode is
indistinguishable from slowness at the application layer, which is why §7.1's "no message for 15s is a
defect" exists and why the edge configuration is asserted rather than assumed.

## D4 — Approval routes to the existing gate

**Decision.** An approval given in the conversation is submitted to `internal/approval`.

**Why.** A "yes" in chat is not a new authorization primitive. A second approval path is a second place
for the entitlement check, the automation-level check, and the attribution to be wrong — and they are the
checks that stand between a proposal and a customer's repository.

**Consequence.** The conversation cannot approve anything the rest of the platform could not. That is the
point, and it means the surface will sometimes have to render "you cannot approve this here" — which it
does by delivering an already-un-approvable `approval_request` carrying the reason, rather than by hiding
the message. A hidden control is indistinguishable from one that does not exist.

## D5 — Intent routing abstains

**Decision.** A classifier over a closed intent set, permitted to abstain. An abstention produces a
`refusal` naming what the surface can do.

**Why.** The metric that matters is not accuracy. A router that is 95% accurate and never abstains
silently answers a different question 1 time in 20, and the user has no way to notice — the answer is
well-formed, it is just about something else. A router that is 88% accurate and abstains on the remainder
is strictly better here, because every failure is visible.

**Consequence for evaluation.** Report per-intent recall and abstention precision, never a mean. An
aggregate over intents hides the single intent that is broken, which is the AI lens's standing warning.

## D6 — Provenance is recorded per message

**Decision.** Each message records whether it came from a pinned inference or was generated in this turn.

**Why.** P30's determinism guarantee is invisible without it. A user who asks the same question twice and
gets the same answer cannot tell whether the system is deterministic or merely consistent, and a guarantee
nobody can falsify is not a guarantee — it is a claim.

## D7 — Effects need artifacts a model cannot mint

**Decision.** `proposal`, `approval_request` and `result` are constructed by the platform from typed
artifacts — a `proposal_id` resolvable in the verification ledger, a delivery record — never from model
output.

**Why.** This is the only defence in §7.3 that does not depend on detection working. Injection detection
is a classifier and classifiers have a false-negative rate; if the *only* defence is detection, the system
is secure at exactly the rate the classifier is accurate. Under D7, a fully compromised model can still
only produce text, and text is not a ledger row.

**How it is tested.** The fence must construct genuinely adversarial model output — including a
well-formed identifier that does not exist in the ledger — and assert no proposal is created. A test
whose fixture was already sanitized by a helper proves nothing, and is the shape this fence will take if
nobody is watching.

## D8 — The turn is an agent loop with named phases, not a request that takes a while

**Decision.** Every turn advances through `understand → plan → act → verify → respond`. The phase is
carried on `progress`. The `plan` declares a budget envelope — turn ceiling, token budget, tool-call
ceiling, wall-clock ceiling — before the first step runs. The terminal message names the stop reason from
a closed vocabulary, and reconciles every step the plan declared.

**Why.** The failure this prevents is not a crash; it is a plausible short answer. An agent that ran
three of eight planned steps because it hit a token budget produces prose indistinguishable from an agent
that ran all eight — same tone, same confidence, fewer facts. Nothing errors, nothing logs, and the
reader has no denominator. Announcing the plan first creates the denominator; reconciling it at the end
makes the shortfall a rendered state rather than an absence.

The budget is declared rather than discovered for the same reason a refusal is a message kind: a limit
you meet without warning is indistinguishable from a bug.

**Rejected.** *A spinner plus a final answer.* It is what every chat product does and it is why every
chat product's users cannot tell a hang from a loop from a finished-but-partial run.

**Rejected.** *Two new message kinds, `checkpoint` and `verification`.* The verify step already has a
home — `internal/verification` — and inventing a message kind for it would create a second notion of
"checked" beside the ledger the platform already gates proposals on. `result` cites the verdict instead.
Plan reconciliation is a field on `result`, not a kind, because it is not a separate event: it is what
the terminal message *is*.

**Consequence.** `harnessruntime.StopReason` (`satisfied` | `ceiling` | `single-shot`) is extended, not
duplicated — a conversation that ran out of tokens and a node loop that ran out of turns are the same
concept and must not acquire two vocabularies. Extending a closed set that is hashed into `version_id`
is expand-only; see P34's identical hazard.

## D9 — The intent set IS the working-surface set, and a fence asserts it

**Decision.** Fourteen intents, each resolving to exactly one working surface. A fence compares the
intent table against the console's route table and fails when either side has a member the other does
not.

**Why.** Without the fence, the two sets drift in one direction only: a surface ships, nobody adds its
intent, and the conversation quietly cannot reach it. The drift is invisible because the failure is a
*refusal* — well-formed, polite, and indistinguishable from the surface not existing. This is the same
shape as P26's discovery that fourteen phases of operator-console drift happened with nothing failing.

**Consequence, and it is the point.** The agent's goal set is now defined by what the product can show,
not by what a model can be prompted to attempt. An intent nobody can render is refused at the fence, in
the build, by whoever added it — not at run time, by a customer.

**What the fence cannot catch.** That the intent *routes well*. Set equality is structural; recall is
statistical, and D5's per-intent reporting is what covers it. Two different fences, two different
failures, and neither substitutes for the other.

## Data-model sketch

```
Conversation
  conversation_id
  tenant_id            ← from the session, never from the request
  workflow_id
  run_id               ← the durable subject
  created_at

Message                (ephemeral until Q1; ordered, monotonic id per conversation)
  message_id           ← acknowledgement cursor for resume
  kind                 ← closed enum, generated into the console type union (ADR-007)
  provenance           ← pinned | generated
  payload              ← kind-specific, typed
```

`Message.payload` per kind carries: `finding` → surface, claim, evidence ref, `measured | not_measured`;
`proposal` → `proposal_id`, axis, node, verified delta with CI, diff ref; `refusal` → axis, node, cause
text verbatim from the lower layer; `result` → run id, delivery ref.

## Risks this design accepts

- **A reload loses the transcript** (D1) until Q1 is decided. Stated in the UI, not discovered.
- **The surface can feel curt** — abstention (D5) and verbatim refusals (FR15) both make the agent say "no"
  in the user's words rather than softening it. This is deliberate: a re-worded safety boundary is a
  second, softer statement of it.
- **The plan is a promise the agent makes before it knows the work.** A reconciliation that is honest
  about a step it should never have planned still reads as a shortfall. Accepted: an over-planned step
  resolving to `skipped` with a reason is strictly better than an unannounced step that silently did not
  run.
- **Two rendering paths for the same information** — a finding in the conversation and the same evidence
  on `/app/workflows/...`. Mitigated by linking rather than duplicating: the conversation renders a card
  and links into the existing page, and never becomes the second place the number is computed.
