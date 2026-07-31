# Live run — the P8 operator console against `nousresearch/hermes-agent`

| Field | Value |
|---|---|
| Target repository | https://github.com/nousresearch/hermes-agent |
| Checkout | `/tmp/claude-501/hermes-agent`, upstream `46c7a40` (shallow clone, 2026-07-24) |
| Stack | `cmd/proof/operatorconsole -repo <checkout>` (admin API, `:4311`) + `web/admin-console` BFF (`:4310`) |
| Console build | §15 experience layer, all tasks complete |
| Date | 2026-07-24 |

> **Why run against a real repository.** P8's load-bearing audit claim is that **every** autonomous
> merge is on the tamper-evident record with its motivating diagnosis, verified delta and merge commit
> (FR16). Against a fixture, "the merge commit is recorded" is a string comparison. Against this
> checkout it is a git object: the SHA the audit chain carries is the SHA `git show` resolves.

---

## 1. What was run

```bash
git clone --depth 1 https://github.com/nousresearch/hermes-agent.git
/tmp/claude-501/p6hermes -repo <checkout> -headless      # P6: seed the spec, run the loop
/tmp/claude-501/p8hermes -repo <checkout> -addr :4311    # P8: one real merge into the audit chain
cd web/admin-console && ADMIN_API_BASE=http://127.0.0.1:4311 npm run dev
```

**P6 (the autonomous optimizer)** converged after 4 iterations with **2 real merges** and a revert:

```
model_upgrade   node:auxiliary_client:async_call_llm            +0.380   merge 7f62a6865d
prompt_rewrite  node:trajectory_compressor:_generate_summary    +0.190   merge 79fc053b36
revert          git revert of merge 79fc053b369f → live 7bc390d06e6d
stop            converged: verified gain 0.0150 below min-improvement 0.0300
```

**P8** then performed one merge through the operator stack — through the same admission gate the
console's kill switch arms — and mirrored it into the hash-chained audit log.

## 2. What the console showed

| Claim | Evidence in the console |
|---|---|
| Every autonomous merge is on the record (FR16) | Audit log, filtered `action=p6.autonomous.merge`: seq 1 `attempted` (write-ahead) and seq 2 `applied`, actor `system:p6-autonomous-loop`, target `tenant:tenant-hermes` |
| …with its motivating diagnosis | Evidence drawer: `diagnosis id  diag-hermes-latency-p95` |
| …its verified delta | `verified delta  merge model_upgrade at node:auxiliary_client:async_call_llm: held-out +0.420 [0.370,0.470], cost -0.0031, latency -85` |
| …and its merge commit | `merge commit  67acf518343aa5d41e3be20a820f0ef77ab90c60` — resolves in the checkout: `Merge: 0308609 30b38fd`, `variant_spec.json | 4 ++--` |
| The chain is tamper-evident | Chain verification: **Intact**, 26 entries verified end to end |
| The operator brake is wired to the same gate | Arming the per-tenant kill switch for `tenant-castle` produced audit entry 5 and moved the Overview to **HALTED** and Tenants to **1 halted** |

## 3. A defect this run exposed

**The audit viewer showed no evidence.** The backend recorded `merge_commit`, `diagnosis_id`,
`verified_delta` and both config hashes — and the console rendered none of them. FR16 was satisfied in
the store and unsatisfied on the screen, which is the half that matters to the auditor the requirement
exists for. Running against a fixture would not have surfaced it: the gap is only obvious when there
is a real SHA that a real person would want to `git show`.

Fixed: an evidence disclosure per audit row (progressive disclosure — the row reads at a glance, the
evidence is one disclosure away, collapsed so it cannot push the table around).

A second, smaller one followed immediately: with a drawer open, the row's own header centred itself
while its cells top-aligned, dropping the subject's name to the bottom of a tall row. Fixed in the
table primitive.

## 4. Operational note

`p8hermes -repo` is **single-shot against a given checkout state**: it proposes one specific candidate,
so once that candidate is merged a second run finds nothing to merge and exits with
*"search space exhausted with no gate-passing, verified gain"*. To re-run, reset the checkout
(`git reset --hard <upstream>`) and re-run `p6hermes` first. Worth knowing before assuming the stack is
broken.

## 5. Verification at the time of this run

```
typecheck        OK
token scan       38 files, no colour/spacing/type-size/radius literal
tests            35 passed, 0 failed (incl. the live half against :4310)
visual baseline  11 routes checked against 11 recorded signatures
production build bundle scan passed: 29 client chunks, no credential material or priced literal
```
