# PRD — P38: The Agent Contract

| | |
|---|---|
| **Phase** | P38 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p38-agent-contract`](../../openspec/changes/p38-agent-contract/) |
| **Lead roles** | System Designer + Frontend Dev |
| **Support roles** | Backend Dev, AI Engineer, QA, DevOps, Product Designer, Sales Operations |
| **Upstream** | P30 (HEROS as a Variant Spec; `axiseditor.go`; the rehearsal gate) · P8 (operator console, RBAC, audit) · P26 (the operator surface ledger) · P36 (the agent as a graph) |
| **Unblocks** | An operator being able to change the platform's agent without pasting hashes · P35 running under a configuration somebody can read |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

There is a folded spec in this repository —
[`openspec/specs/operator-agent-authoring/spec.md`](../../openspec/specs/operator-agent-authoring/spec.md),
which means *this is true today* — whose first requirement is **"Each axis SHALL be edited against its
existing vocabulary and never as free text"**, and whose header carries the sentence
**"🚫 No axis is a text box."** It specifies a prompt editor that parses slots, a skill picker that shows
compiled schemas, a tool picker that shows scope and risk tier, and params forms derived from each
vocabulary's schema.

There is a Go file — [`internal/herosagent/axiseditor.go`](../../internal/herosagent/axiseditor.go) —
that implements exactly that. `ParsePrompt`, `SelectableSkills`, `BindableTools`,
`ValidateHarnessParams`, `ValidatePolicyParams`. Its own header says *"axiseditor.go is D12: NO AXIS IS A
TEXT BOX, and every param validates AT SAVE"*.

**Every exported function in that file has zero non-test callers.**

And there is the console the operator actually uses:
[`web/admin-console/src/app/agent/page.tsx`](../../web/admin-console/src/app/agent/page.tsx), whose
Publish tab is **eight free-text inputs** into which an operator pastes 64-character version ids and a
comma-separated list of tool names. A screenshot of that page shows the axes as opaque hashes —
`f6557e8194753480ed9572a35700c74307e0efc19b8c076377736ce50f2bda8f` — with no way to see what the prompt
says, which tools are bound, or what the loop does.

So the first half of this phase is not new scope. It is **conformance recovery**: the specified editor
exists, the implementation exists, and nothing connects them.

The second half is the ask that motivated the phase. The platform's agent is not adequately described by
six axis refs, because the axes are only six of the twenty things that decide whether an agent is
reliable. Goal, boundaries, validation, guardrails, stopping conditions, evaluation set, reliability
targets, observability, approval policy, rollout and the improvement loop **all already exist in this
codebase** — as constants, as gates, as ladders, as fences — and **none of them is visible on the surface
that claims to configure the agent.** An operator reading `/agent` cannot tell what the agent is for,
what it refuses, what stops it, or what it costs before it stops.

P38 makes the agent's whole contract one surface: **twenty dimensions, all twenty always rendered,
each in exactly one of three states — `authorable`, `observable`, `fixed` — and a `fixed` one always
names why and what would change it.**

The three states are the phase's central decision, and they exist because "make everything editable" is
wrong here in a specific, checkable way. `axiseditor.go:31` says of the turn ceiling: *"🔴 It is a
CONSTANT rather than configuration. A ceiling an operator can raise is not a ceiling."* That is a level-1
safety property. Rendering it as `fixed` with its reason satisfies the real requirement — **an operator
can see every dimension and knows which ones they own** — without trading a safety boundary for a form
field.

---

## 2. Problem & context

### 2.1 What the operator can configure today, exactly

`/agent` has six tabs. Axes, Availability, Versions, Instruction, Publish, Rehearsal.

- **Axes** renders a read-only table: axis, status (`set` / `defaulted` / `not in effect`), value as a
  64-character hash, and a "why not in effect" column. The status vocabulary is good and is kept. The
  value column is unreadable.
- **Instruction** is a real editor — a name and a textarea — and it is the *only* dimension on the page
  that can be authored as content rather than as a reference.
- **Publish** is eight text inputs: `prompt`, `model`, `credential_ref`, `context`, `harness`, `memory`,
  `skill_refs` (comma-separated), `tool_names` (comma-separated). Composing a definition means having
  the version ids somewhere else and pasting them.
- **Rehearsal** shows the per-fixture report. It is the surface that gates activation, and it is well
  built.

What is nowhere on the page: what the agent is for, what it refuses to do, what validates a tool call,
what stops a runaway, what it is measured against, what it costs before it is cut off, who must approve
what, how a change reaches customers, and what happens to a version that fails.

Those are not missing features. **Every one of them is implemented.** They are missing *surface*.

### 2.2 The twenty dimensions, and where each already lives

The list below is the agent-engineering principle set the operator asked to be able to configure, mapped
onto what this codebase already contains. The "state" column is P38's proposal, not a fact of the code.

| # | Dimension | Where it lives today | Proposed state |
|---|---|---|---|
| 1 | **Goal** — what problem the agent solves, what success looks like | implicit inside the prompt body | `authorable` (new; recorded, **not** hashed) |
| 2 | **Boundaries** — what it may not do, when it refuses or escalates | `herosagent/errors.go` refusals; `placement.go` (`platform` / `customer` / `disabled`) | placement `authorable`; refusal set `observable` |
| 3 | **Workflow** — the step decomposition | `harnessruntime` strategies; P36's node graph | `authorable` (harness + graph axes) |
| 4 | **Model** | the operator model registry; refused at publish if unregistered (`ErrModelUnregistered`) | `authorable` (model axis) |
| 5 | **System prompt** | prompt registry; `axiseditor.ParsePrompt` derives slots | `authorable` — **implemented, unwired** |
| 6 | **Context engineering** | context registry and its named policies; `ValidatePolicyParams` | `authorable` — **implemented, unwired** |
| 7 | **Tools** | tool index; `BindableTools` carries scope, risk tier, approval, network declaration | `authorable` — **implemented, unwired** |
| 8 | **Agent loop** | `harnessruntime`: five strategies, each declaring the host service it needs | `authorable`, with unavailable strategies shown disabled |
| 9 | **State & memory** | memory registry; `memoryruntime`; refuses rather than degrades | `authorable` — **implemented, unwired** |
| 10 | **Retrieval** | `internal/embeddings`; the `retrieval-tuning` capability; `rag-retrieval` is a declined context policy with a stated reason | `authorable` where the deployment supplies it, `fixed` with that reason where it does not |
| 11 | **Validation of every action** | `skillgate.CheckInput` / `CheckOutput` against compiled JSON Schemas; `typedcontract` | `observable` — the schemas are shown, not edited here |
| 12 | **Guardrails** | `internal/sandbox`, `sandboxaudit`, the skill gate, and P31 §7.3's untrusted-source boundary | `observable`, with the injection-detection posture stated |
| 13 | **Stopping conditions** | `TurnCeiling = 16` (a constant); the per-inference budget; `caps.go`'s tenant and fleet token ceilings over a 30-day rolling window | **mixed**: caps `authorable`, ceiling `fixed` |
| 14 | **Evaluation dataset** | `fixtures.go`'s calibration set; `evalgen` | `authorable` (which fixtures), with a floor that may not be zero |
| 15 | **Reliability measurement** | `rehearsal.go`: per-fixture precision and recall against declared floors, gating activation | floors `authorable` above a minimum; results `observable` |
| 16 | **Observability** | `internal/telemetry`, `metricevent`, the spend surface | `observable` |
| 17 | **Human approval for high-risk actions** | `internal/approval`; the entitlement and automation-level checks | `authorable` (which classes require approval) |
| 18 | **End-to-end testing** | `internal/e2e`; the rehearsal gate | `observable` |
| 19 | **Gradual rollout** | `rollout.go`: internal → design partner → opt-in → default-on, where `Advance` **refuses** unless it can read the evidence itself | `authorable` (advance / hold), never automatic |
| 20 | **Continuous improvement** | `internal/optimizer`, `proposalgen`, re-inference | `observable` |

Two readings of this table matter.

**First: this is a surfacing problem far more than a building problem.** Fifteen of twenty already exist
and work. Four of the six axes have a written, tested editor core sitting unused. Exactly one dimension —
the goal — has no home at all.

**Second: the three-state split is forced by the code, not invented for the PRD.** Rows 13 and 15 contain
values the codebase deliberately made unchangeable, with the reason written next to them. A design that
makes all twenty editable would have to overrule those decisions, and it would do so for convenience.

### 2.3 Why the gap survived fourteen phases

The operator console has a fence. `openspec/operator-surface-ledger.md` requires every capability in
`openspec/specs/` to have a row naming an operator destination, and
`web/admin-console/scripts/scan-ledger.mjs` fails the build otherwise. Its own header explains why it
exists: *"Fourteen phases of operator-console drift happened with nothing failing."*

The ledger's row for this capability reads:

```
| operator-agent-authoring | surface | /agent#publish, /agent |
```

That row is **true**. `/agent#publish` exists. And it is why the gap survived: **the fence asserts that a
destination exists, not that the destination implements the capability.** Eight text boxes at a URL
satisfy it exactly as well as eight editors would.

