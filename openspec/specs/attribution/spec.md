# Attribution — Spec (folded from P4.5)

Product rationale: [`../../../docs/prd/P4.5-attribution-diagnosis.md`](../../../docs/prd/P4.5-attribution-diagnosis.md) §6 (FR1–FR5).

Covers per-node contribution decomposition + first-divergence, failure clustering into named
categories, ablation/counterfactual isolation of a single node with statistical rigor, cost/latency
bottleneck flags, and the read-only per-run scorecard. All attribution outputs are read-only reports;
ablation runs are ephemeral measurement variants that are never applied.

## Requirements

### Requirement: Attribution SHALL derive locality from traces and work without a discovered workflow graph

The engine SHALL derive per-node contribution, first-divergence order, failure clustering, and
cost/latency bottleneck flags from the **traces** — span attributes plus **start-time execution
order** — and SHALL NOT require statically-discovered IR edges to do so. Given a run's traces with no
IR edges (a hand-rolled agent whose nodes are bare instrumented call sites), the engine SHALL still
produce the per-node breakdown, name the first-divergence node from trace order, cluster the failing
cases, and flag cost/latency bottlenecks. The Workflow IR, when present, SHALL be used only to supply
a node's **output contract** (for contract-violation detection) and its **P3.5 pattern label** (for
scoping); both are optional enrichment.

#### Scenario: A hand-rolled agent with no workflow graph is still localized

- **WHEN** a failing variant's traces are attributed and the IR carries **no edges, no per-node
  output contracts, and no pattern labels** (the nodes are bare instrumented call sites)
- **THEN** the engine produces the per-node contribution breakdown from the spans
- **AND** names the first-divergence node from the span **start-time execution order**
- **AND** clusters the failing cases and flags the cost/latency bottleneck node(s)
- **AND** requires no discovered or authored workflow graph to do any of this

#### Scenario: A declared output contract sharpens but is not required

- **WHEN** the same traces are attributed once with per-node output contracts present in the IR and
  once with none
- **THEN** with contracts, first-divergence localizes a node whose output violates its contract
  (contract-violation)
- **AND** with no contracts, first-divergence falls back to reference-mismatch or a span failure and
  reports **no** contract violation it could not check
- **AND** both runs produce a usable per-node breakdown and scorecard

### Requirement: The engine SHALL consume recovered node edges by provenance and never present an inferred edge as certain

The agent's node edges MAY be recovered from linkage signals beyond framework detection — a **static**
call-graph / data-flow / shared-conversation-state analysis (P1) and a **dynamic** span parent-child /
shared-thread-id / temporal analysis (P2.5) — and carried in the IR each tagged with a **provenance**
(`framework` | `inferred_static` | `inferred_dynamic`) and a confidence. When such edges are present,
the engine SHALL order first-divergence and scope ablation's upstream-hold by the **highest-provenance
edge set available**, and SHALL fall back to raw span **start-time order** only when no edge links the
calls. The engine SHALL NOT render an `inferred_*` edge as a `framework` edge; a first-divergence along
an inferred edge is a weaker claim that ablation upgrades. The engine **consumes** recovered edges; it
does not invent them (recovery is owned by P1 static and P2.5 dynamic inference).

#### Scenario: First-divergence orders by a recovered edge set, not wall-clock

- **WHEN** a failing case's IR carries a linear recovered chain A→B→C tagged `inferred_static` (from
  data-flow: A's response feeds B's prompt, B's feeds C's) whose edge order differs from the raw span
  start-time order
- **THEN** the engine orders first-divergence by the recovered A→B→C edge DAG
- **AND** each consumed edge's provenance (`inferred_static`) is available on the scorecard, rendered
  distinctly from a `framework` edge
- **AND** removing the edges makes the engine fall back to span start-time order and still localize

#### Scenario: An inferred edge is a hypothesis, upgraded by ablation

- **WHEN** first-divergence names node B along an `inferred_static` edge
- **THEN** the localization is surfaced as inferred (weaker) rather than framework-certain
- **AND** an ablation of node B (Decision 2) is what upgrades it to a measured causal claim with a CI

### Requirement: The engine SHALL decompose end-to-end failure/cost/latency to individual nodes and identify the first-divergence node per failing case

For a failing variant, the engine SHALL decompose end-to-end failure, cost, and latency to individual
nodes using the OTel traces and the P4 per-node contribution signal, and SHALL identify, per failing
case, the node whose output **first diverges** from success. Every attribution output SHALL be keyed
`{variant_id, eval_set_hash, config_hash, node_id, case_id}` and queryable per node and per case.

