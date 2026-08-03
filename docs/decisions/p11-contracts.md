# P11 Contracts — allowlist, machine output, exit codes, and the open-question resolutions

This document is the **referenceable contract** for P11 (tasks 1.1–1.7, 11.2). It is deliberately not
prose scattered through the CLI: each contract below has exactly one machine source of truth in Go, and
this file explains and renders it. A customer's security reviewer reads §1; a customer's pipeline
branches on §2 and §3. The moment either happens, these are public contracts — so they are decided
here, versioned, and changed in one place or not at all.

| Contract | Machine source of truth (single) | Consumed by |
|---|---|---|
| Egress allowlist (§1) | [`internal/runlink/allowlist.go`](../../internal/runlink/allowlist.go) `Allowlist` | payload builder, egress test, this doc |
| Machine output format (§2) | [`internal/cli/output.go`](../../internal/cli/output.go) `Envelope`, `OutputContractVersion` | every command's stdout, CI parser |
| Exit codes (§3) | [`internal/cli/exit.go`](../../internal/cli/exit.go) `ExitOK`…`ExitInvalidCfg` | every command, the CI action |
| Link endpoint pin (§4) | [`internal/runlink/allowlist.go`](../../internal/runlink/allowlist.go) `PlatformBaseURL` | `link`, `login`, the client |

---

## 1. The egress allowlist (task 1.1)

The linked payload is **constructed** from this list field by field — never produced by serializing a
run object and stripping sensitive fields. A field added to an internal run struct is **absent** from a
transmitted payload by default; its absence is the default outcome, not the result of an exclusion
rule (PRD FR11, design Decision 3). The list is ratified here as a security-review artifact.

**Permitted — structure, metrics, provenance, scores, run metadata:**

| Wire key | Category | Why it may cross |
|---|---|---|
| `metrics.cost` | metrics | provider spend in the customer's own unit — the input SUM is derived from; no markup |
| `metrics.latency` | metrics | per-node/aggregate wall time, for comparison |
| `metrics.tokens` | metrics | token **counts**, never the tokens |
| `ir_structure.node_ids` | ir_structure | that a node exists — not what its prompt said |
| `ir_structure.edges` | ir_structure | the workflow's shape, so the console can draw the graph |
| `ir_structure.model_refs` | ir_structure | which model a node used, not what it was asked |
| `ir_structure.pattern_labels` | ir_structure | P3.5 shape tags, no source text |
| `config_hash` | provenance | a determinism anchor, not the config's contents |
| `source_revision` | provenance | a commit id, not the code |
| `scores.value` | scores | the P4 harness score — a number, not the eval-set behind it |
| `scores.ci_low` / `scores.ci_high` | scores | the confidence interval, so the console never shows a bare point estimate |
| `run_metadata.run_id` | run_metadata | run identity — the idempotency key and what the returned URL resolves to |
| `run_metadata.workflow_id` | run_metadata | which workflow the run belongs to |
| `run_metadata.timestamp` | run_metadata | the period the linked event lands in |
| `run_metadata.seed` | run_metadata | a reproducibility number |
| `run_metadata.tool_version` | run_metadata | the CLI version, for the support window (NFR9) |
| `runs_reported` | run_metadata | the **coverage denominator** (see §7) — a count, never the runs it counts |
| `metrics.per_node` | metrics | the same cost/latency/token quantities **attributed to a node id**. Both halves already cross; this is the join, and it is what the scorecard exists to show |
| `eval.case_count` | eval | how many eval cases a score is computed over — the board's denominator. A count, **never the cases** |
| `eval.seed_count` | eval | how many seeds ran. The seed *list* already crosses under `run_metadata.seed`; this is its length |
| `eval.gate_outcome` | eval | `pass` / `fail` / `not-configured` — the verdict your own CLI already printed on your terminal |
| `eval.gate_failures` | eval | which **metric names** failed the gate. Names only; metric names already cross under `scores.metric` |
| `eval.single_seed` | eval | the provisional caveat, travelling with the number it qualifies |

> **The `eval` group is a deliberate widening, added when the eval board and scorecard were built.**
> Those two surfaces render the *evidence that qualifies* a score — how many cases it is over, whether
> your gate passed, which node the cost came from. The platform previously held the claim and none of
> the evidence, so the only ways to mount them were to **invent** a gate outcome or to leave them
> unmounted. They were left unmounted. This group is the other fix: send the evidence, on purpose,
> named, each field with its justification above.
>
> **Your gate THRESHOLDS do not cross.** The verdict does; the policy that produced it is yours. Nor do
> the eval cases, expected answers, judge prompts, or model outputs — a case *count* says how much
> evidence there was and carries none of it.

**Never permitted, and not expressible as a field at all:** prompt text · source code · file contents ·
generated diffs · environment-variable values · provider credentials. These are *content*; the
dashboard's job is *comparison*, which structure and metrics fully satisfy (Decision 4). This holds on
**every** path — success, failure, error reporting, diagnostics, and the highest verbosity (FR13,
NFR10): a verbose flag cannot add a field, because there is no path that adds a field except editing
`Allowlist`.

The guarantee is enforced by a test that **adds a sensitive field to the source struct and asserts it is
absent from a transmitted payload** ([`internal/runlink/egress_test.go`](../../internal/runlink/egress_test.go)).
If that test cannot be made to fail, the guarantee is decoration (design Risk table).

## 2. Machine output format and its version (task 1.2)

Every command writes **one** JSON document to **stdout**; every word a human reads goes to **stderr**.
The stdout document is the `Envelope`:

```json
{
  "contract_version": "p11.cli.v1",
  "command": "eval",
  "ok": true,
  "exit_code": 0,
  "data": { "…command-specific…": "…" },
  "gate": { "name": "quality-floor", "passed": true },
  "error": null
}
```

- `contract_version` changes when the **shape** changes, never for content. A consumer detects a format
  change rather than misparsing it (FR4).
- `ok` mirrors the success axis of the exit code; `exit_code` echoes the process code into the document
  so a consumer that captured stdout but not `$?` still has it.
- `data` is command-specific and never carries narration. `gate` names a customer-configured gate
  outcome. `error` is a content-free machine failure summary that obeys the same allowlist as
  everything else.

Narration split rationale: a CI job consumes stdout without parsing prose, and a developer reads stderr
without their pipeline seeing it (Decision 9).

## 3. The exit-code table (task 1.3)

Three remedies must never share a code.

| Code | Name | Meaning | Customer's remedy |
|---|---|---|---|
| `0` | success | did what was asked; no gate failed, nothing broke | — |
| `1` | configured-gate-failed | a quality gate **the customer configured** failed | fix the regression, or change the gate |
| `2` | operational-error | the tool broke, or a platform-facing command could not reach the platform | retry, check connectivity, file a bug |
| `3` | invalid-config | malformed invocation — missing required input, unreadable config, flag out of range | fix the invocation |

The 1↔2 gap is load-bearing: "your gate failed" and "our tool broke" have opposite remedies, and a CI
step that fails for an unclear reason gets disabled, after which the check protects nothing.

## 4. The link endpoint is pinned to `https://heros-agent.space` (constraint)

Run linking transmits to **`https://heros-agent.space` and nowhere else**. This is a hard pin in the
security-critical package (`runlink.PlatformBaseURL`), not a flag with a default an environment variable
can move. The client refuses to transmit a payload to any other origin (`runlink.IsLinkTarget`). A
review promise — "you can read exactly where this goes" — is only checkable if the destination is fixed
and named. `discover`, `apply`, `eval` and `status` never contact it; only `login` and `link` do, and
only under an authenticated identity.

---

## Open-question resolutions (PRD §14 → ratified here, tasks 1.4–1.7)

### Q1 — First-class forge (task 8.1)
**GitHub first.** It has the largest reach and the best-defined ephemeral token (`GITHUB_TOKEN`),
which is what makes build-safety and secret-isolation testable. GitLab and Bitbucket ship as
**documented invocations** of the same binary at M14 — sufficient for the commercial claim because the
binary, the exit codes, and the output format are forge-agnostic; only the wrapper differs.

### Q2 — Where local cost events live before linking (task 1.4)
**On disk, under a run store the customer controls.** `eval` writes each run's allowlist-shaped record
to `.heros/runs/<run_id>.json` in the working directory (git-ignored). Rationale: a file makes
**retroactive linking** and **CI retries** possible — the two workflows the PRD's personas actually
need — and keeps `link` a pure reader of a prior `eval`. The cost of the alternative (in-memory only) is
that a crashed or retried CI job loses its run and cannot link it. The on-disk data is **already
allowlist-shaped** — it contains only what `link` may transmit, never prompts, source, or keys — so "the
data at rest is the data on the wire" is true, and a customer inspecting the file sees exactly the
egress surface. The store lives in the customer's environment and is never read except by an explicit
`link`.

### Q3 — May `apply` write to the working tree (task 1.5)
**No — strict worktree isolation, always.** `apply` realizes the Variant Spec against an isolated
working copy (`internal/worktree`, ADR-001) and emits a **reviewable diff**; it never mutates the
caller's working tree in place. Developers will ask for in-place application; the answer is "apply the
emitted diff yourself with `git apply`", which keeps the destructive act in the developer's hands and
the tool's guarantee ("it did not touch my branch") absolute. In-place application is explicitly **not**
shipped at M14.

### Q4 — Authentication mechanism for `login` (task 1.6)
**Token path ships at M14; device-code is deferred.** CI needs a token regardless (a headless runner
cannot complete a device-code flow), so the token path is the one that must exist. `login` reads the
platform token from `--token`, `$HEROS_PLATFORM_TOKEN`, or stdin, validates it against
`https://heros-agent.space`, and stores it under the customer's config dir with `0600` permissions.
Device-code is a later UX uplift, not an M14 blocker, and shipping only the token path keeps the auth
surface small for the first security review.

### Q5 — Link-coverage denominator (task 1.7)
**The CLI reports a `runs_reported` count as a single allowlisted integer.** "Runs the platform knows
about" is circular — the platform only knows about linked runs — so the denominator has to come from the
CLI. It is a **count**, never a list of the runs it counts: a list would be a second egress surface
needing its own scrutiny, whereas a single non-negative integer discloses nothing but "how many runs
happened this session". Coverage is then `runs_linked / runs_reported`, and `runs_reported == 0` is
rendered as **unknown** coverage, distinct from **complete** (FR17). This field went through the same
allowlist review as every other and appears in `Allowlist` as `runs_reported`.

### Q6 — Retroactive linking (deferred, noted)
Retroactive linking is **supported within an open billing period only**. Because cost events land in
P2.5 keyed by their own timestamp, a run linked after its period closed must not reopen a closed meter;
the ingest endpoint rejects a linked event whose timestamp falls in a closed period and reports it
distinctly. The full retroactive-window policy is a P7 metering concern and is tracked there; P11 ships
the open-period case, which is what CI retries and same-session "link the one worth keeping" need.
