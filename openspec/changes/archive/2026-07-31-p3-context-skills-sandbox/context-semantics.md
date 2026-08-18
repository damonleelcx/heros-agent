# Context-Policy Semantics — AI-Engineer Confirmation (tasks 6.1–6.3)

This note records the AI-Engineer sign-off that the P3 context-policy family matches the
context-engineering discipline, its params surface admits the P5.5 change operators additively, and its
lossy policies are diagnosable in P4.

## 6.1 — The policy family matches the discipline

The context-engineering discipline names five strategies; P3 implements each behind the P2
`Policy.Assemble(conversation, params, seed)` interface:

| Discipline | Policy | Params | Semantics |
|-----------|--------|--------|-----------|
| Full history | `full-history` (alias of `full`) | — | lossless identity |
| Sliding window | `sliding-window` | `{window_size}` | keep the most recent N messages |
| Summarization | `summarization` | `{summarizer_model_ref}` | replace history with a host-summarized digest |
| RAG just-in-time retrieval | `rag-retrieval` | `{top_k, retriever_ref, rerank?}` | retrieve top-k for the latest turn and prepend |
| Semantic compaction | `semantic-compaction` | `{target_tokens}` | drop oldest whole messages to a token bound |

Confirmed executable: `registry.TestPolicyFamily_MatchesContextEngineeringDiscipline`.

`rag-retrieval` **is** the just-in-time retrieval operator: retrieval is performed at assembly time for
the node's current turn, not pre-baked. Its `rerank` flag threads the seed so a reranked retrieval is
reproducible at the resolved-request level.

## 6.2 — The params surface reserves room for P5.5 operators

The interface is additive, so P5.5's operators land behind the same seam with no breaking change:

- **A new operator is a new policy**, registered via `Store.AddPolicy` — never a schema change to the
  registry or an edit to existing policies. `rag-retrieval`'s optional `rerank` shows the pattern for
  extending a policy within its own version; **sub-agent context isolation** arrives as a new policy
  (e.g. `sub-agent-isolation {...}`) the same way, and **just-in-time retrieval** is already present.
- **Each policy owns its params schema** (`ParamsSchema`), so a rich param surface (retriever handles,
  a summarizer model_ref, future budget knobs) is added without this package learning its shape. Per
  policy, `additionalProperties:false` is deliberate — it fails a typo'd param loudly at registration —
  and it does **not** block extension, because extension is a new policy or a new immutable entry
  version, not a mutated schema.
- **`AssembledContext` is extensible**: it already carries the fields future operators need
  (`RetrievedChunks`, `ResolvedRequest`, `Lossy`, `DropRatio`), so a new operator reports its telemetry
  through the same struct without breaking existing consumers.
- **Content-addressed entries** mean swapping a policy or a param is a `config_hash` change with no
  workflow-code change — exactly what makes "swap context policy" a clean P5.5 change operator
  (`registry.TestContextEntry_ConfigSwapChangesContentAddressOnly`).

## 6.3 — Lossy policies emit drop/compaction ratios (diagnosable in P4)

`summarization` and `semantic-compaction` set `AssembledContext.Lossy = true` and populate `DropRatio`
(fraction of source tokens removed). The telemetry layer emits a `context_drop_ratio` event **only for
lossy policies** (so a lossless 0.0 is never mistaken for a measured drop), and `rag-retrieval` emits
`context_retrieved_chunks`. Every event carries the P0 tag set, so in P4 a "compaction dropped the
answer" defect is sliceable by policy.

Confirmed: `registry.TestPolicyFamily_MatchesContextEngineeringDiscipline`,
`telemetry.TestEmitContextAssembly_LossyPolicyEmitsDropRatioWithP0Tags`,
`telemetry.TestEmitContextAssembly_LosslessPolicyOmitsDropRatio`.

**AI-Engineer sign-off: the context-policy family, params surface, and lossy-diagnosability are
correct and forward-compatible with the P5.5 operators.**
