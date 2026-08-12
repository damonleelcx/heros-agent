# P30 — tasks

**Q1 and Q2 are decided** (PRD §14, damon): platform-side HEROS spends the **platform's** credential and
the platform stores no customer provider key under any placement; the default placement is **`disabled`**.
No workstream is blocked.

Workstreams 1 and 2 still ship first, because they are correct whether or not HEROS is ever configured.
🔴 Note the consequence of `disabled` by default: **nothing fills on deploy**, so task 10.13's acceptance
run must include the enablement step explicitly — an acceptance that silently depends on a default is an
acceptance that stops proving anything the day the default changes.

---

## 1. Surface honesty — ships first, depends on nothing in HEROS

- [x] 1.1 `graphview.go`: emit a `no_topology` reason on a graph with nodes and zero edges, sourced from
      the discovery diagnostics (which language frontend ran, and that it is syntactic) — not
      hand-written in the view.
- [x] 1.2 `graph/page.tsx`: when edges are zero, render the statement and the cause instead of the
      positional drawing. Keep the node list as a table.
- [x] 1.3 `graph/page.tsx`: fix the `llm_calls === 0` copy. Three cases — zero labels, full coverage,
      partial — with three sentences. Assert the rendered string in a test.
- [x] 1.4 `graphview.go`: replace the single "not yet classified" reason with the four distinct causes
      (no signature matched / model not consulted / model abstained / no topology to match against).
- [x] 1.5 `hostedproposals`: carry `proposalgen.State` through to `ProposalSurface`. Reserve `empty` for
      "a pass ran and found nothing"; add `never_analysed`.
- [x] 1.6 Store the last pass's timestamp, state and sentence (migration 0044). Restart-durable.
- [x] 1.7 `proposals/page.tsx`: render the state's sentence and its implied action. Delete the
      unconditional "Nothing is pending."
- [x] 1.8 Move the generate action to the flat path `/api/v1/proposal-generations`, identifier in the
      body. Serve both shapes for one release (expand-contract).
- [x] 1.9 Publish the flat path as an `Exact` rule in `deploy/k8s/overlays/prod/ingress.yaml`. 🚫 No
      `Prefix` rule under `/api/v1/workflows/`.
- [x] 1.10 Extend the edge-reach fence to cover the new path. Verify it goes **red** when the Ingress
      rule is removed.
- [x] 1.11 `proposals/page.tsx`: add the trigger, wired to the flat path, updating without a reload.
- [x] 1.12 New route `/app/workflows/{id}/evalset`: list case id, family, oracle kind, indecisive flag.
      Deep-linkable, not a modal.
- [x] 1.13 Surface `evalgen.CoverageReport.Vacuous()` by name, not as a count.
- [x] 1.14 Board: link `n_cases` to the new route; assert the count and the list agree.
- [x] 1.15 Fence: the eval-set list count must equal `n_cases`, and a mismatch is an error, not a render.

## 2. Provenance and the inference store

- [x] 2.1 Migration 0045: `provenance TEXT NULL` on IR fact storage, both dialects. Additive,
      down-migratable. 🚫 No back-fill — NULL reads as `legacy`.
      ⚠️ **Deviation, agreed with damon:** PostgreSQL only. There is no second dialect to write to —
      `db/migrations/README.md` and migration 0042's header both argue that these tables exist in one
      dialect, and the SQLite store in `internal/db/db.go` is the retired agent's dev ledger (API keys,
      registries, episodic memory). It holds no IR fact and no part of this domain, so a copy there
      would be a second schema nothing reads. Stated in both migration headers. Carried to §11.5.
- [x] 2.2 `discovery`/`patternclassifier`: stamp `frontend` and `detector` on emission. One place each.
- [x] 2.3 Reader: NULL → `legacy`, distinguishable in a query from `frontend`.
- [x] 2.4 Migration 0046: `heros_agent_version`, `heros_inference`, `heros_abstention`, `heros_spend`
      (see design.md). Both dialects, real migration in tests — 🚫 no inlined `CREATE TABLE`.
