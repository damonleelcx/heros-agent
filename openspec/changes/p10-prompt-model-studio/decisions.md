# P10 — Recorded decisions (System Designer, §1)

Three contracts that had to be fixed **before any write path or codegen shipped**, because each is a
one-way door. The binding-document *format* decision is [ADR-009](../../../docs/adr/ADR-009-binding-document-format.md)
(task 1.1). This file records the other three: `in_scope` extraction depth (1.2), prompt version
lineage (1.3), and the additive `config_hash` extension for bindings (1.4).

---

## D-1.2 — `in_scope` records only the symbols already reaching the call, and fails **closed**

**Problem.** An `expr` binding names a variable in the user's lexical scope, and P10 validates it at
spec-resolve against a set of in-scope symbols the IR records per call site (variable-bindings spec,
§"An expr binding SHALL be validated against the in-scope symbols recorded for that call site"). The IR
node object is `x-frozen: {policy: additive-only}`, so whatever we record must be an **additive** field,
and we must choose **how deep** the scope extraction goes: (A) the *full lexical scope* — every symbol
visible at that source position, or (B) only the symbols that *already reach the call* (its operands and
the identifiers on the enclosing statement/assignment path).

**Decision.** Record **(B): only the symbols already reaching the call site**, as an additive
`in_scope []string` on `call_site`. When extraction cannot prove a symbol is in scope, it is **omitted**,
never guessed in.

**Why this is the appropriate design.** The validation this feeds is a *rejection* gate: a binding
naming a symbol not in the recorded set is refused. So the only two ways to be wrong are a **false
rejection** (the symbol was really in scope but we did not record it → the user is told to spell it at
the call site, an annoyance) and a **false acceptance** (we recorded a symbol that is not actually
usable → the codemod generates a call site that does not compile, discovered late, after publish and
submit — exactly the trap P10 exists to remove). Design 3 in `design.md` names this: "a conservative
record produces false rejections, never false acceptances — the safe direction." Option (B) is the
conservative one: it records a **subset** of true scope, so it can only under-record, so it can only
false-reject. The transform's own `promptExprFor` check remains the backstop for anything that slips
through.

**Alternatives + decision point.** Option (A), full lexical scope, is more permissive and feels more
"complete." Rejected on **stability (L2) over UX (L3)**: full-scope extraction across closures, method
receivers, and imported package-level identifiers is where an over-broad record creeps in, and an
over-broad record is a false acceptance — a stability defect (a non-building codemod) traded for the
convenience of not having to spell an expression. Under the eight-level law an L2 risk cannot be bought
with L3 convenience. (B) is narrower and occasionally makes a user restate a binding as a call-site
expression; that is an L3 cost, and L3 is the one we are allowed to pay here.

**Effect.** A user binding `{{ticket}}` to `ticket` when `ticket` is an argument of the enclosing
function is accepted at resolve time. A user binding to a symbol the extractor could not prove reaches
the call is told so **in the editor**, and can either spell it at the call site or pick from the offered
in-scope list — never learning at codemod time that the rewrite will not build.

---

## D-1.3 — Prompt lineage stays a **name-grouped read model**; no `derived_from` is stored

**Problem.** "The history of prompt X" is today inferred from versions sharing a `name` (proposal
§"There is also no history"). P10 could instead record an explicit `derived_from` parent id at publish.
A stored fact is one-way (tasks.md §1.3): once the write path stamps `derived_from`, we can never
retract it, and every future reader may depend on it.

**Decision.** **Keep the name-grouped read model.** The version timeline (task 2.4) is
`PromptTimeline(name)` — the existing registry rows sharing a name, ordered by creation. **No
`derived_from` is written**, and no lineage column is added to `prompt_entry`.

**Why this is the appropriate design.** The registry is content-addressed and structurally immutable —
`version_id = sha256(envelope)`, no mutation API, a DB trigger as backstop (registry.go). Two facts
follow. First, **an explicit parent link is not derivable and not verifiable**: content addressing knows
nothing about *edit intent*, so `derived_from` would be an author-asserted claim the platform cannot
check, and a wrong or absent link would read as truth. Second, the standing constraint is explicit — "no
new registry table … timeline and diff are **read models over rows that already exist**" (tasks.md
Standing constraints, proposal §"Not changed here"). A `derived_from` column is exactly the stored
lineage fact that constraint forbids.

