# Design — P38: The Agent Contract

## Context

A page that configures the agent this platform runs over every customer's source. It exists, it is
reachable, its capability has a green ledger row, and it asks the operator to paste hashes into eight
text boxes while the editor that was specified and written for it sits in the tree with no callers.

Two design problems, and they are different in kind. The first is mechanical: connect what exists. The
second is a modelling problem — the agent's contract is twenty dimensions, most of them already enforced
somewhere in the codebase, and the naive way to surface them (one table, one row per dimension, all
editable) breaks a determinism guarantee in a way no test can see.

## D1 — Three states, and no fourth

**Decision.** Every dimension is `authorable`, `observable`, or `fixed`. All twenty are always rendered.
A `fixed` dimension carries its reason verbatim from the decision that fixed it, **and** what would
change it.

**Why.** These are three different situations with three different next actions for the operator — change
it here / it is enforced and decided elsewhere / it is deliberately constant — and a boolean expresses
two of them. The page already makes this exact argument one level down, where axis status is
`set` / `defaulted` / `not_in_effect` rather than a checkbox, because *"an axis that is inert for an
unstated reason is a configuration an operator cannot act on"*.

**Rejected.** *Render only what is editable.* The smallest page, and it removes any way to discover that
a turn ceiling exists. An operator who cannot see a guardrail cannot ask for it to change, and the
guardrail's absence from the page reads as its absence from the system.

**Rejected.** *Everything editable, silently clamped to a hard maximum.* Maximum apparent flexibility.
A clamped value rendered as accepted is a refusal rendered as success — the specific failure this
repository names and bans — and it weakens a level-1 boundary to buy a level-3 convenience.

**Consequence, stated plainly because it is a partial refusal of the request.** The request was
interactive editors for every dimension. Guardrails, validation, and three ceilings are not given
editors. They are given surfaces. §D5 is the argument.

## D2 — 🔴 The contract is a VIEW; the hash boundary is the whole design

**Decision.** The contract does not become an object that subsumes the definition. Dimensions that
already participate in `config_hash` continue to participate exactly as they do. Dimensions that do not
are recorded as **operating policy** — versioned, audited, attributed — and never enter the hash.

**Why.** Every pinned inference is keyed by `(source_revision, agent config_hash)`. A contract table
that carried the goal statement *and* the model ref would change the definition's shape, and changing the
definition's shape orphans every pin in the fleet. The failure mode is the worst kind this codebase
produces: nothing errors, the console keeps rendering, and weeks later assessments silently re-run at
provider cost against a configuration that no longer exists. **No test goes red.**

**Rejected.** *One contract row per agent version.* Much simpler to render and to reason about, and it
makes an editorial change to a sentence describing the agent's purpose invalidate every measurement the
platform holds.

**How it is tested, and this is the acceptance criterion, not a nicety.** A fence in **both** directions.
Change a non-hashed dimension → assert the `config_hash` is unchanged and the pins still resolve. Change
a hashed dimension → assert a new `config_hash` exists. One direction catches the orphaning bug; the
other catches a dimension quietly drifting out of the identity it is supposed to be part of. A fence in
one direction only would pass while the system was broken in the other.

## D3 — Wire the editor that exists; a second implementation is a review failure

**Decision.** The per-axis editors are `internal/herosagent/axiseditor.go`, connected to routes.

**Why.** It is written, it is tested, and its refusals are the ones the folded spec describes — an
uncompilable skill schema is not selectable, a schema with a remote `$ref` is rejected rather than
fetched, an unapproved tool is not bindable, params validate at save naming the parameter. Writing a
second implementation in the handler layer would fork the vocabulary between the route and the package,
which is the failure the package was written to prevent.

**Consequence.** If the package's API turns out not to fit the routes, the correct response is to change
the package, not to grow a parallel one beside it. This is worth writing down because the parallel
implementation is always the faster option in the moment.

## D4 — The ledger fence gains one conformance assertion, not a general one

**Decision.** `scan-ledger.mjs` additionally asserts, for the `operator-agent-authoring` row only, that
the destination renders a picker bound to each axis vocabulary and that no axis is served by a free-text
input.

**Why.** The general fix — every row asserting that its destination *does* something rather than *exists*
— is the real answer to §2.3 and it is a large change to a fence that fourteen phases depend on. Getting
it wrong turns the operator console red for reasons nobody can act on, which is how a fence gets
disabled. One row, one assertion, and the general finding written down and assigned.

**What this does not claim.** It does not close the presence-versus-conformance hole. Every other row in
the ledger is still satisfied by a URL. Saying so here is the point; a narrow fix presented as a general
one is worse than no fix, because it retires the concern.

## D5 — Guardrails and validation are `observable`, deliberately

**Decision.** The skill gate, the sandbox posture, the typed contracts and the untrusted-source boundary
are rendered in full — what they check, what they currently enforce, where they are decided — and are not
editable on this surface.

**Why.** They are the mechanisms that stand between the agent and harm to a customer's repository. An
operator console that can turn one off is a level-1 risk created by a level-3 feature. Rendering them
read-only gives the operator everything the request was actually for — *see every dimension, know which
ones you own* — and gives up only the ability to weaken them from a web form.

**If a specific one should become tunable**, that is its own argument with its own blast radius, made
once, for that mechanism. It is not a side effect of building a configuration page.

## D6 — Grouped by question, not by the principle's numbering

**Decision.** The twenty dimensions group into five sections — *what it is for*, *what it runs*, *what
stops it*, *how it is measured*, *how it ships* — inside the console's existing tab component.

**Why.** A list in the order the principle document happens to use is an index. An operator arrives with
one of five questions, and the on-call operator's question — *what stops this thing?* — must be
answerable from one screen rather than assembled from rows 12, 13 and 17.

## Data-model sketch

```
Definition                       ← hashed; unchanged from P30
  prompt_ref model_ref credential_ref skill_refs tool_names
  context_ref memory_ref harness_ref set_versions
  ⇒ config_hash                  ← every pinned inference keys on this

OperatingPolicy                  ← NOT hashed; versioned + audited separately
  goal_statement
  placement_default
  caps (tenant, fleet)           ← already stored; surfaced here
  approval_classes
  eval_floors                    ← already stored on the rehearsal; surfaced here
  rollout_stage                  ← already stored; surfaced here
  updated_by updated_at reason

ContractView                     ← derived, never stored
  dimension × { state, value, why_fixed, what_would_change_it, creates_version }
```

`OperatingPolicy` is the one candidate for a new table in this phase, and it may not need to be one —
the audit log already holds every change with actor and reason, so "current operating policy" may be a
projection. PRD §14 Q1 asks it, and the default answer in this repository is **do not create a table**.

## Risks this design accepts

- **The refusal in D1 and D5 will read as a smaller version of the ask.** It is stated in the proposal,
  in the PRD summary, and here, rather than delivered quietly as a page with ten disabled controls.
- **Twenty dimensions is a lot of surface.** D6 groups them; the read-only phase ships first so the
  navigation is exercised before any of it is writable.
- **The loop and graph dimensions render `fixed` until P36 lands**, with "HEROS is one node" as the
  reason. That reason is true today and stops being true in a change that will also update the row —
  which is the good case for a stale-able reason, and it is worth noticing that not every `fixed` reason
  will have that property.
