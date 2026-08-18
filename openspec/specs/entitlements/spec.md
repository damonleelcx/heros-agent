# Entitlements — Spec (folded from P7)

Product rationale: [`../../../docs/prd/P7-billing-metering.md`](../../../docs/prd/P7-billing-metering.md)
§6 (FR5–FR8) and §7.

Covers feature access gated by the active **plan AND automation level** (Advisory / Assisted /
Autonomous); plan definitions as **configuration** changeable **without a code deploy**; over-limit
and under-entitled actions **denied with an upgrade path, never silently**; and **Autonomous
auto-merge entitled to Enterprise only** (the P6 loop consults the gate and falls back to open-PR).

> No dollar amounts, percentages, or price bands appear in this spec. Plans are named
> (Free / Team / Business / Enterprise); limits + price references are configuration, not code, not
> in git.

## Requirements

### Requirement: Feature access SHALL be gated by the active plan AND the automation level

Feature access SHALL be gated by the customer's **active plan** *and* the **automation level**
(Advisory / Assisted / Autonomous): **CLI and discovery** SHALL be available to **all plans including
Free**; **Assisted verified PRs** SHALL be available to **Team and above**; the **Web dashboard**
(with seats, trace/metric retention, and SUM band **per plan**) SHALL be available to **Team and
above**; **Autonomous auto-merge** SHALL be available to **Enterprise only**. An action outside the
customer's plan-and-level entitlement SHALL NOT be performed.

#### Scenario: CLI and discovery work on every plan including Free

- **WHEN** a Free-plan customer invokes the CLI and discovery
- **THEN** the entitlement gate allows it
- **AND** the same is allowed on Team, Business, and Enterprise.

#### Scenario: Assisted PRs and the dashboard require Team or above

- **WHEN** a Free-plan customer requests an Assisted verified PR or the Web dashboard
- **THEN** the entitlement gate denies it
- **AND** the same request on a Team, Business, or Enterprise plan is allowed.

#### Scenario: Autonomous auto-merge is entitled to Enterprise only

- **WHEN** a Team or Business customer requests Autonomous auto-merge
- **THEN** the entitlement gate denies it
- **AND** the same request on an Enterprise plan is allowed.

### Requirement: Plan definitions SHALL be configuration changeable without a code deploy

Plan definitions — limits, SUM band, seat and retention allowances, and **price references** — SHALL
be **configuration** resolved at runtime from a config store, **not** compiled into code and **not**
in git. A plan or price change, or the introduction of a new plan, SHALL take effect **without a code
deploy** (no code change and no migration). No plan definition or price SHALL appear in a git-tracked
file.

#### Scenario: A plan/price change takes effect with no code deploy

- **WHEN** a fixture plan's limit or price reference is repointed in the config store and the new
  version is published
- **THEN** the new limit/entitlement takes effect for the affected customers
- **AND** it required zero code change and no deploy.

#### Scenario: A new plan is introduced by configuration

- **WHEN** a new named plan is added to the config store
- **THEN** the entitlement gate resolves and enforces it
- **AND** no code change or migration was required to introduce it.

#### Scenario: No priced value is committed to git

- **WHEN** the repository is scanned for plan definitions or prices
- **THEN** none are present in any git-tracked file — they exist only in the config store.

### Requirement: An over-limit or under-entitled action SHALL be denied with a named reason and an upgrade path

An action that exceeds a metered limit or falls outside the customer's plan entitlement SHALL be
**denied with a clear, named reason and an upgrade path** — the response SHALL identify the
limit/entitlement that was hit and name the plan that lifts it. Such an action SHALL NOT be silently
dropped, silently degraded, or silently allowed.

#### Scenario: An over-limit action is denied with the plan that lifts it

- **WHEN** a customer attempts an action that would exceed their plan's SUM band or seat limit
- **THEN** the action is denied
- **AND** the denial names the limit that was hit and the plan that lifts it (the upgrade path).

#### Scenario: An over-limit action is never silently allowed or dropped

- **WHEN** a customer exceeds a metered limit
- **THEN** the action is neither silently performed anyway nor silently discarded
- **AND** the outcome is an explicit denial carrying the upgrade path.

### Requirement: Autonomous auto-merge SHALL be performed only for an entitled plan and the P6 loop SHALL consult the gate

The Autonomous auto-merge capability (P6) SHALL be performed **only** for a customer whose active plan
entitles it (**Enterprise**). The P6 auto-merge loop SHALL consult the entitlement gate **before** a
merge; absent the Enterprise entitlement it SHALL fall back to a lower automation level — opening a
pull request for a human — rather than merging.

#### Scenario: The loop falls back to open-PR without the entitlement

- **WHEN** the P6 loop reaches a merge for a customer whose plan does not entitle Autonomous auto-merge
- **THEN** the gate denies the merge
- **AND** the loop opens a pull request for a human instead of merging.

#### Scenario: The loop merges only for an entitled customer

- **WHEN** the P6 loop reaches a merge for an Enterprise customer with auto-merge entitled and all P6
  gates green
- **THEN** the entitlement gate allows the merge
- **AND** for a non-entitled customer under identical conditions the merge is not performed.
