# HEROS — the ablation protocol, and Q4's first measurement

P30 tasks 5.8 and 5.9. This is the procedure for changing HEROS and knowing whether the change helped.

---

## 1. The protocol: one axis at a time

**Change one axis. Publish. Rehearse. Report the delta against the previous `config_hash`.**

That is the whole rule, and the reason it is a rule rather than advice is that HEROS's definition has
**six** authorable axes. A change that moves the prompt *and* the harness *and* the model produces one
number, and that number cannot be attributed. Six axes changed together is a measurement of nothing —
and it is the measurement people take, because taking it is faster.

### The steps

1. **Record the baseline.** The active definition's `config_hash` and its stored rehearsal report. It is
   already on the version row (task 5.6), so this is a read, not a re-run.
2. **Change exactly one axis** in the console's axis editor, or through `DefinitionFromAxes`.
3. **Publish.** The new version lands `pending`. It is not active and is never rendered as if it were.
4. **Rehearse.** `Rehearsal.GateActivation` runs the pinned calibration set and stores the per-fixture
   report on the new version row.
5. **Report the delta**, per fixture, against the previous `config_hash`. Not the mean — see §2.
6. **Activate, or do not.** A definition that did not pass cannot be activated; the database refuses it
   as well as the Go path.

### 🔴 What the delta is reported ON

**Per fixture**, and against the **previous `config_hash`** by name. Two things follow that are easy to
get wrong:

- A mean delta hides the case that matters. An axis change that improves seven fixtures by 0.02 and
  destroys one is an improvement on the mean and a regression for one language's customers. The gate
  already reads the minimum; the ablation report must be read the same way.
- The comparison names a `config_hash`, not "the previous version". Versions are immutable and
  content-addressed precisely so a comparison stays meaningful months later; "the previous one" is a
  pointer that moves.

### 🚫 What does not count as an ablation

- Changing an axis and reading the mean.
- Changing two axes because "they go together". They may well go together in the shipped
  configuration — that is a reason to ship both, not a reason to measure both at once.
- Re-running the same definition and comparing to itself. The model is not deterministic across
  provider-side revisions (D2 says so at length); a delta of ±0.03 between two runs of one
  `config_hash` is noise, and mistaking it for signal is how an axis gets credit for nothing.
- Comparing against a definition that never passed its rehearsal. Its numbers exist; they are not a
  baseline anybody shipped.

---

## 2. Q4 — the floors, and the first measurement

### The floors this build ships with

| Floor | Value | Why asymmetric |
|---|---|---|
| Precision | **0.90** | A WRONG edge actively misleads eval scope, proposals and cost attribution. |
| Recall | **0.70** | A MISSING edge degrades to today's behaviour, which is the sparse graph P30 exists to improve on. |

They are `DefaultMinPrecision` / `DefaultMinRecall` in `internal/herosagent/rehearsal.go`, as constants
rather than defaults buried in a constructor, so that moving them is a visible edit.

🔴 **These are design.md's proposed starting point, not a measurement.** The design says the number
"must come from measurement before activation, not before build". A model has now been measured against
this set (§2.1) and it did **not** pass, so no definition has been activated and the floors are still
the proposed ones. They will be set from a measurement that supports them, not from this one.

### 2.1 The first live runs — `15f6d8efc399`, gpt-4o, one axis unchanged between them

Both runs are the same `config_hash`: same prompt, same model, same fixtures. What changed between them
is the **harness**, and that is the only reason they are compared here at all — an ablation compares
axes, and this pair is a bug fix, reported as one.

| fixture | truth | run 1 · P/R | run 2 · P/R | |
|---|---|---|---|---|
| `go_chain` | measured | **0.00 / 0.00** (tp 0, fn 2) | **1.00 / 1.00** (tp 2) | the fixture was unanswerable — see below |
| `py_linear_chain` | declared | 1.00 / 1.00 | 1.00 / 1.00 | |
| `py_fanout_no_merge` | declared | 1.00 / 1.00 | 1.00 / 1.00 | |
| `py_independent_calls` | declared | 1.00 / 1.00 | 1.00 / 1.00 | |
| `java_svc`, `rust_svc` | declared | 1.00 / 1.00 | 1.00 / 1.00 | |
| `kotlin_svc` | declared | 0.00 / 1.00 (fp 1) | 1.00 / 1.00 | 🔴 **noise, not the fix** |
| `python_triage` | declared | 0.00 / 1.00 (fp 1) | 0.00 / 1.00 (fp 1) | genuine over-proposal |
| `typescript_svc` | declared | 0.00 / 1.00 (fp 1) | 0.00 / 1.00 (fp 1) | genuine over-proposal |
| **verdict** | | **failed**, worst P 0.00 | **failed**, worst P 0.00 | activation refused both times |

🔴 **`kotlin_svc` moved and nothing was done to it.** Two runs of one `config_hash` produced a different
answer on an empty-truth fixture, which is §1's own rule about re-running a definition and comparing it
to itself — except the swing is a whole fixture rather than ±0.03. Read it as the size of the noise on
this set, and do not read it as evidence for the change above it. `go_chain` is the claim being made
here, and it is the one that moved for a reason a test can state.

🚫 **Not tuned until green.** The gate refuses this definition, and the refusal is the correct outcome:
one wrong edge on an empty-truth near-miss is precision 0.00 by the rule those fixtures exist to apply.

### 🔴 The second defect this measurement found: a fixture that could not be answered