#### Scenario: A 60%-scoring workflow's failures are localized to a node

- **WHEN** a variant scores 60% task success over an eval set and attribution is run on its failing
  cases
- **THEN** the engine decomposes end-to-end failure/cost/latency to individual nodes from the traces
- **AND** for each failing case it names the node whose output first diverges from success
- **AND** the per-node contribution is queryable per node and per case without re-running the variant

#### Scenario: The injected-fault node is named as first-divergence

- **WHEN** attribution runs on a workflow with a fault injected at exactly one node (that node drops
  its output contract, causing a downstream parse failure)
- **THEN** the engine names that node as the first-divergence node on the failing cases

### Requirement: The engine SHALL cluster failing cases into named categories with sizes and representative cases

The engine SHALL embed failing cases' inputs + traces and cluster them into **named categories**, and
SHALL emit for each cluster a size, a representative case, and the member `case_id`s — so failures are
addressable as categories rather than one-offs.

#### Scenario: Failures resolve into two distinct named clusters

- **WHEN** a failing eval set contains one group of cases that fail on multi-hop reasoning and another
  group that fails when a tool returns empty
- **THEN** the engine emits two distinct named clusters (e.g. "fails on multi-hop" and "fails when a
  tool returns empty")
- **AND** each cluster reports its size, a representative case, and its member `case_id`s

### Requirement: The engine SHALL isolate a single node's causal contribution by ablation, with a confidence interval, and SHALL NOT apply the ablation

The engine SHALL perform ablation/counterfactual isolation: holding every **other** node's config
fixed, swap exactly **one** node's config and re-run through the P4 harness **multi-seed**, reporting
the measured delta with its **confidence interval** via the P4 comparison primitive. A delta whose CI
overlaps zero SHALL be reported as **inconclusive**; only a non-overlapping delta SHALL name the node
a bottleneck. Ablation runs SHALL be ephemeral measurement variants — never persisted as user variants
and never applied to the user's workflow.

#### Scenario: Ablation isolates one node as the bottleneck

- **WHEN** ablation holds every other node's config fixed, swaps only the faulty node's config, and
  re-runs multi-seed
- **THEN** the reported delta's confidence interval does not overlap zero
- **AND** the engine names that single node the bottleneck with the measured delta ± CI

#### Scenario: A non-faulty swap is reported inconclusive, not a bottleneck

- **WHEN** ablation swaps only a non-faulty node's config and re-runs multi-seed
- **THEN** the reported delta's confidence interval overlaps zero
- **AND** the verdict is `inconclusive`
- **AND** no node is named a bottleneck from that ablation

#### Scenario: Ablation runs are ephemeral and never applied

- **WHEN** an ablation completes for a candidate node
- **THEN** the only runs enqueued were the ablation's ephemeral measurement variants
- **AND** no ablation variant is persisted as a user variant
- **AND** the user's Variant Spec and node configs are unchanged (same `config_hash`)

### Requirement: The engine SHALL flag the cost/latency bottleneck node(s) from a per-node Pareto

The engine SHALL compute a cost/latency Pareto across the nodes from the P2.5 per-node metrics and
SHALL flag the node(s) that dominate spend or sit on the critical path, tagging each flag with its
dimension (cost or latency).

#### Scenario: The spend-dominating and critical-path nodes are flagged

- **WHEN** one node accounts for the majority of end-to-end cost and a different node sits on the
  latency critical path
- **THEN** the engine flags the first node as a cost bottleneck
- **AND** flags the second node as a latency bottleneck
- **AND** each flag is tagged with its dimension

### Requirement: The engine SHALL emit a read-only per-run scorecard of overall metrics, per-node breakdown, and top failure clusters

The engine SHALL produce a per-run scorecard containing the overall metrics, the per-node contribution
breakdown (including first-divergence and bottleneck flags), and the top failure clusters. The
scorecard SHALL be a read-only report and SHALL expose no affordance to apply or change a Variant
Spec, registry, or config.

#### Scenario: The scorecard reports localization read-only

- **WHEN** the per-run scorecard is generated for a failing variant
- **THEN** it shows the overall metrics, the per-node breakdown, and the top failure clusters
- **AND** it exposes no apply/change control
- **AND** generating it mutates no Variant Spec, registry, or config (same `config_hash`)