- [x] 2.5 `UNIQUE (workflow_id, source_revision, agent_config_hash)` and the conflict path in the writer.
- [x] 2.6 Timestamps as `int64` ms; no timestamp literals in SQL.
- [x] 2.7 Down-migration test: every previously readable IR stays readable.
- [x] 2.8 Fence: a fact written without provenance fails the writer's own check.

## 3. Agent definition

- [x] 3.1 `internal/herosagent`: `Definition` as a Variant Spec over the six axes; resolve against the P2
      registries; `config_hash` via `internal/confighash`.
- [x] 3.2 Refuse a wiring (P15) override at publish, naming the axis.
- [x] 3.3 No mutation API. `Publish` only; content determines identity.
- [x] 3.4 Model ref validated against `internal/adminstore` model registry; refuse an unregistered model.
- [x] 3.5 Credential as a provider **reference**, resolved through `providergateway`'s `Secrets`.
      🔴 No field, column, log line or response may carry a key value. Fence it.
- [x] 3.6 Unresolvable reference → `credential_unresolved`, zero provider calls, no provider substitution.
- [x] 3.7 Exactly one active version, enforced in the activation transaction.
- [x] 3.8 Deprecated-model notice on an active definition; do not auto-switch.

## 4. Inference runner

- [x] 4.1 Residue selection from the IR plus the frontend diagnostics. The input type has **no** field
      for a whole-repository pass — NFR1 holds by construction.
- [x] 4.2 `Runner.Infer` through `providergateway`. Static fence against a bare `http.Client` in the
      package.
- [x] 4.3 Output validation against the closed vocabularies: edge `kind ∈ {data, control}`, node ids
      already in the IR, labels in the 20-pattern taxonomy. **Reject, never repair.**
- [x] 4.4 Confidence floor; below-floor output becomes a stored abstention with a reason from a closed
      enum.
- [x] 4.5 🔴 Never emit an edge where a frontend emitted one; never delete one. Test with a fixture that
      pushes toward the conflict.
- [x] 4.6 Labels emitted as `patternclassifier.RegionProposal` so they enter the existing partitioner and
      precedence rule. 🚫 No second arbitration path.
- [x] 4.7 Cache: read-through on `(workflow_id, source_revision, agent_config_hash)`; second request
      makes zero provider calls.
- [x] 4.8 Explicit re-inference, presented as a diff, replacing only on confirmation.
- [x] 4.9 Per-run token and wall-clock budget; exceeding aborts, records the abort, writes no partial IR.
- [x] 4.10 Provider failure → `analysis failed` with the cause. 🚫 Never an empty graph.
- [x] 4.11 Events and error codes from the central enumeration only. Fence literals.

## 5. Calibration and the rehearsal gate

- [x] 5.1 Fixture set per language: at least one repository with a known true graph, plus the
      near-misses (linear chain that is not a router; fan-out with no merge; two calls with no data
      dependency).
- [x] 5.2 Go fixtures use the Go frontend's real edge output as ground truth.
- [x] 5.3 Rehearsal harness: per-fixture precision and recall on **edges**.
- [x] 5.4 🔴 The gate reads the **minimum** across fixtures, not the mean. Report the mean; gate on the
      minimum.
- [x] 5.5 Block activation on failure; name the failing fixture, its language and its numbers.
- [x] 5.6 Store the per-fixture report on the version row.
- [x] 5.7 Anti-vacuity: a fixture set that fails to load **fails** the rehearsal. 🚫 Never passes over an
      empty set.
- [x] 5.8 Ablation protocol documented: one axis at a time; report the delta against the previous
      `config_hash`.
- [x] 5.9 Fill Q4's numbers from the first measurement; record them in the PRD.
      ⚠️ **Recorded in `docs/heros/ablation-protocol.md` §2 rather than the PRD**, and stated there:
      the floors shipped (P ≥ 0.90, R ≥ 0.70) are design.md's proposed starting point and NOT a
      measurement, because no definition has been activated so no model has been measured. What HAS
      been measured is the calibration SET, against three synthetic analysers — and that measurement
      found a real defect (the set could not fail an agent that proposes nothing). Carried to §11.5.

## 6. Operator console — the agent overview

