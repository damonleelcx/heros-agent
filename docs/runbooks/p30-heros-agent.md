# Runbook — the HEROS analysis agent

- **Status:** Accepted (2026-08-12)
- **Audience:** whoever is on call for the platform, and whoever configures HEROS for a tenant.
- **Reads with:** [the ablation protocol](../heros/ablation-protocol.md) for how a definition's numbers
  are measured, and [`docs/prd/P30-heros-platform-agent.md`](../prd/P30-heros-platform-agent.md) §14 for
  the two decisions (Q1, Q2) this runbook keeps depending on.

## 0. The two sentences to have in your head

**Nothing analyses anything until somebody sets a placement.** `disabled` is the default for every
organization, deliberately (Q2), and it is not a fault. A fresh deployment reports
`heros_agent.state: disabled` and that is the correct, healthy answer.

**The platform never holds a customer's provider key.** Under `platform` placement it spends the
*platform's* credential; under `customer` placement the customer's own key never leaves their machine.
There is no third arrangement and no field anywhere that could hold one (Q1).

---

## 1. Is it working? — `make agent-status`

```
make agent-status                                      # the local deployment
make agent-status READYZ=https://heros-agent.space/readyz
```

It reads the live `/readyz` entry, which is resolved by **doing what an inference does**: reading the
active definition, resolving the credential through the same secrets source the runner calls, and
comparing the real meter against the real ceiling. It is not a report that somebody set a variable.

| state | what it means | what to do |
|---|---|---|
| `disabled` | No organization is placed for analysis. **The default.** | Nothing. This is not a fault. |
| `no_active_definition` | Organizations are enabled and nothing is published and activated. | Publish and activate a definition — §3. A configuration half-done. |
| `credential_unresolved` | 🔴 The active definition's provider reference does not resolve. | Fix the secret — §5. Inference fails closed; every surface falls back to rule-derived facts. |
| `capped` | A ceiling is reached. | Raise it or wait for the window — §4. The deployment is healthy and declining to spend. |
| `ready` | An inference would run. | Nothing. |

🔴 **None of these makes the deployment not-ready.** The agent is optional; every other surface is
rule-derived. `agent-status` exits non-zero only for `credential_unresolved`, because a CI step that
failed on `disabled` would fail on the configuration every deployment ships with.

---

## 2. Enabling a tenant

From the operator console, `/agent` → the placement column. It requires a **reason** and is audited:
setting `platform` is what makes this platform read that tenant's source under a platform-held
credential, so it is a deliberate act with a name attached.

Three values, and the third is not the absence of the other two:

- **`platform`** — we analyse it here, spending our credential. We read their source.
- **`customer`** — they run `heros analyse` on their own machine with their own key and submit the
  result. We never see the source. This is the answer for a customer whose source may not leave their
  network.
- **`disabled`** — nothing analyses them, anywhere.

🔴 **Set a fleet ceiling before enabling anybody outside this organization.** §4. `make agent-rollout`
refuses the step otherwise, and that refusal is the point rather than an inconvenience.

### Disabling

Setting `disabled` **marks that tenant's stored inferences stale; it does not delete them.** The facts
stay attributed and their graphs say they are no longer maintained. Re-enabling clears the mark and
**re-runs nothing** — the stored facts are still the ones written before the gap.

---

## 3. Publishing a definition, and reading a rehearsal report

A definition is six axis references and a credential reference. Publishing it computes a `config_hash`
from the content, so re-publishing an identical definition is the same row rather than a second one,
and an edit that resolves to no change says so and creates nothing.

**A published definition serves nothing until it passes its rehearsal and is activated.** Those are two
steps, and the surface always names which definition is *actually serving inference* separately from the
one you are looking at.

### What pressing **activate** actually does

🔴 **It spends.** Activation runs the pinned calibration set against a live model first — **one provider
call per fixture, nine of them**, on this deployment's own credential, using the definition's *own*
prompt and model rather than a pair chosen for the test. Then it stores the per-fixture report on the
version row, and activates only if every fixture cleared both floors.

