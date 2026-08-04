---
title: CLI reference
tier: reference
summary: Every subcommand of the heros binary, its flags, its exit code, and whether it runs offline.
platform_version: 0.21.0
boundary: This reference is generated from the command registry. It describes what the binary accepts; it does not describe what the platform does with a linked run.
generated: true
order: 1
---

This page is **generated** from the command registry in `internal/cli`, on every build. Editing it by hand has no lasting effect — the next build overwrites it. Change the registry instead, and the reference follows.

Every command below exists in the binary today. A command in the registry with no entry here fails the build, and an invocation anywhere in this documentation naming a command or flag the registry does not have fails the build too. The reference and the binary cannot drift apart in either direction.

## Exit codes

The exit codes are a **public contract**: they are public the moment a customer's pipeline branches on them. Three remedies never share a code.

| Code | Name | Means | Your remedy |
|---|---|---|---|
| `0` | ok | the command did what it was asked; no gate failed and nothing broke. | nothing. This is success. |
| `1` | configured-gate-failed | a quality gate YOU configured failed — for example --min-quality was not met. | fix the regression, or change the gate. This is not a tool failure, and retrying will not change it. |
| `2` | operational-error | the tool broke, or a platform-facing command could not reach the platform. | retry, check connectivity, or file a bug. Your workflow is not necessarily worse than it was. |
| `3` | invalid-config | the invocation is malformed: a missing required input, an unreadable config file, a flag out of range. | fix the invocation. The message names the input that was missing rather than reporting a generic flag error. |

The gap between `1` and `2` is the load-bearing one. **`1` is your gate failing** — the tool worked and told you something true about your workflow, and retrying will not change it. **`2` is our tool breaking** — retrying might. Collapsing them into one code is how a CI step that fails for an unclear reason ends up with `|| true` appended to it, after which a real regression is invisible too.

## Configuration and which source wins

Every setting can come from four places. They resolve in this order, highest first: **flag > env > file > default**.

| Source | How you set it | Notes |
|---|---|---|
| flag | `--repo .` | Highest. Only a flag you actually pass counts — a flag left at its default does not shadow the others. |
| env | `HEROS_REPO=.` | Environment variables are namespaced `HEROS_`. |
| file | `.heros.json` | A project file resolved relative to the repository. |
| default | — | The built-in value. `heros status` names the source that won for every setting. |

Run `heros status` to see the resolved value **and its provenance** for every setting, including which sources were overridden. That answers "why is my config file being ignored" without guessing.

The three gate flags — `--min-quality`, `--max-cost-per-run`, `--latency-sla-ms` — deliberately have **no environment equivalent**. A quality gate that a stray environment variable can relax is a gate that silently stops gating, and the symptom is a green build.

## Commands

### apply

realize a Variant Spec as a reviewable diff, in an isolated worktree.

**Runs offline, with no account.**

**Before this runs:** a Variant Spec JSON at --spec — see the schema reference for its shape.

```bash
heros apply --repo . --spec variant.json --out change.diff
```

**On success:** a unified diff at --out. Your working tree is untouched — the change is realized in a worktree. Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--commit` | string | *unset* | `HEROS_COMMIT` | source revision (default: derived from .git) |
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--out` | path | *unset* | `HEROS_OUT` | output path (IR for discover, diff for apply) |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--repo-url` | string | *unset* | `HEROS_REPO_URL` | workflow repo url (default: derived from .git) |
| `--spec` | path | *unset* | `HEROS_SPEC` | path to a Variant Spec JSON |
| `--workflow-id` | string | *unset* | `HEROS_WORKFLOW_ID` | workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow) |

### author

make a change yourself: preflight it, and with --apply write its diff.

**Runs offline, with no account.**

**Before this runs:** a Variant Spec JSON at --spec. `author` changes an existing spec; it does not create the first one.

```bash
heros author --repo . --spec variant.json --node n_triage --model anthropic/claude-sonnet-5
```

**On success:** one of three verdicts — admissible, refused by name, or not yet measurable. Without --apply it writes nothing. Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--apply` | bool | `false` | `HEROS_APPLY` | write the diff; without it, author previews and writes nothing |
| `--apply-mode` | string | *unset* | `HEROS_APPLY_MODE` | inline \| bound |
| `--clear-drop-tolerance` | bool | `false` | `HEROS_CLEAR_DROP_TOLERANCE` | remove a declared drop tolerance — NOT the same as 0 |
| `--commit` | string | *unset* | `HEROS_COMMIT` | source revision (default: derived from .git) |
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--context-policy` | string | *unset* | `HEROS_CONTEXT_POLICY` | set the node's context policy ref |
| `--drop-tolerance` | float | *unset* | `HEROS_DROP_TOLERANCE` | declare the node's context drop tolerance, 0..1 |
| `--model` | string | *unset* | `HEROS_MODEL` | set the node's model ref |
| `--node` | string | *unset* | `HEROS_NODE` | node id to change |
| `--out` | path | *unset* | `HEROS_OUT` | output path (IR for discover, diff for apply) |
| `--prompt` | string | *unset* | `HEROS_PROMPT` | set the node's prompt version ref |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--repo-url` | string | *unset* | `HEROS_REPO_URL` | workflow repo url (default: derived from .git) |
| `--skills` | string | *unset* | `HEROS_SKILLS` | bound skills, comma-separated, in order |
| `--spec` | path | *unset* | `HEROS_SPEC` | path to a Variant Spec JSON |
| `--tools` | string | *unset* | `HEROS_TOOLS` | keep only these discovered tools, comma-separated |
| `--workflow-id` | string | *unset* | `HEROS_WORKFLOW_ID` | workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow) |