This is the institutional layer of the root cause, and naming it matters more than fixing the page,
because the same hole covers every other row in the ledger. A capability can be folded into
`openspec/specs/` — declared true — while nothing on the surface exercises it, and no build goes red.
§6.7 proposes the narrow fix; the general one is flagged for the ledger's owner rather than smuggled in
here.

---

## 3. Goals & non-goals

### Goals

1. **G1** — An operator can read, on one surface, **all twenty dimensions** of the agent's contract, and
   for each one: its current value in a human-readable form, its state, and — when it is not editable —
   why, and what would change that.
2. **G2** — No axis is a text box. Every authorable dimension binds to its existing vocabulary, validates
   at save, and refuses by name. This satisfies a requirement already folded into `specs/`.
3. **G3** — An operator can see what the prompt **says**, which tools are bound **with their scope and
   risk tier**, what the loop **does** and how many turns it may take — without leaving the page and
   without resolving a hash by hand.
4. **G4** — 🔴 Changing a dimension that does not participate in `config_hash` **does not** create a new
   agent version and **does not** orphan a single pinned inference. Changing one that does, does — and
   the surface says which is which **before** the operator saves.
5. **G5** — Every change carries a reason, lands in the audit log, and is attributed. Unchanged from P8;
   extended to the eleven dimensions that had no editor.
