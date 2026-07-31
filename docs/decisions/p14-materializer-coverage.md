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
| [`internal/transform/skillbind.go`](../../internal/transform/skillbind.go) `toolValueForms` | **The source of truth.** One row per **(language, provider)** cell whose tool value this engine knows how to spell, naming the SDK generation it targets. The syntactic spellings register into the same map from [`skillbind_span.go`](../../internal/transform/skillbind_span.go) — one table, not two. |
| [`internal/transform/coverage.go`](../../internal/transform/coverage.go) `AxisCoverage()` | The **total** read across every axis × every registered language, where a gap is a present cell with a named cause rather than an absence. |
| `transform.MaterializerCoverage()` | The exported read of that table. Anything that needs to *state* coverage calls this. |
| The table below | A human-readable copy, **gated by a test** (`TestCoverageDocMatchesTheFormTable`) that fails if it stops matching. |

A copy with a gate is not a second source of truth; a copy without one is.

## Coverage today

<!-- BEGIN COVERAGE (checked by internal/transform TestCoverageDocMatchesTheFormTable) -->
| Language | Provider | SDK generation the emitted spelling targets |
| --- | --- | --- |
| go | anthropic | anthropic-sdk-go v1 (the generation that drops the F() wrapper) |
| go | openai | openai-go v1 (the generation with ChatCompletionToolUnionParam) |
| javascript | anthropic | @anthropic-ai/sdk v0.2x (tools as objects with name/input_schema) |
| javascript | openai | openai-node v4 (tools as objects with type: 'function') |
| python | anthropic | anthropic-sdk-python v0.3x (tools as dicts with name/input_schema) |
| python | openai | openai-python v1 (tools as dicts with type=function) |
| typescript | anthropic | @anthropic-ai/sdk v0.2x (tools as objects with name/input_schema) |
| typescript | openai | openai-node v4 (tools as objects with type: 'function') |
<!-- END COVERAGE -->

**Everything not in that table refuses, by name** — and the name says **which of three things** is
missing (P13 `language-coverage`). Specifically:

- **A cell with no spelling** — Kotlin, Java and Rust today, and any provider without a row — refuses as
  `no-materializer-for-this-language`, naming the cell and listing the cells that WOULD have worked.
  Their SDKs bind tools on a **builder** or a **request value**, so closing them needs a registry row
  declaring that binding site (P13 FR52/FR53) as well as a spelling.
- **A call site whose provider has no row in ITS language** refuses naming the provider. "Python is
  supported" is not "every Python call site is supported".
- **A call site whose tool set is assembled at run time**, or whose arguments are **unpacked** from a
  mapping, refuses as `call-site-cannot-carry-it` — in every language, before and after any row lands.
  🔴 That refusal is reported **ahead of** the coverage question, because it stays true afterwards.
- **An SDK that carries tools in an opaque serialized body** (Bedrock) refuses as a fact about that SDK:
  there is no tool value to construct or delete in any language.
- **A sealed contract with no `properties`** refuses rather than materializing an empty tool, in every
  language. An empty property bag is a valid tool that accepts nothing — it parses, and then fails every
  call the model makes against it.

## Tool PRUNING coverage is separate, and it moved for a different reason

Pruning is a different mechanic (a call-site deletion, not a construction) and was blocked in a different
package. The rewriter was never the problem: deleting a written element from a written list needs no
per-SDK knowledge. What was missing was the **frontend recording which written element is which tool**.

That landed in wave 14d ([`internal/discovery/toolsplit_span.go`](../../internal/discovery/toolsplit_span.go)),
built on the shared list splitter ([`listsplit.go`](../../internal/discovery/listsplit.go)) that P16's
context selection uses too — one implementation, per-language syntax as data. So pruning now covers
**every language with a list splitter** (python, typescript, javascript, kotlin, java, rust) plus Go's
AST path, and `discovery.RecordsToolSplit` is the single read behind both the coverage cell and the
refusal.

🚫 The pruner still **never infers** which element is which tool — not by position, not by text
similarity, not by matching the selection against element text. A tool the frontend could not locate is
recorded as *unlocatable*, explicitly, and every prune over it refuses (D-14.5 part 3).

## The total table, and what is still open

The table above lists the cells that MATERIALIZE. It is no longer the whole answer: `AxisCoverage()`
emits a cell for **every** axis × **every** registered language, and a gap is a present cell carrying a
cause and the artifact that would close it. `TestCoverageIsTotalOverRegisteredLanguages` fails the moment
a frontend is added without one — absence is not a value.

Still open on this axis, stated as the cells themselves state it:

| Cell | Cause | What would close it |
|---|---|---|
| (kotlin \| java \| rust) × anthropic/openai — **binding** | `no-materializer-for-this-language` | a registry row declaring the SDK's **builder-chain / request-field** binding site (P13 FR52/FR53), plus the tool-value spelling for that cell |
| any language × bedrock — **binding** | `call-site-cannot-carry-it` | nothing: the tools live in an opaque serialized body, so there is no value to construct in any language |

The first row is platform work with a named artifact. The second is not, and must never borrow the
first's wording — that distinction is the whole point of the cause classes.

## Adding a row

1. Add the provider to `toolValueForms` with the SDK generation its spelling targets. The row is a
   **claim that this spelling compiles against that SDK**, and the build gate is what proves it.
2. Run `go test ./internal/transform/` — `TestCoverageDocMatchesTheFormTable` will fail until this
   file's table is updated, which is the point.
3. Nothing else needs editing. The console badge, the refusal text and this page all read the same table.
