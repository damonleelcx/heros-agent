# Tasks — P10: Prompt & Model Studio

Two waves. **Wave 10a** = prompt authoring + variable bindings + the studio surface — entirely
ADR-001-compatible, no runtime-path risk, and a complete phase on its own if 10b is cut. **Wave 10b** =
the runtime binding layer (`bound` apply mode, resolution, reconciliation, unverified marking),
sequenced second **deliberately** so its stability surface is isolated.

**Standing constraints.** No new pipeline, no new statistics, no new registry table — the timeline,
diff and impact analysis are **read models over rows that already exist**. Immutability stays
structural: no interface may express mutation of a published version. ADR-002 is untouched —
cross-provider swapping remains refused. Every user-visible behavior is accepted on rendered-browser
evidence per P9, never on a green build.

---

## 1. System Designer — Decide the contracts before anything is generated (10a)

- [ ] 1.1 Decide the **binding document format and location** (PRD §14 Q1). It becomes a public
      contract the moment it ships in a customer repository. Preference: a JSON document plus a
      generated accessor, so values are diffable as data and the accessor is type-checked by the
      customer's own build. Record as an ADR.
- [ ] 1.2 Decide the **`in_scope` extraction depth** (PRD §14 Q2) — full lexical scope vs. only symbols
      already reaching the call. The schema object is `x-frozen: {policy: additive-only}`, so the
      extension must be **additive**, and a conservative record must fail **closed** (false rejections,
      never false acceptances).
- [ ] 1.3 Decide **prompt version lineage** (PRD §14 Q3) — record an explicit `derived_from` at publish,
      or keep the name-grouped read model. A stored fact is one-way; decide before the write path ships.
- [ ] 1.4 Specify the **additive** `config_hash` extension for `bindings`: a specification declaring no
      bindings must hash **byte-identically** to today.

## 2. Backend — Prompt authoring write path (10a)

- [ ] 2.1 Add the authenticated **publish** route over the existing `registry.RegisterPrompt`.
      Idempotent on identical content; tenant-scoped **server-side** — a client-supplied tenant
      identifier must not widen scope. This is the platform API's first **write** surface.
- [ ] 2.2 Keep the interface surface free of mutation: **no `Update`, no `Delete`, no soft-delete.** The
      DB trigger remains the last line of defence, not the first.
- [ ] 2.3 Reject a malformed template **at publish**, naming the offending position (templates already
      parse at registration — surface that failure rather than adding a second check).
- [ ] 2.4 Add the **version timeline** read model over existing registry rows — ordered, each entry with
      version id, slot set, and creation metadata. No new table.
- [ ] 2.5 Add the **version diff** read model: body text difference **and** slots added/removed reported
      **explicitly**, not inferable only from the body diff.
- [ ] 2.6 Add **impact analysis** for a proposed body: which nodes pinning that prompt would fail to
      transform under the proposed slot set, and why — available **before** publish, and **naming any
      node it could not analyze**.
- [ ] 2.7 Test — publish/read: idempotent republish returns the same id; an edit leaves the prior
      version resolvable and rendering identically **through the read path** (a test that stops at the
      write return cannot see a shadowed entry); no operation mutates or deletes.

## 3. Backend + System Designer — Variable bindings (10a)

- [ ] 3.1 Add `Bindings map[string]BindingSource` to the node override with kinds `literal`, `expr`,
      `env`, `input`. Record the kind explicitly — never infer it from the value's shape.
- [ ] 3.2 Implement **resolve-time** validation for every failure class, reported through the existing
      `variantspec.SpecError{NodeID, Dim, Ref}` — **no second error channel**.
- [ ] 3.3 Enforce **exactly-once satisfaction** per slot: an explicit binding **or** an
      identically-spelled call-site expression. Neither → reject. Both → reject as ambiguous, rather
      than silently preferring one.
- [ ] 3.4 Validate `expr` against the IR's recorded in-scope symbols; `env` against declared variables;
      `input` against the node's P5 typed contract.
- [ ] 3.5 Make an absent `env` value at run time a **typed failure**, never an empty substitution — a
      prompt with a silently empty slot still returns a plausible completion that still gets scored.