6. **G6** — Publishing still serves nothing. The rehearsal gate and the separate activation act are
   untouched.
7. **G7** — A dimension that is `fixed` is **rendered**, not hidden. A hidden control is indistinguishable
   from one that does not exist, and an operator who cannot see a guardrail cannot ask for it to change.

### Non-goals (with the phase that owns them)

- **The agent's shape as a graph** — [P36](P36-agent-self-configuration.md). P38 renders the wiring and
  graph dimensions; P36 is what makes them non-vacuous.
- **The customer's own axis editors** — [P37](P37-source-bound-editors.md). Same complaint, different
  console, different blast radius.
- **Changing any gate.** Rehearsal floors, the activation gate, the rollout ladder's preconditions and
  the placement rule are surfaced, not relaxed. A surface that made a gate easier to pass would be the
  opposite of this phase.
- **Making the fixed dimensions configurable.** Explicitly refused, per dimension, with the reason
  rendered. If a specific one should become authorable, that is a separate decision with its own
  argument — not a side effect of building a form.
- **A general audit of the operator surface ledger's presence-versus-conformance hole.** Named in §2.3,
  narrowly fixed in §6.7, and flagged for its owner.

---

## 4. Users & personas

| Persona | What they need from `/agent` | What they get today |
|---|---|---|
| **Platform operator** (primary, holds `agent.admin`) | change the instruction, the model, the tools, the loop — and know what it costs and what it will break | a textarea for the instruction and eight boxes for hashes |
| **Operator on call** (holds `agent.read`) | "what is serving, and what stops it if it runs away?" | what is serving; nothing about what stops it |
| **Whoever owns the security posture** | "what can this agent do to a customer's repository, and what validates that?" | nothing on this page |
| **Whoever signs off a rollout** | "which stage are we in, what does advancing require, what is the evidence?" | `/agent/spend#placements`, a different page, partially |

---

## 5. User stories

- **US1** As an operator I open `/agent` and read the prompt the agent is actually running, as text.
- **US2** As an operator I bind a tool by selecting it from a list showing its scope, risk tier and
  whether it declares network access — instead of typing its name into a comma-separated field.
- **US3** As an operator I choose a harness strategy and see the ones this deployment cannot run, greyed,
  each naming the service it needs.
- **US4** As an operator I edit the goal statement and the surface tells me, **before I save**, that this
  does not create a new version and does not invalidate any pinned inference.
- **US5** As an operator I change the model and the surface tells me, **before I save**, that this creates
  a new version, that it must pass rehearsal, and how many pinned inferences will need re-inference.
