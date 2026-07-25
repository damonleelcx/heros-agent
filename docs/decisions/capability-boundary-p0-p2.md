# Capability Boundary — what we may promise after P0–P2

| Field | Value |
|---|---|
| Phase coverage | P0 (foundations), P1 (discovery), P2 (config + runtime) |
| Date | 2026-07-17 |
| Owner | Sales Operations (statements) · System Design (boundaries) |
| Cross-refs | [`m2-exit-review.md`](m2-exit-review.md) · [`ADR-001`](../adr/ADR-001-source-transformation-apply-model.md) · [`ADR-002`](../adr/ADR-002-provider-gateway-serves-platform-callers.md) · [`p1 tasks`](../../openspec/changes/p1-discovery-mvp/tasks.md) · [`p2 tasks`](../../openspec/changes/p2-config-runtime/tasks.md) |

> 🔴 **Every sentence here is a contract.** A customer takes these statements to their own engineers.
> A promise that does not survive that conversation is not a communication problem at delivery time —
> it is a breach.
>
> **Maturity tiers.** Only ✅ may be stated to a customer. 🟡 / 🧪 are internal planning states and
> **must not be promised**. ⛔ is a stated limit we **volunteer** rather than wait to be asked about.
>
> | Tier | Meaning | External? |
> |---|---|---|
> | ✅ Delivered | Shipped in the current build, test-backed | 🟢 May be stated |
> | 🟡 Evolving | Implemented but narrow or still being hardened | 🔴 Internal only |
> | 🧪 Reserved | The seam exists; the capability does not | 🚫 Never promise |
> | ⛔ Limit | We deliberately cannot / will not do this | 🚫 Never promise — **proactively disclose** |

---

## 1. Discovery — "what LLM calls does my codebase make?"

| Capability | Tier | The honest statement |
|---|---|---|
| Find LLM call sites without running your code | ✅ | Static analysis only. Enforced two ways: a structural guard fails our build if the analysis path imports `os/exec`/`plugin`/`net`, and a container posture (`network_mode: none`, read-only repo mount, no ambient credentials) that is **proven by a test which we also prove can fail**. |
| Go, Python | ✅ | Fully supported: real extraction, framework readers, wrapper-via-declaration. |
| TypeScript, Rust, Java, Kotlin | ✅ | Real tree-sitter parsing, real registry rows, schema-valid IR in CI, six fixture kinds each (where applicable). |
| JavaScript | 🟡 | A frontend and 6 registry rows are registered — **but zero fixtures.** Support is asserted, never demonstrated. **Do not state it.** |
| Declarative graph reading (LangGraph / CrewAI) | ✅ | Go + Python + TypeScript. |
| Declarative graph reading for Rust / Java / Kotlin | ⛔ | Those ecosystems are request/response SDKs and imperative builders — there is no statically-readable node/edge graph to read. Not a roadmap item; a property of those ecosystems. |
| C#, Ruby, PHP, Swift, Scala | ⛔ | No frontend. We report the file as `LANGUAGE_UNSUPPORTED` rather than silently finding nothing — but we do not support it. Available **on demand**, not shipped. |
| Wrappers around an SDK | ✅ | Found **when you declare the entrypoint** in `llm-eval.yaml`. Undeclared in-house wrappers are invisible — say this plainly; it sets the setup expectation correctly. |
| Loop / agent node counts | ✅ | Flagged `variable_at_runtime`. **We never emit a fixed runtime count for a variable node** — a number we cannot stand behind is worse than an honest flag. |

---

## 2. Configuration & apply — "change my workflow and prove it helped"

