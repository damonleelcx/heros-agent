# eval-set-visibility

## ADDED Requirements

### Requirement: The cases behind a board SHALL be listable
`n_cases` is rendered as an integer in three places and as a list in none, so a reader who sees
"8 cases" cannot answer "8 cases of what".

#### Scenario: A route lists the cases
- **WHEN** a reader opens the eval set behind a board
- **THEN** each case is listed with its id, its family, and its oracle kind
- **AND** the route is deep-linkable rather than a modal

#### Scenario: The count and the list agree
- **WHEN** a board reports `n_cases`
- **THEN** the listed case count equals it
- **AND** a mismatch is reported as an error rather than rendered

#### Scenario: An unavailable eval set says so
- **WHEN** the cases behind a board cannot be read
- **THEN** the surface states that they could not be read
- **AND** it does not render an empty list

### Requirement: A case whose oracle can never fail SHALL be named as such
`evalgen`'s quality report already counts indecisive cases and calls them the most misleading in the
set. Counting them without naming them leaves the reader unable to act.

#### Scenario: Indecisive oracles are named
- **WHEN** the eval set contains cases whose oracle can never fail
- **THEN** each such case is marked in the list
- **AND** the count matches the quality report's `NIndecisive`

#### Scenario: Vacuous dimensions are listed
- **WHEN** the coverage report names vacuous dimensions
- **THEN** they are listed by name
- **AND** not only counted

### Requirement: The eval set SHALL be reported against the graph it is meant to exercise
A high score over cases that never reach the failing path is not evidence about the workflow.

#### Scenario: Nodes no case reaches are named
- **WHEN** both a graph and an eval set exist for a workflow
- **THEN** the surface reports which graph nodes no case exercises
- **AND** where per-case node attribution is unavailable it says so rather than reporting zero

#### Scenario: HEROS assesses exercise, it does not score
- **WHEN** HEROS assesses an eval set
- **THEN** it may report that the set leaves part of the graph unexercised
- **AND** it does not score a case, author an expected output, or rank the set

### Requirement: Unmeasured coverage SHALL state what it invalidates
#### Scenario: Coverage was not measured
- **WHEN** an eval set has no coverage report
- **THEN** the surface states that a high score is therefore not evidence that the workflow is correct
  where it matters
- **AND** it reuses the board's existing sentence rather than introducing a second wording
