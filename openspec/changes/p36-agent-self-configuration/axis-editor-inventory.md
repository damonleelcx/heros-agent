# Axis-editor inventory — P36 task 6.1

> **Task 6.1 says to do this FIRST, and calls it the highest-risk UI revision in the program.** The
> reason is not that the page is complicated. It is that a control which quietly disappears in a
> redesign leaves no trace: the route still exists, the backend still enforces its rule, and the only
> symptom is an operator who cannot find the thing they came for. Three routes in this console have
> already shipped unreachable (`agent/prompt`, `agent/publish`, `agent/activate`), each found by a
> person going to do the thing and discovering there was no thing to press.
>
> So every control below has **a named destination or a stated removal**. Nothing is left implicit.

## The rule this table is scored against

**Collapse, do not omit.** A hidden axis is indistinguishable from one that does not exist. Where the
nine-axis × N-node surface would be too dense to read, the answer is a collapsed group that can be
opened — never a control that is not rendered.

---

## `/agent` — "What is serving inference" (header, read-only)

| # | Control today | Destination | Why |
|---|---|---|---|
| 1 | state pill + `serving_config_hash` | **kept, unchanged** | The one question the page exists to answer, stated before anything else. |
| 2 | `sentence` | **kept, unchanged** | Explains the state; a four-valued state with no sentence is unactionable. |
| 3 | kill-switch pill + note | **kept, unchanged** | The agent surface must not show a healthy agent that is in fact halted. |
| 4 | stored inferences count / `unknown` | **kept, unchanged** | `unknown` ≠ `0` — no inference store wired is a different claim from "never did anything". |
| — | *(new)* node-count badge | **added** — "1 node" / "3 nodes" beside the serving hash | After P36 "which definition is serving" has a second half: what SHAPE it is. A three-node graph and a single node are different products and the header said nothing about which was running. |

## `/agent` — tab: **Axes**

| # | Control today | Destination | Why |
|---|---|---|---|
| 5 | `DataTable`: Axis \| Status \| Value \| Why not in effect — 7 authorable rows | **kept, gains a `Node` column and a `loop` row** (tab renamed **Configuration**) | Eight per-node axes × N nodes. The table groups by node with a node header row; every node's eight rows are rendered, never the first node's only. |
| 6 | the `wiring` row (fixed, read-only, with its reason) | **kept, renamed to `graph`, reason preserved verbatim for single-node** | Task 6.3. The reason text is NOT deleted because multi-node exists — it is rendered *conditionally*, and it is still the truth for the default shape. The rename is task 10.3's noun dictionary; the old spelling is refused by name at the API with the new one stated. |

## `/agent` — tab: **Availability**

| # | Control today | Destination | Why |
|---|---|---|---|
| 7 | Harness strategies table | **kept, unchanged** | Computed from what the runner supplies; unavailable strategies stay SHOWN with what they need. |
| 8 | Memory strategies table | **kept, unchanged** | Same rule. |
| — | *(new)* Loop strategies table | **added** | The loop axis became authorable, so its availability has to be visible on the same terms — otherwise `react-loop` is a dropdown entry that fails at publish for a reason the page never showed. |

## `/agent` — tab: **Versions**

| # | Control today | Destination | Why |
|---|---|---|---|
| 9 | `DataTable`: config_hash \| Model \| Credential \| Rehearsal \| Serving | **kept, gains a `Nodes` column** | `Model` and `Credential` are the DISTINCT SET now, which is byte-identical to today's value for a single-node row. `Nodes` is the shape, because two versions differing only in topology are otherwise indistinguishable in this list. |
| — | *(new)* **Roll back** control per non-serving passed version | **added** | Task 5.5 requires rollback to be ONE ACT. Without a control it is a capability with no way to press it — the exact failure this console has shipped three times. |

## `/agent` — tab: **Instruction**

| # | Control today | Destination | Why |
|---|---|---|---|
| 10 | prose about `prompt_ref` and immutability | **kept, unchanged** | |
| 11 | `ActionForm` → `agent.publish_prompt` (name, body) | **kept, unchanged** | A definition's instruction is still published separately and bound by ref. Per node, each node binds its own — which is a change to what the ref is used FOR and not to this control. |

