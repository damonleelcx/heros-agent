# P1 Discovery — Design: Node-ID scheme (resolves PRD Q4)

> **Task:** P1 `tasks.md` §2.3. **Phase:** ② Design (System Designer lead with Backend, per PRD Q4).
> **Inputs:** [invariant I3 (stable IDs)](03-discovery-invariants.md),
> [contract confirmation §2 (`node_id` required, `minLength 1`)](01-ir-contract-confirmation.md),
> P0 diffability scenario (*"byte-stable after canonicalization"*).

## §0 TL;DR

`node_id` is a hash over a **content-addressed tuple** that **excludes absolute line numbers and
formatting** but **includes structural position within its enclosing symbol**:

```
node_id = "n_" + short_sha256( module_pkg_path ⋮ enclosing_symbol_fqn ⋮ selector ⋮ occurrence_index )
```

This makes the id **stable across benign line shifts** (comments, gofmt, code moved to another file in the
same package) yet **unique per call site** (two calls in one function differ by `occurrence_index`;
different selectors differ by `selector`). This resolves PRD Q4.

## §1 The problem (PRD Q4 restated)

PRD Q4: *"what exact tuple is content-addressed so IDs stay stable across benign refactors (line shifts)
yet unique per call site?"* Two failure modes bound the design:
- **Too positional** (id includes the line number) → any edit above the call shifts the line → id churns →
  the diff shows spurious node changes → diffability (the M1 exit criterion) is destroyed.
- **Too coarse** (id is just pkg+function) → two LLM calls in the same function collide → one silently
  overwrites the other → a **silently dropped node** (the worst failure mode, I4).

The scheme must sit exactly between: fine enough to separate every call site, coarse enough to ignore
formatting.

## §2 The tuple

| Component | Value | Why it's in the identity | Stability property |
|---|---|---|---|
| `module_pkg_path` | module path (`go.mod`) + package dir, e.g. `github.com/acme/app/internal/llm` | Scopes the id to a package; disambiguates same-named funcs across packages. | A Go package **is** a directory ⇒ **moving a file within the package doesn't change it** (nice: file renames don't churn). |
| `enclosing_symbol_fqn` | fully-qualified enclosing func/method, e.g. `(*Service).Summarize`; for a closure, the **named parent** + closure ordinal (catalog W10) | Two calls in different functions must differ. | Stable unless the function is renamed (a rename **is** a semantic change, not a benign line shift — acceptable churn). |
| `selector` | the matched call selector, e.g. `Messages.New`, `Complete`, `Converse` | Two *different* calls in the same function (an OpenAI call and an Anthropic call) must differ even at the same occurrence slot. | Stable unless the call target changes (again, semantic). |
| `occurrence_index` | 0-based index of this call **among call sites with the same `selector` in the same symbol, in source order** | Disambiguates N identical calls in one function. | Stable under line shifts; **changes only if same-selector calls are reordered/added/removed** (semantic edits). |

```go
type NodeIdentity struct {
    ModulePkgPath      string // "github.com/acme/app/internal/llm"
    EnclosingSymbolFQN string // "(*Service).Summarize"  |  "RunAgent.func1#0" for a closure
    Selector           string // "Messages.New"
    OccurrenceIndex    int    // 0,1,2… among same-selector calls in this symbol, source order
}

func (id NodeIdentity) NodeID() string {
    // "⋮" (U+22EE) is a delimiter that cannot appear in a Go import path, symbol, or selector.
    canon := strings.Join([]string{
        id.ModulePkgPath, id.EnclosingSymbolFQN, id.Selector, strconv.Itoa(id.OccurrenceIndex),
    }, "⋮")
    sum := sha256.Sum256([]byte(canon))
    return "n_" + hex.EncodeToString(sum[:8]) // 16 hex chars; collision-safe at repo node counts (tens–hundreds)
}
```

## §3 Key decisions

