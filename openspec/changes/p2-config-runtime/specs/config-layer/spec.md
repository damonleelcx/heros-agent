# Configuration Layer — Spec Delta (P2)

Product rationale: [`../../../../../docs/prd/P2-config-runtime.md`](../../../../../docs/prd/P2-config-runtime.md) §6 (FR1–FR5).
Applies the source-transformation apply model per
[ADR-001](../../../../../docs/adr/ADR-001-source-transformation-apply-model.md).

## ADDED Requirements

### Requirement: The system SHALL realize a Variant Spec by generating a source transformation that rewrites each node's overridden dimensions at its discovered call site

The Configuration Layer SHALL treat the Variant Spec as the canonical desired-state config and
realize it by **generating an AST-level source transformation (codemod)** that rewrites the
discovered call sites — the **model** argument, the **prompt** construction, the **skills/tools**
passed, and the **context** assembly — so the hardcoded parameters at each call site match the
Variant Spec's values. It SHALL NOT resolve parameters from a runtime config store; it rewrites the
source.

#### Scenario: Override is realized as a call-site rewrite
- **WHEN** a Variant Spec sets `model_ref` for node `N` to a registry model entry different from
  the IR-captured default
- **THEN** the system generates a transformation whose diff rewrites the model argument at node
  `N`'s call site to the Variant Spec's value
- **AND** running the transformed source executes node `N` using the overridden model

#### Scenario: Absent override leaves the call site unchanged for that dimension
- **WHEN** a Variant Spec entry for node `N` omits `prompt_ref`
- **THEN** the generated transformation makes no edit to node `N`'s prompt construction
- **AND** node `N` runs with its discovered default prompt

### Requirement: Each override dimension SHALL be independently transformable

A node's four dimensions SHALL be settable independently; overriding one dimension SHALL rewrite only
that dimension at the call site and SHALL NOT force re-specification or rewriting of the others.

#### Scenario: Model-only override edits only the model argument
- **WHEN** a Variant Spec sets only `model_ref` for node `N` and omits `prompt_ref`,
  `skill_refs`, and `context_policy`
- **THEN** the generated diff edits only node `N`'s model argument
- **AND** node `N`'s prompt, skills, and context construction are unchanged, and no other node's
  call site is edited

### Requirement: A Variant Spec SHALL be a per-node reference map plus a node ordering, referencing registry entries by immutable ID only

A Variant Spec SHALL have the structure `{node_id → {model_ref, prompt_ref, skill_refs[],
context_policy}}` together with a node ordering/graph and the target `source_revision`. Every `*_ref`
SHALL be an immutable registry version ID; inline definitions SHALL NOT be permitted.

#### Scenario: Spec references entries by version ID
- **WHEN** a Variant Spec is submitted
- **THEN** every `model_ref`, `prompt_ref`, `skill_ref`, and `context_policy` is an immutable
  registry version ID
- **AND** a spec that inlines a model/prompt/skill definition instead of referencing a version ID
  is rejected

### Requirement: A Variant Spec SHALL hash to a stable config_hash that changes iff a referenced version or the ordering changes

The `config_hash` SHALL be derived from a canonical serialization of the Variant Spec that is
invariant to key ordering and serialization whitespace, pinning each `*_ref` to its immutable
version ID and including the node ordering.

#### Scenario: Whitespace and key order do not change the hash
- **WHEN** two syntactically different serializations of the same Variant Spec (differing only in
  key order and whitespace) are hashed
- **THEN** they produce the identical `config_hash`

#### Scenario: Changing a referenced version changes the hash
- **WHEN** a node's `prompt_ref` is changed to a different immutable version ID
- **THEN** the resulting `config_hash` differs from the original
- **AND** changing the node ordering while keeping all refs identical also changes the `config_hash`

### Requirement: The generated transformation SHALL be a deterministic AST-level codemod