## `/agent` — tab: **Publish**

| # | Control today | Destination | Why |
|---|---|---|---|
| 12 | prose: content-addressed, publishing serves nothing | **kept, unchanged** | |
| 13 | `prompt_ref` input | **kept, now inside a NODE fieldset** | |
| 14 | `model_ref` input | **kept, now inside a NODE fieldset** | |
| 15 | `credential_ref` input | **kept, now inside a NODE fieldset** | Per node (D-36.1): a node binds a model and a model is served by one vendor. |
| 16 | `context` input | **kept, now inside a NODE fieldset** | |
| 17 | `harness` input | **kept, now inside a NODE fieldset** | |
| 18 | `memory` input | **kept, now inside a NODE fieldset** | |
| 19 | `skill_refs` input | **kept, now inside a NODE fieldset** | |
| 20 | `tool_names` input | **kept, now inside a NODE fieldset** | |
| — | *(new)* `node_id` input | **added** per node fieldset | Node identity is data now. Blank on a single-node definition means the default id, which is what keeps the compatibility encoding's bytes. |
| — | *(new)* `loop_ref` input | **added** per node fieldset | The ninth axis, minus graph. |
| — | *(new)* **Add a node** control | **added** | Without it the graph axis is authorable in the API and unreachable from the console — a capability with no way to press it. |
| — | *(new)* topology fieldset: `order`, `edges`, `graph_groups` | **added**, rendered read-only with its reason when one node is declared | Task 6.3's conditional render. |
| 21 | the italic note: *"The `wiring` axis is fixed… HEROS is a single node"* | **kept as the SINGLE-NODE branch of the topology fieldset's reason** | 🔴 Not deleted. Task 6.3: "Do not delete the reason; render it conditionally." Its claim is still true whenever one node is declared. |

## `/agent` — tab: **Rehearsal**

| # | Control today | Destination | Why |
|---|---|---|---|
| 22 | prose: floor on every fixture individually | **kept, extended with the per-node sentence** | D-36.7: evaluation is per node AND per axis, and the gate reads the minimum across both. |
| 23 | newest-definition rehearsal pill | **kept, unchanged** | |
| 24 | `ActionForm` → `agent.activate` (config_hash) | **kept, unchanged** | |
| 25 | `<pre className="mono">` report | **kept, unchanged** | |

## `/agent` — tab: **Nodes** *(new)*

| # | Control | Why |
|---|---|---|
| — | per-node table: node \| inferences \| provider calls \| tokens \| latency \| failures \| skipped | Task 6.4 and task 8.1. An aggregate over a graph says the agent is slow; it does not say WHICH NODE is slow, and that is the only form of the answer anybody can act on. |
| — | "which node produced which inference" — the producing node beside each recent inference | Task 6.4 verbatim. Operator-side only (D-36.2). |

## `/agent/spend`

| # | Control today | Destination | Why |
|---|---|---|---|
| 26 | per-tenant meter, fleet cap, placement editor | **kept, entirely unchanged** | The ceiling is per ASSESSMENT (D-36.6), so nothing on this page acquires a node dimension. Its absence is the point: a per-node column here would be the first place somebody proposed a per-node budget. |

---

## Deliberately removed

**Nothing.** Every control on the page today has a destination above.

The one thing that CHANGES rather than moves is the axis NAME `wiring` → `graph` (task 10.3, the noun
dictionary). The old spelling is not silently accepted: `DefinitionFromNodes` refuses it by name with
the new name stated, because a rename that quietly accepts the old spelling never finishes.

## What the build fence must now cover (task 6.5)

- every one of the **nine** axes has an operator surface, derived from `AuthorableAxes()` rather than a
  list in the test;
- the **node dimension** is rendered — the axes table has a node column and the publish form has a node
  fieldset;
- the `graph` axis is rendered in BOTH states, and its single-node reason text survives;
- the per-node observability tab exists;
- the rollback control exists.

Removing any operator surface must fail the build, which is what "extend P26's operator build fence"
means here.
