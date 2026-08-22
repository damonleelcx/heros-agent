# P34 — Recorded decisions (System Designer)

The narrative reasoning lives in [`design.md`](design.md) and the arbitration in
[ADR-014](../../../docs/adr/ADR-014-harness-loop-graph-axis-split.md). **This file is the contract of
record**: the seven things that had to be settled before any P34 code shipped, each walked through the
five-step record (problem → decision → why appropriate → alternatives and the level each was rejected on →
effect) and tagged with the governing cost-and-complexity level.

`tasks.md` §2 owns these. The first five answer PRD §14 Q1–Q5, which
[ADR-014](../../../docs/adr/ADR-014-harness-loop-graph-axis-split.md) deferred here rather than guessing.

| # | Question | Answer |
|---|---|---|
| **D-34.1** (§14 Q1) | Where does the **spend** ceiling sit? | **Harness.** It is imposed, not chosen. |
| **D-34.2** (§14 Q2) | Predicate grammar? | **Reuse `expr`.** One grammar, one validator, one scope check. |
| **D-34.3** (§14 Q3) | Concurrent-group failure semantics? | **Declared on the merge, required, closed set.** Never defaulted. |
| **D-34.4** (§14 Q4) | Three pages, or one with three sections? | **Three sibling axis pages.** |
| **D-34.5** (§14 Q5) | Does the legacy path get an end-of-life date? | **No. Permanent, on the record.** |
| **D-34.6** | How is the open P18 change reconciled with this one? | **At fold time, by the change that folds second — and it is P34's spec files that carry the superseding text.** |
| **D-34.7** | What does each of the three axes declare about its own coverage? | **Derived from the materializer tables, per language, never hand-declared.** |

---

## D-34.1 — The **spend** ceiling sits with the harness envelope (PRD §14 Q1)

**Problem.** §2.3 settles the *turn* ceiling by `boundedCeiling`'s own argument: a ceiling is a policy about
blast radius, so it is imposed by the envelope, while the value chosen inside it belongs to the loop. Spend
is less obvious. A spend cap is a blast-radius policy in exactly the same sense — and it is *consumed*
almost entirely by loop iterations, which makes it look like a property of the loop.

**Decision.** The spend ceiling is a field of the **harness envelope**. The loop names no monetary value at
all — it is inexpressible on a loop entry, exactly as `max_turns` is inexpressible on `single-shot`.
Exhaustion is reported as a **named stopping condition**, not as an error.

**Why appropriate.** The split line is *imposed vs chosen*, not *who consumes it*. By the consumption test,
the turn ceiling would also belong to the loop — a loop is the only thing that consumes turns — and §2.3
already rejected that reading. Applying two different tests to two ceilings would put a seam inside the
axis boundary, which is the exact reviewability failure this phase exists to end: an operator tightening a
spend cap and an engineer picking four turns instead of two would once again be editing the same kind.

Reporting exhaustion as a *stopping condition* rather than an error follows from the same place. A run that
stopped because it hit its budget produced a real, partial answer under a known configuration; calling that
an error would file it beside "the provider was down", and the two need different responses from a reader.

**Alternatives + decision point.**
- *Spend on the loop, beside `max_turns`.* Rejected on **L3 UX**: it would let a spec author raise their own
  spend cap, which is not a thing an author may do. A policy an author can edit is not a policy.
- *Spend in a third place — a tenant-level budget outside both axes.* Rejected on **L5 不可演进**: a ceiling
  that is not part of the resolved configuration cannot be pinned by `config_hash`, so a run's spend bound
  would not be reproducible from its hash, and "the same configuration" would mean two different things on
  two days.

**Effect.** One rule for both ceilings: the envelope imposes, the loop chooses, and a loop value above an
envelope ceiling is refused at resolve naming both numbers. An operator raising either ceiling changes no
loop entry's content and no loop entry's `version_id` (design D2, fenced by task 1.4).

---

## D-34.2 — A predicate is an `expr` binding, validated by the ADR-004 path (PRD §14 Q2)

**Problem.** A conditional edge is the first place a **customer-authored expression affects control flow**.
[ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md)'s `expr` grammar was designed for a *value*
binding — "put the variable `ticket` into this prompt slot" — and a control-flow decision is a different
use. A narrower, purpose-built predicate grammar would be safer in isolation.

**Decision.** Reuse `expr`. A predicate is validated at spec-resolve time by the same check that validates a
`BindExpr` binding: the symbol must be recorded as in scope at that call site in the IR, or the spec is
refused, naming the symbol. There is no second grammar and no second validator.