- [x] 6.1 `/agent`: active definition, `config_hash`, rehearsal state, stored-inference count.
- [x] 6.2 Publish flow → resolved diff against the active definition + the resulting `config_hash`,
      confirmed before it happens. An edit resolving to no change says so and creates no version.
- [x] 6.3 New version lands `pending rehearsal`; never rendered as active before the gate. The surface
      always names which definition is actually serving inference.
- [x] 6.4 Rehearsal report with the per-fixture delta against the previous definition; regressions marked.
- [x] 6.5 `/agent/spend`: per-tenant meter, estimate-labelled, `unpriced` never `0`.
- [x] 6.6 Cap editors, per tenant and fleet-wide.
- [x] 6.7 Placement column per tenant, distinguishing "defaulted" from "explicitly set to `disabled`".
- [x] 6.8 Kill-switch wiring for HEROS through the existing durable store.
- [x] 6.9 Nav registration — all three pieces, or the slot disappears silently.
- [x] 6.10 Every axis shows **set / defaulted / not in effect**; "not in effect" carries its reason.

## 6b. Operator console — the six axis editors

Each binds to its existing vocabulary. 🚫 No axis is a text box. All params validate at **save**.

- [x] 6b.1 **Prompt**: template body editor; slots parsed and derived, not typed. Save registers a new
      content-addressed prompt version. A bound slot missing from the template is refused by name.
- [x] 6b.2 **Model**: primary model from the operator registry; deprecation shown, 🚫 never auto-switched.
- [x] 6b.3 **Credential**: provider reference selector + live resolution state. 🔴 No key field anywhere —
      fence the absence, not just the behaviour.
- [x] 6b.4 **Skills**: selection from registered skill versions with `impl_handle` and compiled schemas.
      A skill whose schema does not compile is not selectable. 🚫 Remote `$ref` rejected, not fetched.
- [x] 6b.5 **Tools**: selection from `toolindex` showing tenant scope, description, `risk_tier`, approval.
      Unapproved is not bindable. Scope always displayed — `_global` and tenant-scoped tools of the same
      name are different bindings.
- [x] 6b.6 🚫 **Tools**: a tool declaring outbound network access is not bindable, and the refusal is not
      overridable from the console. Test it.
- [x] 6b.7 **Context**: one of the seven named policies; params form generated from its `ParamsSchema`;
      failures named by policy and parameter.
- [x] 6b.8 **Memory**: one of the five named strategies; params validated; set version recorded.
- [x] 6b.8a 🔴 **Memory availability** on the FR1.7 rule: `none`, `scratchpad`, `entity-memory` need no
      host service. `summary-buffer` needs a `Summarizer` — a **model call**, so surface the second spend
      line. `vector-recall` needs an `Embedder` **and** a pinned `embedding_ref`; refuse the save without
      one. 🚫 Never degrade — a truncating summary-buffer is scratchpad under the wrong `config_hash`.
- [x] 6b.8b 🔴 **Memory scope**: `SessionID` = the inference id. Discard entries when the inference
      completes. 🚫 Memory never spans inferences, workflows or tenants. This is the cross-tenant fence
      **and** what keeps D2's three-part cache key honest — implement it in the runner, not as a rule.
- [x] 6b.8c Console states the trade plainly: HEROS does not learn across analyses; a repository analysed
      twice starts cold. A deliberate scope, not a gap.
- [x] 6b.9 **Harness**: one of the five named strategies; `max_turns` required for multi-turn, ≤
      `MaxTurnsCeiling` (16); a retry budget that could multiply turns past the ceiling is refused with
      the same reasoning.
- [x] 6b.10 🔴 **Harness availability**: compute it from the host services the HEROS runner actually
      supplies. `single-shot` and `reflexion` need none. `react-loop` needs a `ToolInvoker`,
      `plan-execute` a `Planner`, `critic-loop` a `Critic`. Show each as available or unavailable **with
      the service it needs** — do not hide the unavailable ones.
- [x] 6b.11 🔴 Refuse an unsupplied-service selection at **save**, naming the service and what supplying
      it means. 🚫 Never offer a neighbouring strategy as a substitute.
