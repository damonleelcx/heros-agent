# P1 Discovery — M1 exit verification (§9)

> **Task:** P1 `tasks.md` §9. **What:** run the built Discovery CLI on a **real external repository**
> and confirm the M1 acceptance criteria hold on real code, not just fixtures. **Result:** all five
> criteria pass. This is the evidence record for the M1 gate.

## Target repo (real, external, mixed-language)

- **Repo:** `github.com/nousresearch/hermes-agent` (an agent codebase), cloned read-only.
- **Commit:** `2ea39daeb1f675d72e5c21c9400f2d58d7e6d71a`.
- **Size / mix:** 4,400 source files — **3,020 Python, 924 TS, 460 TSX, 12 JS, 9 Rust, 1 Ruby**.
- **Run:** `discover --repo <clone> --out ir.json --report report.json` (all six frontends active).
- **Throughput:** whole repo discovered in **~10 s** (budget ≤60 s / ~200k LOC — NFR3 met with margin).

## Criteria

### 9.1 — Static nodes extracted, IR emitted, IR diffable ✅
- **39 nodes** extracted (37 OpenAI, 2 Bedrock), IR emitted; `files_skipped: 0` (tree-sitter parsed
  every real file without a hard failure).
- **Diffable:** two runs over the same commit produced **byte-identical** IR.
- **Valid:** the emitted IR **validates against the frozen `workflow-ir.schema.json`**.
- **Per shipped language:** Go (this repo's own tests + fixtures), Python (hermes, below), and TS/JS/
  Rust/Java (fixtures + probes) all extract on real/representative code; on hermes all six frontends
  ran on the mixed tree without conflict.

### 9.2 — Wrapper nodes found via user-declared entrypoints ✅ (the decisive test)
`agent/auxiliary_client.py:call_llm` is a real in-house wrapper called across the repo (`oneshot.py`,
`moa_loop.py`, `context_compressor.py`, tests, …). Its call sites are invisible to the signature
registry. Declaring it in `llm-eval.yaml`:
```yaml
version: "1.0.0"
entrypoints:
  - symbol: "agent.auxiliary_client.call_llm"
    provider: openai
    args: { prompt: { name: "messages" } }
```
took the run from **39 → 110 nodes** — `detections_by_source: { registry: 39, declared: 71 }`. The 71
new nodes are the `call_llm(...)` call sites the registry alone misses. **User-declared entrypoints are
the mechanism that finds wrapped nodes — proven on real code, per the Python `from module import func`
binding.**

### 9.3 — Loop/agent nodes flagged variable-at-runtime; count per static definition ✅
- Invocation mix: **loop 4, conditional 19, single 16**. The 4 loop nodes carry
  `variable_at_runtime = true` (e.g. `MiniSWERunner.run_task`, `TrajectoryCompressor._generate_summary_async`,
  `auxiliary_client.call_llm` — genuine agent/iteration loops).
- **No fixed runtime count** field appears on any node (asserted programmatically).
- Node count = number of static definitions (`report.nodes_emitted == len(ir.nodes)` = 39).

### 9.4 — No-execution invariant held ✅
- **Read-only:** the clone's git working tree was **clean after discovery** — Discovery created,
  modified, and deleted nothing.
- **No execution:** tree-sitter (and `go/ast`) only parse; the structural import guard forbids
  `os/exec`/`plugin`/`net`, and the `init()`/Python-side-effect tests already prove no target code
  runs. `files_skipped: 0` and zero panics on 4,400 real files confirm the robustness path held.

### 9.5 — Mixed-language repo yields one coherent IR spanning frontends ✅
- hermes is genuinely mixed (py/ts/tsx/js/rs/rb); **all six frontends ran on one tree and merged into a
  single IR** without conflict. On this repo the LLM SDK calls are concentrated in Python, so
  `workflow.language = "python"` (only Python contributed nodes — honest, not "mixed" for its own sake).
- The **multi-language-nodes-in-one-IR** property is proven directly by the committed `mixed` fixture
  (Go + Python + TypeScript → 3 nodes, `workflow.language = "mixed"`) and its golden IR in CI.

## Verdict

**M1 exit criteria satisfied on a real, external, multi-language repository.** Discovery extracts a
faithful, diffable, schema-valid node graph from untrusted source across languages; honestly marks what
static analysis can't resolve (78 ambiguity flags, models `unresolved` where not literal); finds
wrapped nodes via declaration; flags agent loops as variable-at-runtime; and never executes or mutates
the target.
