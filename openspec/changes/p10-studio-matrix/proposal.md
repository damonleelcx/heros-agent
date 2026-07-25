## Why

P10 shipped the studio as a deep, single-scroll page: a prompt browser, then a timeline, then a diff,
then a preview, then an editor, stacked vertically. Everything works — it is browser-verified against
the real hermes-agent checkout — but the *shape* is wrong for the job. A user configuring a workflow
thinks **per node** ("what should the triage node use — which model, which prompt?"), and the flat page
makes them scroll to assemble that picture one control at a time. The studio is also nested where a
primary surface should be.

The established pattern for "try prompts across models" is a **matrix**: LangSmith's Playground compares
configurations side by side, promptfoo lays prompts against models in a grid, Opik's playground swaps
models over one prompt. The grid is scannable where a scroll is not. This change adopts it — with one
deliberate divergence that is the whole point: **the matrix ranks nothing.** Every eval tool's grid
ends in a best-cell highlight; ours must not, because ranking is D8's forbidden amateur loop wearing a
new layout. The heros matrix is a **configuration surface**, not a leaderboard.

## What Changes

- **The studio's primary surface becomes a node × model matrix.** Agent nodes are columns (sourced from
  the workflow's discovered IR), models are rows (from the model registry). Each cell is where a user
  edits, previews, test-runs, and binds the node's prompt against that model. The studio is promoted to
  a top-level console destination.
- **A prompt is node-scoped.** It belongs to a column (a node). Editing from any cell produces a new
  immutable version of that node's prompt; the row (model) is what the cell previews and tests against.
  This reuses the P10 content-addressed registry unchanged — no per-cell prompt fragmentation.
- **"Save and inject into runtime" is `bound` apply mode, reused whole.** Binding a cell writes the
  node's binding-document entry `{model, prompt version}`; it is **marked unverified** (a selection is
  not a proof) and **refusable by automation level**. The matrix adds **no new runtime path** — it is a
  UX over the existing runtime-config-binding capability.
- **At most one cell per column is *in force*.** A node runs one model + one prompt at runtime, so
  exactly one cell per column is the bound configuration; the rest are exploratory. "In force" is
  rendered distinctly from "verified" — the two states must never look the same.
- **New read/compute surfaces, all thin:** list models (rows), list a workflow's nodes (columns), and a
  studio test-run route. Test-run routes through `providergateway` (ADR-002) and meters under the studio
  spend kind (P10 FR31); a deployment without provider credentials uses a deterministic echo completer
  so the surface is demonstrable without spending real money.
- **Not changed.** No new registry table; no ranking, score, winner, or promotion path anywhere in the
  matrix (P10 FR30 extended to the grid's layout); ADR-002 untouched; the binding document, resolver,
  reconciliation, and verified/unverified marking are P10's, reused verbatim.

## Impact

- **Affected capability:** `prompt-studio` (P10) — extended with the matrix surface (ADDED requirements
  below). No other P10 capability changes.
- **Affected code:** `internal/registry` (a model-catalog read model, additive), `internal/api`
  (models/nodes/bind/run routes over the existing P10 store + `studio.Runner`), `web/console`
  (the matrix grid + cell panel, replacing the deep page as the landing view), `cmd/p2uidemo` (seed
  models + load a workflow IR so the matrix has real rows and columns).
- **Dependencies:** P10 (all of it) — this is a UX layer over the shipped capabilities.
- **Breaking:** none. Additive endpoints; the prior studio components remain available as drill-downs
  from a cell (timeline/diff/history). No config_hash, registry, or runtime behavior changes.