- [x] 6b.12 **Critic model** (only if `critic-loop` is made available): selected from the operator
      registry, resolving its own credential reference, with its spend metered and attributed separately.
      A second model is a second cost and a second credential; make all three visible.
      ⚠️ **Deviation — CONDITIONAL AND NOT TRIGGERED.** `critic-loop` is unavailable because the runner
      supplies no `Critic` (D11 computes availability from `RunnerHosts`), so no critic model is
      selectable and the "only if" this task opens with excludes it. The three things it requires be
      VISIBLE are built and asserted: separate `CriticModelRef`/`CriticCredentialRef` fields so spend
      attributes separately, a refusal when a critic model is bound without its own credential, and the
      `SecondSpendLine` marking on the strategy. What is not built is the runner-side critic itself.
      Carried to §11.5.
- [x] 6b.13 **Wiring**: rendered fixed and read-only.
- [x] 6b.14 Record the **set version** of every closed vocabulary the definition references.

## 7. Customer-side runtime and parity

- [x] 7.1 CLI runs the same definition with the customer's credential on their machine.
      `heros analyse -ir <path>` — a NetCommand, because `internal/cli` links no network stack and
      `herosagent` reaches a provider. It fetches the active definition from the platform
      (`GET /api/v1/agent-definition`, published `Exact`), runs the SAME `Runner` under
      `NewCustomerRunner`, and resolves the provider key from THIS machine's secrets source.
      ⚠️ **Consequence recorded:** D1 leaves the definition "operator-only in P30", which governs
      SURFACES. Placement `customer` necessarily puts the rendered prompt on the customer's machine —
      there is no way to execute a prompt without it being there — so the endpoint serves it ONLY to a
      `customer`-placed tenant. `platform` and `disabled` get their placement and nothing else.
      Carried to §11.5.
- [x] 7.2 One context-assembly code path shared by both placements. 🔴 This is the anti-skew fence.
      `AssembleModelInput` is the only way to obtain bytes: `ModelInput` is opaque, its wire struct is
      unexported, and `Bytes()` refuses a value the assembler did not produce — so a second runner
      cannot build one field by field with the compiler's blessing.
- [x] 7.3 Submit results through P29's structure ingest carrying provenance, confidence and
      `agent_config_hash`. 🚫 No second transport.
      The wire calls provenance `author` (`frontend` | `heros`): `IREdge.Provenance` is already
      `internal/linkage`'s evidence-STRENGTH vocabulary, and overloading it is what `discovery/author.go`
      argues against at length. Four new allowlist rows, all identifiers/closed sets/numbers. No contract
      bump — every field is `omitempty`, so a pre-P30 CLI's payload is byte-identical.
- [x] 7.4 Ingest applies the confidence floor; refuses an unknown `agent_config_hash` by name.
      Plus: a `heros` fact naming no hash, a hash naming a non-ACTIVE version, an out-of-vocabulary
      `kind`, an unknown node, and D3's fence-1 (an agent edge over a pair the same payload's frontend
      edges establish) — every one recorded as an abstention with the closed cause the runner would use.
- [x] 7.5 Placement enforcement: `customer` runs nothing platform-side; `disabled` refuses ingest.
      `Host.MayRun(Placement)` is one function both runners call. On the Runner it sits AHEAD of the
      cache read, so a placement that changed after an inference was stored does not keep being answered
      from the row it left behind; `ReInfer` carries its own copy because it reaches the model without
      passing through `Infer`. Migration 0047 gives placement a durable home — it had none, so nothing
      could be set.
- [x] 7.6 CI parity test: equal **edge sets** on a fixture at one `config_hash`. Narrative not compared.
      The fake answers FROM the assembled context rather than returning a constant, so divergence
      changes the edge set; its narrative names the host, so a version of the test that quietly began
      comparing prose would fail.

## 8. Customer console surfaces

- [x] 8.1 Composition panel above Patterns: patterns present, node coverage, unlabelled remainder,
      per-pattern provenance.
      Computed in `patternclassifier` from the assembled view. 🚫 It DISPATCHES NOTHING —
      `TestTheCompositionIsNotADispatcher` parses the whole tree for a `MetricSetFor`-family call whose
      argument comes off a Composition, because the failure is a convenient edit somebody makes three
      packages away next year.