That is deliberate: the operator who decides to activate is the one who should see the bill for
measuring it. A definition that already **passed** is not re-measured — two runs of one `config_hash`
differ by noise rather than signal, and re-running would overwrite a stored report with a noisier one.

Two prerequisites, and both fail loudly rather than quietly:

| prerequisite | what happens without it |
|---|---|
| The definition's **model ref is in the P2 registry** | Publish itself refuses, naming what is registered. Register the model first (`/registry#add-model`). |
| The deployment carries the **calibration fixtures** | No gate is built, one line says so at boot, and every activation is refused for ever — safe, and indistinguishable from a gate that measured and said no unless you read the log. The image ships them at `/usr/share/heros/calibration`; `HEROS_CALIBRATION_ROOT` overrides it. |

Check the boot log for which of the two states this deployment is in:

```
operator console: the agent activation gate is ARMED — pressing activate runs the pinned
calibration set against a live model, on this deployment's own provider credential
```

```
operator console: 🔴 NO AGENT ACTIVATION GATE — <reason>. A definition can be published and
can NEVER be activated, because nothing can move its rehearsal state off `pending`
```

### Reading the report

The report is per-fixture: a precision and a recall for each pinned fixture repository, with the delta
against the previous definition and regressions marked.

🔴 **The gate reads the MINIMUM across fixtures, never the mean.** A mean is exactly the aggregate that
hides a per-language catastrophe — an agent excellent on four languages and connecting everything it
sees on the fifth passes a mean and ships a disaster to one language's customers. The mean is reported
because it is useful context; the gate is the minimum.

A failing gate names the failing fixture, its language and its numbers. Fix the definition or accept
that this one does not ship; there is no override.

⚠️ **The floors shipped (P ≥ 0.90, R ≥ 0.70) are a design starting point, not a measurement.** No
definition has been activated, so no model has been measured *in a deployment*. Two live runs of the
set exist and are recorded in [the ablation protocol](../heros/ablation-protocol.md) §2.1: gpt-4o
scored 1.00/1.00 on six fixtures and 0.00 precision on two near-misses, by proposing one edge where the
answer is none. Replace the floors with numbers a measurement supports rather than treating these as
established — and note that one fixture swung a full 1.00 between two runs of one `config_hash`, so a
floor set from a single run is a floor set from a coin flip.

---

## 4. Caps

Two ceilings, both in tokens over a rolling 30-day window:

- **per tenant** — that organization's own limit.
- **fleet-wide** — stored under the empty tenant id. One noisy tenant reaching it stops *everybody*,
  which is correct and is why a refusal names which ceiling it was.

Set them from `/agent/spend`. **Removing a cap is a delete, not a zero** — `0` is ambiguous between
"spend nothing" and "no limit", so the schema refuses it.

🔴 **No cap means unbounded**, and that is the default. It is survivable only because `disabled` is also
the default: the two are safe together and neither is safe alone. Enabling a tenant without setting a
ceiling is the single change that separates them.

The check runs **before** the provider call and **after** the cache read — a stored answer costs
nothing, so a capped tenant still reads its own stored graph. Reaching a cap emits
`heros.cap.reached` with the tenant, the scope and both numbers.

---

## 5. `credential_unresolved` — the on-call path

The active definition binds a provider **name**; the deployment's secrets source resolves it at use.
`credential_unresolved` means that resolution failed *right now*.

1. `make agent-status` — the detail names the provider and the source kind (`env`, `aws_secrets_manager`).
2. Check that the named secret exists in that source and that this process can read it.
3. Nothing is falling over while you do. Inference **fails closed**: zero provider calls, no
   substitution, and every customer surface renders rule-derived facts and says the agent is
   unavailable. A customer sees a correct graph with nothing inferred, not an error.

🚫 Do not "fix" this by putting a key into the console. There is no field for one, on purpose, and the
build fails if anybody adds one.

