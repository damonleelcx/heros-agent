# The walk — P37 against `nousresearch/hermes-agent`

> **Run:** 2026-08-27 · `make sourcebound-hermes` over a shallow clone of
> [nousresearch/hermes-agent](https://github.com/nousresearch/hermes-agent) at `/tmp/hermes-agent`.
> No provider call, no sandbox, no credential, no cost.

Every fence in §6 is green and every one has been drilled red. Green fences prove the **parts**, against
a two-node fixture this repository wrote with values chosen to make the assertion clean. That is the
right shape for a fence and it is not evidence about a customer.

This is the **walk**, and the walk is the phase's whole claim:

> a page that explains what the platform *would* do to a hypothetical node, while the reader's real node
> is one query away, has stopped being cautious and become stale.

Every node id below is a symbol somebody at Nous Research wrote, in a file this repository has never
seen.

---

## What the editors would render, on their 29 call sites

```text
-- 1) discover the repository's own call sites
    29 node(s), 0 edge(s)

-- 2) run the transform engine over those call sites, here, where the source is
    29 node(s) carry verdicts, computed against coverage table cov-fab38f6e

-- 3) build the structure the platform would hold, exactly as `heros link --with-ir` sends it
    29 node(s) crossed the boundary; no prompt text, no source, no keys

-- 4) read each node's CURRENT value on each axis — the read every editor binds to

    what a reader would see, per axis, across all 29 of their nodes:
      model        0 observed ·  29 not measured
      prompt       0 observed ·  29 not measured
      skills       0 observed ·  29 not measured
      context     29 observed ·   0 not measured
      tools       29 observed ·   0 not measured
      memory       0 observed ·  29 not measured
      harness      0 observed ·  29 not measured
      loop         0 observed ·  29 not measured
      graph        0 observed ·  29 not measured

    and where the absences come from — the NAMED missing inputs:
      not_visible_in_static_ir    174
      unresolved_in_ir             29

-- 6) read the live per-node context coverage — what replaced the transcribed table
    python     8 policy row(s): 4 not-at-call-site, 2 identity, 2 select

-- 7) resolve the subject the shell would resolve, over their node list
    29 candidates and none chosen → the shell asks ONCE, in the shell, and the answer
    applies to all seven axis surfaces.

✔ 29 node(s) × 9 axes = 261 readings, on a repository nobody here wrote.
  Every one was READ or reported absent WITH A NAMED INPUT. None was supplied.
```

One node, in full, as `/app/context` and `/app/studio` would render it:

```text
    n_0110783593788f45  (_relay_sync_stream)
      agent/auxiliary_client.py · python
      model     not measured  missing: unresolved_in_ir
      prompt    not measured  missing: not_visible_in_static_ir
      skills    not measured  missing: not_visible_in_static_ir
      context   observed      inline_messages  (python)
      tools     observed      0 tools
      memory    not measured  missing: not_visible_in_static_ir
      harness   not measured  missing: not_visible_in_static_ir
      loop      not measured  missing: not_visible_in_static_ir
      graph     not measured  missing: not_visible_in_static_ir
```

---

## 🔴 What this run found, and why it is the most valuable line in this document

**All 29 of hermes-agent's nodes carry `model_id = "unresolved"`** — `discovery`'s own sentinel, written
into the field when a call site's model cannot be resolved. Verified directly against the IR rather than
inferred from the report:

```text
model fields across 29 nodes: 29 carry the `unresolved` SENTINEL, 0 empty, 0 a real id
  n_0017914833d3d240  provider="openai" model_id="unresolved" context="inline_messages"
  n_008b903482b33087  provider="openai" model_id="unresolved" context="inline_messages"
  n_0110783593788f45  provider="openai" model_id="unresolved" context="inline_messages"
```

`unresolved` is a **literal string**. The obvious implementation of "does this node have a model?" is
`n.ModelID != ""`, and it is wrong — it reads the sentinel as a value. §5.3 checks the sentinel by name
for exactly this reason, and this repository is what that check is worth:

**with the naive check, every one of hermes-agent's 29 nodes would render a model called `unresolved` in
the position that says *what this node runs today*.** Not an edge case, not one node in forty — 100% of a
real repository, on the first one the phase was pointed at.

### The mutation, run

The check was replaced with `return false` — the naive implementation — and both fences went red:

```text
=== unit fence under mutation ===
--- FAIL: TestTheUnresolvedSentinelIsNeverRenderedAsAValue

=== the WALK under mutation ===
      model     observed      unresolved  (openai)
  ✖ n_0017914833d3d240/model rendered "unresolved" as a value
  ✖ n_008b903482b33087/model rendered "unresolved" as a value
  ✖ n_0110783593788f45/model rendered "unresolved" as a value
  …
```

`model observed unresolved (openai)` is the line a reader would have been shown. They would then have
authored a change *from a baseline that never existed* — which is the failure P37 §5.3 exists to prevent,
stated in the abstract when it was written and demonstrated here on somebody else's code.

---

## What a sceptical reader should take from the shape of this result

**Two axes resolved and seven did not, and that is the honest answer rather than a disappointing one.**

- `context` and `tools` have a field on the wire, so they read: every node's assembly is
  `inline_messages`, in `python`, which is a true statement about their source.
- `model` has a field and it carries the sentinel, so it reports absence **naming `unresolved_in_ir`** —
  a different missing input from the other six, because it is a different problem with a different owner.
  Discovery could not resolve it; nothing is missing from the platform.
- `prompt`, `skills`, `memory`, `harness`, `loop` and `graph` have **no field on the wire at all**, so
  they report `not_visible_in_static_ir` on every node. That is P37 §5.3's second rule: the tempting move
  is to render the vocabulary's identity element (`memory: none`, which is what `discovery` emits for
  every node everywhere), and `internal/discovery/emit.go` says why that would be wrong in its own words
  — *"a statement about the EVIDENCE, not a placeholder."*

**A run where everything resolved would be a weaker result, not a stronger one.** It would mean the
sentinel path never executed and the absence path never rendered — the two things this phase is actually
for. The exit code is written accordingly: it fails when a value was **supplied**, never when one was
**absent**.

---

## Reproducing it

```bash
git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
make sourcebound-hermes
```

Related walks over the same repository: `make repo-intake-hermes` (P32), `make assessment-hermes` (P33),
`make axissplit-hermes` (P34), `make agentgraph-hermes` (P36).