- [x] 8.2 Narrative rendered as `assessed`, visually distinct; absent (not fabricated) when HEROS is off.
      The conditional ends `: null` and the fence extracts it by BALANCING BRACES — the regex version
      passed against a page that had a generated-prose else branch, because a non-greedy match ran on
      and found a later `) : null}`.
- [x] 8.3 Inferred edges visually distinct, reusing the existing model-labelled-region treatment; legend
      updated.
      Reuses `--llm`, the console's one "a model was consulted" channel, plus its own dash and a smaller
      arrowhead — two channels, so the distinction survives greyscale. 🔴 Authorship beats KIND: an
      inferred control edge is drawn inferred, not control. The edge TABLE carries the column too, so a
      reader using the drawing's text alternative does not lose it.
- [x] 8.4 🔴 Mixed counts report total **and** inferred portion. No undifferentiated number.
      A node covered by both a rule label and an agent label is MEASURED, so the parts sum to the whole.
- [x] 8.5 Four states kept distinct everywhere: `measured`, `inferred`, `not analysed`, `unavailable`.
      `not_analysed` is neutral and `unavailable` is warn: one is the default every organization has
      before an operator turns analysis on, and styling it as a fault would report a deliberate
      configuration as a problem on every customer's first visit.
      ⚠️ A DEFECT found by reading the rendered page: `not_analysed` is reached two ways — the agent is
      off, or the agent is on and this workflow has nothing from it — and one sentence covered both, so
      a `platform`-placed organization was told "Analysis is off for this organization". Two sentences
      now, the same shape task 1.3 gave `llm_calls_note`.
- [x] 8.6 Placement attribution on the graph.
- [x] 8.7 Analysis action where available; a stated reason where not.
      A `customer`-placed tenant is still offered an ACTION — the reader can run it even though the
      platform cannot — and the panel renders no control at all, because an action that fails on press
      is worse than an absent one with a sentence.
- [x] 8.8 Panel-level degradation on HEROS failure. 🚫 Never a full-screen error.
      Every path in `agentPanelFor` returns a panel. A failed NARRATIVE read does not even do that: it
      costs a paragraph, and downgrading the panel would hide real inferred facts to report the loss of
      their commentary.
- [x] 8.9 Eval-set surface reports graph nodes no case exercises; says so where attribution is
      unavailable rather than reporting zero.
      §1 built the list; the missing half was the third state. With no coverage report the list is
      empty, which is indistinguishable from every node being exercised — the reassuring reading. The
      absence now joins `Unattributed`, which is the mechanism that already exists for it.
- [x] 8.10 English only; no improvised styling — every token has an existing anchor.
      Two new classes (`assessed`, `edge--inferred`) and both resolve through `--llm`.

## 9. DevOps

- [x] 9.1 `/readyz` reports `disabled | ready | credential_unresolved | capped`, resolved from what
      inference actually uses — 🚫 not asserted from configuration.
      The credential is RESOLVED through the same `providergateway.Secrets` the runner calls at use, so
      a reference pointing at a secret nobody provisioned reports the fault instead of looking healthy.
      🔴 The entry is top-level, NOT in `components`: every entry there is a gate, and none of the
      agent's states may take a deployment down — `disabled` is the default and `capped` is a ceiling
      working. A fifth state, `no_active_definition`, separates "nobody enabled" from "somebody enabled
      and there is nothing to run".
- [x] 9.2 Caps enforced **before** the provider call; reaching one emits an event.
      Migration 0048 gives ceilings a home (the fleet cap under a `tenant_id = ''` sentinel; a zero is
      refused at the schema because it is ambiguous between "spend nothing" and "no limit"). The check
      sits AFTER the cache read — a stored answer costs nothing, and refusing it would make the ceiling
      an availability limit rather than a spend limit — and BEFORE the residue and the model.
- [x] 9.3 Off by default fleet-wide; enablement per tenant.
      `AgentSpendSource` was nil on every deployed path, so §6's placement column and cap editors wrote
      to nothing. Wired through an adapter in `adminlaunch`, the one package that sees both vocabularies.