**Why appropriate.** The scope check is the thing standing between a predicate and a name that does not
exist at that call site. A second grammar is a second implementation of that check, and the second one is
always the one that is wrong — it is written by someone who believed the problem was simpler. One grammar
means there is exactly one place a scope rule can be got right or wrong, and exactly one place to narrow it
if `expr` proves too permissive. **That narrowing is only possible because there is one place.**

**Alternatives + decision point.**
- *A bespoke boolean-only predicate grammar.* Rejected on **L1 安全**: two expression paths means two scope
  validators, and the looser one becomes the way in. A safety property that depends on two implementations
  agreeing is not a safety property.
- *No validation — evaluate the predicate at run time and fail there.* Rejected on **L1 安全** and **L3 UX**:
  it moves a knowable answer past the point where a codemod has already been generated and applied.

**Effect.** `Edge.Kind` grows a `predicate` member; the predicate string travels the same validation path as
`BindExpr`, produces the same `ErrBindingOutOfScope` sentinel, and names the symbol. Narrowing the grammar
later is a change to one function.

---

## D-34.3 — A concurrent group's failure semantics are **declared on the merge**, required, from a closed set (PRD §14 Q3)

**Problem.** When one member of a concurrent group fails, something has to happen to the other members and
to the downstream node. Fail fast, or run everything and merge the partials? Both are defensible, and design
D6 has already refused to let the platform pick a default for the *combination* of a fan-in's inputs. The
same reasoning applies to its failures — but D6 did not say **where** the declaration lives, and PRD §14 Q3
explicitly says not to default it.

**Decision.** Failure semantics are a **required field on the merge declaration**, `on_node_failure`, from
a closed two-value set:

| value | meaning |
|---|---|
| `fail-fast` | the first node failure aborts the group; the downstream node is not entered |
| `collect-partial` | every node runs to completion; the merge receives only the nodes that succeeded |

A merge that omits it is refused at validate, naming the group. There is **no default and no global rule**.

🔴 **`collect-partial` carries a typed consequence, and it is enforced rather than documented.** If the merge
may deliver fewer inputs than the group has nodes, the downstream node's input contract must admit that
absence — so a `collect-partial` merge whose downstream contract makes every member's field *required* is
refused at validate, through `internal/typedcontract`, unchanged. Without that rule `collect-partial` would
be a promise the type system does not keep, discovered at run time by whoever was unlucky.

**Why appropriate.** Failure semantics are a statement about **the author's program**, not about the
platform. "Cancel everything if the enrichment call fails" and "answer with whatever enrichment returned"
are different products, and a platform that picks one is deciding what the customer's code means. Putting it
on the merge rather than on the group is what D6's own reasoning points at: the merge is already the place
where "what arrives at the downstream node" is declared, and failure is a statement about exactly that.

**Alternatives + decision point.**
- *A global platform rule (always fail-fast).* Rejected on **L3 UX**: it makes half of the legitimate
  programs inexpressible, and the half it makes inexpressible is the one where partial results are the
  product.
- *Default to `fail-fast`, overridable.* Rejected on **L2 稳定**: a default here is silent, and the failure
  mode of the wrong default is a run that produced a plausible answer from incomplete inputs. Loud beats
  convenient — the same argument D6 makes for the merge itself.
- *A field on `GraphGroup` rather than on `Merge`.* Rejected on **L7 维护**: it would put two halves of one
  semantic decision in two structures, so a reader would have to hold both to know what a fan-in does.

🔴 **The wire name is `nodes`/`on_node_failure`, not `members`/`on_member_failure`, and the rename happened
at implementation time for a reason worth recording.** `p27_hash_recording_test.go` bans the OWNERSHIP
vocabulary — tenant, owner, account, **member**, seat — from every field that reaches `config_hash`,
because a hashed field naming WHO forks one configuration per organization and orphans every result filed
under the old hash. The ban is deliberately on the *vocabulary* rather than on a list of known fields,
"because the thing being prevented is a field nobody has written yet" — so it fired on `members` in a
completely different sense of the word. The graph axis yielded: narrowing a fence built to catch unwritten
fields, in order to admit one written today, spends the fence to save a synonym.

**Effect.** One required, closed, hashed field. A group with no merge is refused; a merge with no failure
semantics is refused; a `collect-partial` merge whose downstream contract cannot admit absence is refused.
None of the three is defaulted.

---

## D-34.4 — Three sibling axis pages (PRD §14 Q4)

