# ADR-007 — One Go generator emits both the JSON Schema and the TypeScript; a regeneration diff fails the build

- **Status:** Accepted (2026-07-24)
- **Deciders:** System Design + Frontend (proposed) + User (ratified)
- **Resolves:** [`openspec/changes/p9-web-console/tasks.md`](../../openspec/changes/p9-web-console/tasks.md)
  §2.2, and PRD [P9](../prd/P9-web-console.md) §14 Q3 — `design.md` Decision 5 fixed *that* types are
  generated with a drift gate and left the toolchain open.
- **Relates to:** `design.md` Decision 5 (the reasoning for generating at all) and Decision 3 (the
  browser derives nothing, so the view types **are** the console's entire data contract).
- **Owns:** phase **P9 — Web Console**, §6.

## Context

The console's data contract is a set of Go view structs — `evalboard.View`, `patternclassifier.GraphView`,
`telemetry.RunMonitor`, `scorecard.View`, the P5.5 `Surface`, and the P2 `runView` / `transformView` /
`submitResult` / `specError` in `internal/api`. Decision 5 established **why** they must be generated:

> the failure mode of a drifted hand-written type is not a compile error, it is a **blank cell in
> production** — a field renamed in Go becomes `undefined` in TypeScript and renders as an em-dash that
> looks like legitimately absent data.

It named a preference — *emit JSON Schema from the view types and generate TypeScript from that,
composing with the existing [`schemas/`](../../schemas/) discipline* — without committing to it. Two
things about the repository bear on the choice. First, `schemas/` is already a real discipline with a
validation gate (`make schema` runs `schemas/validate.py` plus three contract proofs), so a JSON Schema
artifact is not a new concept here. Second, four of the ten view types are **unexported**
(`internal/api.runView` and friends), so any generator must run from inside the package that declares
them or be handed instances by it.

## Decision

**A single Go generator reflects over the view types once and emits both artifacts — a JSON Schema
document under `schemas/` and a checked-in `.d.ts` for the console — and CI regenerates and fails the
build on any diff.**

```
internal/api/consoletypes.go      ConsoleViewTypes() []any   — the registry of view types, in the
                                                               package that can see the unexported ones
cmd/consoletypes/main.go          the generator
schemas/console-view.schema.json  generated · validated by the existing schemas/ gate
web/console/src/lib/types.generated.ts
                                  generated · the console's only view type import
make console-types                regenerate
make console-types-check          regenerate into a temp dir and diff — the drift gate
```

Three properties make this the version worth having:

1. **Both artifacts come from one reflection pass**, so the schema and the TypeScript cannot disagree
   with each other. A two-step pipeline (Go → schema → third-party generator → TS) has an intermediate
   that can be stale independently, and its failure is silent.
2. **No third-party generator in the chain.** `json-schema-to-typescript` and the Go-struct-to-TS tools
   are all reasonable; none of them is worth a supply-chain dependency in the build path of the surface
   whose defining property is that it holds a credential (NFR10). The emitter is a few hundred lines of
   `reflect` over structs the repository owns.
3. **The gate is a diff, not a warning.** `make console-types-check` regenerates and compares. A Go
   read-model change that is not reflected in the checked-in artifacts is a **red build**, which is the
   entire point — Decision 5's argument is that the alternative failure is invisible.

The generated TypeScript is **checked in**, not built on demand. A `.gitignore`d build product cannot be
diffed in review, and the diff is where a read-model change becomes visible to the person who has to
render it.

## Alternatives rejected

**Hand-written types maintained by review.** Rejected by Decision 5 already, on evidence rather than
principle: the failure mode is a blank cell that looks like absent data, and no review reliably catches
a field rename in a struct the reviewer is not reading.

**Go → JSON Schema → `json-schema-to-typescript`.** The PRD's stated preference, and genuinely
attractive because it reuses a mature generator. Rejected on two counts. It adds a build-path dependency
to the credential-holding surface for a job the repository can do in one pass; and it introduces an
intermediate artifact that can be regenerated on its own, which is exactly the kind of partial staleness
a drift gate is supposed to make impossible. The **JSON Schema is still emitted** — it composes with
`schemas/` as the PRD wanted — it simply is not load-bearing for the TypeScript.

**OpenAPI for the whole `/api/*` surface, with a client generated from it.** More capable: it would
generate the request functions too, not just the types. Rejected as scope and as a **one-way door in the
wrong direction** — an OpenAPI document is a published contract for the platform API, and publishing one
is P2/P4's decision to make about their own surface, not a side effect of building a console. It also
conflicts with 🔴 `careful-api-creation`: describing the API is one step from freezing it.

**`tsc`-side runtime validation (zod, io-ts) instead of generated types.** Solves a different problem —
it validates a payload at runtime rather than proving the type matches the server. Rejected because the
BFF is a pass-through (Decision 3): a payload that fails validation at the browser is a platform bug the
console cannot fix and must not hide, and the render-as-received rule (FR14) means the console's correct
behavior on a surprising payload is to render what it got, not to reject it.

## Consequences

- `internal/api` gains one small exported function whose only purpose is the generator's registry. That
  is a deliberate, minimal widening of the package's surface, recorded here so it is not read as
  accidental.
- Adding a field to a view struct is now a **two-file** change: the struct, and `make console-types`.
  Forgetting the second is a red build rather than a blank cell.
- The generator understands the Go types the view models actually use. A view type that introduces a
  construct the emitter does not handle (an interface field, a `map` with a non-string key, a custom
  `MarshalJSON`) fails the generator loudly rather than emitting `any` — because `any` is how a
  generated contract quietly stops being a contract.