- **US6** As an operator I see the token ceiling, the turn ceiling and the budget together, and I can
  change the ones I own and read why I do not own the others.
- **US7** As an on-call operator I can answer "what stops this thing?" from one screen.
- **US8** As a security reviewer I can see the validation and guardrail dimensions and what they check,
  without reading Go.
- **US9** As an operator I am refused at save, by name, when a parameter is invalid — not at run time, by
  a customer's failed analysis.

---

## 6. Functional requirements

### 6.1 The contract and its three states (capability `agent-contract`)

**FR1 — The surface renders all twenty dimensions, always, in a stable order and grouped.** A dimension
is never omitted because it is empty, unavailable, or not editable in this deployment.

**FR2 — Each dimension carries exactly one of three states.**

| State | Meaning | What it must carry |
|---|---|---|
| `authorable` | the operator may change it here | the editor, the current value, the vocabulary it binds to |
| `observable` | it is real, it is enforced, it is not changed here | the current value **and** where it is decided |
| `fixed` | deliberately not configurable | the reason, verbatim from the decision that fixed it, **and** what would change it |

**FR3 — A `fixed` dimension names its reason and its escape hatch.** "A ceiling an operator can raise is
not a ceiling" is the reason; "a change to the constant, in a release, with the argument in the PR" is
the escape hatch. A refusal that does not say what would change it is a wall with no door drawn on it.

**FR4 — The three-valued axis status already on the page (`set` / `defaulted` / `not in effect`) is
retained**, unchanged, *within* a dimension. It answers a different question — whether a value was chosen
— and collapsing the two vocabularies would lose one of them.

### 6.2 🔴 The `config_hash` boundary

This is the requirement that, if it is got wrong, produces the failure no test catches.

Every pinned inference is keyed by `(source_revision, agent config_hash)`. If P38 adds contract sections
to the hashed definition, **every existing pin is orphaned**: assessments silently re-run at provider
cost, weeks later, while the console shows results attributed to a configuration that no longer exists.
Nothing errors.

**FR5 — The contract is a VIEW over things that already have identity.** It is not a new object that
subsumes the definition. Dimensions that already participate in `config_hash` continue to; dimensions
that do not are recorded in an operating-policy record that is versioned and audited **separately** and
does **not** enter the hash.

**FR6 — The surface states, per dimension and before the save, whether a change creates a new version.**
Two sentences, not one badge: *"this creates a new agent version and requires rehearsal"* or *"this
changes operating policy and does not create a version"*.

**FR7 — Where a change does create a version, the surface states how many pinned inferences it would
require re-inference for**, and at what cost, before the operator confirms. P30 made re-inference an
explicit act; P38 must not make it an accidental one.

**FR8 — A fence asserts the boundary in both directions.** Changing a non-hashed dimension and observing
a new `config_hash` fails the build. Changing a hashed dimension and observing no new `config_hash` fails
it too. One direction is the orphaning bug; the other is the silent-drift bug.

### 6.3 The editors (satisfying `operator-agent-authoring`)

**FR9 — No axis is a text box.** Prompt binds to the prompt registry and is authored as a template whose
slots are derived from the body. Model binds to the operator model registry. Skills bind to the skill
registry, each showing its `impl_handle` and its compiled schemas; a skill whose schema does not compile
is not selectable. Tools bind to the tool index, each showing tenant scope, description, risk tier and
approval; an unapproved tool is not bindable, and scope is always displayed because a `_global` tool and
a tenant-scoped tool of the same name are different bindings. Context and memory bind to their named
policies with params forms derived from each policy's schema. Harness binds to the strategy set, with
`max_turns` required for multi-turn strategies and refused above the ceiling.

**FR10 — Every param validates at save, naming the entry and the parameter.** A malformed value
discovered at run time is discovered by the wrong person: the operator who typed it has moved on, and
the person who meets the refusal cannot tell a bug from a configuration.

**FR11 — An option this deployment cannot run is shown, disabled, naming the service it needs.** Never
hidden. `react-loop` and `plan-execute` need host services this runner does not supply; an operator who
cannot see them cannot ask for them.

**FR12 — The credential is bound by provider name, never entered as a value.** Unchanged from P30 and
non-negotiable: no field on this surface accepts a key, and the reflective fence that discovers this by
type rather than by a maintained list stays.

### 6.4 The eleven dimensions that had no surface