**Alternatives + decision point.** Storing `derived_from` gives a precise edit tree instead of a
name-grouped bag, which is genuinely nicer for a "fork history" view. Rejected on **evolvability (L5)
and one-way-door discipline**: the read model can be *upgraded* into a stored-lineage model later
(add the column, backfill nothing, start stamping) if a real consumer ever needs it — but a stored fact
cannot be *downgraded* once customers depend on it. Choosing the reversible option now, when no
consumer needs the precision, is the L5-correct call. The name-grouping also cannot lie: it is a pure
function of rows that already exist.

**Effect.** "Show me the history of `triage_prompt`" returns every version of that name, newest first,
each with its slot set and creation metadata (task 2.4) — with **zero** new schema and no
unverifiable author claim. If a future feature needs true fork lineage, it is an additive column then,
decided with its consumer in hand.

---

## D-1.4 — Bindings extend `config_hash` **additively**: a no-binding spec hashes byte-identically

**Problem.** `config_hash` is P0's frozen contract with golden vectors that the live producer "MUST
reproduce bit-for-bit" (`resolved.go`). Every existing row is keyed by it. Bindings must join the
resolved configuration so the hash "changes iff a binding changes" (variable-bindings spec), **without**
changing the hash of any configuration that declares no bindings — else every existing spec becomes
non-reproducible and every keyed row orphans.

**Decision.** Add a single field to `ResolvedNode`:

```go
// Bindings is the per-slot resolved binding source, keyed by slot name. OMITTED ENTIRELY
// (not "{}") when the node binds no slot, so a node with no bindings serialises byte-identically
// to how it did before this field existed. Present only when non-empty; its keys are slot names
// and JCS sorts them, so binding order is not identity-bearing.
Bindings map[string]ResolvedBinding `json:"bindings,omitempty"`
```

- **`omitempty`, and the map is left nil when empty** — this is the load-bearing detail. A node with no
  bindings emits **no `bindings` key at all**, so its canonical bytes are identical to today's seven-field
  node. (Contrast the sibling fields, which are `[]`/`{}`-normalised and *always present*: those predate
  the golden and their emptiness is part of the frozen bytes. `bindings` is new, so its *absence* is what
  must be byte-compatible, and absence is achieved by omission.)
- **`ResolvedBinding` records `kind` and `value` explicitly** (`{"kind":"literal","value":"gold"}`),
  never inferring kind from value shape (variable-bindings spec, "recorded explicitly rather than
  inferred").
- **Keys are slot names; JCS sorts object keys** — so two specs with the same bindings in different
  authoring order canonicalise identically (spec scenario "differing only in binding order hash
  identically"). No array, no sort-in-code needed; the canonicalizer already sorts object keys.

**Why this is appropriate.** The expand-contract rule the registry and IR already live by is: a new
*optional* field must leave old serialisations byte-identical (registry.go, "a spec struct that gains an
optional field must leave old envelopes byte-identical"). `bindings,omitempty` with a nil-when-empty map
is precisely that rule applied to `ResolvedConfig`. The golden vectors carry no `bindings` key, so they
keep reproducing; `resolved_config_golden_test.go` is the guard that tells us if a tag change broke it.

**Alternatives + decision point.** (a) Make `bindings` always-present as `{}` like its siblings — would
change the golden bytes for **every** node in **every** existing config, breaking P0's contract; rejected
outright. (b) Fold bindings into `context_params` or `provider_params` — overloads a frozen field with a
second meaning and would let a binding change masquerade as a param change in the hash; rejected on
single-source-of-truth. (a) loses at L2/L5 (reproducibility of a frozen contract); the chosen shape is
the only one that satisfies "changes iff a binding changes" **and** "no-binding spec unchanged"
simultaneously.

**Effect.** Every config authored before P10 hashes to exactly the byte it did before and stays
reproducible. A config that adds, removes, or changes any binding gets a new `config_hash`; reordering
bindings does not. Backward compatibility is a *test*, not a hope (task 3.9).