`go_chain`'s ground truth is the Go frontend's own edges (task 5.2). D3's fence 1 removes every edge a
frontend established from the residue — and the residue is the only thing HEROS is ever shown. Those two
sentences are each correct and together they made the one fixture that measures **edge-finding against a
measurement** unanswerable: the agent was asked for two edges it was structurally forbidden to propose,
and scored 0.00/0.00 whatever it said.

The number read as a model result. It was a harness result, and it was reported against the model.

The fix is a **held-out answer**: `Rehearsal.Run` removes the fixture's true edges from the IR it shows
the agent, so the pairs land in the residue and the question becomes askable. That is not a concession
to get a pass — it manufactures on a Go tree the condition every other language is in permanently, a
graph whose dependencies the frontend did not resolve, which is the product's actual claim. The ground
truth stays MEASURED, because what was withheld was measured. `Score.HeldOutEdges` records the count on
the stored report, so a held-out measurement can never be read as a finding over an untouched graph.

The fence is `TestEveryFixtureAnswerIsProposableInItsOwnResidue`, and it asserts the property rather than
the instance: **every** fixture's true edges must appear in the residue that fixture is scored against.
It also asserts that `go_chain`'s answer is *not* proposable without the hold-out, so the hold-out cannot
quietly become decoration. Nothing that existed could see this — `TestAPerfectAgentPassesAndCanBeActivated`
least of all, because its oracle answers with the truth directly and never passes through the write fence
a real runner applies.

### What HAS been measured: the calibration set itself

Task 5.9 asks for numbers from the first measurement. The first measurement available without an
activated agent is of the **set**, driven by three synthetic analysers. It is worth recording because it
established what the set can and cannot detect — and it found a defect.

| Analyser | Passed | mean P | mean R | worst P | worst R |
|---|---|---|---|---|---|
| oracle — answers with exactly the truth | ✅ | 1.00 | 1.00 | 1.00 | 1.00 |
| connect-everything — an edge for every candidate pair | ❌ | 0.30 | 1.00 | **0.00** | 1.00 |
| emit-nothing — never proposes an edge | ❌ | 0.78 | 0.78 | **0.00** | **0.00** |

Read the two failures, because they are the two failure modes the set exists to separate:

- **connect-everything** dies on PRECISION, at the near-misses. `py_independent_calls`, whose true edge
  count is zero, scores 0.00 — which is exactly the sentence in design.md: "an agent that emits a
  complete graph over a repository with no dependencies scores zero precision on a case that exists for
  that purpose."
- **emit-nothing** dies on RECALL, at the fixtures with a non-empty truth. Note its mean precision of
  0.78: on a mean-gated set it is not far off passing, which is the argument for the minimum in one
  number.

### 🔴 The defect this measurement found

The first calibration set **could not fail `emit-nothing`**. It pointed at
`internal/discovery/testdata/samplerepo` as its measured Go fixture, and the Go frontend resolves **zero
edges** for that tree — so all nine fixtures had an empty true graph, and an agent that proposes nothing
ever scored a perfect 1.00/1.00 on every one of them and passed the gate.

That is task 5.7's vacuity one level up: not an empty *set*, but a set of *empty answers*. Nothing in the
harness could see it, and reading the fixture list would not have shown it either — the fixtures are real
trees with real call sites, and only running the frontend over them reveals that none carries an edge.

The fix is `testdata/fixtures/go_chain`: a purpose-built Go tree whose intra-procedural data-edge rule
actually fires (`x := <call A>` then `<call B>(… x …)`), giving a two-edge chain plus an unrelated call
in the same package. `TestTheSetCanMeasureEdgeFinding` is the fence, and it asserts both halves — that
some fixture has a non-empty truth, and that an agent emitting nothing fails.

### When these numbers are replaced

The first real activation. At that point:

1. Run the rehearsal against the candidate definition.
2. Record the per-fixture precision/recall here, per language.
3. Set the floors from what the measurement supports, keeping the asymmetry.
4. Note any fixture whose floor had to be relaxed **and why** — a floor relaxed without a reason is a
   floor that keeps being relaxed.

---

## 3. Known gaps in the set

Stated rather than left to be discovered, per §11.5's rule against silent deferral.

- **Five of the seven supported languages carry a rich fixture and no near-misses of their own.** The
  three named near-misses are Python. The properties they test — sequence is not routing, fan-out
  without a merge is not parallelization, proximity is not dependency — are properties of the GRAPH
  rather than of the language, so an agent that connects two independent Python calls connects two
  independent Rust ones. Per-language near-misses are still worth adding; adding one is adding a
  directory and a row in `calibrationSet`.
- **Only one fixture has a MEASURED ground truth**, and it can only ever be Go: the other six frontends
  are syntactic and emit no edges, which is the problem HEROS exists to address. Every non-Go answer in
  the set is hand-declared, and `Score.Truth` records which is which so a report cannot present one as
  the other. That fixture is also the only one whose answer has to be **held out** to be askable at all
  (§2), and holding it out is what makes it a Go tree standing in for a syntactic frontend's output
  rather than a Go tree as the Go frontend really sees it.
- **Six of the nine fixtures have an EMPTY truth, and one of those swings between runs.** `kotlin_svc`
  flipped between the two runs in §2.1 at one `config_hash`. A set where a single spurious edge takes a
  fixture from 1.00 to 0.00 is doing its job on precision and is noisy in exactly that way; a floor set
  from one run of it would be a floor set from a coin flip. Repeat runs before moving a floor.
- **No fixture exercises a control edge.** The Go frontend's data-edge rule is intra-procedural and
  produces `data`; a control-edge fixture needs a framework graph, which is a different reader. An agent
  that proposes `control` edges is therefore measured on precision but not on its ability to tell the
  two kinds apart.