Given the same `config_hash` and the same target `source_revision`, the system SHALL generate a
**byte-identical diff**. The transformation SHALL operate on the parsed abstract syntax tree
anchored to the P0 call-site metadata — not on textual/regex substitution — and the generated patch
SHALL be content-hashed and stored so the review artifact is itself reproducible.

#### Scenario: Same config_hash and source produce a byte-identical diff
- **WHEN** the transformation for a given `{config_hash, source_revision}` is generated twice
- **THEN** the two generated diffs are byte-identical
- **AND** the patch's content hash is identical across both generations

#### Scenario: AST anchoring, not text matching
- **WHEN** the discovered model literal string also appears inside a comment or an unrelated string
  literal in the same file
- **THEN** the transformation rewrites only the model argument at node `N`'s call site
- **AND** the comment and the unrelated string literal are left unchanged

### Requirement: A transformation that fails to build the target SHALL be rejected before it is proposed or run

After applying the codemod to the isolated working copy, the system SHALL run the target's
build/compile. If the build fails, the transformation SHALL be **rejected**: it SHALL NOT become a
proposed diff, SHALL NOT be executed, and SHALL surface a typed error naming the node and dimension
whose rewrite failed to build.

#### Scenario: Build-breaking transform is rejected up front
- **WHEN** a generated transformation for node `N` produces a working copy that fails to compile
- **THEN** the transformation is rejected with terminal status `build-rejected`
- **AND** no run is executed and no diff is surfaced for review
- **AND** the error names node `N` and the offending dimension

### Requirement: The transformation SHALL be behavior-preserving except for the intended change

The generated diff SHALL change **only** the configured dimension(s) at the targeted call site(s).
It SHALL NOT reformat untouched code, edit other call sites, or make any incidental change. A
transformation that produces edits outside the targeted call sites SHALL be rejected.

#### Scenario: Diff is minimal and targeted
- **WHEN** a Variant Spec overrides the model of node `N` only
- **THEN** the generated diff's changed lines are confined to node `N`'s call site and touch only
  the model argument
- **AND** a transformation that additionally reformats or edits any unrelated line is rejected

### Requirement: Transformations SHALL be applied only to an isolated working copy, never the user's working tree in place

The system SHALL apply every generated codemod to an isolated git worktree/branch checked out at the
target `source_revision`. It SHALL NOT mutate the user's working tree in place.

#### Scenario: User's working tree is never mutated
- **WHEN** a transformation is generated, applied, and built for a Variant Spec
- **THEN** the edits exist only on an isolated worktree/variant branch
- **AND** the user's original working tree at `source_revision` is byte-for-byte unchanged

### Requirement: Every applied change SHALL be surfaced as a reviewable diff and be cleanly revertible

No change SHALL reach the user's repository except as a diff/PR a human can read. Nothing SHALL merge
to the default branch without passing the build + eval + regression gates and — below the Autonomous
automation level (P6) — without human approval. Every applied change SHALL be revertible as a single
`git revert`.

#### Scenario: Change is delivered as a reviewable diff
- **WHEN** a transformation builds green and passes the verification gates
- **THEN** it is surfaced as a reviewable diff/PR against the target repository
- **AND** below the Autonomous level it is not merged to the default branch without human approval

#### Scenario: Applied change reverts cleanly
- **WHEN** an applied change is rolled back
- **THEN** a single `git revert` of the variant commit restores the prior source exactly
- **AND** no residual edits remain in the repository

### Requirement: The system SHALL reject an invalid Variant Spec before any transformation is generated

A Variant Spec that references a non-existent node, a `*_ref` that does not resolve to a registry
entry, an unregistered `context_policy`, or a discovered call site the transform cannot rewrite
safely SHALL be rejected before any transformation is generated, applied, built, or run, with no
side effects.

#### Scenario: Unregistered context policy rejected up front
- **WHEN** a Variant Spec sets `context_policy` for node `N` to a name that is not registered
- **THEN** the spec is rejected before any transformation is generated
- **AND** no diff, worktree, run record, or provider call is created
- **AND** the rejection names node `N` and the offending dimension