**FR13 — Goal** is a short statement of what the agent is for and what success means. It is authorable,
audited, versioned in the operating-policy record, and **not** hashed — it changes no behaviour, and
hashing it would orphan pins for an editorial change.

**FR14 — Boundaries** render as two parts: the placement (authorable — `platform`, `customer`,
`disabled`) and the refusal set (observable — the typed refusals the agent can produce, listed with their
causes).

**FR15 — Stopping conditions** render as one group: turn ceiling (`fixed`, with its reason), per-inference
budget, tool-call ceiling, wall-clock ceiling, and the tenant and fleet token caps (`authorable`, with
their rolling window stated, because a cap whose window is unstated is a number nobody can act on).

**FR16 — Evaluation** renders the calibration set — which fixtures, and the per-fixture floors. Floors are
authorable **above a minimum**; a zero floor passes everything and is already refused in Go, and the
surface refuses it with the same sentence rather than a second one.

**FR17 — Approval policy** renders which action classes require human approval, authorable, routed to
`internal/approval` — never a second gate.

**FR18 — Rollout** renders the ladder, the current stage, the preconditions for advancing and whether each
is currently met. Advancing stays an explicit act that refuses unless it can read the evidence itself.

**FR19 — Validation, guardrails, observability, end-to-end testing and continuous improvement** render as
`observable`: what is enforced, what checks it, and where it is decided. An operator who can see them can
ask for them to change; an operator who cannot, cannot.

### 6.5 Preview, publish, activate

**FR20 — An edit shows its effect before it is published** — the resulting `config_hash`, the axis-by-axis
diff against what is active, and any refusals. This already exists in the publish route's response and is
rendered; P38 keeps it and extends it to the operating-policy dimensions.

**FR21 — Publishing serves nothing.** A published definition lands pending, analyses nothing until it
meets its floor on **every** calibration fixture individually, and is activated as a separate act. This
is the platform's strongest safety property and P38 changes none of it.

**FR22 — `no_change` remains its own outcome.** An edit resolving to something already published creates
nothing, and saying "published" would leave an operator waiting for a version that was never made.

### 6.6 Audit and attribution

**FR23 — Every change carries a reason and lands in the audit log with its actor**, including the eleven
dimensions that previously had no editor. An operating-policy change is audited exactly as a definition
change is; "it does not create a version" is not "it is not a change".

### 6.7 The narrow ledger fix

**FR24 — The operator surface ledger gains a conformance assertion for this capability.** A row may not
be satisfied by a destination that exists; for `operator-agent-authoring` the fence additionally asserts
that the destination renders a picker bound to each axis vocabulary, and fails if any axis is served by a
free-text input. This is narrow on purpose — the general presence-versus-conformance hole is §2.3's
finding and belongs to the ledger's owner.

---

## 7. Non-functional requirements

### 7.1 🔴 Determinism and the pinned-inference hazard

Stated again because it is the phase's one silent failure: adding a field to the hashed definition
orphans every pinned inference, and **no test goes red**. The console keeps rendering. The assessments
re-run weeks later at provider cost. The fix is FR5's separation and FR8's two-directional fence, and
both are acceptance criteria rather than design intentions.

### 7.2 Security

No dimension's editor may accept a credential value (FR12). The guardrail and validation dimensions are
`observable` precisely so that this surface cannot be used to weaken them: an operator console that could
turn off the skill gate would be a level-1 regression delivered as a convenience. Rendering them read-only
with their current posture is the whole of what this phase does to them.

### 7.3 RBAC

`agent.read` reads the contract. `agent.admin` changes it. Unchanged from P8, extended to the new
dimensions. A denied operator sees the dimension, its state and who holds the capability — not a blank
page.

### 7.4 Accessibility, i18n, tokens

Operator console tokens only; no new literals. Twenty dimensions do not fit a viewport, so the surface
groups them into the existing tab component rather than stacking — P9's rule applies to this console too.
Every editor is a labelled control reachable by keyboard, with validation errors associated to fields.
`en-US` through the single swap point.

---

## 8. System design summary

### 8.1 Shape

