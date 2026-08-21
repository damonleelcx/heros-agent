# Tasks — P38: The Agent Contract

> **Nothing here is implemented.** This change set is documents only, as the whole GEHA program is.
> Every box is unchecked and stays unchecked until the code lands.

## 1. System Designer — the boundary, before anything is writable

- [ ] 1.1 🔴 Enumerate, in one Go location, which dimensions participate in `config_hash` and which are operating policy. The list is the contract's identity rule and a later reviewer must be able to read it rather than reconstruct it.
- [ ] 1.2 🔴 Record the orphaning hazard next to that list: every pinned inference keys on `(source_revision, agent config_hash)`, so a dimension added to the hashed side invalidates every pin and **no test goes red**.
- [ ] 1.3 Define the three-state vocabulary (`authorable` | `observable` | `fixed`) as a closed enum, generated into the console type union per ADR-007.
- [ ] 1.4 Decide PRD §14 Q1 — whether `OperatingPolicy` is a table or a projection over the audit log — **before** any write path is built. The default in this repository is not to create a table.
- [ ] 1.5 Decide PRD §14 Q2 — whether the approval policy is authorable in both directions or only toward more approval.
- [ ] 1.6 Record, per `fixed` dimension, the reason verbatim from the decision that fixed it and what would change it. A reason invented here rather than quoted is a second source of truth for a safety decision.

## 2. Backend Dev — read paths first

- [ ] 2.1 Per-axis read routes resolving refs to **content**: the prompt body and its derived slots, the bound tools with scope and risk tier, the strategy with its params, the policies with theirs. A route returning the ref is the current page with extra steps.
- [ ] 2.2 Contract read route assembling all twenty dimensions with their states.
- [ ] 2.3 Surface the already-stored operating values that have no route today: caps and their rolling window, placement, rollout stage and its preconditions, rehearsal floors.
- [ ] 2.4 Surface the `observable` dimensions from their owners — the refusal set, the skill gate's checks, the sandbox posture, the telemetry posture, the improvement loop — read-only, each naming where it is decided.
- [ ] 2.5 Wire `internal/herosagent/axiseditor.go` to per-axis editor routes. **Do not write a second implementation**; if the package's API does not fit, change the package.
- [ ] 2.6 `creates_version` computed server-side per dimension and returned with the read, so the browser never derives it.
- [ ] 2.7 Pinned-inference count for a prospective hashed change, returned with the preview.
- [ ] 2.8 Write paths for the operating-policy dimensions, each requiring a reason and writing an audit entry with its actor.
- [ ] 2.9 Central event names — `admin.agent.contract_read`, `.dimension_saved`, `.dimension_refused`, `.policy_saved` — in the central enum, no literals.
- [ ] 2.10 Every WARN/ERROR carries `request_id` / `trace_id` / `span_id`.
- [ ] 2.11 New routes added to the ingress allowlist as `Exact` paths.

## 3. Frontend Dev — the surface

- [ ] 3.1 Render all twenty dimensions, grouped by question — *what it is for*, *what it runs*, *what stops it*, *how it is measured*, *how it ships* — in the existing tab component.
- [ ] 3.2 One renderer per state; a `fixed` dimension renders its reason and its escape hatch, never a bare disabled control.
- [ ] 3.3 🔴 Preserve the four behaviours the current page documents as things it must never do otherwise: `serving_config_hash` rendered separately from the definition being viewed; the three-valued axis status; unavailable strategies shown with the service they need; the wiring axis read-only **with** its reason.
- [ ] 3.4 Replace the eight free-text inputs with bound editors: prompt template with derived slots, model picker, skill picker with compiled schemas, tool picker with scope and risk tier, context and memory policy pickers with schema-derived params, harness picker with `max_turns`.
- [ ] 3.5 Before every save, state whether it creates a version — and when it does, how many pinned inferences would need re-inference and at what cost.
- [ ] 3.6 Preview renders the resulting `config_hash`, the axis-by-axis diff against what is active, and any refusals.
- [ ] 3.7 A dimension present in Go and absent from the surface fails the type-check (ADR-007).
- [ ] 3.8 No credential field anywhere; the credential is bound by provider name.
- [ ] 3.9 Operator console tokens only; no new literals. Hazard palette on destructive and `refused` states only.
- [ ] 3.10 Every editor is a labelled control reachable by keyboard, with validation errors associated to their fields.
- [ ] 3.11 A denied operator sees the dimension, its state, and who holds the capability — not a blank page.