### coverage

show what this build can apply, per axis and language.

**Runs offline, with no account.**

```bash
heros coverage
```

**On success:** a matrix of axis by language; every registered language appears on every axis, and a gap names what is missing Exit code `0`.

### discover

extract the Workflow IR and discovery report from a repository.

**Runs offline, with no account.**

```bash
heros discover --repo . --out ir.json --report discovery.json
```

**On success:** the IR at --out and the report at --report, and a summary naming how many call sites were found Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--commit` | string | *unset* | `HEROS_COMMIT` | source revision (default: derived from .git) |
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--out` | path | *unset* | `HEROS_OUT` | output path (IR for discover, diff for apply) |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--repo-url` | string | *unset* | `HEROS_REPO_URL` | workflow repo url (default: derived from .git) |
| `--report` | path | *unset* | `HEROS_REPORT` | discovery report output path |
| `--workflow-id` | string | *unset* | `HEROS_WORKFLOW_ID` | workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow) |

### doctor

check this machine is ready, and name the ONE next action for each gap.

**Runs offline, with no account.**

```bash
heros doctor
```

**On success:** one line per check, and for each gap the single next action that closes it Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |

### eval

run a scored, multi-seed evaluation with your own provider keys.

**Runs offline, with no account.**

```bash
heros eval --repo . --seeds 5 --cases 8
```

**On success:** a scorecard with a confidence interval per metric. A gate you configured that fails exits 1, not 0. Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--cases` | int | `8` | `HEROS_CASES` | evaluation cases |
| `--commit` | string | *unset* | `HEROS_COMMIT` | source revision (default: derived from .git) |
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--latency-sla-ms` | float | `unset` | *none* | fail if latency exceeds this (ms) |
| `--max-cost-per-run` | float | `unset` | *none* | fail if cost per run exceeds this (USD) |
| `--min-quality` | float | `unset` | *none* | fail if quality is below this (0..1) |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--repo-url` | string | *unset* | `HEROS_REPO_URL` | workflow repo url (default: derived from .git) |
| `--seeds` | int | `5` | `HEROS_SEEDS` | evaluation seeds |
| `--workflow-id` | string | *unset* | `HEROS_WORKFLOW_ID` | workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow) |

### help

print the command surface and the exit-code contract.

**Runs offline, with no account.**

```bash
heros help
```

**On success:** the usage text, listing every command and the four exit codes Exit code `0`.

### init

write a starter llm-eval.yaml whose defaults already work.

**Runs offline, with no account.**

```bash
heros init --repo .
```

