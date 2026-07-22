# Scoring — Spec Delta (P4)

Product rationale: [`../../../../docs/prd/P4-eval-harness.md`](../../../../docs/prd/P4-eval-harness.md) §6 (FR13–FR20).

Covers metric normalization, named weight profiles with cached re-ranking, the gate-vs-weight
separation with disqualification, composite-score confidence intervals and the tie rule, leaderboard
row contents, and the Pareto view.

## ADDED Requirements

### Requirement: Each metric SHALL be normalized to [0,1] before it enters the weighted sum

Every metric SHALL be normalized to the range [0,1] (min-max or z-score across the variant set)
before it is combined into the composite score, so that metrics in different units (dollars,
percent, milliseconds) are comparable.

#### Scenario: Raw dollar cost and percent quality are normalized before weighting

- **WHEN** a variant has a raw cost in dollars and a raw quality in percent
- **THEN** each is normalized to a value in [0,1] across the variant set
- **AND** the composite score is computed only over the normalized values, never the raw units

### Requirement: The composite score SHALL be the weighted sum of normalized quality, inverted cost, inverted latency, reliability, minus penalties

The composite score SHALL be computed as
`Score = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties`
over normalized metrics.

#### Scenario: Composite matches the defined formula

- **WHEN** a variant's normalized quality, cost̂, latencŷ, reliability, and penalties are known and a
  weight profile is active
- **THEN** its composite score equals `w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties`

### Requirement: Weight profiles SHALL be named, and switching profiles SHALL re-rank without re-executing any run

Weights SHALL be expressed as a named profile (at least quality-first, cost-optimized, and
balanced). Per-variant normalized metric values SHALL be cached so that switching the active profile
recomputes only the weighted sum and re-ranks the leaderboard without re-executing any run.

#### Scenario: Profile switch re-ranks from cache with zero new runs

- **WHEN** the active weight profile is switched from quality-first to cost-optimized
- **THEN** the leaderboard re-ranks using the cached normalized metric values
- **AND** no new run is enqueued or executed
- **AND** for ≤ 500 variants the re-rank completes in under 200 ms

### Requirement: Hard constraints SHALL be disqualifying gates evaluated before weighting, and soft weights SHALL apply only to variants that pass every gate

Hard constraints (max cost/run, latency SLA, min quality, provider allowlist) SHALL be gates: a
variant that violates any gate SHALL be **disqualified** — excluded from the ranked order —
regardless of how favorable its weighted score would be. Gates SHALL be evaluated before the
weighted sum, and the soft weighted preferences SHALL apply only to variants that pass every gate.

#### Scenario: Cheapest-but-below-quality variant is disqualified, not ranked first

- **WHEN** the cost-optimized profile is active and the cheapest variant violates the min-quality
  gate
- **THEN** that variant is disqualified and excluded from the ranked order
- **AND** it is listed separately with the min-quality gate named as the reason
- **AND** it is not ranked first despite being the cheapest

#### Scenario: A gate violation disqualifies rather than merely penalizes

- **WHEN** a variant exceeds the max-cost-per-run gate by a small margin
- **THEN** the variant is disqualified, not given a reduced-but-still-ranked score
- **AND** it does not appear anywhere in the ranked list of gate-passing variants

### Requirement: A metric that cannot separate the variant set SHALL NOT decide the ranking

When every variant's confidence interval on a metric overlaps every other's, that metric SHALL be
treated as **degenerate**: it normalizes to the same value for every variant and contributes nothing
to the ordering. The reason SHALL be recorded alongside the normalization scale.

#### Scenario: A metric whose spread is indistinguishable from noise does not order the board

- **WHEN** three variants' latency intervals all overlap, while their quality intervals do not
- **THEN** the latency metric is flagged degenerate and normalizes to the same value for all three
- **AND** the ranking is decided by the metric that does separate them

  *(Min-max normalization divides by the observed spread. When that spread is comparable to the
  measurement noise, dividing by it amplifies noise across the whole [0,1] axis — the step meant to
  make metrics comparable would otherwise promote noise to a ranking signal.)*

### Requirement: A penalty derived from a measured quantity SHALL carry its uncertainty into the composite interval

A penalty term computed from a measured metric SHALL enter the composite-score bootstrap per
replicate, so its uncertainty widens the interval. A penalty computed from an exact count SHALL be
subtracted as a constant. The composite score SHALL lie within its own confidence interval.

#### Scenario: A noise-sized penalty difference does not produce a confident ordering

- **WHEN** three variants are statistically indistinguishable on every metric, and differ only in
  the measured quantity a penalty reads
- **THEN** their composite intervals overlap and all three are shown tied
- **AND** no variant is ranked strictly above another on that difference

### Requirement: A variant whose runs did not complete SHALL be excluded from the ranking and named

A variant with no usable measurement — a fan-out cut short by a spend cap, or fewer seeds than the
configured floor — SHALL be excluded from the ranked order rather than scored from partial data, and
SHALL be listed with the reason. The board SHALL report a partial state. A board on which no variant
has a usable measurement SHALL be refused.

#### Scenario: A budget-capped sweep produces a partial board, not a crash and not a silent short list

- **WHEN** a spend cap stops a sweep after 26 of 84 variants have complete measurements
- **THEN** the 26 measured variants are ranked
- **AND** the 58 unmeasured variants are named with the reason, as one summary plus a list
- **AND** the board reports a partial state rather than presenting 26 rows as the whole field

### Requirement: Each composite score SHALL carry a confidence interval and CI-overlapping variants SHALL be shown as tied

Each composite score SHALL be reported with a confidence interval derived from the multi-seed runs.
When two variants' composite-score confidence intervals overlap, they SHALL be shown as tied rather
than one ranked strictly above the other as a winner.

#### Scenario: Overlapping composite CIs render as a tie

- **WHEN** two variants have composite scores whose confidence intervals overlap
- **THEN** the leaderboard marks the pair as a tie
- **AND** neither is presented as the strict winner over the other

### Requirement: The leaderboard SHALL rank gate-passing variants and show score ± CI, component breakdown, gate status, and config lineage

The leaderboard SHALL rank gate-passing variants by composite score under the active profile. Each
row SHALL show the score with its confidence interval, the component-metric breakdown, the gate
pass/fail status, and the full config lineage (`config_hash`). Disqualified variants SHALL be listed
separately with the failed gate named.

#### Scenario: Leaderboard row exposes CI, breakdown, gate status, and lineage

- **WHEN** the leaderboard is rendered under the balanced profile
- **THEN** each gate-passing row shows score ± CI, the per-component metric breakdown, `gate: pass`,
  and the variant's `config_hash`
- **AND** a disqualified variant appears in a separate section showing `gate: fail` and the name of
  the gate it violated

### Requirement: A Pareto view SHALL surface the quality/cost/latency frontier and re-render on weight change

A Pareto view SHALL surface the quality/cost/latency frontier so multi-objective tradeoffs are
visible without collapsing to a single number, and SHALL re-render when the active weight profile
changes.

#### Scenario: Non-dominated variants form the visible frontier

- **WHEN** the Pareto view is rendered over a set of variants
- **THEN** the non-dominated variants (no other variant is better on all of quality, cost, and
  latency) form the visible frontier
- **AND** switching the active weight profile re-renders the view without re-executing any run
