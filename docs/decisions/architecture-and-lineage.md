# End-to-End Architecture & Lineage (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | System Designer (lead) |
| Status | Draft — freeze at M0 |
| Tasks | 1.8 (this diagram set) |
| Cross-refs | `docs/prd/P0-foundations.md` §8.4–§8.5; `docs/decisions/storage-decision-record.md`; `docs/decisions/config-hash-spec.md` |

The canonical Mermaid diagrams for P0. They are the single source the PRD references for the
end-to-end flow and the lineage of a `config_hash`. Nothing here runs in P0 — the diagrams show the
contracts and the shape everything downstream builds toward.

---

## 1. End-to-end architecture (repo → verified PR)

How a repo becomes a verified optimization, and where the P0 contracts (IR, `config_hash`, the
seven-tag event, the three stores) sit. Phase labels show where each stage is *built*; P0 freezes the
**bold** contracts they all depend on.

```mermaid
graph TD
  R[Repo @ commit_sha] --> D[Discovery · P1]
  D --> IR[["Workflow IR<br/>ir_version · nodes · edges · io_contract"]]:::p0

  IR --> C[Config Layer · P2]
  C --> VS[Variant Spec<br/>per-node bindings + ordering]
  VS -->|canonicalize + SHA-256| H[["config_hash<br/>(immutable, content-defined)"]]:::p0
  VS --> CM[Source-transformation codemod · P2]
  CM --> RT[Runtime · P2<br/>sandboxed, traced]

  RT --> EV[["Tagged events<br/>7 non-null tags"]]:::p0
  EV --> TSDB[("TSDB · metrics")]:::store
  EV --> SPAN[("Span store · drill-down")]:::store
  RT --> BLOB[["Object store<br/>content-hashed blobs"]]:::store
  RT --> PG[("Postgres · eval results<br/>NOT NULL tags · FKs")]:::store

  PG --> EVAL[Eval + Attribution + Diagnosis · P4–P4.5]
  TSDB --> EVAL
  SPAN --> EVAL
  H --> EVAL
  EVAL --> OPT[Proposal + Verification gate · P5.5–P6]
  OPT -->|verified on held-out data| PR[Pull Request]

  classDef p0 fill:#1f6feb22,stroke:#1f6feb,stroke-width:2px;
  classDef store fill:#2da44e22,stroke:#2da44e;
```

**Reading it:** the four bold/blue nodes are P0's deliverables — every downstream phase reads or writes
them. *Diagnosis proposes, verification decides*: no edge reaches **Pull Request** without passing the
verification gate on held-out data.

## 2. Lineage of a config_hash (reproducibility unit)

How a configuration becomes an immutable hash, what the hash keys, and how it resolves back to exact
bytes. This is the picture behind `config-hash-spec.md`.

```mermaid
graph LR
  Repo[Repo @ commit_sha] --> IR[Workflow IR<br/>ir_version]
  IR --> VS[Variant Spec<br/>resolved refs@version + ordering]
  VS -->|"RFC 8785 canonical + SHA-256<br/>(excludes run_id, seed, timestamp)"| CH[[config_hash]]

  CH --> PG[("Postgres<br/>eval results · FKs · NOT NULL tags")]
  CH --> TSDB[("TSDB<br/>metrics · low-card series")]
  CH --> SPAN[("Span store<br/>OTel drill-down")]

  VS -. resolves .-> MR[Model registry]
  VS -. resolves .-> PR2[Prompt registry @ver]
  VS -. resolves .-> SR[Skill registry @ver]
  PR2 --> BLOB[["Object store<br/>content-hashed prompts/artifacts"]]
  PG -. references .-> BLOB
  SPAN -. references .-> BLOB
```

**Reading it:** `config_hash = SHA-256(canonical(resolved_config))`, **excluding** `run_id`, `seed`,
`timestamp` — so the same config under seeds 1..5 shares one hash and multi-seed results roll up. The
hash resolves through the versioned registries and content-hashed blobs, so any result is replayable
from lineage alone. Reproducibility unit = **`config_hash + seed`**.

## 3. Static definition vs. runtime invocation (why node count is stable)

The IR distinguishes one *static definition* from its *many runtime invocations* (Decision 1). Node
count is per-definition; a loop is one node flagged `variable_at_runtime`, not N nodes.

```mermaid
graph LR
  subgraph IR["Workflow IR (static)"]
    A["classify · single<br/>variable_at_runtime=false"]
    B["resolve_agent · loop<br/>variable_at_runtime=true"]
    A -- data --> B
  end
  subgraph RUN["One run (runtime invocations)"]
    B0["inv 0"]; B1["inv 1"]; B2["inv 2"]; B3["…"]
  end
  B -. "1 definition → many invocations<br/>(node_id + invocation_index)" .-> B0
  B -.-> B1
  B -.-> B2
  B -.-> B3
```

**Reading it:** a repo with 20 call sites — one an agent loop — reports **20** nodes. The loop fires a
runtime-variable number of times; each firing is a `runtime-invocation` record referencing the one
definition's `node_id`, so metrics attribute a variable count of invocations back to a stable node,
and the graph stays diffable across runs.
