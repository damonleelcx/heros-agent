# OpenSpec Conventions

This repository uses **OpenSpec** for spec-driven development. Specs describe behavior in
testable, `SHALL`-based requirements with concrete scenarios. This file is the format contract —
every change and every spec must conform to it.

## Directory layout

```
openspec/
  project.md              # project context (stack, conventions)
  AGENTS.md               # this file — the format contract
  specs/                  # the CURRENT truth: capabilities already built
    <capability>/
      spec.md
  changes/                # PROPOSED changes not yet merged into specs/
    <change-id>/          # one per delivery phase, e.g. p0-foundations
      proposal.md         # why + what changes + impact
      tasks.md            # ordered implementation checklist
      design.md           # technical decisions & trade-offs (for non-trivial changes)
      specs/
        <capability>/
          spec.md         # DELTA: ## ADDED / ## MODIFIED / ## REMOVED Requirements
    archive/              # DEPLOYED changes, already folded into specs/
      <YYYY-MM-DD>-<change-id>/    # date the phase landed; same layout as above
```

A **capability** is a coherent, enduring area of behavior (e.g. `workflow-ir`, `discovery-engine`,
`eval-harness`) — named as a kebab-case noun, not a phase. One phase-change may touch several
capabilities.

### Archiving a deployed change

When a change is deployed, it is **archived** in two steps, and the second is not optional:

1. **Fold** each delta into `specs/<capability>/spec.md` — `ADDED` requirements are appended,
   `MODIFIED` replaces the matching requirement, `REMOVED` deletes it. The folded file drops the
   operation headers (it is truth, not a diff) and its title records the provenance, e.g.
   `# Run Linking — Spec (folded from P11, P29)`.
2. **Move** `changes/<change-id>/` to `changes/archive/<YYYY-MM-DD>-<change-id>/`.

`changes/` therefore answers "what is still open" and `specs/` answers "what is true today". A
change that is finished but still sitting in `changes/` makes both questions unanswerable, which is
the failure this layout exists to prevent.

Within a **folded** spec, a link to another capability is `../<capability>/spec.md` — the live
sibling, never a path into `archive/`. Links to the originating change's own `design.md` /
`proposal.md` / `tasks.md` do point into `archive/`, because that is where the reasoning now lives.

## Change files

### `proposal.md`
```markdown
## Why
<1–3 paragraphs: the problem, and why now. Reference upstream dependencies.>

## What Changes
- <bulleted list of behavior/capability changes; mark **breaking** changes>

## Impact
- Affected capabilities: <list>
- Affected code/systems: <services, stores, UIs>
- Dependencies: <upstream phases that must land first; what this unblocks>
```

### `tasks.md`
Ordered, checkbox implementation tasks grouped by workstream/role. Each is independently
verifiable.
```markdown
## 1. <Workstream / role>
- [ ] 1.1 <task>
- [ ] 1.2 <task>
```

### `design.md` (include for any change with real technical trade-offs)
Context, the decisions taken, alternatives rejected and why, data-model/interface sketches,
risks. Anchor decisions in numbers where possible.

## Spec format (the important part)

Delta spec files under a change use operation headers; the folded `specs/` files omit them.

```markdown
## ADDED Requirements

### Requirement: The system SHALL <single, testable behavior>
<optional clarifying prose>

#### Scenario: <short name>
- **WHEN** <trigger / precondition>
- **THEN** <observable, checkable outcome>
- **AND** <additional outcome, optional>
```

Rules:
- Operation headers are `## ADDED Requirements`, `## MODIFIED Requirements`, `## REMOVED Requirements`.
- Every `### Requirement:` uses **SHALL** (normative) and states exactly one behavior.
- **Every requirement has at least one `#### Scenario:`** — a requirement without a scenario is invalid.
- Scenarios are concrete and observable (an engineer or a test can decide pass/fail). Prefer
  WHEN/THEN; add AND/BUT as needed.
- For `MODIFIED`/`REMOVED`, restate the requirement header exactly as it appears in `specs/` so
  the delta is unambiguous.

## Quality bar

- Requirements are behavioral ("the system SHALL reject a Variant Spec whose node ordering
  violates a typed I/O contract"), not implementation trivia ("use a for-loop").
- Non-functional requirements (latency, scale, security, reproducibility) are first-class
  requirements with their own scenarios, not footnotes.
- Cross-reference the PRD in `../../docs/prd/<phase>.md` for the product rationale behind each spec.
