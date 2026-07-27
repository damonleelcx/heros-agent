# P14 — Skill-materializer coverage

**This file is the ONE place per-language / per-provider skill-materializer coverage is stated** (P14
NFR7, task 9.4). Everything that talks about what the skill axis can APPLY — a refusal a user reads, a
capability a doc claims, a badge a console renders, a sentence a salesperson says — resolves back here
or to the table this file describes.

## Why coverage needs a single source at all

`internal/transform` refuses what it cannot do, by name. The refusal is written to be acted on: it says
which language has no materializer, or which provider has no declared tool-value form. The moment a
second place also states coverage — a capability matrix in a deck, a README bullet, a hard-coded list in
the console — the two can disagree, and the way they disagree is always the same: **the document is
optimistic and the engine is not**. A user is then told a change is supported, asks for it, and gets a
refusal that contradicts the page they read it on.

This is the same discipline `argumentForm` already applies to the model/prompt refusals
([`internal/transform/rewrite_span.go`](../../internal/transform/rewrite_span.go)): *"This table is the
single place those reasons are written down, so a refusal a user reads and the capability doc a
salesperson reads cannot drift apart."*

## Where the truth lives

| Artifact | Role |
| --- | --- |
| [`internal/transform/skillbind.go`](../../internal/transform/skillbind.go) `toolValueForms` | **The source of truth.** One row per provider whose Go tool value this engine knows how to spell, naming the SDK generation it targets. |
| `transform.MaterializerCoverage()` | The exported read of that table. Anything that needs to *state* coverage calls this. |
| The table below | A human-readable copy, **gated by a test** (`TestCoverageDocMatchesTheFormTable`) that fails if it stops matching. |

A copy with a gate is not a second source of truth; a copy without one is.

## Coverage today

<!-- BEGIN COVERAGE (checked by internal/transform TestCoverageDocMatchesTheFormTable) -->
| Language | Provider | SDK generation the emitted spelling targets |
| --- | --- | --- |
| go | anthropic | anthropic-sdk-go v1 (the generation that drops the F() wrapper) |
| go | openai | openai-go v1 (the generation with ChatCompletionToolUnionParam) |
<!-- END COVERAGE -->

**Everything not in that table refuses, by name.** Specifically:

- **Every language other than Go** keeps the interim refusal
  ([`decisions.md`](../../openspec/changes/p14-skills-tools-optimization/decisions.md) D-14.3, D-14.4):
  *"binding skills … requires constructing SDK-specific tool values at the call site, and no
  materializer for this language has landed yet."* Discovery still finds those call sites and the IR
  still records them — what is missing is the materializer, not the language.
- **A Go call site whose provider has no row above** refuses naming the provider and listing the ones
  that would have worked. "Go is supported" is not "every Go call site is supported".
- **A Go call site whose tool set is assembled at runtime** refuses whatever the provider: there is no
  static declaration to replace, and overwriting it would silently discard what it builds.
- **A sealed contract with no `properties`** refuses rather than materializing an empty tool. An empty
  property bag is a valid tool that accepts nothing — it compiles, and then fails every call the model
  makes against it.

## Tool PRUNING coverage is separate, and narrower

Pruning is a different mechanic (a call-site deletion, not a construction), so it has its own boundary:
**Go only**, and only where the node's tool set is written as a static list. The tree-sitter frontends
record no tool split at all, so a prune there has nothing to prune *against* and refuses
([`internal/transform/rewritetools.go`](../../internal/transform/rewritetools.go) `spanRewriteTools`).

## Adding a row

1. Add the provider to `toolValueForms` with the SDK generation its spelling targets. The row is a
   **claim that this spelling compiles against that SDK**, and the build gate is what proves it.
2. Run `go test ./internal/transform/` — `TestCoverageDocMatchesTheFormTable` will fail until this
   file's table is updated, which is the point.
3. Nothing else needs editing. The console badge, the refusal text and this page all read the same table.