- [ ] 3.6 **Preserve the unclaimed-operand refusal** in `promptExprFor`. Satisfying slots via explicit
      bindings must not license dropping a call-site value.
- [ ] 3.7 Extend the IR with per-call-site in-scope symbols per §1.2 — **additive** to the frozen node
      object.
- [ ] 3.8 Wire `bindings` into the resolved configuration so `config_hash` changes iff a binding
      changes, order-independently.
- [ ] 3.9 Test — one case per failure class asserting the error names **node, dimension and slot** and
      that it fires **before** any transformation is generated, worktree created, or build attempted;
      plus the backward-compatibility case: a spec with no bindings hashes byte-identically to today.

## 4. Frontend + Product — Studio surface (10a)

- [ ] 4.1 Build the prompt browser and **version timeline**, making the **live** version unmistakable —
      a list of hashes where the running one is not obvious invites pointing a node at the wrong one.
- [ ] 4.2 Build the **diff view** showing the **slot-set change separately** from the body change; a
      slot change is what alters where a prompt can be applied and is nearly invisible inside a body diff.
- [ ] 4.3 Build the editor. Its action is **"Save as new version"** — never "Save". Publishing is
      immutable and content-addressed; a verb that misdescribes system behavior is a bug.
- [ ] 4.4 Surface **impact analysis before publish**, including the nodes that could not be analyzed.
- [ ] 4.5 Build the **binding editor**, offering in-scope expressions from the IR as a pick list wherever
      possible — a validated choice cannot be made wrong, a free-text box can.
- [ ] 4.6 Build **preview** (byte-identical to what a run sends) and **test-run** (output + cost +
      latency + tokens), with render failures naming the offending slot.
- [ ] 4.7 Build **side-by-side comparison** — two prompt versions, or one version across two models.
- [ ] 4.8 🔴 Label every studio result **unranked / exploratory**. **No score, rank, winner,
      significance claim, or confidence interval anywhere**, and **no promotion path** from a studio
      result.
- [ ] 4.9 Build the per-node **model + prompt selector** showing the platform-computed `config_hash`
      before submission, and reporting binding failures before submission.
- [ ] 4.10 State **per node which facts are runtime-changeable and which need a new change** — the
      honest feature is narrower than "runtime configuration" implies.
- [ ] 4.11 Render prompt bodies as **text, never markup**; never log them.

## 5. AI Engineer + DevOps — Studio metering (10a)

- [ ] 5.1 Route studio executions through `providergateway` (studio is a **platform** caller — ADR-002).
- [ ] 5.2 Record studio cost under its **own spend kind**, distinct from eval spend, reusing the
      existing per-kind spend report.
- [ ] 5.3 Enforce a bounded per-user and per-tenant studio spend cap; exhausting it **stops** execution
      and reports the cap as configured behavior, not failure.
- [ ] 5.4 Test — studio cost never appears within eval cost; the cap stops execution rather than
      overspending.

## 6. QA — 10a acceptance gate

- [ ] 6.1 **Preview fidelity is a byte-comparison**, not an eyeball: the previewed string must equal
      what a real run sends. A preview that approximates is a confident lie.
- [ ] 6.2 Assert **no ranking artefact** — score, rank, winner, or interval — appears in any studio
      result, and that no promotion path exists from one. This is a product guarantee, so it is a
      failing test, not a review note.
- [ ] 6.3 Assert immutability **through the read path**, and that no interface expresses mutation.
- [ ] 6.4 Assert every binding failure class fires at resolve time with node/dimension/slot named.
- [ ] 6.5 Browser-rendered acceptance per P9 for every studio surface, error paths walked.

---

## 7. Backend + System Designer — Bound apply mode (10b)

- [ ] 7.1 Add per-node apply mode `inline` | `bound`, **defaulting to `inline`**. Nothing acquires an
      indirection unless asked.
- [ ] 7.2 Generate, in **one** change: the rewritten call site, the binding artifact, and the
      **resolved binding document containing actual values**.
- [ ] 7.3 🔴 **Reject** a transformation that introduces an indirection without its resolved values —
      a hard gate on the same footing as a failed build, not a warning.