```
                    ┌──────────── Agent Contract (a VIEW) ─────────────┐
                    │                                                  │
   hashed ──────────┤  axes: prompt model skills tools context memory  │
   (config_hash)    │        harness loop graph                        │──► Definition ──► rehearsal ──► activate
                    │                                                  │      (immutable, content-addressed)
   not hashed ──────┤  goal · placement · caps · approval policy ·     │
   (operating       │  eval floors · rollout stage                     │──► Operating policy record
    policy)         │                                                  │      (versioned, audited, NOT hashed)
                    │                                                  │
   read-only ───────┤  refusal set · validation · guardrails ·         │
   (observable)     │  observability · e2e · improvement loop          │──► rendered from their owners
                    └──────────────────────────────────────────────────┘
```

### 8.2 Decisions

**D1 — Three states, not a checkbox.** *Why:* `authorable`, `observable` and `fixed` are three different
situations with three different next actions for the operator, and a boolean expresses two. This is the
argument the page already makes for `set` / `defaulted` / `not_in_effect`, applied one level up.
*Rejected:* rendering only what is editable — the smallest page, and it hides the existence of every
guardrail. *Rejected:* making everything editable with a hidden clamp — a clamped value rendered as
accepted is a refusal rendered as success.

**D2 — The contract is a view, not a new object.** *Why:* FR5's hazard. A contract table that subsumed
the definition would change the definition's shape, and the definition's shape is hashed. *Rejected:* one
contract row per agent version — simpler to render, and it makes an editorial change to the goal
statement orphan every pinned inference in the fleet.

**D3 — Wire `axiseditor.go` rather than write new editors.** *Why:* it exists, it is tested, and its
refusals are the ones the folded spec describes. Writing a second implementation would fork the
vocabulary between the route and the package, which is the failure the package was written to prevent.
*Consequence worth stating:* this phase's largest deliverable is routes and TSX, not domain logic — which
is why it is smaller than it looks.

**D4 — The ledger fence gains a conformance check for one row, not for all of them.** *Why:* the general
fix — every capability asserting that its destination *does* something — is a large change to a fence
fourteen phases depend on, and getting it wrong turns the whole operator console red for reasons nobody
can act on. One row, one assertion, and the general finding written down for its owner.

**D5 — Guardrails and validation are `observable`, and that is a deliberate refusal of the ask.** The
request was editors for every dimension. Two of them are the things that stop the agent doing harm, and a
console that can weaken them is a larger risk than a console that cannot show them being changed.
They are rendered in full, with their current posture and where it is decided. If a specific guardrail
should become tunable, that is its own argument.

### 8.3 Design key points

- Nothing about the rehearsal gate, the activation split, the immutability of versions, or the credential
  posture changes. P38 is a surface over them.
- The one genuinely new persisted thing is the operating-policy record, and §14 Q1 asks whether it should
  be a record at all or derived from the audit log.
- The three-state vocabulary is closed and generated into the console's type union per ADR-007, so a
  fourth state added in Go without a renderer fails the type-check.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The input reduced is the hash. An operator should never type or paste a 64-character identifier to
express "use this prompt" — the id is a machine's name for a thing that has a human name, and asking for
the machine's name is asking the operator to do a lookup the platform can do.

The truth that must not reduce is the `fixed` dimension. It is tempting to hide the ten things the
operator cannot change, because the page would be half the length and would consist entirely of controls.
That page would also be one where nobody can discover that a turn ceiling exists. G7 is the rule; the
`not_in_effect` status already on the page is the precedent.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

Two calls, and one of them overrules the request as stated.

**Safety (1) versus UX (3).** The ask is editors for all twenty dimensions. Three of them are ceilings the
codebase made constant on purpose, with the reason written beside them. Making them editable is a level-1
degradation bought with level-3 convenience, which L1 forbids outright. The three-state design is what
satisfies the *goal* behind the ask — see everything, know what you own — without the trade.

**The one-way door is the hash.** Adding contract fields to the hashed definition is not reversible in
any meaningful sense: the pins orphaned by the change are orphaned, and re-inference is a provider cost
somebody pays weeks later. D2 keeps the door shut.

No new table is proposed lightly; the operating-policy record is the one candidate and §14 Q1 asks
whether the audit log already answers it.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

Eleven dimensions gain a write path. Each one gets the four-layer treatment: HTTP, then `SELECT` the row,
then assert the surface renders it, then assert the audit entry exists with its actor and reason. A
config surface that returns 200 and writes nothing is the worst possible version of this page, because
the operator believes the change is live and the agent keeps running the old one.

The per-axis read routes must resolve refs to **content** — the prompt body, the tool names with their
scopes, the strategy with its params — because a route that returns the ref is the current page with
extra steps.