| Capability | Tier | The honest statement |
|---|---|---|
| Apply a change as a **reviewable diff on an isolated branch** | ✅ | Your working tree is never touched (asserted by tree hash, not `git status`). Rollback is a single `git revert`. |
| **Open a pull request** | 🧪 | ⛔ **Do not promise this.** The diff, the branch and the revert exist; **the PR does not.** There is no push path to your remote at all. Positioning that leans on "automated optimization PRs" is **ahead of the build.** |
| **Model override within a provider** (e.g. Sonnet → Opus) | ✅ | Minimal targeted diff, deterministic, build-verified before it ever runs. |
| **Prompt override** | ✅ | Rewrites the prompt inside your existing SDK construction; your message list, role helpers and SDK types are untouched. Template variables bind to your call site's own expressions — **verified against your code, never guessed.** **Narrowed by P10:** explicit `bindings` now also let a slot bind to a `literal`, an `env` variable, or a typed `input` — not only a call-site expression. The binding kind is stated explicitly and validated at spec-resolve (node/dimension/slot named on failure); an unclaimed call-site value is still refused. See [`openspec/changes/p10-prompt-model-studio/specs/variable-bindings/spec.md`](../../openspec/changes/p10-prompt-model-studio/specs/variable-bindings/spec.md). |
| Prompt override at *every* call site | 🟡 | We rewrite when the prompt argument holds exactly one string expression — which covers normal single-turn SDK calls. **Multi-turn message lists are refused, not guessed.** State the refusal as a feature: we decline rather than emit a diff we cannot guarantee. |
| **Cross-provider swap** (Anthropic → OpenAI at your call site) | ⛔ | **Not shipped, and volunteer this early** — it is the single most likely thing to be assumed. Swapping providers means rewriting the SDK call itself: different client, request shape, response type. We swap models *within* a provider today. Per [ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md), routing your code through our gateway to fake this is something we **will not** do — it would either put our uptime inside your production path or make our measurements describe code you never ship. |
| **Skill / tool configuration** | 🧪 | The registry, schema validation and gateway plumbing exist; **the codemod refuses to bind skills at your call site.** Not configurable in this build. |
| **Context-policy configuration** | 🧪 | `full` only. Real policies are P3's. |
| A transform that breaks your build | ✅ | Rejected **before it is ever proposed or run**, with a typed error naming the node and dimension. |
| Reproducibility | ✅ | Same `config_hash` + `source_revision` + `seed` → byte-identical diff, deterministic build, identical seed at every provider call. |
| Never double-charged on a retry | ✅ | Idempotency key per node invocation + a DB primary key making a double-write a caught conflict, not a duplicate row. Backed by a control test proving that *without* the key the same retry really would bill three times. |
| Provider credential handling | ✅ | Sourced at call time, never persisted, scrubbed from logs/errors/diffs/rows. Proven by a sweep of **every text column** in the database — plus a planted-secret control proving the sweep is looking where it claims. |
| **AWS Secrets Manager** | 🟡 | Integration is real and code-complete; **never executed against a live AWS account.** Unproven: real IAM policy, real ARN resolution, real KMS decrypt, real endpoint TLS. Say "supported, pending validation in your environment" — **not** "we run on Secrets Manager today." |
| Vault / GCP Secret Manager | 🧪 | The seam accepts them. Neither is written. |
| **Running a submitted variant to completion** | ⛔ | **Volunteer this.** Submit → resolve → transform → build → enqueue is complete. **Nothing consumes the queue — no worker exists.** A submitted run sits pending indefinitely. The end-to-end path is proven in tests; it is **not** proven by a deployed worker, because there isn't one. |

## 2b. Which languages can we *change*, not just *find* — updated 2026-07-17

> 🔴 **This is the distinction most likely to be over-promised, because "we support six languages" is
> true of Discovery and false of Apply.** Finding a call site and rewriting it are different products.
> See [ADR-003](../adr/ADR-003-multi-language-apply-and-verification-strength.md).

