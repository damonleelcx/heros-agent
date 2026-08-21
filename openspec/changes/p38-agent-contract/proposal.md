## Why

[`openspec/specs/operator-agent-authoring/spec.md`](../../specs/operator-agent-authoring/spec.md) — a
**folded** spec, which in this repository means *this is true today* — requires that
*"Each axis SHALL be edited against its existing vocabulary and never as free text"*, under a header that
reads **"🚫 No axis is a text box."** It specifies a prompt editor that parses slots, a skill picker
showing compiled schemas, a tool picker showing scope and risk tier, and params forms derived from each
vocabulary's schema.

[`internal/herosagent/axiseditor.go`](../../../internal/herosagent/axiseditor.go) implements exactly that —
`ParsePrompt`, `SelectableSkills`, `BindableTools`, `ValidateHarnessParams`, `ValidatePolicyParams` —
under a header that reads *"axiseditor.go is D12: NO AXIS IS A TEXT BOX, and every param validates AT
SAVE"*. **Every exported function in that file has zero non-test callers.**

The operator console's Publish tab is **eight free-text inputs** into which an operator pastes
64-character version ids and a comma-separated list of tool names. The Axes tab renders each axis as an
opaque hash. So the first half of this change is conformance recovery: the spec exists, the
implementation exists, and nothing connects them.

The gap survived because of how it was measured. `openspec/operator-surface-ledger.md` carries the row
`| operator-agent-authoring | surface | /agent#publish, /agent |`, and that row is **true** —
`/agent#publish` exists. The fence asserts that a destination exists, **not that the destination
implements the capability**, so eight text boxes satisfy it exactly as well as eight editors would. That
is the institutional layer of the root cause and it covers every other row in the ledger; this change
fixes one row and states the general finding for the ledger's owner rather than smuggling in a rewrite of
a fence fourteen phases depend on.

The second half is what the axes do not cover. Reliable agents are decided by twenty dimensions — goal,
boundaries, workflow, model, prompt, context, tools, loop, state and memory, retrieval, validation,
guardrails, stopping conditions, evaluation data, reliability measurement, observability, human approval,
end-to-end testing, gradual rollout, and the improvement loop. Fifteen of the twenty **already exist and
are enforced** in this codebase, as constants, gates, ladders and fences. None of them is visible on the
surface that claims to configure the agent. An operator reading `/agent` cannot tell what the agent is
for, what it refuses, what stops it, or what it costs before it stops.

## What Changes

- **ADDED** `agent-contract`: one surface rendering **all twenty dimensions**, always, each in exactly one
  of three states — `authorable`, `observable`, `fixed` — where a `fixed` dimension always names its
  reason **and** what would change it. Nothing is hidden: a hidden control is indistinguishable from one
  that does not exist.
- **ADDED** 🔴 the `config_hash` boundary. The contract is a **view** over things that already have
  identity. Dimensions already participating in `config_hash` continue to; the rest are recorded as
  operating policy, versioned and audited separately and **never hashed** — because every pinned
  inference is keyed by `(source_revision, agent config_hash)`, and adding contract fields to the hashed
  definition orphans every pin while nothing errors.
- **ADDED** the requirement that the surface states, per dimension and **before** the save, whether the
  change creates a new agent version, and — when it does — how many pinned inferences would need
  re-inference and at what cost.
- **ADDED** a fence asserting the boundary **in both directions**: a non-hashed change producing a new
  `config_hash` fails, and a hashed change producing no new `config_hash` fails.
- **ADDED** surfaces for the eleven dimensions that had none: goal, boundaries (placement + refusal set),
  stopping conditions (ceilings, budget, tenant and fleet caps with their rolling window), evaluation set
  and floors, approval policy, rollout ladder with its preconditions, and validation, guardrails,
  observability, end-to-end testing and the improvement loop as `observable`.
- **ADDED** to `operator-surface-ledger` a **conformance** assertion for the `operator-agent-authoring`
  row: a destination satisfies it only if it renders a picker bound to each axis vocabulary, and any axis
  served by a free-text input fails the build.
- **MODIFIED** the operator agent surface to satisfy the folded `operator-agent-authoring` requirements by
  wiring `internal/herosagent/axiseditor.go` to routes — not by writing a second implementation beside it.
- **NOT CHANGED, and deliberately:** the rehearsal gate, the separate activation act, version
  immutability and content addressing, the `no_change` outcome, the credential-by-name posture and its
  reflective fence, the rollout ladder's refusal to advance without evidence, and the turn ceiling —
  which stays a constant, because *a ceiling an operator can raise is not a ceiling*.
- **NOT ADDED, and this refuses part of the request as stated:** editors for the guardrail and validation
  dimensions. They are what stops the agent doing harm; a console that can weaken them is a larger risk
  than a console that cannot show them being changed. They render in full, read-only, with their current
  posture and where it is decided.

## Impact

- **Affected capabilities:** `agent-contract` (new), `operator-surface-ledger` (modified),
  `operator-agent-authoring` (satisfied, not respecified)
- **Affected code/systems:** `web/admin-console/src/app/agent`, `web/admin-console/src/lib/actions.ts`,
  `internal/api/adminconsole.go` (per-axis editor routes), `internal/herosagent` (wiring
  `axiseditor.go`; the operating-policy record), `web/admin-console/scripts/scan-ledger.mjs`,
  `cmd/consoletypes` (the three-state union per ADR-007)
- **Dependencies:** upstream — P30 (the definition, the editor core, the rehearsal gate, placement and
  cap stores), P8 (operator shell, RBAC, audit), P26 (the ledger and its fence), ADR-007. P36 supplies
  the loop and graph dimensions; until it lands they render `fixed` with "HEROS is one node" as the
  reason. Unblocks — an operator changing the platform's agent without pasting a hash.
- **Documents only in this program.** Every task is unchecked; no Go, TSX or migration ships with this
  change set.