### 9.4 Senior Frontend Dev — *do not lose a feature in a rename*

The existing page has four things that must survive: the `serving_config_hash` displayed separately from
"the definition I am looking at"; the three-valued axis status; unavailable strategies shown rather than
hidden; and the wiring axis rendered read-only with its reason. All four are documented on the page as
things it *must never* do otherwise, and all four are easy to lose when a page grows from six tabs to
twenty dimensions.

Twenty dimensions do not stack. They group into the existing tab component, and the grouping is by
question — *what it is for*, *what it runs*, *what stops it*, *how it is measured*, *how it ships* —
because a list ordered by the principle document's numbering is an index, not a page.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

The rehearsal report is already per fixture and must stay per fixture. A "rehearsal: 94%" summary on a
contract page would hide the one fixture that fails, and the one fixture that fails is the entire reason
the gate exists — activation requires meeting the floor on **every** fixture individually, not on
average. If the surface renders a summary at all, it renders `n passed / n total` beside the failing
names, never a percentage alone.

The same rule applies to spend: per tenant and per dimension, never one fleet number, because the fleet
number is what a runaway looks like normal inside.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Blast radius: this surface configures the agent that reads every customer's source. The mitigations are
the ones that already exist and are untouched — publish creates nothing that serves, activation is
separate, rehearsal gates it, and the rollout ladder refuses to advance on anything but evidence.

Reversible: versions are immutable and content-addressed, so the undo for a bad definition is publishing
the previous one, which creates nothing because it already exists. That sentence should be in the UI, and
it already is in the publish action's `undo` field — it stays.

Observable: the contract's own health — whether the operating-policy record is readable, whether the
registries resolve, how many pins the active `config_hash` covers — is exposed on a readable endpoint,
not only in the page.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

Five fences, each shown to fail:

1. **Hash boundary, both directions** (FR8) — the phase's one silent failure.
2. **No free-text axis** — submit a version id into an axis field; there is no field to submit it to, and
   a test that adds one fails the build.
3. **Every dimension renders** — a dimension present in Go and absent from the surface fails the
   type-check; a dimension rendered without a state fails a runtime fence.
4. **A `fixed` dimension carries its reason** — mutate the reason to empty; the test fails.
5. **Save writes and audits** — HTTP → row → rendered → audit entry with actor and reason.

The acceptance that is not a fence: an operator, in a browser, changes the instruction and the tools
without leaving the page or resolving a hash by hand.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Two things must be said plainly and not softened.

**The ten `observable` and `fixed` dimensions are not "coming soon".** They are enforced today and
deliberately not editable, and the copy says exactly that. Rendering them with a disabled control and no
explanation would read as an unfinished feature, and would generate the request to finish it.

**This page cannot make the agent do more.** It shows what the platform already enforces. A summary that
implied the operator can now "configure the agent's guardrails" would be promising a capability this
phase explicitly refuses to build.

---

## 10. Dependencies

| Dependency | What P38 needs from it | If it is not there |
|---|---|---|
| [P30](P30-heros-platform-agent.md) | `axiseditor.go`, the definition, the rehearsal gate, the placement and cap stores | the phase has nothing to wire; this is the bulk of the work already done |
| [P8](P8-admin-console.md) | the operator shell, RBAC capabilities, the audit log, the action-with-reason pattern | every new editor would need its own auth and audit path |
| [P26](P26-operator-console-refresh.md) | the surface ledger and its fence | FR24 has nothing to extend |
| [P36](P36-agent-self-configuration.md) | the loop and graph axes as real dimensions | those two rows render as `fixed` with "HEROS is one node" as the reason — correct today, and P36 is what changes it |
| ADR-007 | generated console types for the state union | a fourth state could ship without a renderer |

---

## 11. Risks & mitigations

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| R1 | A contract field enters `config_hash` and orphans every pinned inference; nothing goes red | **Critical** | FR5 separates the records; FR8 fences both directions; A1 is the acceptance |
| R2 | The three-state design is read as "we said no to the request" | Medium | §8.2 D5 states the refusal and its reason explicitly rather than delivering a quieter version of the ask |
| R3 | One of the four existing must-never-lose behaviours is dropped in the rewrite | **High** | §9.4 enumerates them; one fence each |
| R4 | Twenty dimensions produce a page nobody can navigate | Medium | Grouped by question, not by principle numbering; existing tab component |
| R5 | Wiring `axiseditor.go` reveals that its API does not fit the routes, and a second implementation appears beside it | Medium | D3; a second implementation is a review failure, and the fix is to change the package |
| R6 | The narrow ledger fence (FR24) is read as closing the general hole | Medium | §2.3 states the general finding separately and assigns it |
| R7 | An operator changes a cap and believes it applies retroactively | Low | The rolling window is stated on the control, and caps are checked **before** the call, never after |

