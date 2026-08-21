# Design — P39: The Conversation-Only Console

> Arbitration law for every decision below: **security > stability > UX > operability > non-evolvable >
> non-extensible > maintenance > implementation cost**. Level 8 never outranks anything.

---

## D1 — The drift fence anchors on a reader registry, not on the route table

### The problem, stated as the failure it produces

`TestIntentSetEqualsTheWorkingSurfaceSet` asserts `RouteBackedSurfaces()` equals the console's
`WORKING_SURFACES`. It exists because of one specific, silent rot: a surface ships, nobody adds its
intent, and the conversation answers questions about it with a **refusal** — well-formed, polite, and
indistinguishable from the surface not existing. Nothing goes red. P26 found that shape after fourteen
phases of it.

Delete ten routes and this fence fails. It fails *correctly*, and it fails **for a reason that looks
like bookkeeping**, which is precisely what makes it dangerous: the cheapest repair is to delete the
intents with the routes, and the second cheapest is to exempt removed routes. Both restore green. Both
remove the protection.

### Alternatives

| | Option | Arbitration |
|---|---|---|
| (a) | Relax the fence to skip removed routes | 🚫 Trades a level-2 guarantee (contract stability) for level-8 convenience. Refused by L1. An exemption in a fence is how a fence stops being one. |
| (b) | Keep the ten routes mounted but unlinked from the nav | 🚫 Declined by the product owner, and leaves ten unmaintained route trees whose content no longer has a consumer — level 7, plus it does not deliver the phase. |
| (c) | Re-anchor on a registry of readers | ✅ More code (level 8, lowest), and the guarantee gets **stronger**. |

### Chosen: (c)

A route was only ever a proxy for *"the platform can answer this."* The registry states that directly:

```go
type Backing string
const (
    BackedByRoute      Backing = "route"       // a console route that still exists
    BackedByReader     Backing = "reader"      // a registered reader with a declared detail shape
    BackedByCapability Backing = "capability"  // mounted per deployment (assess, improve)
)
```

The fence becomes two assertions instead of one:

- every `BackedByReader` intent resolves to exactly one registered reader **and** the reader declares a
  detail shape;
- every registered reader is named by exactly one intent.

🔴 **Ordering is load-bearing.** This lands in Wave 1, with all twelve routes still mounted and the
route fence still passing over all twelve. Nothing is deleted until the new anchor is proven able to go
red — see the drill in `tasks.md` §1.4.

### 🔴 What this fence still cannot catch

That a reader has **useful** depth. A reader returning a one-cell grid satisfies the registry and
satisfies the shape declaration. §6.6's per-reader depth is checked by Wave 3's browser acceptance —
each reader compared against the page it replaces, *before* that page is deleted — and that is a human
comparison, not a fence. Stated so nobody reads a green build as "the pages are replaced".

---

## D2 — `Detail` is one optional field carrying a closed union

### The problem

A finding must carry a grid, a table, a diffstat or an ordered record. Each new wire shape published to
a customer console is a contract, and this repository's rule is that new API surface needs two
alternatives on the table before it is created.

### Alternatives

| | Option | Arbitration |
|---|---|---|
| (a) | Four new message kinds (`grid`, `table`, …) | 🚫 Expands the vocabulary the effect table is checked against. Every new kind is a new question of *"can this cause an effect?"* — level-1 adjacency for a rendering concern. |
| (b) | Free-form JSON blob on `finding` | 🚫 The worst kind of unevolvable: no consumer can be type-checked, the console renders whatever arrives, and repository-authored strings reach the DOM through a path nobody typed. Level 5, and it grazes level 1. |
| (c) | One optional field, closed union of four shapes | ✅ One field, one generated TypeScript union, four renderers with no `default:` arm. |

### Chosen: (c)

```go
// DetailShape is closed. A fifth member requires a spec delta — it is a published wire contract.
type DetailShape string
const (
    ShapeGrid     DetailShape = "grid"     // node × axis cells, each with state and cause
    ShapeTable    DetailShape = "table"    // named columns, ordered rows
    ShapeDiffstat DetailShape = "diffstat" // per-file counts + one unified diff
    ShapeRecord   DetailShape = "record"   // ordered key/value facts
)
```

**The one-way door in this phase is this union**, and it is named here so review can concentrate on it.
Four shapes are enough for the ten readers being deepened; a fifth is a spec change, not a commit.

