# ADR-001 — Apply optimizations by transforming source code, not by a runtime shim

- **Status:** Accepted (2026-07-11)
- **Deciders:** Product + System Design
- **Supersedes:** the "adapter/shim resolves config at runtime without editing source" mechanism in the original source plan (§3 Configuration Layer, §4 Runtime) and its downstream PRDs/specs.

## Context

The original plan proposed that discovered LLM call sites be **wrapped by a shim/adapter** that
resolves per-node parameters (model, prompt, skills, context) from a config store *at runtime*,
"because the system can't edit arbitrary source safely." This was chosen to avoid touching the
user's code.

Two problems made this the weakest link in the design:

1. **It is infeasible for compiled languages.** You cannot intercept `anthropic.messages.create`
   at runtime in a compiled Go binary — there is no monkey-patch seam. Go is the P1 discovery
   target, so the primary mechanism failed on the primary language.
2. **Runtime interception measures the wrong thing.** A shimmed run does not exercise the code that
   will actually ship, so measured cost/latency/quality can diverge from production.

## Decision

**The system applies a configuration by transforming the user's source code, and delivers every
change as a reviewable diff / pull request.**

- A **Variant Spec** remains the canonical desired-state config (`{node_id → {model_ref,
  prompt_ref, skill_refs[], context_policy}}` + graph). "Applying" a Variant Spec means
  **generating an AST-level source transformation (codemod)** that rewrites the discovered call
  sites — the model argument, the prompt construction, the tools/skills passed, the context
  assembly, or the node wiring — to match the spec.
- Transformations are **deterministic and AST-level** (not string substitution): the same
  `config_hash` against the same source produces the same diff, content-hashed for reproducibility.
- Transformations are applied to an **isolated working copy** (git worktree/branch), never the
  user's working tree in place. The Runtime **executes the transformed copy** in a sandbox, so every
  measurement reflects the code that would actually ship.
- Every applied change is a **reviewable diff**, delivered as a **patch or pull request** against
  the user's repository. Git history is the audit trail; `git revert` is rollback.
- This works on any language the Discovery Engine can parse (Go included), because it rewrites
  source rather than intercepting a runtime.

This reframes the product's applied output as **automated, verified optimization pull requests** —
the mental model developers already trust from Dependabot / Renovate / coding agents — not
zero-code-change "magic."

## New requirements this introduces (must appear in the specs)

Editing user code is high blast radius. The following become first-class, testable requirements:

1. **AST-level, deterministic transforms.** Same `config_hash` + same source → byte-identical diff.
2. **Build-preserving.** A transform that fails to compile/build the target is rejected before it
   is ever proposed.
3. **Behavior-preserving except for the intended change.** The diff changes only the configured
   dimension(s) at the targeted call site(s); no incidental edits.
4. **Isolated application.** Transforms apply to a worktree/branch; the user's working tree is never
   mutated in place.
5. **Always reviewable.** No change reaches the user's repository except as a diff/PR a human can
   read. Nothing merges to the default branch without passing the verification gates (build + eval +
   regression) and — below the Autonomous automation level — without human approval.
6. **Clean rollback.** Every applied change is revertible as a single git revert.

## Consequences

**Positive**
- Resolves the compiled-language feasibility hole; Go-first is now coherent.
- Measurements are faithful — the eval harness runs the real, transformed code.
- PR-native delivery fits developer workflow; git provides audit + rollback for free.
- Stronger, more familiar positioning ("verified optimization PRs," not zero-touch magic).

**Negative / new risk surface**
- **Transform correctness** replaces runtime-interception fragility as the top risk. A bad codemod
  can break a build or subtly change behavior — mitigated by requirements 2–3 and the build+eval
  verification gate before any proposal is surfaced.
- **Review burden** on the user for every change — mitigated by ranking, evidence-attached diffs,
  and batching.
- The Runtime must manage **working copies and builds per variant** (worktree pool, build cache),
  which is heavier than a runtime shim but far more accurate.

## Terminology mapping (apply consistently across all docs)

| Old framing (remove) | New framing (use) |
|---|---|
| "shim / adapter layer" wrapping call sites | "source transformation engine (codemod)" that rewrites call sites |
| "the system can't edit arbitrary source, so call sites are wrapped" | "the system rewrites the call sites via a deterministic AST transformation, delivered as a reviewable diff" |
| "resolve parameters from a config store at runtime rather than from hardcoded values" | "rewrite the hardcoded parameters at the call site to the Variant Spec's values" |
| "without editing / without regenerating source" | "by generating and applying a reviewable source change (patch/PR)" |
| "zero-touch" (no code changes) | "no *manual* code changes — the system generates the changes as reviewable PRs" |
| executor "walks the graph through the shim" | executor "runs the transformed working copy in an isolated sandbox" |
| P6 "apply" a change | open (and, under Autonomous, merge) a PR; rollback = `git revert`; audit = git history |

The **provider gateway** (LiteLLM-style) is unaffected — it still abstracts providers at execution
time. Only the *config-application mechanism* changes: from runtime shim to source transformation.
