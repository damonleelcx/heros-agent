# Language Coverage & the Refusal Contract

| Field | Value |
|---|---|
| Status | Draft (reviewed alongside P13 13d / P14 14d / P15 15e / P16 16d) |
| Created | 2026-07-28 |
| Updated | 2026-07-28 |
| Related | PRDs [P13](../prd/P13-prompt-model-optimization.md) · [P14](../prd/P14-skills-tools-optimization.md) · [P15](../prd/P15-workflow-wiring-optimization.md) · [P16](../prd/P16-context-strategy-optimization.md); OpenSpec `changes/archive/2026-08-01-p13-prompt-model-optimization/specs/language-coverage/spec.md` and the four per-axis coverage capabilities; [ADR-001](../adr/ADR-001-source-transformation-apply-model.md), [ADR-003](../adr/ADR-003-multi-language-apply-and-verification-strength.md); [`p14-materializer-coverage.md`](p14-materializer-coverage.md) |

---

## 0. Summary — the decision in one sentence

> **Every language discovery finds, the apply path either changes or refuses by name — and "we only
> built Go" is a cause with an owner, not a category.**

The transform engine registers seven languages. What each can *apply* differs per axis, and the spread is
real: model and prompt materialize in four, context and wiring in two, skill binding and tool pruning in
one. The spread is not the defect. Three things about how it is *stated* are:

1. **Absence is being used as a value.** Coverage tables list what works. A language that does not appear
   renders on every surface as *not applicable* — a claim about the customer's code — when it means *we
   have not built this yet*, a claim about our backlog.
2. **Three different failures share one sentence.** *No language can carry this change*, *this call site
   cannot carry it*, and *this language has no materializer* are answered by three different people.
   Collapsing them sends the reader to the wrong one.
3. **The language question is being asked first.** The most common real refusal — on a real repository,
   in the most common language — is about the customer's own call site and would be unchanged by any
   rewriter we ship. Naming the language there is true and useless.

The fix has two halves: make coverage a **total function**, and make the refusal **say which of the three
it is, most specific first**. Then close the gaps — which turns out to be rows, splitters, resolvers and
frontend fields, not seven separate inventions.

---

## 1. Original requirement (plain language)

The ask: **"skills / tools refuses — Go-only materializer — it should support all languages as well."**

The factual correction that shaped the scope: it is not one axis. Coverage as it stands is

| Axis | Materializes in | Source of truth |
|---|---|---|
| model / prompt | go, python, typescript, javascript | `argumentForms` (`rewrite_span.go`) |
| context policy | go, python | `spanContextMaterializers` (`contextmaterialize_span.go`) |
| wiring transposition | go, python | `statementResolvers` (`wiringswap.go`) |
| skill binding | go, and only `anthropic` / `openai` | `toolValueForms` (`skillbind.go`) |
| tool pruning | go | `rewritetools.go` + the frontends' tool split |

So "all languages" is a program across four PRDs, and the packaging follows the precedent set by
`authored-change`: the contract is defined **once** in P13 as capability `language-coverage`, and each
axis adds a thin capability that references it and never restates it.

---

## 2. The coverage model

**Coverage is a total function over (axis × registered language × form).** Every cell has a value; the
values are:

| Value | Means | Who closes it |
|---|---|---|
| `materializes` | the axis emits a change in this cell | — |
| `refuses: not-expressible-at-a-call-site` | the value does not exist until run time (a summarized context, a retrieved chunk set, a routing decision that belongs at the gateway) | nobody — this is the honest dead end |
| `refuses: call-site-cannot-carry-it` | this source cannot express it: unpacked argument mappings, a run-time-assembled tool list, no registry row locator, a binding the frontend did not record | the customer's engineer |
| `refuses: no-materializer-for-this-language` | the platform has not landed the artifact, **named** | the platform's backlog |

**The form matters as much as the language.** A cell is (language, provider, SDK generation) for a tool
value; (language, registry row) for an argument binding; (language, policy) for a context selection;
(language, statement form) for a wiring move. *"Go is supported"* is not *"every Go call site is
supported"* — a Go call site whose provider has no declared spelling refuses today, and always did.

**Absence is not a value.** A generated check enumerates the registered language set and fails while any
axis has a missing cell, so adding a discovery frontend cannot ship half-covered and silent.

---

## 3. The refusal contract

**Order: the change → the registry row → the call site's source → the language.** The most specific true
cause is reported; a language-level cause is never reported while a more specific one is true.

This is not a style rule. It is the correction P16 already had to make after shipping the context refusal
the other way round, and the evidence is `hermes-agent`: 41 Python nodes, 30 of which pass `**kwargs`.
Those call sites have no written argument to rewrite and no written message list to select among — and
they would still have none the day a Python materializer lands for every axis. The sentence that helps
their author is about the unpacking. The sentence about Python is true, useless, and costs them a quarter
of waiting.