### The emitter rule that makes it honest

A reader **declares** its shape in the registry. If the reading arrives with the shape absent, the
emitter refuses the finding — the same refusal path an evidence-less finding takes today. A card with a
claim and nothing beneath it is how a surface with no data reads as a surface that was checked.

An *empty* grid is not an absent one: zero cells with a stated zero renders as "no cells", and that is a
measurement.

---

## D3 — A node the IR does not contain refuses; it never widens

`Read` gains a `Subject`:

```go
type Subject struct {
    NodeID string // empty means workflow-wide
}
```

A struct rather than a `nodeID string` parameter, because the next scoping axis — a run id, a file path
— must be a field, not a signature change rippling through every reader.

**Arbitration: level 3.** When someone asks about `reranker` and `reranker` is not in the reported IR,
answering about the whole workflow produces a true sentence about the wrong subject, and nothing in the
message says so. That is indistinguishable from a correct answer, which is the failure class this entire
program exists to remove. The refusal names the string typed **and** lists the node identifiers that do
exist, because "not found" without the alternatives costs the reader another round trip.

---

## D4 — Carry-forward is stated in the `plan`, or it does not happen

The router receives the prior turn's resolved `(intent, subject)` and may use it when a question names
an axis but no subject. When it does, the `plan` message says which subject it carried and from which
turn.

**Arbitration: level 3 against level 8.** Implicit conversational state is state the reader cannot
audit — and the P31 Ask page currently *promises in copy* that no memory is carried, so changing the
behaviour silently would make the product contradict its own printed statement. One line in a payload
that is already emitted is the entire cost.

Bounded deliberately: carry-forward does not cross conversations, and does not survive a pin replay
whose pinned subject differs. 🚫 It is **not** general conversational memory; it is one field, carried
one turn, stated out loud.

---

## D5 — Deleted routes 404, behind a dated retirement window

**Arbitration: level 3 vs level 4.** A permanent redirect to `/app/ask` is operationally free and
teaches nobody where the answer went — a person with a `/app/coverage` bookmark lands in a text box with
no idea what to type. An immediate 404 strands live bookmarks in a deploy.

Resolution: for 30 days a deleted route serves a page that names the surface, says it now lives in the
conversation, and **pre-fills the Ask box with that surface's question, verbatim from the intent
table's `Question` field**. That field already exists for the refusal copy; this is a second consumer,
not a second source of truth.

The window closes on `console.route.retired_hit` going quiet, not on the calendar alone — a decision
that reads a counter rather than a guess.

---

## D6 — The act path is not in this phase

`/app/studio`, `/app/authoring` and `/app/workflows/[id]/proposals` are the only surfaces through which
a customer changes anything. In production, `Runner.Run` emits five kinds and never `proposal` or
`approval_request` — those exist in the vocabulary, are validated by the emitter, are rendered by
`messages.tsx`, and are emitted **only in tests**.

**Arbitration: level 2.** Deleting those three surfaces before the conversation can propose removes a
shipped capability from the product, and — because the fence set is about intent/surface *equality*, not
about capability — nothing in this repository would go red when it happened. That is §2.3's failure at
its most expensive.

Consequence to state plainly: **P39 leaves a two-item task navigation beside a conversation.** That is a
worse shape than either endpoint, and it is why the PRD recommends P40 follows immediately rather than
being scheduled on its merits.

---

## D7 — Truncation is a property of the reading, not of the renderer

A 500-node workflow across seven axes is 3,500 cells. The ceiling (2,000 cells, PRD Q4) is enforced in
the reader, before serialisation, and the reading carries `omitted` plus the narrowing that would show
the rest.

**Arbitration: level 2 and level 3 together.** A ceiling in the console is a ceiling that ships the
payload anyway; a ceiling with no count renders a partial grid that looks complete. The count travels
with the data that was not sent, or the truncation is silent — and silent truncation reads as "covered
everything" when it did not.

---

## Cross-cutting: untrusted repository content

P31 §7.3's boundary holds unchanged, and the effect table is untouched by this phase: `finding` gains a
payload, not an effect.

What widens is the **volume and variety** of repository-authored strings reaching the browser — node
identifiers, cell causes, file paths, diff hunks. Every one is rendered as text, never as markup, and
the fence for it runs with injection detection **deliberately disabled**, matching P31 §6.3's method: a
fence that only passes with the classifier on is testing the classifier.