**Problem.** Today the console has `/app/harness` and `/app/wiring`. After the split there are three axes.
§9.4's real risk is not the layout, it is **content loss in the re-cut** — the standing failure mode of a UI
revision is that something on the old page has no destination on any new one.

**Decision.** Three sibling pages: `/app/harness` (the envelope), `/app/loop` (new), `/app/graph` (which
supersedes `/app/wiring`). Each reuses the existing axis-page structure — `PageFrame` + `Tabs` +
`DataTable` + `AxisProjectionPanel` — with no improvised styling; `scan:tokens` stays green. **Confirmed with
the user.**

Two obligations ride with it, and they are the reason the decision is recorded rather than assumed:
1. **§7.2's inventory is a gate, not a checklist.** Every item on the two existing pages gets a named
   destination, and anything with no home comes back to the user before it is removed. Confirmed with the
   user: *carry everything; ask before removing anything.*
2. **`/app/wiring` does not 404.** It redirects to `/app/graph`, permanently, because a bookmark that
   stops working is indistinguishable from a feature that was withdrawn.

**Why appropriate.** A refusal names an axis. With three pages, "refused on the `loop` axis" has somewhere to
link; with one page and three sections, it has an anchor at best. The axis vocabulary the backend now refuses
in is the vocabulary the reader navigates in, and keeping those two the same is worth the larger re-cut —
especially since the re-cut's real risk is controlled by §7.2 rather than by page count.

**Alternatives + decision point.**
- *One page, three sections.* Rejected on **L3 UX**: it nests a three-tab page inside a three-section page,
  and it re-introduces the conflation on the one surface a reviewer actually looks at.
- *Two pages (envelope+loop together, graph apart).* Rejected on **L3 UX** for the same reason: the pair it
  keeps together is precisely the pair the phase exists to separate.

**Effect.** Three pages, one axis each, sharing one structure. A hidden axis renders read-only **with its
reason** (§9.1), because a hidden axis is indistinguishable from one that does not exist.

---

## D-34.5 — The legacy loop-bearing harness path is **permanent**, with no end-of-life date (PRD §14 Q5)

**Problem.** D1 refuses the contract half of expand-contract. That leaves a permanent legacy read path, and
the maintainer's instinct is to schedule its removal — a date is more honest to whoever inherits this.

**Decision.** **No date.** The legacy path is permanent, and its removal is an **amendment to ADR-014**
rather than a cleanup ticket.

**Why appropriate.** A date does not shorten ADR-014's orphaning chain; it hands it to somebody else on a day
when the reasoning has been forgotten. On that day the arithmetic is unchanged — removing the loop fields
changes every loop-bearing entry's `version_id`, which changes the `config_hash` of every spec referencing
it, which makes every measurement taken on a multi-turn node **unreachable from any spec anyone can
construct**. Not invalid. Unreachable. A scheduled date would make that chain arrive as a routine
maintenance task, which is the worst possible framing for it.

Recording the refusal on the record is what converts a future removal from a cleanup into an argument
somebody has to win.

**Alternatives + decision point.**
- *An EOL date two majors out.* Rejected on **L2 稳定**: it converts a permanent, understood cost into a
  scheduled, forgotten catastrophe.
- *Silence — no answer either way.* Rejected on **L5 不可演进**: an unanswered question is answered by
  whoever is in a hurry.

**Effect.** `HarnessSpec` keeps its loop fields. Legacy entries resolve indefinitely; new authoring surfaces
cannot create one; a spec setting both is refused at resolve naming both refs. Task 11.2 re-confirms this at
the **end** of the phase, when the residue is visible and the temptation to finish the job is highest.

---

## D-34.6 — P18 is reconciled at fold time by **this change's spec text** (tasks.md §2.6)

**Problem.** P18 is **still an open change**. Its harness capabilities — `harness-strategy`,
`harness-authoring`, `harness-runtime`, `harness-delivery`, `harness-materialization`, `agent-loop` — live
under [`../p18-harness-strategy-optimization/`](../p18-harness-strategy-optimization/) and have **not been
folded into `openspec/specs/`**. P18 defines the harness axis as carrying **both** the scaffold and the
control loop. This change splits them. Whichever folds second must reconcile them, and **folding P18
unchanged after this one would restore the conflation the phase exists to end**.

**Decision.** Three rules, in force from now:

1. **P34's spec files carry the superseding text.** `harness-envelope/spec.md` states its requirements as
   `ADDED` (not `MODIFIED`) with an explicit note saying why — there is no folded requirement to modify —
   and `loop-strategy/spec.md` states that the loop vocabulary is **relocated**, not extended.