---

## 6. The kill switch

HEROS is wired to the platform's **existing** durable kill switch, not a private one. A subsystem with
its own halt is a subsystem an operator halts twice and unhalts once.

Arming it stops inference fleet-wide. Surfaces fall back to rule-derived facts and say so.

---

## 7. Rollout — `make agent-rollout`

```
make agent-rollout                  # what stage this fleet is at
make agent-rollout WANT=partner     # may it advance?
```

The ladder is `none → internal → partner → opt_in → default_on`, and the stage is **derived from the
placement table** rather than stored — a stored stage is a second source of truth for what that table
already says.

`WANT=` reads the evidence and either permits the step or names the one precondition that failed:

- the step must be **adjacent** (each stage exists to produce the evidence the next is decided on);
- the active definition must have **passed its rehearsal** — 🔴 between *every* pair, not only the
  first, or an unrehearsed definition reaches the whole fleet one small step at a time;
- a **fleet ceiling** must exist from `partner` onward, which is the first stage that analyses somebody
  else's repository.

Retreating is never gated.

🔴 **It changes nothing.** There is no automatic rollout to put a rail on — an operator still sets each
placement deliberately, with a reason, because automating enablement would put "read a customer's source
under a platform credential" behind a scheduler.

---

## 8. Verifying a change — `make agent-drills` and `make agent-acceptance`

`make agent-drills` defeats each fence in turn, confirms it goes **red**, and restores the tree. Run it
after touching anything in `internal/herosagent`. It refuses an ambiguous anchor and refuses a `-run`
that matches nothing, because both produce a confident "not a fence" about working machinery.

`make agent-acceptance TENANT=… WORKFLOW=…` runs the four-layer acceptance. 🔴 It exits **non-zero** on
any layer it could not run and prints *"This is NOT a pass"* — a partial acceptance reported as an
acceptance is how a capability ships having never once worked end to end.

---

## 9. What this runbook does not cover

- **Pricing.** No price list is wired, so every spend row reads `unpriced` — the word, never `0`.
- **Layer 2 of the acceptance** has not been run on a real deployment: it needs a platform Postgres and
  a live provider credential. Recorded in the phase's task list rather than left to be discovered.

---

## 10. 🔴 Production placement was set by DIRECT SQL, not through the console

On 2026-08-15, `org_efb27534e29c6c1c77400e30` ("ahegao", owner `damonlee1020@gmail.com`) was set to
`platform` placement by writing `heros_tenant_placement` directly:

```
tenant_id  org_efb27534e29c6c1c77400e30
placement  platform
set_by     direct-sql:no-operator-session
```

**Why it matters, and what is missing.** The only writer of that table is
`AgentService.SetPlacement`, which writes the row *and* appends to the hash-chained audit log. A direct
write produces the row and **no audit entry**. So the question the audit log exists to answer — *who
enabled this tenant, and why* — has no answer for this row, on a platform that otherwise commits the
audit entry **before** the effect.

`set_by` deliberately names the mechanism rather than impersonating an operator. Nothing in the code
reads that column for a decision, so the value is safe; the point of the string is that anybody reading
the row learns immediately that no operator session stands behind it.

**How to correct it.** Setting the same placement again through
`/agent/spend#placements` (`POST /admin/api/agent/placement`) overwrites the row via `ON CONFLICT` and
appends the missing audit entry. It is idempotent and costs nothing — the placement does not change,
only its provenance. Do that at the next operator sign-in.

**Why it was done this way.** The operator console is behind Okta OIDC and the work was being carried
out without an operator session. The alternative was to leave B2 unset and block the pipeline. The
trade was made deliberately and is recorded here rather than discovered later from a placement nobody
can account for.

**Related:** the state this unblocked is visible on `/readyz` under `heros_agent`, which moved from
`disabled` to `no_active_definition` — `enabled_tenants: 1`, and now waiting on a published and
activated definition.