- [x] 9.4 Rollout: internal tenant → one design partner → opt-in → default-on, rehearsal gate between
      each. 🚫 No stage verified by hand.
      `Advance` reads the evidence itself — it counts the placement table, reads the active definition's
      rehearsal state and checks the fleet ceiling — and names the ONE precondition that failed.
      🔴 The gate is between EVERY pair, not only the first: a republished definition that reached one
      rung has its own rehearsal to pass, or an unrehearsed one reaches the fleet a rung at a time.
      Retreating is never gated. `make agent-rollout` asks the question; it changes nothing.
- [x] 9.5 Disabling returns every surface to rule-derived facts; stored inferences retained and marked
      stale, not deleted (pending Q5).
      Q5's assumed answer implemented rather than deferred — deferring means the first tenant to disable
      gets delete-by-omission. The mark is written when the placement changes and 🚫 is NOT computed at
      read time, which would erase the fact that a gap existed the moment somebody re-enabled.
- [x] 9.6 Repeatable commands into the Makefile — 🚫 no throwaway shell.
      `make agent-status` (reads the LIVE signal, exits non-zero only on a fault), `make agent-rollout`,
      `make agent-drills` — which defeats each of seven fences, confirms it goes red, and restores.

## 10. QA — every fence proved red before it is trusted

- [x] 10.1 Frontend edge wins over a HEROS proposal → revert the fence, confirm red, restore.
- [x] 10.2 Go fixture IR byte-identical with HEROS on and off.
      Bytes, not edge sets: an edge-set assertion passes while the agent adds a field, reorders a slice
      or stamps an author. The model is handed proposals over BOTH established pairs, so a pass is not
      the absence of an opportunity, and the refusals are asserted as abstentions.
- [x] 10.3 Zero provider calls on a fully rule-covered repository — assert the **count**, not "no error".
- [x] 10.4 Cache hit asserts zero provider calls and an identical body.
      The body comparison is over the FACTS, marshalled — `ProviderCalls` and `Usage` are correctly
      different on a hit — and the fixture is asserted non-thin so the identity is not vacuous.
- [x] 10.5 Provenance survives aggregation on a mixed graph.
      Aggregation is where authorship is most likely to be lost, because aggregating is the operation
      that discards per-item detail — and a summary reporting a mixed graph all one way looks tidy.
- [x] 10.6 Unresolvable credential → `unavailable`, zero calls.
      The SURFACE half was missing: an enabled agent that cannot reach its provider contributes nothing,
      so the panel read `not analysed` — false, and the reassuring one of the two readings. It now reads
      the readiness answer rather than re-deriving it.
- [x] 10.7 Placement parity on a fixture.
- [x] 10.8 Concurrent double-submit against **real Postgres** writes one row. A unique index is invisible
      to a test that never contends.
      Eight writers released from a barrier, each with its OWN inference id — a shared id would let the
      primary key do the work and never test the UNIQUE index. 🔴 Running it found TWO defects in
      migrations 0047/0048 that no unit test could reach: see the deviation note below.
- [x] 10.9 Provider fake is **recording with injectable errors**. 🚫 No silent-return stub.
- [x] 10.10 ≥30% of new test functions target error and boundary paths.
      Measured, not eyeballed: 59 of 105 (56%). The first classifier said 91% because it matched prose
      like "never" and "cannot" in comments — a classifier that classifies everything measures nothing.
      Comments are stripped and the phrase list is two entries; symbols alone give 37%.
- [x] 10.11 Injection fixture: a repository whose source instructs the analyser.
- [x] 10.12 Rendered-string assertions for the `llm_calls` copy and the four unlabelled causes.
- [~] 10.13 🔴 Live acceptance on `openclaw` — **3 of 4 layers run; NOT a pass.**
      ⚠️ **Deviation, stated rather than reported green.** `scripts/agent_acceptance.py` is the machine
      form of all four layers and `make agent-acceptance` runs it. Against the local proof stack layers
      1, 3 and 4 PASS — placement explicitly set, the served IR carrying an agent-authored edge whose
      count the composition agrees with, and the rendered page carrying `edge--inferred`, the
      composition panel and the `assessed` mark. Layer 2 — a row in `heros_inference` — did NOT run: it
      needs a platform Postgres and a real provider credential, and the run spends real tokens. The
      runner exits NON-ZERO on an incomplete run and prints "This is NOT a pass", because a partial
      acceptance reported as an acceptance is the failure the four-layer shape exists to prevent.
      🔴 `openclaw/openclaw` is not checked out on this machine; the layers that ran used the local
      fixture stack. Carried to §11.5.
