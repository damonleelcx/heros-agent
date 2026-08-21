## Why

The customer console carries **31,628 words** across `web/console/src/app/app/**`. `/app/context` alone
carries 1,995 — roughly eight minutes of reading — and the reader can change nothing there. Its coverage
table is transcribed from Go by hand; its diff is a fixture; the sibling axis surfaces bind their
authoring panels to a demonstration node (`/app/memory`'s `const NODE_ID = "recall"`).

None of that was wrong when it was written, and the source says why: `/app/context`'s own header states
the table *"is transcribed rather than fetched because this page is a capability explanation, not a live
view"*, and `/app/memory`'s panel explains that it is interactive *"even though the change will not reach
their source at this milestone"*. These pages were built in phases where the engine existed and the
reader's repository did not. Explaining the engine was the honest maximum.

[P32](../p32-repo-intake/) removes that constraint. Once a repository is connected the platform holds the
reader's IR — their nodes, their current policies, their current strategies. A page that explains what
the platform *would* do to a hypothetical node, while the reader's real node is one query away, has
stopped being cautious and become stale.

One rule decides what changes: **if a sentence is the same for every reader it is documentation; if it
changes with the reader's data it is the product.** That rule cuts the prose and simultaneously protects
what must not be cut — a refusal's verbatim cause text, `not_measured` with its named missing input, and
the boundary stated above a control all change with the reader's data.

## What Changes

- **ADDED** `source-bound-editing`: every axis surface is bound to one `(workflow, node)` subject drawn
  from the reader's imported source, renders that node's **current** value, and never renders a fixture,
  sample node or demonstration value in the position the reader's own data would occupy.
- **ADDED** the subject resolved **once in the shell** and persisted across axis surfaces; a reader with
  one candidate node is asked nothing, and the resolved subject is always named on screen and always
  changeable.
- **ADDED** `not_connected` as a fourth rendered state beside `not-mounted` / `read-failed` /
  `not-reported`, naming the missing input and linking to the connection flow — delivered as a 200
  carrying that word, never as a 404.
- **ADDED** one **editor kit** serving all six axes — picker bound to the axis's closed vocabulary at its
  recorded set version, params form derived from that vocabulary's schema, validation at save, preflight
  showing the resulting `config_hash` and the diff against the parent, and an `unverified` stamp until
  the harness has run. Extracted from `/app/memory`'s existing panel, not invented.
- **ADDED** `working-surface-composition`: the moving rule, a fenced static-prose budget per working
  route, an enumeration of the text that may **not** move or be shortened, and a prohibition on tooltips,
  accordions and modals as destinations for moved text.
- **MODIFIED** `axis-node-projection` — the P29 requirement that worked examples be retained now requires
  them to be retained **at a named destination**: on the working surface when they vary with the reader's
  data, on the reading surface, labelled as the platform's fixture, when they do not. "Nothing is
  removed" is preserved; "nothing moves" was never the point of it.
- **MODIFIED** the coverage read on `/app/context`: a live per-node read replaces the hand-transcribed
  table, and `TestContextCoverageTableMatchesEngine` is removed **only** after that lands, because the
  artifact it guards no longer exists.
- **NOT CHANGED** the browser derives nothing; P9's viewport rule; the four-state axis vocabulary; the
  three transport treatments; the engine, the registries and every materializer boundary.

## Impact

- **Affected capabilities:** `source-bound-editing` (new), `working-surface-composition` (new),
  `axis-node-projection` (modified)
- **Affected code/systems:** `web/console/src/app/app/{context,memory,harness,wiring,authoring,delivery}`,
  `web/console/src/lib/projection.ts` (subject resolution), `web/console/src/app/(reading)` (destinations
  for moved text), `web/console/src/components` (the extracted editor kit), the console's axis read
  handlers in `internal/api`
- **Dependencies:** upstream — P32 (a connected repository and its IR), P9 (shell, BFF, viewport rule),
  P23 (`reading-surface` as the destination), P13–P18 (axis vocabularies and param schemas), P33 (the
  four-state vocabulary). Unblocks — P31 findings having a surface to link into, P35 proposing against a
  node the reader has already seen.
- **Documents only in this program.** Every task is unchecked; no TSX, Go or migration ships with this
  change set.
