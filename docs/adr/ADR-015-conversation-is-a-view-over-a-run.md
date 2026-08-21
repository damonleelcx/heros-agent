# ADR-015 — A conversation is a view over a run; the run is the subject

- **Status:** Accepted (2026-08-20) — implemented by
  [P31](../../openspec/changes/p31-conversational-console/) §1–§6
- **Deciders:** System Design (proposed) + User (ratified: per-person scope, PRD §14 Q1–Q7 recommendations)
- **Resolves:** [P31 design.md D1](../../openspec/changes/p31-conversational-console/design.md) and
  [PRD §14 Q1/Q4](../prd/P31-conversational-console.md) — *does the transcript persist, and is it
  per-person or per-tenant?*
- **Relates to:** [ADR-008](ADR-008-console-tenant-identity-seam.md) (scope comes from the session,
  never from the request) and P23's data inventory, which is the register this decision keeps a row out of
- **Owns:** phase **P31 — The Conversational Console** ([PRD](../prd/P31-conversational-console.md))

## Context — what problem this solves

P31 adds a surface where a person types a sentence and an agent works for minutes. Every chat product
ever built stores the transcript, so "store the transcript" arrives as an assumption rather than a
decision, and by the time anyone asks about retention there is a table with customer prose in it.

Three things are true at once and they pull in different directions:

| | |
|---|---|
| The **work** is already durable and already owned | the run carries the tenant, the evidence, the budget and the phase |
| The **transcript** is not the work | it is a sequence of messages *about* a run, most of which are re-derivable from the run's own record |
| The transcript holds **the customer's own words about their own source** | which is a data class this repository has never held before |

The third row is the one that decides it. A stored transcript is not "the run, again". It is free text a
person typed about a private repository, and it acquires retention, export, deletion, disclosure and
subpoena properties the moment it lands in a table.

## Decision

**The run is the record. The conversation is a view over it.**

1. A conversation is bound to exactly one run at creation and holds no task state of its own.
2. Phase, remaining budget, completed steps and every emitted message are read **from the run**. Resume
   replays the run's message log; it never reconstructs state from what a client says it saw (FR21).
3. The transcript is **not** an independently queryable, exportable, retained object. It lives as long as
   its run's record does, under that record's existing rules.
4. **Cross-conversation memory is refused**, not merely unimplemented. A question that depends on an
   earlier conversation gets a `refusal` saying the surface carries no memory across conversations, and
   no cross-conversation store is read or written.
5. **Scope is per-person** (PRD §14 Q4). A conversation is visible to, resumable by and approvable by the
   person who started it, within the tenant that owns the run.

## Why — the argument, level by level

The eight-level rule decides this without a debate, which is why it is written down rather than argued
each time:

| Level | What persistence costs or buys |
|---|---|
| **1 · Security** | a new data class containing customer prose about customer source |
| **5 · One-way door** | retention, export and deletion rules written once are written forever; the first version gets written by whoever is closest to a deadline |
| **3 · UX** | scrolling back through last week's question — real, and genuinely valuable |

Level 3 loses to levels 1 and 5. It is not close.

**Why per-person rather than per-tenant.** Per-tenant makes a team's runs legible to each other, which is
a real benefit. It also shows one member which repository another member was worried about and the exact
sentence they typed about it. The deciding property is reversibility: widening per-person to per-tenant
later is **additive** and breaks nobody; narrowing per-tenant to per-person later **removes a capability
people have built habits around**. Nothing is hidden that was previously shared — the tenant still owns
the run, and a colleague reaches the same work through `/app/runs`.

**Why "refused" rather than "not yet built" for cross-conversation memory.** A surface that silently has
no memory and a surface that has decided not to have one are indistinguishable to a user, and only one of
them will still be true after somebody adds a cache. Refusing makes the decision observable and makes its
reversal a deliberate act with this ADR to argue against.

## Rejected alternatives

**Persist transcripts from day one.** Better UX, and the version of the retention rule it produces is
written under deadline pressure by whoever needs the feature. Rejected on levels 1 and 5.

**Persist, but scrub prose before storing.** Sounds like both. It is not: the scrubber is a classifier,
the class it is protecting is "everything a person might type about their private code", and a scrubber
with a false-negative rate over that class is a retention rule with a false-negative rate.

**Store the transcript on the run record itself.** This is the tempting middle, and it is the same
decision wearing the run's clothes: the bytes are the same bytes and the data-inventory row is the same
row. What makes the chosen design different is not *where* the messages live but that **nothing outside
the run's own lifetime, ownership and deletion can reach them** — no separate query path, no export, no
retention clock of their own.

## Consequences, including the one users will complain about

- 🔴 **A reload resumes the run; it does not restore a chat history spanning runs.** This is stated in the
  UI (task 4.10) rather than discovered. Users will ask for the latter, and this ADR is what that request
  has to argue against.
- Resume is a read of the run, which makes FR21 checkable: a fence tampers with the client's claimed
  history and asserts the server ignored it (task 6.18).
- P23's data inventory gains no row from P31.
- A future phase that wants persistence has one decision to make and one document to change, rather than
  a table to discover and a retention rule to retrofit.

## Related decisions in the same change set, recorded here so they are not re-litigated

| Question | Answer | Why |
|---|---|---|
| **Q2** stale pin | answer from the pin, label it stale, name the revision, offer a re-run | refusing on staleness is P30's operator rule applied to a customer, which is too rigid; answering silently is worse |
| **Q3** clarification | at most one, from a closed set of disambiguations, never free-form | free-form clarification is the channel through which an ambiguous intent becomes a confident wrong one |
| **Q5** session expiry mid-run | the run continues (it was authorized when started); its result is retrievable by the tenant, not by the expired session | cancelling loses work a person paid for; continuing under the original authorization is what "the run is the subject" already implies |
| **Q6** budget envelope | derived from the tenant's entitlement, displayed, not editable in the conversation | an editable budget is how one question spends a month's allowance |
| **Q7** step re-entry ceiling | reuse `harnessruntime`'s `TurnCeiling`, per step, as a constant | a ceiling an operator can raise is a ceiling that gets raised at 2am |