## 4. AI Engineer — what the numbers on this page may be

- [ ] 4.1 The rehearsal report stays **per fixture**. If a summary is rendered at all it is `n passed / n total` beside the failing names — never a bare percentage, because activation requires the floor on every fixture individually.
- [ ] 4.2 Spend renders per tenant and per dimension, never as one fleet number; a fleet aggregate is what a runaway looks normal inside.
- [ ] 4.3 The eval-floor editor states, on the control, that lowering a floor is how a failing definition passes — and records the reason on the rehearsal report itself (PRD §14 Q4).

## 5. DevOps

- [ ] 5.1 Contract health — operating-policy readable, registries resolving, pin count for the active `config_hash` — on a readable endpoint, not only in the page.
- [ ] 5.2 Read-only phase deploys and is exercised before any write path ships.
- [ ] 5.3 The undo sentence already in the publish action (`republish the previous definition; it is immutable and content-addressed, so republishing creates nothing`) is preserved in the UI.

## 6. QA — fences that can go red

- [ ] 6.1 🔴 **Hash boundary, direction one**: change a non-hashed dimension → `config_hash` unchanged **and** the pins still resolve. Mutate the boundary list to include it; the test must fail.
- [ ] 6.2 🔴 **Hash boundary, direction two**: change a hashed dimension → a new `config_hash` exists. Mutate to exclude it; the test must fail.
- [ ] 6.3 No axis is served by a free-text input. **Add one; the build must fail.**
- [ ] 6.4 Every dimension renders with exactly one state; a dimension with no state fails.
- [ ] 6.5 Every `fixed` dimension carries a non-empty reason **and** a non-empty escape hatch. Empty either → fail.
- [ ] 6.6 Save writes and audits: HTTP 200 → `SELECT` the row → `SELECT` the audit entry with actor and reason → assert the surface renders it. A 200 is not evidence of a write.
- [ ] 6.7 The four must-never-lose behaviours (3.3), one case each.
- [ ] 6.8 An unapproved tool is not bindable; a skill whose schema does not compile is not selectable; a schema with a remote `$ref` is rejected and **not fetched**.
- [ ] 6.9 A param invalid for its schema is refused at save, naming the entry and the parameter — one case per axis that takes params.
- [ ] 6.10 A `max_turns` above the ceiling is refused; the ceiling itself is not settable from any route.
- [ ] 6.11 Publishing creates a pending version that analyses nothing until activated.
- [ ] 6.12 Ledger conformance fence: point the row at a destination whose axes are text boxes → build fails.
- [ ] 6.13 Browser acceptance for A5 — an operator publishes a definition without pasting a version id. A green build is not acceptance.

## 7. Product Designer + Sales Operations

- [ ] 7.1 Copy for a `fixed` dimension that reads as a deliberate decision, not as an unfinished feature. A disabled control with no explanation generates the request to finish it.
- [ ] 7.2 Copy for the `observable` guardrail dimensions that says what is enforced and where it is decided, without implying it can be changed here.
- [ ] 7.3 Nothing in the summary claims the operator can now "configure the agent's guardrails".
- [ ] 7.4 Noun dictionary: `axis`, `dimension`, `definition`, `version`, `config_hash`, `contract` used consistently, and `dimension` is defined once where it first appears.

## 8. Sign-off

- [ ] 8.1 PRD §14 Q1–Q6 answered and folded into this change set.
- [ ] 8.2 🔴 The hash boundary list (1.1) reviewed by whoever owns P30's determinism guarantee, before any write path is built.
- [ ] 8.3 D5's refusal — no editors for guardrails and validation — confirmed with the user, since it is a deliberate narrowing of the request as stated.
- [ ] 8.4 §2.3's general presence-versus-conformance finding raised with the ledger's owner as its own decision, not closed by FR24.