- [x] 10.14 Default-posture fence: a freshly migrated deployment runs **zero** inferences and makes zero
      provider calls until a placement is set. Assert the call count, not the absence of an error.
- [x] 10.15 🔴 Axis-authoring fences, each proved red first — all six, as drills 10.15a–f.
- [x] 10.16 Vocabulary-drift fence: a definition referencing a closed set records that set's version, and
      a stored `config_hash` remains interpretable after the set is versioned forward.
      Plus: a definition recording NO set versions must hash differently from one that does, or a
      pre-P30 row and a post-P30 row collide and one answers for the other.
- [x] 10.17 🔴 No-key fence across all four surfaces — request schema, storage schema, logs, rendered
      output. Auto-discovering: the columns are parsed out of the migration FILES, so a table added
      tomorrow is covered.
- [x] 10.18 🔴 Cross-tenant memory fence, proved red by widening the session scope to the tenant id.
- [x] 10.19 Memory lifetime fence.
      The existing test asserted against a hand-rolled `map[key]string` — a test of a map, in a file
      named for a behaviour. Rewritten against the real `memoryruntime.MemStore`, discarding through
      `Expire(key, 0)`.
- [x] 10.20 Memory host-service fences: an unsupplied summarizer or embedder is refused at **save**; a
      similarity-recall strategy without a pinned `embedding_ref` is refused; neither degrades.

### 🔴 What running the fences found

**Migration 0047 could never have been applied anywhere.** Its own guard queried
`information_schema.constraint_table_usage`, which does not list CHECK constraints at all — so it found
nothing and raised on every run. It was written in §7, reviewed, committed, and only ran for the first
time when 10.8 pointed a real Postgres at it. Fixed to `pg_constraint` joined to the table AND the
current schema, which is 0028's repair applied in advance.

**And then it raised again**, on `information_schema.columns`: that catalog is database-wide, the proof
harness gives every test package its own schema, and a `count(*)` over `table_name` alone counted the
table once per schema. Same class of defect, twice, in the same file — a predicate that asks "does this
name exist anywhere" while meaning "does THIS table have it".

## 11. Documentation and commitments

- [x] 11.1 Fold the delta specs into `openspec/specs/` on deploy.
      Nine capabilities, 48 requirements. The operation headers are dropped per `openspec/AGENTS.md`:
      a folded spec STATES requirements, it does not describe a change.
- [x] 11.2 Operator runbook: tuning HEROS, reading a rehearsal report, arming the switch, caps.
      `docs/runbooks/p30-heros-agent.md`. It opens with the two sentences to have in your head —
      nothing analyses until a placement is set, and the platform never holds a customer key — because
      both are what somebody on call at 2am would otherwise get wrong.
- [x] 11.3 Customer docs: what `inferred` means, which placement applies, how to disable.
      `/docs/concepts/inferred-structure`. 🔴 Writing it tripped the CLI-reference fence: `heros analyse`
      was added to the registry in §7 and `make docs-facts` was never re-run, so the generated CLI
      reference had never listed it. A §7 artifact left unregenerated, found by §11.
- [x] 11.4 Sales boundary card from PRD §9.8 — including the sentences that may not be said.
      `docs/sales/P30-heros-agent-claims.md`. Five, not four — §9.8 carries five rows, and §5 of the card
      adds what is NOT yet true and must not be implied: no definition activated, no measured numbers,
      no price list, the acceptance incomplete.
- [x] 11.5 Update the PRD's DAG and mark anything deferred, with the reason. 🚫 No silent deferral.
      PRD §15, the delivery register: the ordering against what happened, all ten acceptance criteria
      with status, seven deferrals each with its reason, and the two defects the phase's own fences
      found. §12 now points at it, because a DAG is a plan and §15 is the record.