**Causes are identified, not narrated.** Each carries a stable identifier so the console, the CLI and the
tests branch on it rather than on prose.

**A refusal names the legitimate path where one exists, and says so plainly where none does.** A
`no-materializer` cause names the missing artifact. A `call-site-cannot-carry-it` cause points at what
discovery *did* find. A `not-expressible-at-a-call-site` cause does not borrow "not yet".

---

## 4. What each axis actually needs

The load-bearing discovery is that the gaps are **not** seven rewriters per axis.

**P13 — model and prompt.** Kotlin has named arguments and a working analyzer and still refuses, because
every Kotlin registry row declares no argument locator: those SDKs bind the model on a **builder** at
construction. Java and Rust have no named-argument form at all and bind in a builder chain or a request
struct. One missing concept explains all three — the engine points at *the argument a call site wrote*
when it should point at **the binding site the program wrote**, of which a named argument is one form, a
builder-chain call a second, and a request-value field a third. Generalize the locator once and Kotlin is a
registry row; Java and Rust are a frontend extraction plus rows.

**P14 — skills and tools.** Two mechanics, two blockers, two packages:
- *Binding* is blocked on a **spelling** — this provider's SDK in this language at this generation. The
  *shape* already comes from the skill's sealed schema and is language-independent, so a new language is a
  set of rows, not a second source of truth about what a bound skill means. A row is admitted only with a
  **build gate**: this axis exists because a wrong tool schema *compiles*.
- *Pruning* is blocked in `internal/discovery`, not `internal/transform`. Deletion at the syntactic floor
  is the easiest edit in the engine; what is missing is the **tool split** — the frontends record no tools
  and no declaration locations, so there is nothing to prune against. Letting the span pruner *infer* which
  unnamed element is which tool is refused outright: it deletes the wrong element in a diff that parses.

**P15 — wiring.** Five statement resolvers, and nothing else. The plan, the edge-set check, the coherence
gate and the permutation invariant stay one neutral path — five languages must not become five dialects of
one safety property. The subtlety recorded as a one-way door: Go and Python place statements on whole
lines, so today's invariant is "the same lines, permuted"; a TypeScript chain or a Rust
expression-statement can put two nodes on one line, so the invariant generalizes to the **resolved
statement multiset** with the line rule kept as the stricter aligned case — decided before the first
non-line-oriented resolver lands, rather than conceded by whoever writes it.

**P16 — context.** Five list splitters, and one rule that must not bend: **retention is not per language**.
Which turns a policy retains *is* the policy, so the shared selection code decides it everywhere and a
splitter answers only "what are the written elements of this list". Same for the drop record — produced by
the shared path, language-free, byte-comparable across languages, so a newly covered language cannot
delete turns without recording them.

---

## 5. Invariants that hold across all four

1. **No gate is weakened to reach a language.** No guessed SDK spelling, no skipped reparse assertion, no
   inferred binding site, no per-language fast path around an invariant. Reach is worth nothing if what
   arrives is wrong — L1/L2 over L8, without exception.
2. **A row is a proof.** A coverage entry requires a test that emits the change in that cell and asserts
   the result parses, plus the build gate wherever source is constructed. A row admitted on a document is
   not a row.
3. **One coverage source.** Transform, preflight, console, CLI and every published table read it, asserted
   in **both** directions — a surface may not offer what the engine refuses, and the engine may not
   materialize what no surface offers.
4. **The same override means the same thing everywhere.** Spelling is per language; meaning never is.
   Asserted over a shared fixture — seven dialects of one spec is how a measurement stops measuring.
5. **Coverage growth moves nothing.** Adding a form, a row, a splitter or a resolver leaves every
   previously materializable change byte-identical and every `config_hash` unchanged; golden vectors
   reproduce.
6. **Coverage is identical on every plan.** No tier, role, flag or setting materializes a refused cell, and
   a coverage gap is never presented as something a contract can move.
7. **The offline table is versioned and names its version in a refusal**, so a CLI/console disagreement is
   a one-line diagnosis.

---

## 6. What this explicitly does not promise

- **Cross-SDK or cross-provider translation.** Coverage means stating the *same* configuration in each
  language's own SDK. Rewriting one SDK's call into another's is not in scope, and the cross-provider
  refusal (ADR-002) is unchanged by any amount of coverage.
- **One patch across two languages.** One patch, one language, one verifier. A polyglot workflow refuses
  by name, listing the languages found.
- **Applying a change to a call site that cannot express it.** An SDK that carries its tools inside an
  opaque body, a call site that assembles values at run time, a policy whose content a model produces —
  all stay refused after every row above has landed, in every language including Go.
- **A schedule.** The waves are `[ ]` implementation tasks. What this contract guarantees is that while
  they are in progress, every surface tells the truth about which cells are done.