- [ ] 7.4 Make the artifact **deterministic, dependency-free, and byte-identically regenerable**; small
      and readable enough to review, because the customer now owns it.
- [ ] 7.5 Ensure a `bound` change reverts in a **single revert** covering all three parts.
- [ ] 7.6 Keep `expr`/`input` bindings at the **call site** and `literal`/`env`, model and prompt in the
      **document** — the data/structure line is on lexical scope, not convenience.
- [ ] 7.7 Test — the rejection in 7.3 fires; regeneration is byte-identical; one revert restores
      everything.

## 8. Backend + DevOps — Resolution (10b)

- [ ] 8.1 Implement resolution order: **embedded → local override → remote-if-enabled**. Remote is
      **opt-in**; the default posture has no runtime dependency on the platform.
- [ ] 8.2 Implement **fail-static**: unreachable / unparseable / invalid override → keep last known-good,
      report **degraded**. 🚫 Never fail open to an arbitrary, empty, or default configuration.
      🚫 Never block process startup.
- [ ] 8.3 Expose the degraded state on a **readable endpoint** and in telemetry — a resolver quietly
      serving stale configuration is worse than the outage it avoided.
- [ ] 8.4 Parse the document once and hold it; resolution must add no measurable per-invocation latency.
- [ ] 8.5 Test — override unreachable, malformed, and stale-but-valid in turn; assert last known-good
      stays in force, degraded is reported, and startup succeeds with every override source unavailable.

## 9. Backend + AI Engineer — Reconciliation and verification interlock (10b)

- [ ] 9.1 Emit the **resolved** `config_hash` on **every** invocation as part of the standard tag set.
- [ ] 9.2 Make the eval harness **fail** a run whose observed resolved hash differs from the requested
      one — on **any** invocation, not partially scored from the ones that matched.
- [ ] 9.3 **Pin** the resolver during eval and verification runs: embedded document only, override
      sources disabled in the sandbox.
- [ ] 9.4 Record the `config_hash` carrying a **verified delta** in the document; mark resolutions
      without one **unverified** at every invocation and wherever the configuration is displayed; make
      them **refusable by automation level**.
- [ ] 9.5 🔴 Test — **the reconciliation must be able to go red**: deliberately resolve a mismatched
      document and assert the run **fails**. A check that cannot fail is decoration.
- [ ] 9.6 Test — a bound candidate under verification resolves the embedded document and consults no
      override; an unverified resolution is marked and is refused at the highest automation level.

## 10. Frontend — Bound-mode surfaces (10b)

- [ ] 10.1 Show apply mode per node, and render the **effective resolved values** for bound nodes rather
      than the indirection.
- [ ] 10.2 Render **unverified** distinguishably from verified — "someone selected this" and "this was
      proven better" must never look the same.
- [ ] 10.3 Render the **degraded** resolver state, naming which source failed and stating that the last
      known-good configuration is in force.
- [ ] 10.4 Update the per-node runtime-changeable statement (§4.10) for bound nodes.

## 11. Sales Operations — Claim discipline (10b)

- [ ] 11.1 Write the capability statement with its **boundary**: model and prompt version are data;
      wiring, skills, context policy and call-site-expression bindings require a code change. A
      customer planning around general runtime reconfiguration discovers the truth during delivery.
- [ ] 11.2 🚫 The demo script must not present a studio comparison as a result. The honest pitch is
      stronger: *try it in seconds, then prove it with a multi-seed evaluation and ship it as a verified
      pull request.*
- [ ] 11.3 Do not promise 10b's runtime layer before it is delivered; until then every configuration
      change is still a reviewed diff.

## 12. Documentation

- [ ] 12.1 Fold the four P10 capability specs into `openspec/specs/` when the change deploys, and apply
      the `verification` MODIFIED delta to the folded P5.5 spec.
- [ ] 12.2 Record the §1.1 binding-document ADR and the §1.3 lineage decision.
- [ ] 12.3 Update `docs/decisions/capability-boundary-p0-p2.md` — its "template variables bind to your
      call site's own expressions" statement is narrowed by explicit bindings and should say so.
- [ ] 12.4 Update the P2 PRD and the `p2-config-runtime` change **by reference** — P10 extends the
      registries and the codemod; their merged specs are not edited.
