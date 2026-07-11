# Attribution — Spec Delta (P4.5)

Product rationale: [`../../../../docs/prd/P4.5-attribution-diagnosis.md`](../../../../docs/prd/P4.5-attribution-diagnosis.md) §6 (FR1–FR5).

Covers per-node contribution decomposition + first-divergence, failure clustering into named
categories, ablation/counterfactual isolation of a single node with statistical rigor, cost/latency
bottleneck flags, and the read-only per-run scorecard. All attribution outputs are read-only reports;
ablation runs are ephemeral measurement variants that are never applied.

## ADDED Requirements

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