---

## 12. Rollout & test strategy

1. **Read paths first**: resolve every ref to content, and render all twenty dimensions read-only with
   their states. This alone answers most of §5's stories and ships without a single new write path.
2. **The hash boundary and its fence**, before any operating-policy dimension is writable.
3. **The four unwired axis editors** — prompt, skills, tools, context/memory/harness params — by wiring
   `axiseditor.go` to routes. Prompt first: it already has a working editor to compare against.
4. **The operating-policy dimensions** — goal, caps, approval policy, eval floors, rollout — each with
   audit and reason.
5. **FR24's conformance assertion** last, so it lands against a page that satisfies it.
6. **Browser acceptance**: an operator publishes a definition without pasting a hash.

Steps 1 and 2 are independently valuable and independently verifiable. A deployment that stops after
step 1 is strictly better than today.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | Changing a non-hashed dimension produces no new `config_hash` and orphans no pin; changing a hashed one does produce a new one | fence in both directions, mutation-verified; then a live check that pins resolve after an operating-policy edit |
| A2 | All twenty dimensions render, each with exactly one state | fixture matrix; a dimension in Go and absent from the surface fails the type-check |
| A3 | Every `fixed` dimension names its reason and what would change it | fence; empty reason fails |
| A4 | No axis is served by a free-text input | fence over the rendered form; adding one fails the build |
| A5 | An operator publishes a definition without pasting a version id | browser acceptance — not a green build |
| A6 | Every write lands with actor and reason | HTTP → `SELECT` the row → `SELECT` the audit entry → assert the surface renders it |
| A7 | The four must-never-lose behaviours survive | one case each: serving hash separate, three-valued status, unavailable strategy shown, wiring read-only with reason |
| A8 | An unapproved tool is not bindable and a non-compiling skill is not selectable | one case each, driven through the real registries |
| A9 | A param invalid for its schema is refused at save, naming the entry and the parameter | one case per axis that takes params |
| A10 | Publishing still serves nothing | assert a published definition is pending and analyses nothing until activated |
| A11 | The rehearsal report renders per fixture, never as a bare percentage | fixture where one of several fails; assert the failing name is visible |

---

## 14. Open questions

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Is the operating-policy record a new table, or is it derived from the audit log plus current values? | A new table is a one-way door and this repository refuses them lightly. The audit log already holds every change with actor and reason, so "current operating policy" may be a projection rather than a store. **Recommendation: derive it; add a table only if a query proves impractical.** |
| **Q2** | Which action classes require human approval, and who decides the list? | FR17 makes it authorable, which means an operator could reduce it. That may be exactly wrong: an approval policy an operator can weaken is the same shape as a ceiling an operator can raise. **Recommendation: authorable in the direction of MORE approval only, refused in the direction of less.** |
| **Q3** | Should the goal statement be shown to customers, or is it internal? | It is the clearest one-sentence description of what the agent does, and customers ask for exactly that. It is also written for operators and would become marketing copy the moment it is public. **Open.** |
| **Q4** | Do the eval floors become authorable at all, given that lowering a floor is how a failing definition passes? | Go already refuses a zero floor. It does not refuse a floor of 0.01. **Recommendation: authorable above a stated minimum, with a lowering requiring a reason that is rendered on the rehearsal report itself.** |
| **Q5** | Does P38 wait for [P36](P36-agent-self-configuration.md), or render loop and graph as `fixed` until it lands? | Rendering them fixed is honest today and becomes stale the day P36 ships. **Recommendation: render them fixed with "HEROS is one node" as the reason — the reason is true, and it stops being true in a change that will also update the row.** |
| **Q6** | Should FR24's conformance assertion be generalised to the whole ledger in this phase? | It is the real fix for §2.3 and it is a large change to a fence fourteen phases depend on. **Recommendation: no — narrow here, and raise the general one with the ledger's owner as its own decision.** |
