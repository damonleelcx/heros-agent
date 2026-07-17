# ADR-003 — The apply path is language-neutral, and verification strength is a recorded property

- **Status:** Accepted (2026-07-17)
- **Deciders:** System Design (proposed) + User (ratified)
- **Extends:** [ADR-001](ADR-001-source-transformation-apply-model.md) requirement 2 ("Build-preserving")
- **Relates to:** [ADR-002](ADR-002-provider-gateway-serves-platform-callers.md)
- **Amends:** `docs/prd/P2-config-runtime.md` FR14; `openspec/changes/p2-config-runtime/specs/runtime/spec.md`

## Context — the gap, and how it was found

P1 was rescoped Go-only → multi-language and delivered six language frontends. **P2's Transform Engine
never followed.** `internal/transform` is built on `go/ast` and `discovery.IndexGoCallSites`; the build
gate is `go build`. So the platform can **find** LLM call sites in Python, TypeScript, JavaScript, Rust,
Java and Kotlin, and can **change** them only in Go.

This was recorded in **no task and no document**. It surfaced only when discovery was pointed at a real
repository — [`nousresearch/hermes-agent`](https://github.com/nousresearch/hermes-agent), 3,055 Python
files — which found 39 real call sites that the apply path then could not touch. A doc-only review would
not have caught it; neither did an audit of the task lists. That is itself evidence for the
「本地跑通 ≠ 交付闭环」 rule: the seam between two correct halves is where the gap lived.

Most LLM application code in the world is Python. A Go-only apply path means the product does not work
where its market is.

## The hard problem this ADR exists to solve

Making the transform language-neutral is mostly mechanical. **The build gate is not.**

ADR-001 requirement 2 says *"a transform that fails to compile/build the target is rejected before it is
ever proposed."* That sentence is carrying more safety weight than it appears to. `go build` is a **full
type check**: a codemod that renames an argument wrongly, passes a string where an enum belongs, or drops
a required field **cannot reach the customer** — it fails the gate. This is what makes "we edit your
source automatically" defensible at all.

**Python has no compiler.** `python -m py_compile` proves the file *parses*. It does not prove the call
is well-formed. A rewrite that changes `model="x"` to `modle="x"` compiles perfectly and fails at runtime
— or worse, silently takes a different code path. The languages differ **fundamentally**, not
incidentally:

| Language | Strongest gate available | What it actually proves |
|---|---|---|
| Go | `go build ./...` | Well-typed |
| Rust | `cargo check` | Well-typed |
| Java / Kotlin | `javac` / `kotlinc` via the project's build tool | Well-typed |
| TypeScript | `tsc --noEmit` | Well-typed **if** the repo has a usable `tsconfig.json` |
| Python | `pyright` / `mypy` **if configured**, else `py_compile` | Well-typed, **or only syntax** |
| JavaScript | `node --check` | **Syntax only** — no type system exists |

So the guarantee is a **property of the target language and repository, not of our engine.** Any design
that pretends otherwise is lying to whoever reads the diff.

## Decision

**1. The apply path becomes language-neutral.** The rewrite primitive — replace the bytes of a located
argument expression — is already language-neutral; only the *locating* is per-language. Discovery's
normalized call-site shape is extended to carry **byte spans** for rewritable arguments (it currently
carries their values but discards their positions), and each language gets a rewriter behind one seam.
Go keeps its `go/ast` path: it has real type information and is strictly stronger than a syntactic one.

**2. `Builder` becomes `Verifier`, and it declares what it proved.** Not a boolean. Every verification
returns a **strength**:

- **`type-checked`** — a type checker proved the rewritten program is well-typed.
- **`syntax-checked`** — only parse validity was proved. A type error would not have been caught.

**3. Strength is persisted on the transform record and surfaced in the UI.** It is not a log line and not
derivable after the fact. A reviewer looking at a diff must be able to see, without asking, whether a
compiler stood behind it. 🚫 A `syntax-checked` diff must never be presentable as though it were
`type-checked`.

**4. The strongest gate the repository actually offers is the one used.** If a Python repo configures
pyright or mypy, we use it and record `type-checked`. If it does not, we use `py_compile` and record
`syntax-checked`. We do not *require* a type checker, and we do not *pretend* to have used one.
🚫 Falling back **must** emit a WARN naming what was unavailable (禁止静默回落默认值).

**5. Automation level is gated on strength, not on language.** Autonomous auto-apply requires
`type-checked`. A `syntax-checked` transform is always human-reviewed, at every automation level. This is
the rule that keeps the weaker gate from silently becoming the customer's problem.

## Why — the arbitration

Applying [八级法則](../../../aikeylabs-skills/shared/00-核心法则.md), settled per **L2** at the highest
level where the options differ.

| Option | Verdict |
|---|---|
| **A. Keep the apply path Go-only** | ❌ The product does not function where its market is. But note: this option is **safe** — it was never a safety failure, only a capability one. |
| **B. Support all languages; treat the gate as pass/fail** | ❌ **Rejected at L1/L2.** This is the dangerous option, and it is the one that looks like "just make it work." It silently converts a type-checked guarantee into a syntax-checked one **for the majority of customers**, with nothing in the diff, the record, or the UI revealing the downgrade. A reviewer would extend trust earned by the Go path to a Python diff that never earned it. Worse, it degrades a **higher-level** property (L1/L2 safety) to buy a **lower-level** one (L6 extensibility) — a textbook 越级 violation. |
| **C. Support all languages; record what was proved** *(chosen)* | ✅ The capability ships **and** the guarantee stops being implicit. Strength becomes reviewable data. Go's guarantee is *strengthened* by this too: it is now explicit rather than assumed. |
| **D. Require a type checker in every repo** | ❌ Rejected at L3. It makes the customer reconfigure their repo before we do anything — pushing our implementation convenience onto their setup cost, which L3 forbids. |

**The decisive point:** the honest thing was never "refuse Python." It was **"stop letting the word
*build* hide a guarantee that varies by an order of magnitude."** C does not weaken any existing promise;
it makes the promise legible. B would have kept every promise *sounding* the same while making most of
them mean less.

## Consequences

**Positive**
- The P2 apply path accepts **4 of the 7 languages Discovery supports** — Go, Python, TypeScript,
  JavaScript. A real Python repository ([`nousresearch/hermes-agent`](https://github.com/nousresearch/hermes-agent),
  3,055 `.py`) now goes submit → transform → verify → record end to end.
- "Build-preserving" stops being a single ambiguous word and becomes a recorded, per-transform fact.
- Byte spans in the normalized call-site shape benefit every current and future frontend.
- P6's Autonomous level gets a principled gate that does not need a language allowlist.

**🔴 Correction (2026-07-17, same day): this ADR was wrong about coverage.**

The draft above asserted the apply path would accept *every* language Discovery supports, and called
making the transform language-neutral "mostly mechanical." **Implementation disproved both claims**, and
the truth is recorded here rather than quietly diverging:

| Language | Rewritable? | Why |
|---|---|---|
| Go, Python, TypeScript, JavaScript | ✅ | Named/keyword arguments carrying the model or prompt at the call site |
| **Kotlin** | ❌ | **The SDKs are builder-bound.** `OpenAiChatModel.builder().modelName("gpt-4o").build()` then `model.generate(prompt)` — there is no model argument *at the call site* to rewrite. Spans and the ` = ` insert spelling are real and work; the registry rows declare no `arg_map` because **there is nothing to declare**. Fixable only by a registry row + a floor extension, if the shape ever admits one. |
| **Java, Rust** | ❌ | The languages have **no named-argument form at all**, and the syntactic floor resolves only `LocParamName`. Not fixable at the call site. |
| **Polyglot repos** (`workflow.language == "mixed"`) | ❌ | One `Verifier` gates one language. Refused, honestly, rather than gated by the wrong compiler. |

The three refusals are **not** one failure with one cause, and the code keeps them apart deliberately
(`TestGenerate_Kotlin_RefusalBlamesTheRegistryNotTheLanguage`): collapsing Kotlin into Java's
"no named arguments" would send someone to fix the wrong thing forever.

**The lesson worth keeping:** "language-neutral apply" was never blocked by the engine. It is blocked by
whether a language's *SDK idiom* puts the configurable value at the call site. That is a property of the
ecosystem, discoverable only by looking at real registry rows — which is why an estimate made from the
engine's shape was wrong.

**A third kind of failure, found by running against a real repository (added 2026-07-17)**

This ADR named two kinds of failure and asserted they must never be confused. Running the gate against
hermes-agent produced a **third** that neither covers, and the vocabulary's gap became a lie to a user:
the worker's `python3` was 3.9, the target used 3.10+ `match`, `py_compile` failed on a file the diff
never touched, and the pipeline recorded `build-rejected` — telling the user *"this transform does not
build"* while citing valid Python, with an empty `rejected_node_id`.

`ErrToolchainUnavailable` does not cover it: nothing was missing. A **minimum-version table** was
considered and rejected — it needs a row per language per feature per release, goes stale the day a
language ships new syntax, and still misses every other reason a pristine tree fails a gate. The general
answer is empirical: **before a rejection may blame a diff, the same gate must pass the UNMODIFIED tree
at `source_revision`.** If it fails there too, the diff is exonerated and no verdict is recorded
(`ErrBaselineFails`). It is deliberately *not* a `ToolchainError` — a baseline can also fail because the
customer's revision is genuinely broken, and "install something" would then be as wrong as the original
lie. Cost is one extra verification **on the failure path only**.

	repository configures no type checker  -> LEGITIMATE syntax-checked + WARN  (the customer's choice)
	toolchain absent from the worker       -> ERROR, no record, no verdict      (our problem)
	pristine source fails the same gate    -> ERROR, no record, no verdict      (nobody's diff)

**Negative / accepted cost**
- 🔴 **L4 operations.** The worker now needs the toolchains it verifies with: Go, Python, Node, Rust,
  and a JVM + build tool. This is a real and ongoing operational surface, and it was **accepted
  deliberately** when "all 6 languages" was chosen over a Python-first slice. A missing toolchain must
  **fail loudly**, never silently degrade to `syntax-checked` — that would turn an ops problem into a
  safety problem.
- Java/Kotlin verification depends on the target's build tool (Gradle/Maven), which may need network for
  dependency resolution — in tension with the least-privilege posture of the discovery worker. Verification
  runs in the *apply* worker, not the discovery worker; the two postures are deliberately different and
  must not be conflated.
- `syntax-checked` languages carry genuinely more risk. That risk is now **visible and gated**, not
  eliminated.

**🔴 Sales-operations boundary.** "We verify every change builds before you see it" is **only true for
type-checked languages**. For JavaScript, and for Python without a type checker, the honest statement is:
*"we verify it parses, and a human reviews every diff — we don't auto-apply those."* See
[`capability-boundary-p0-p2.md`](../decisions/capability-boundary-p0-p2.md).