**On success:** a new llm-eval.yaml, and a line naming the file it wrote. It never clobbers an existing one without --force. Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--force` | bool | `false` | `HEROS_FORCE` | overwrite an existing config |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |

### link

transmit a run's allowlisted metrics and structure to the platform.

**Needs the network and a platform account.**

```bash
heros link --run run-7 --dry-run
```

**On success:** with --dry-run, the exact payload printed and nothing transmitted. Without it, the run URL the platform stored. Exit code `0`.

**When this build does not include it:** the command exits `2` and prints `link is a platform command and is unavailable in this build`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--dry-run` | bool | `false` | `HEROS_DRY_RUN` | show exactly what would be transmitted, and transmit nothing (link: the payload itself; push-source: the snapshot's revision, file count and size) |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--run` | string | *unset* | `HEROS_RUN` | run id to link |
| `--with-ir` | path | *unset* | `HEROS_WITH_IR` | ALSO transmit this workflow's structure (symbols, files, line spans, models, tool counts) as a second, opt-in payload — never prompt text or source (link) |

### login

store a platform token.

**Needs the network and a platform account.**

```bash
heros login --token <your platform token>
```

**On success:** the token stored, and the non-secret identity it resolves to echoed back. The token value is never printed. Exit code `0`.

**When this build does not include it:** the command exits `2` and prints `login is a platform command and is unavailable in this build`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--token` | string | *unset* | `HEROS_TOKEN` | platform token (login) |

### push-source

send a source snapshot so the platform can discover and classify this workflow itself.

**Needs the network and a platform account.**

**Before this runs:** a git repository with at least one commit — the snapshot is `git archive` of a revision, never the working directory

```bash
heros push-source --repo . --dry-run
```

**On success:** with --dry-run, what the snapshot contains and its size, and nothing transmitted. Without it, the snapshot pushed and the classified graph the platform discovered from it. Exit code `0`.

**When this build does not include it:** the command exits `2` and prints `push-source is a platform command and is unavailable in this build`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--commit` | string | *unset* | `HEROS_COMMIT` | source revision (default: derived from .git) |
| `--dry-run` | bool | `false` | `HEROS_DRY_RUN` | show exactly what would be transmitted, and transmit nothing (link: the payload itself; push-source: the snapshot's revision, file count and size) |
| `--forget` | bool | `false` | `HEROS_FORGET` | DELETE a previously pushed source snapshot from the platform instead of sending one (push-source) |
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |
| `--workflow-id` | string | *unset* | `HEROS_WORKFLOW_ID` | workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow) |

### report-verdict

report a verification verdict your CI measured for a proposal the platform issued.

**Needs the network and a platform account.**

**Before this runs:** a verdict file from your own verification run, and a proposal id the platform issued for your tenant

```bash
heros report-verdict --proposal prop-abc --from verdict.json --dry-run
```

**On success:** with --dry-run, the exact payload and the account of what stayed local, and nothing transmitted. Without it, the verdict recorded and the gate result the platform stored. Exit code `0`.

**When this build does not include it:** the command exits `2` and prints `report-verdict is a platform command and is unavailable in this build`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--dry-run` | bool | `false` | `HEROS_DRY_RUN` | show exactly what would be transmitted, and transmit nothing (link: the payload itself; push-source: the snapshot's revision, file count and size) |
| `--from` | string | *unset* | `HEROS_FROM` | path to the verdict your verification run produced, or `-` for stdin (report-verdict). Its case ids and free-text reason are NOT transmitted |
| `--proposal` | string | *unset* | `HEROS_PROPOSAL` | the platform-issued proposal id a verdict measures (report-verdict). Taken from the flag, never from the verdict file: where the two disagree the command refuses rather than attaching a measurement to the wrong change |
| `--revision` | string | *unset* | `HEROS_REVISION` | the commit the verification ran at (report-verdict) — a revision id, never the code at it |

### status

show effective config with provenance, and the fixed contract facts.

**Runs offline, with no account.**

```bash
heros status --repo .
```

**On success:** each setting with the source that won — flag, env, file or default — and which sources were overridden Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--repo` | path | `.` | `HEROS_REPO` | the target repository |

### upgrade

fetch the latest release, verify it, and replace this binary in place.

**Needs the network. Needs no account.**

```bash
heros upgrade
```

**On success:** a no-op when already current. When a package manager owns this file it defers and prints that manager's command rather than overwriting it. Exit code `0`.

**When this build does not include it:** the command exits `2` and prints `upgrade needs the network and is unavailable in this build; reinstall with the install script instead`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.

### verify-release

verify a downloaded release: checksums, then the signature over the manifest.

**Runs offline, with no account.**

**Before this runs:** the release's SHA256SUMS and SHA256SUMS.sig, downloaded next to the asset — the install page gets them.

```bash
heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig
```

**On success:** one line per asset confirming its checksum, then the manifest signature verified against the release key compiled into this binary. Any failure is a hard stop. Exit code `0`.

| Flag | Type | Default | Environment | Meaning |
|---|---|---|---|---|
| `--asset` | string | *unset* | `HEROS_ASSET` | comma-separated asset names to check |
| `--config` | path | *unset* | `HEROS_CONFIG` | path to llm-eval.yaml |
| `--manifest` | path | *unset* | `HEROS_MANIFEST` | path to a downloaded SHA256SUMS |
| `--sig` | path | *unset* | `HEROS_SIG` | detached signature; defaults to the manifest path with .sig appended |

### version

print the CLI version and the platform contract version it implements.

**Runs offline, with no account.**

```bash
heros version
```

**On success:** the tool version and the contract version, one per line Exit code `0`.

## What this reference does not cover

It describes the **command surface** — what the binary accepts and what it returns. It does not describe the platform's behaviour once a run is linked, and it does not explain when you would use each command; that is what the guides are for.

It also does not promise that every summary here is a good description. The registry is the source, a test asserts each entry has a runnable example and a stated success criterion, and whether that example is the *right* example is a review judgement no generator can make.