2. **If P18 folds first**, P34's `harness-envelope` requirements become `MODIFIED` against the folded
   `harness-strategy` capability, and `loop-strategy` remains `ADDED`.
3. **If P34 folds first** (the current expectation), P18's `harness-strategy` capability must be **edited
   before it folds**: every requirement placing the control loop on the harness axis is rewritten to place
   it on the loop axis, and P18's `agent-loop` capability is folded under `loop-strategy`. P18's runtime,
   delivery and materialization capabilities fold unchanged — they describe *executing* a loop, which is
   not what the split moves.

**Why appropriate.** The conflation is not restored by anybody deciding to restore it; it is restored by a
document folding on a normal day with nobody remembering that a later change split it. So the reconciliation
is written down where the folder will be standing, in **both** changes, rather than being remembered.

**Alternatives + decision point.**
- *Fold P18 now, then apply P34 as MODIFIED.* Rejected on **L4 运维**: it doubles the fold work and creates a
  window in which the folded truth states the conflation as current.
- *Leave it to whoever folds.* Rejected on **L5 不可演进**: an unwritten reconciliation is a conflation with a
  delay on it.

**Effect.** Neither change can fold silently. `tasks.md` §2.6 owns the reconciliation, and this decision is
what it points at.

---

## D-34.7 — Each axis declares its own coverage, **derived**, per language (tasks.md §2.7, PRD FR18)

**Problem.** Three axes now exist where two did. `internal/transform/coverage.go` states the rule this must
obey: *"absence is not a value here"* — a cell the engine cannot apply carries a **cause class** and the
artifact whose absence explains it, and `TestCoverageIsTotalOverRegisteredLanguages` fails the moment a
frontend is added without one. An axis that quietly answered nothing would render on every surface as "not
applicable", which is a claim about the customer's code.

**Decision.** All three axes are members of `CoverageAxes()`, and each declares `EXISTS` / `PARTIAL` /
`ABSENT` per language through `transform.StatusFor`, **derived from the table its rewriter dispatches on**:

| axis | derived from | honest status at ship |
|---|---|---|
| `harness` (envelope) | the sandbox/host-service provision it declares — a resolve-time gate, not a codemod | unchanged from P18 |
| `loop` | the same materializer table the harness strategies dispatch on, because the loop axis is where those strategies now live | `PARTIAL` — `reflexion` materializes where a language can read an answer; three strategies refuse everywhere, permanently, needing a host service no call site can inject |
| `graph` | a `graphMaterializers` table, per (language, form) | `ABSENT` until a language's topology rewriter lands; each cell names which of the frontend, the analysis or the language support is missing |

🔴 **Derived, never hand-declared.** A hand-maintained status is the optimistic copy `coverage.go` exists to
end, and it goes stale the first time a materializer lands.

🔴 **`graph`'s refusal must name which of three things is missing** (FR18) — the frontend, the analysis, or
the language support — and must not report a generic unsupported state. "We do not support this" and "your
call site cannot carry this" and "nobody can carry this in any language" are three different sentences to
three different people.

**Why appropriate.** It is the existing contract, applied a third time, with no new mechanism. The one thing
it adds is `graph`'s three-way refusal, which FR18 requires because `graph` is the first axis where the
*analysis* can be the missing piece independently of the frontend.

**Alternatives + decision point.**
- *Omit `graph` from the coverage read until it materializes somewhere.* Rejected on **L3 UX**: an axis
  absent from the table renders as "not applicable" everywhere, which is the exact failure the file's own
  doc comment describes.
- *One coverage row per axis rather than per (axis, language).* Rejected on **L3 UX**: it would tell a Python
  reader that `reflexion` is unavailable and every reader that `react-loop` is merely pending.

**Effect.** `AxisCoverage()` is total over three more axis × language products. The console reads it, the CLI
reads it, and the published doc reads it — so none of the three can disagree.

---

## Standing note — the predicate is the phase's second one-way door (L1 安全)

ADR-014 names `config_hash` as the door. **The predicate is the other one, and it is quieter.**

Once a customer-authored expression influences control flow, that expression is a permanent part of the
platform's evaluation surface. Every future question — may a predicate call a function; may it reach a
network; may it read a secret; what does it see inside a sandbox — traces back to the grammar chosen here.
D-34.2 keeps it to **one** grammar so those questions have one place to be answered. They are not answered
by this phase, and nothing in this phase should be read as having answered them: today a predicate is a
name that must be in lexical scope at the call site, and that is all it is.