### 3.1 Decision — exclude absolute line numbers from the *identity*; keep them only in `call_site`
**Problem.** The frozen node carries `call_site.line_start/line_end`, but those shift on any edit above.
**Design.** Lines live in `call_site` (which is *allowed* to change between commits) and are **not** hashed
into `node_id`. Identity is structural (occurrence index), not positional (line). **Alternatives compared.**
*Hash the line number* — rejected: guarantees churn on every insertion, defeats the P0 diffability
scenario. *Hash the full call-expression source text* — rejected: churns on gofmt/renaming a local
variable (benign edits), and is over-sensitive. **Effect.** Adding a comment or running gofmt above a call
leaves every `node_id` untouched; only `call_site.line_*` updates — the diff shows "lines moved," not "node
replaced." This is precisely the M1 diffability requirement.

### 3.2 Decision — occurrence index is per-selector, in source order (not a global counter)
**Problem.** How to separate two calls in one function without a line number. **Design.** Index among
same-selector calls, source order. **Alternatives compared.** (a) *Sequential integer over all nodes in
the repo* — rejected outright: reshuffles on any edit, the exact anti-pattern D5 names ("sequential integer
IDs destroy diffability"). (b) *Hash the argument values* — rejected: changing a prompt string would change
the node's identity, so a prompt edit would read as delete-node + add-node instead of modify-node —
breaking lineage keyed on `node_id`. **Effect.** A prompt/model edit **keeps** the `node_id` (shows as a
field diff on the same node — correct for lineage); only adding/removing/reordering *same-selector* calls
in that function moves indices.

### 3.3 Decision — closures resolve to their named parent + closure ordinal (catalog W10)
**Problem.** A call inside `errgroup.Go(func(){ c.Complete(...) })` has an anonymous enclosing func.
**Design.** `enclosing_symbol_fqn = "<parent>.funcN#<closureOrdinal>"`. **Effect.** The node is addressable
and stable; two closures in the same parent don't collide.

## §4 Stability & uniqueness analysis (the acceptance argument)

| Edit | `node_id` behavior | Verdict |
|---|---|---|
| Add a comment / blank line / run `gofmt` above the call | unchanged | ✅ benign shift absorbed |
| Rename a local variable used in the args | unchanged | ✅ (args not in identity) |
| Edit the prompt string or model constant | unchanged (shows as field diff) | ✅ correct for lineage (§3.2) |
| Move the enclosing func to another file **in the same package** | unchanged | ✅ (package = dir) |
| Rename the enclosing function | changes | ⚠️ acceptable — a rename is semantic, not a line shift |
| Reorder two **same-selector** calls in one function | the two ids swap | ⚠️ known limitation, documented below |
| Add/remove a same-selector call before others | trailing indices shift | ⚠️ known limitation |
| Two byte-identical calls in one function | distinct ids (index 0,1) | ✅ no silent drop (I4) |
| Move the whole package to a new import path | changes | ⚠️ acceptable — it *is* a different package |

**Known limitation (documented, not hidden — per the skill's "expose conflicts" rule):** reordering or
inserting **same-selector** calls within one function shifts occurrence indices, so those nodes' ids move.
This is the residual cost of not using line numbers. It is acceptable because (a) it only affects multiple
*same-SDK-call* sites in *one* function — uncommon; (b) the alternative (line numbers) churns on *every*
edit; (c) the run report's node-count-per-source stays correct, so nothing is silently dropped — the diff
just shows the reordered pair as changed. If P5+ needs finer stability, an AST-structural-path component
can be **added** to the tuple additively without breaking existing ids for the common (single-call) case.

## §5 Determinism ties (I3)

- The occurrence index is computed in **source order** (AST traversal order within a function is
  deterministic), never map-iteration order.
- The final IR sorts nodes by `node_id` and edges by `(from,to,kind)` before emission, so serialization is
  byte-stable (P0 diffability scenario). The hash is pure over the tuple — no wall-clock, no randomness.
- **Test that must go red (I3 / §6.7):** discover a fixture twice → byte-identical IR; then apply a
  comment-only edit → all `node_id`s identical, only `call_site.line_*` changed.

## §6 Consumed by

The IR emitter (§5.1), the multi-source merge/dedup (§3.5 — a call site hit by both registry and
declaration produces the **same** `NodeIdentity` ⇒ same `node_id` ⇒ one node, both sources credited in the
report), and every downstream subsystem that keys on `node_id` (Metrics, Eval, Attribution) — which is why
§3.2's "prompt edit keeps the id" property is load-bearing for lineage, not a nicety.
