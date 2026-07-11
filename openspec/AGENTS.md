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
```

A **capability** is a coherent, enduring area of behavior (e.g. `workflow-ir`, `discovery-engine`,
`eval-harness`) — named as a kebab-case noun, not a phase. One phase-change may touch several
capabilities. When a change is deployed, its delta specs are folded into `specs/`.

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
