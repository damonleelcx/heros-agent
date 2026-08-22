# P34 §11 — Sign-off

## 11.1 — PRD §14 Q1–Q5, answered and folded in

All five are settled in [`decisions.md`](decisions.md) with their rejected alternatives and the level
each was rejected on, and folded into
[PRD §14](../../../docs/prd/P34-harness-loop-graph-split.md#14-open-questions--adr-014-deferred-these-here-all-five-are-now-answered),
whose header now reads **answered** rather than **open**. Two further contracts were settled beside them
(D-34.6, the P18 reconciliation; D-34.7, the per-axis coverage read), because neither was safe to leave
to whoever got there first.

| # | Answer | Where it is now enforced |
|---|---|---|
| Q1 | Spend sits with the **harness envelope** | `spend_ceiling_usd` is a required envelope field and is inexpressible on a loop entry |
| Q2 | **Reuse `expr`** | `validatePredicates` calls `CallSite.HasInScope`, the same method a prompt-slot `expr` binding calls, and reports the same sentinel |
| Q3 | **On the merge, required, closed set** | `on_node_failure` ∈ `{fail-fast, collect-partial}`; `collect-partial` against a required downstream field is refused |
| Q4 | **Three sibling axis pages** | `/app/harness`, `/app/loop`, `/app/graph`; `/app/wiring` redirects |
| Q5 | **Permanent, no date** | `HarnessSpec` unchanged; the legacy path has no expiry and its removal is an ADR amendment |

---

## 11.2 — ADR-014's refusal, re-confirmed with the residue visible

> The task asks for this **at the end of the phase**, deliberately: *"when the residue is visible and the
> temptation to finish the job is highest."* So this is a re-examination, not a signature. The residue is
> counted from the code below, and the arithmetic is re-derived rather than cited.

### What ADR-014 refused

Removing the loop fields from `HarnessSpec`. The chain it refused on: remove a field → the entry's
content changes → its `version_id` changes → the `config_hash` of every spec referencing it changes →
every measurement taken on a multi-turn node becomes **unreachable from any spec anyone can construct**.

### The residue, counted

| # | What is permanently carried | Cost |
|---|---|---|
| R1 | Five loop strategies remain in the harness vocabulary, no longer authorable | `HarnessStrategySetSize` is 6: five legacy + `envelope` |
| R2 | `HarnessEntry.IsLoopBearing()` — one predicate, one place | ~3 lines |
| R3 | The ambiguity refusal (`ErrAmbiguousAxis`) and its resolve-time branch | ~15 lines in `resolveNode` |
| R4 | `harnessruntime` keeps loop definitions for the five legacy strategies | none new — the runtime executes them for legacy specs regardless |
| R5 | The emitted harness module still implements the five, for legacy specs | none new, same reason |
| R6 | `OpHarnessStrategy` reserved in the operator enum, emitted by nothing | 1 constant |
| R7 | Two coverage reads over one materializer table (`loop` and legacy `harness` cells) | derived, not duplicated |

**Ten call sites across seven files**, and no data migration of any kind.

### 🔴 One thing changed during implementation, and it changed the calculus in the refusal's favour

ADR-014 assumed the envelope would need somewhere new to live, and reasoned about the residue on that
basis. It did not: the envelope shipped as a **sixth strategy in the existing harness vocabulary**, so
`internal/registry/harness.go` — the file defining `HarnessSpec` and its seal path — is **byte-for-byte
unchanged by this phase**. `git diff` against the pre-P34 tree shows no change to it at all.

That means the residue is **smaller than the ADR predicted**, and it is smaller in the direction that
matters: the sealed unit that every stored `config_hash` depends on was never edited, so the
compatibility guarantee is structural rather than maintained. A future maintainer looking for the loop
fields to remove will find them on a type nothing in this phase touched.

### Would the removal be cheaper now that everything else is built? No — and here is the honest check

The temptation the task names is real: with `loop` shipped, `HarnessSpec`'s loop fields look like dead
weight. Three reasons the arithmetic is unchanged, and the third is the one that would be missed:

1. **The chain has not shortened.** `registry.Kind` is still hashed into the `version_id`, and
   `registry/harness_test.go` still says why in its own failure message. Removing a field still changes
   every loop-bearing entry's address.
2. **Nothing has migrated off the legacy shape, and nothing can.** Registry entries are content-addressed
   and immutable, enforced by a DB trigger. There is no "rewrite the old entries" option — the option
   does not exist, which is why this is a refusal and not a deferral.
3. **The residue is now load-bearing in a way it was not when the ADR was written.** R3 — the ambiguity
   refusal — is the only thing standing between an author and a spec that states its iteration policy
   twice. Removing the legacy shape would remove the refusal with it, and the day somebody restores a
   loop-bearing entry from a backup or an older binary, the resolver would have no rule for it. The
   "cleanup" would delete a check whose subject still exists.

### The one thing that would change this answer

If a future phase makes registry entries **re-addressable** — a content-address migration that carries
forward the measurements filed under the old address — then the chain breaks and the removal becomes
possible. That is a large piece of work with its own one-way doors, and it is the only door through
which this refusal should be re-opened.

**Re-confirmed.** Removing the loop fields from `HarnessSpec` remains refused. A proposal to do it is an
amendment to [ADR-014](../../../docs/adr/ADR-014-harness-loop-graph-axis-split.md), not a cleanup ticket.

---

## What is NOT delivered, stated plainly

Three things, so nobody reads a complete task list as a complete capability:

1. **No language writes a topology into source.** Concurrency, conditional edges and merge resolve, hash,
   validate against typed contracts, and are refused by name at every one of the 7 × 3 cells.
   `graphMaterializers` is empty and `StatusFor("graph")` is `ABSENT`. PRD §12 stages it that way; the
   console says so per form, naming which of the frontend, the analysis or the language support is
   missing.
2. **`OpGraphTopology` proposes concurrency only** — never a merge, because how a fan-in combines is a
   semantic decision about the customer's program (design D6). A converging pair is skipped rather than
   proposed with a guessed merge.
3. **The spend ceiling needs a host-supplied meter.** The runtime cannot price a call it does not make;
   a declared ceiling with no meter is refused at preflight rather than silently unenforced.

## Gates, at sign-off

| Gate | Result |
|---|---|
| `make go` (build, vet, gofmt, full test) | green |
| `make p34-fence-redcheck` | **14/14 mutations proven red-capable** |
| `make attribution-holdout` | green; prints the defect and the fix side by side |
| P0 golden vectors | unchanged, before and after every task |
| `web/console` — `scan:tokens`, `typecheck`, `build`, `npm test` | green · 693/693 |
| Browser | three axis pages render; `/app/wiring` redirects; no console errors; correct at 375 px |
