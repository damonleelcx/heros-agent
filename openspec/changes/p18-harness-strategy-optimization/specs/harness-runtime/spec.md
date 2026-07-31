# Harness Runtime — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§15 (FR22–FR29), §16 (A15–A19). Design reasoning: [`../../design.md`](../../design.md) Addendum Decisions 9, 10;
[`../../decisions.md`](../../decisions.md) D-9, D-10, D-13.

Covers the first of the two artifacts Decision 4's refusal named as missing: a harness **runtime** — a
bounded turn loop, a stop condition, a continuation rule, and the loop semantics of the five sealed
builtin strategies. It does **not** cover the call-site rewriter (that is
[`harness-materialization`](../harness-materialization/spec.md)) or the authoring surface (that is
[`harness-authoring`](../harness-authoring/spec.md)).

> **What "runtime" means here, and what it deliberately excludes.** This runtime backs a module that ships
> **into the customer's repository and runs in their process**, re-invoking a call the author already
> wrote. That single fact decides most of what follows: it calls no provider, it dispatches no tool, it
> reads no credential, it imports nothing, and it never reads a clock. A planner, a tool executor and a
> critic are **injected host services**, and a strategy needing a service it was not given **refuses**
> rather than substituting a cheaper loop — because a `critic-loop` that quietly runs without a critic *is*
> `reflexion`, executing under a `config_hash` that says otherwise.

## ADDED Requirements

### Requirement: Every strategy SHALL run a bounded number of turns, and the bound SHALL be reached rather than exceeded

Every strategy SHALL execute at most its sealed `max_turns` turns. The bound SHALL hold by construction
rather than by a caller's discipline: no strategy, and no combination of params, SHALL express an
unbounded loop. A run that reaches the ceiling SHALL terminate, return the last answer, and record that it
stopped at the ceiling.

#### Scenario: The ceiling is never exceeded

- **WHEN** a strategy runs against a stop condition that is never satisfied
- **THEN** the number of turns executed equals the sealed `max_turns`
- **AND** the run terminates and returns.

#### Scenario: Reaching the ceiling is recorded, not silent

- **WHEN** a run terminates because it reached `max_turns`
- **THEN** the result records the ceiling as the reason it stopped
- **AND** that reason is distinguishable from a run that stopped because its stop condition was satisfied.

#### Scenario: `single-shot` is exactly one turn

- **WHEN** the `single-shot` strategy runs
- **THEN** exactly one turn is executed
- **AND** the result is the answer that turn produced, unmodified.

### Requirement: The loop SHALL be deterministic

The same strategy, the same params, and the same sequence of answers SHALL produce the same turn count,
the same stop reason, and the same continuation inputs on every execution. The runtime SHALL NOT read a
clock, a random source, or any ambient state.

#### Scenario: Repeated execution is byte-identical

- **WHEN** the same strategy and params are run repeatedly against the same answers
- **THEN** every execution produces the same turn count, stop reason, and per-turn record.

### Requirement: A strategy's loop behaviour SHALL have exactly one definition

Each builtin strategy's continue/stop decision SHALL be defined once and called by every consumer,
including the generated artifact. The system SHALL NOT carry a second, per-language re-derivation of a
strategy's turn semantics that can drift from the sealed definition.

#### Scenario: The generated artifact and the runtime agree

- **WHEN** the generated artifact and the runtime are run against the same strategy, params, and answers
- **THEN** they produce the same turn count and the same stop reason.

#### Scenario: Every sealed strategy has a loop definition

- **WHEN** the closed builtin strategy vocabulary is enumerated
- **THEN** every strategy in it has a loop definition in the runtime
- **AND** a strategy without one fails loudly rather than running as a single shot.

### Requirement: The runtime SHALL make no provider call and dispatch no tool

The runtime and the artifact it backs SHALL NOT call a model provider, dispatch a tool, open a network
connection, or read a credential. A planner, a tool executor, and a critic SHALL be host services supplied
by the caller.

#### Scenario: No provider call originates in the runtime

- **WHEN** any strategy runs to its ceiling
- **THEN** the only calls made are re-invocations of the caller-supplied turn function
- **AND** no provider client, credential, or network destination is reached from the runtime.

### Requirement: A strategy whose host service is absent SHALL refuse rather than substitute

A strategy requiring a host service SHALL refuse with a typed error naming the missing service when it is
not supplied. It SHALL NOT fall back to a lighter strategy's loop.

#### Scenario: A missing critic refuses

- **WHEN** `critic-loop` runs without an injected critic
- **THEN** it refuses with a typed error naming the critic service
- **AND** it does not execute a loop that omits the critique.

#### Scenario: A missing tool executor refuses

- **WHEN** `react-loop` runs without an injected tool executor
- **THEN** it refuses with a typed error naming the tool-execution service
- **AND** it does not execute a fixed-turn loop in its place.

### Requirement: The turn surface SHALL be observable and SHALL NOT be hashed

A run SHALL expose the number of turns executed, the stop reason, and a per-turn record. None of these
SHALL participate in `config_hash`.

#### Scenario: The enlarged surface is visible

- **WHEN** a multi-turn strategy completes
- **THEN** the number of turns and each turn's record are readable from the result.

#### Scenario: A run's outcome does not change its configuration's identity

- **WHEN** the same configuration is executed twice with different turn counts
- **THEN** both runs report the same `config_hash`.

### Requirement: Autonomous turns SHALL reach nothing outside the call they re-invoke

The added turns SHALL execute the caller-supplied turn function and nothing else. The runtime SHALL NOT
widen the egress destinations, tool grants, or filesystem scope available at the call site.

#### Scenario: No new destination becomes reachable

- **WHEN** a node's harness is changed from `single-shot` to a multi-turn strategy
- **THEN** the set of destinations and tools reachable from that node is unchanged
- **AND** only the number of times the existing call is invoked differs.
