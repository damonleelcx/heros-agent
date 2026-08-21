# Axis Node Projection — Delta (P37)

Modifies [`../../../../specs/axis-node-projection/spec.md`](../../../../specs/axis-node-projection/spec.md),
folded from P29.

**Why this requirement is touched.** P29 wrote it to stop a redesign silently dropping panels off the
axis surfaces, and that protection is correct and is kept. What it did not anticipate is a rewrite that
*relocates* a worked example rather than deleting it — its scenario reads "every panel present before is
still present", which a move fails even though nothing was lost. P37 keeps the protection and changes its
unit from *the same page* to *a named destination*, and strengthens the second scenario: after P37 a
worked example may not appear in the reader's data position at all, which makes example and live data
distinguishable structurally rather than by labelling.

## MODIFIED Requirements

### Requirement: The worked examples on each axis surface SHALL be retained

The verbatim engine examples SHALL be retained at a **named destination**: on the working surface when
their content varies with the reader's data, and on the reading surface, labelled as the platform's
fixture, when it does not. No example is deleted, and no example occupies the position the reader's own
data occupies.

#### Scenario: Nothing is removed

- **WHEN** an axis surface is compared before and after a change that relocates its text
- **THEN** every panel present before is present at a named destination — the working surface or a
  reading-surface section
- **AND** the change enumerates each relocated panel and where it landed
- **AND** a panel with no destination is not removed.

#### Scenario: Live data and worked examples are distinguishable

- **WHEN** an axis surface renders the reader's own data
- **THEN** no worked example appears in the position that data occupies
- **AND** a worked example rendered anywhere states that it is the platform's fixture
- **AND** a reader can tell the example from their own data without inspecting values.

#### Scenario: A relocated example is still reachable from the surface it left

- **WHEN** a worked example is relocated to the reading surface
- **THEN** the axis surface it left carries a link to the section it landed in
- **AND** a broken link to that section fails the build.
