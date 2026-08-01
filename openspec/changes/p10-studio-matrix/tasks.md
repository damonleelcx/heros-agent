# Tasks — P10 Studio Matrix Surface (M-series redesign)

A UX redesign of the P10 studio: its primary surface becomes a **node × model matrix** (nodes = columns,
models = rows), where each cell edits/previews/tests/binds a node's prompt against a model. It reuses
P10's shipped capabilities (registry, bindings, bound mode, resolver, reconciliation) — it adds a UX
layer and thin read/compute routes, **no new runtime path and no ranking**.

**Standing constraints.** No new registry table. No score, rank, winner, best-cell highlight, or
promotion path anywhere in the matrix (P10 FR30/D8 extended to the grid). A prompt is node-scoped — an
edit is a new immutable version. "Save and inject" is `bound` mode, marked unverified. ADR-002 untouched.

---

## M1. Product Designer — matrix UX research + PRD

- [x] M1.1 Research the node × model matrix pattern (LangSmith/promptfoo/Opik) and decide the design:
      a **configuration surface that ranks nothing**, node-scoped prompt, save-and-bind = bound mode.
- [x] M1.2 Update the P10 PRD: FR34–FR40 (matrix surface) and design decision D9 (matrix ranks nothing).
- [x] M1.3 State that the studio is a **primary destination**, not a deep page.

## M2. System Designer — contracts + openspec

- [x] M2.1 Write the openspec change (proposal, spec delta) for the matrix surface.
- [x] M2.2 Decide the endpoint contracts (careful-api-creation): list models (rows), list workflow nodes
      (columns), test-run a cell, bind a cell. Reuse the P10 store; no new table.
- [x] M2.3 Fix the invariants: one bound cell per column; a bound cell is unverified until a verified
      delta exists; the matrix computes no cross-cell comparison.

## M3. Backend — model catalog, nodes, bind, run

- [x] M3.1 Add a **model-catalog** read model (rows): list registered models with provider/id/params.
- [x] M3.2 Add a **nodes** read surface (columns) from a loaded workflow IR — no new table.
- [x] M3.3 Add **`POST /api/v1/studio/run`** — test-run a cell through `studio.Runner`, returning
      output + cost + latency + tokens, metered under the studio spend kind.
- [x] M3.4 Add **`POST /api/v1/studio/bind`** — bind a node to a (model, prompt version) via bound
      mode, producing the binding-document entry marked **unverified**; enforce one bound cell per node.
- [x] M3.5 Test — model catalog lists rows; run returns figures + meters studio spend; bind produces an
      unverified binding-document entry and replaces a prior bind for the same node.

## M4. AI Engineer — studio run routing + mock completer

- [x] M4.1 Route the cell test-run through `providergateway` via `studio.Runner` (platform caller,
      ADR-002).
- [x] M4.2 Provide a **deterministic echo completer** for deployments without provider credentials, so
      the test-run surface is demonstrable without spending real money; real deployments use the gateway.
- [x] M4.3 Meter every test-run under the studio spend kind; a capped run stops and reports the cap.
- [x] M4.4 Test — the echo completer returns deterministic output/tokens; studio spend is recorded and
      never appears in eval cost.

## M5. Frontend — node × model matrix grid + cell panel

- [x] M5.1 Build the **matrix grid**: agent nodes as columns, models as rows, from the new endpoints.
- [x] M5.2 Build the **cell panel**: select the node's prompt version, inject variables, edit
      (Save as new version), preview (byte-identical), test-run (output + cost + latency + tokens).
- [x] M5.3 Build **save-and-inject**: bind the node to the cell's (model, prompt) — bound mode, marked
      **unverified**, refusable by automation level; render the in-force cell distinctly from verified.
- [x] M5.4 🔴 No ranking: **no score, rank, winner, or best-cell highlight** anywhere in the grid; the
      only cell distinctions are *in force* and *unverified/verified*.
- [x] M5.5 Make the studio a **primary console destination** (a scannable landing grid, not a deep page).
- [x] M5.6 Render prompt bodies and outputs as **text, never markup**; keep the exploratory label.
- [x] M5.7 Browser-test the whole surface in Chrome against the live platform, error paths walked.

## M6. QA — matrix acceptance gate

- [x] M6.1 Assert **no ranking artefact** appears in the matrix (score/rank/winner/best-cell) — a failing
      test, not a review note.
- [x] M6.2 Assert a bound cell is marked **unverified** and that "in force" renders distinctly from
      "verified".
- [x] M6.3 Assert **one bound cell per column** (binding a second replaces the first).
- [x] M6.4 Browser-rendered acceptance of the grid, the cell panel, preview/test/bind, and the empty-axis
      states.

## M7. DevOps — seed models + load IR into the demo

- [x] M7.1 Seed a handful of **model entries** (rows) into the demo platform.
- [x] M7.2 Load a workflow **IR** (the hermes IR) into the demo so the matrix has real columns.
- [x] M7.3 Wire the new mounts (models/nodes/run/bind) and confirm the studio is reachable.

## M8. Sales Operations — matrix claim discipline

- [x] M8.1 Update the capability statement: the matrix is an **exploration/configuration surface, not a
      leaderboard**; a bound cell is "selected, unverified," never "proven best."
- [x] M8.2 The demo script must not present the matrix as a ranking; the honest pitch is
      *try each cell in seconds → prove one with a multi-seed eval → ship it as a verified PR*.

## M9. Run for hermes

- [x] M9.1 Run the matrix studio against `github.com/nousresearch/hermes-agent` (real nodes as columns).