| Language | Find | **Change** | The honest statement |
|---|---|---|---|
| Go | ✅ | ✅ **type-checked** | The strongest path: `go build` proves a bad codemod cannot reach you. |
| Python | ✅ | ✅ | Proven end-to-end on a real 3,055-file repo. Gate is **type-checked only if your repo configures pyright/mypy**; otherwise `py_compile` → **syntax-checked**, and every such diff is human-reviewed. |
| TypeScript | ✅ | ✅ | type-checked **if** a usable `tsconfig.json` exists; otherwise syntax-checked. |
| JavaScript | ✅ | 🟡 | Rewriting works, but **no type system exists** — always `syntax-checked`, always human-reviewed. Also: **zero discovery fixtures** (§1). Do not state it. |
| **Kotlin** | ✅ | ⛔ | **Cannot be changed.** langchain4j / Spring AI are **builder-bound** (`...builder().modelName("gpt-4o").build()`), so there is no model argument at the call site to rewrite. A property of those SDKs, not a gap in our roadmap. |
| **Java, Rust** | ✅ | ⛔ | **Cannot be changed.** Neither language has a named-argument form for us to target. |
| **Polyglot repos** | ✅ | ⛔ | One verifier gates one language; a mixed repo is refused rather than gated by the wrong compiler. |

**What a customer will ask:** *"You said you support Kotlin."*
> "We **find** your LLM call sites in Kotlin, and that's real. We can't **rewrite** them: langchain4j
> configures the model on a builder, not at the call site, so there's no argument for us to change.
> That's about how those SDKs are shaped, not something we're about to ship."

**On "we verify every change builds before you see it"** — only true for `type-checked` languages. For
JavaScript, and Python without a type checker: *"we verify it parses, and a human reviews every diff —
we don't auto-apply those."* 🚫 Never let a `syntax-checked` diff be presented as type-checked.

---

## 3. What a customer will ask, and what we say

**"Can it just swap us to a cheaper provider?"**
> "We swap models within your provider today, as a reviewed diff. Swapping *providers* means rewriting
> the SDK call itself — different client, different response type — and we don't ship that yet. We
> deliberately won't fake it by routing your traffic through us: that would put our service inside your
> production path, and it would mean our measurements describe code you never actually ship."

**"So it opens PRs like Dependabot?"**
> "It produces the reviewable diff on an isolated branch, and rollback is one `git revert`. Opening the
> PR against your repo isn't built yet — that step needs write access to your source, and we're not
> shipping that until the security design is signed off." *(Do not soften this. The demo shows a diff
> in our UI; a customer who hears "PR" will expect it in their repo.)*

**"Does it run my code?"**
> "Discovery never does — it's static analysis, and we enforce that structurally, not by policy. Applying
> a change *does* build and run the transformed copy, in an isolated worktree, because that's the only
> way the measurement describes code you'd actually ship. Note our sandbox bounds blast radius; it is
> not a containment boundary for hostile code — and it's your code."

**"What languages?"**
> "Go, Python, TypeScript, Rust, Java, Kotlin. JavaScript is registered but I'd want to demonstrate it on
> your repo before I promise it. Anything else, tell us and we'll scope it — the interface is built for it."

---

## 4. FAQ → requirements backflow (⑦效果 is the next round's ①调研)

The gaps most likely to become customer questions, in the order a customer will hit them:

| # | Expected question | Feeds |
|---|---|---|
| 1 | "Where's my PR?" | 3.10 — needs the push/token ADR + the PR-URL one-way-door decision |
| 2 | "My run never finished." | 6.3 — **no queue worker exists** |
| 3 | "Swap us to OpenAI." | Cross-provider codemod — a per-(source-SDK, target-SDK) rewriter, correctly sized, own phase |
| 4 | "Configure our tools/skills." | 2.2/3.2 skills escalation — options A/B/C awaiting sign-off |
| 5 | "We're a Kotlin/JS shop." | JS fixtures; Kotlin now real |

> 🔴 **The #1 and #2 rows are the two things most likely to be over-promised**, because the demo makes
> both look done: the UI renders a diff (reads as a PR) and shows a run (reads as an executing worker).
> Neither inference is true. Correct the framing before the customer forms it.
