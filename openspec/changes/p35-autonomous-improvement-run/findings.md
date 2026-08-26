# P35 — findings found while building

Two things surfaced while writing this phase that were **not P35's to change**, recorded rather than
folded in. Both are the shape `scope-fidelity` describes: a real problem, adjacent to the work, whose
fix belongs in a change whose reviewers are looking at the thing being changed.

| # | What | Status |
|---|---|---|
| **F1** | A write-capable scope admitted as a read connection | ✅ **CLOSED** — in its own change, in the owning package |
| **F2** | The requirement says five `proposalgen` states and there are eight | ✅ Documentation corrected; no fix was owed |

---

## F1 · ✅ CLOSED — a classic GitHub `repo` scope was admitted as a READ connection

**Where.** `internal/sourceingest/connection.go`, `writeCapableScopeSubstrings` /
`Authorization.Validate`.

**What.** The list refuses a read grant whose scope can write, by matching **verbs**:

```go
var writeCapableScopeSubstrings = []string{
    "write", "push", "admin", "delete", "maintain", "manage", "create",
}
```

Its own comment says matching on the verb *"fails toward refusal, which is the correct direction for a
control whose failure mode is a write grant admitted as a read one."* That reasoning is right and it has
one exception: **GitHub's classic OAuth scope `repo` is a NOUN that confers every verb.** It grants full
read *and* write to a repository and matches none of the seven substrings, so it passes.

**Reachable how — and this was narrower than first written.** The original write-up said the `repo`
scope was reachable through `GrantAccessToken`, a customer pasting a classic personal access token. That
is only half right, and the correction matters because it changes which forge was exposed:

- On **GitHub**, `Connect` refuses `GrantAccessToken` outright — `ExpectedGrantKind(ForgeGitHub)` is
  `GrantAppInstallation` — so a classic PAT never reached the scope check through the service. The `repo`
  gap was reachable through the exported `Authorization.Validate`, which is the documented control, but
  not through `Connect` on that forge.
- On **GitLab and Bitbucket**, `GrantAccessToken` *is* the expected kind. So the gap's real reachable
  instance was **GitLab's `api` scope**, which grants complete read and write API access and contains no
  verb either.

🔴 That is the more important finding: `repo` was not one missing entry, it was the **first instance of a
class** the denylist could not see — scope names that are NOUNS conferring every verb. Three are known:
`repo`, `public_repo` (GitHub, write to public repositories) and `api` (GitLab, full read/write).

The denylist also refused things it should not have: a GitHub App's `administration:read` is read-only
metadata and contains `admin`.

**Why P35 cares.** `tasks.md` 6.3 requires the write installation to be *structurally* separate from the
P32 read connection. That property is only true while the read grant cannot quietly become both. With
this gap, a customer can hold one credential that reads source *and* pushes — which is the thing
[ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) and
[ADR-013](../../../docs/adr/ADR-013-source-acquisition-posture.md) each independently refuse.

**Why it was not fixed in the P35 change.** It is a security list in another phase's package, and a
change about improvement runs is the wrong place for a reviewer to be asked to evaluate a scope
allowlist — the people who should look at that list would not have been looking at that diff.

---

### ✅ How it was closed

**The list was INVERTED rather than extended.** Adding `repo` would have left the same table missing
`public_repo`, `api`, and whatever a forge ships next. The decision is now an **allowlist**:

- `forgeAdapter.readOnlyScopes` declares, per forge, the scope spellings that forge's **narrowest grant
  actually carries** — `contents:read` + `metadata:read` on GitHub, `read_repository` on GitLab,
  `repository:read` on Bitbucket. `ReadOnlyScopesFor` reads it.
- `Authorization.Validate` admits a scope only if it is on that list. **A spelling nobody wrote down is
  refused by construction**, which is the property a denylist cannot have: the allowlist is a fact about
  what this platform *asks for*, not about a vocabulary three forges keep changing.
- It is the same rule `Validate` already applied to repositories. A grant covering a repository we did
  not name is refused even though it is an ordinary grant; a grant carrying a permission we did not ask
  for is now refused on the same reasoning. **Broader than asked for is refused**, whether the excess is
  a repository or a permission.
- 🚫 It is **per forge**. A GitHub grant reporting GitLab's `read_repository` is not a harmless spelling
  difference — it is evidence that whatever built the authorization is confused about which forge it is
  talking to.

**The verb list survives and decides nothing.** It now picks a better *sentence* for a scope the
allowlist has already refused: *"`repo` grants full control of a repository, including write"* is
actionable where *"`repo` is not a scope this grant carries"* is merely true. If it goes stale, a
refusal becomes less informative — it can never admit anything, and
`TestTheExplainerDecidesNothing` asserts that by refusing a write-capable scope the explainer has never
heard of.

### ⚠️ One thing deliberately NOT changed

**An empty `Scopes` list still passes.** Not every forge reports scopes on every grant kind, so an
absent list means *the forge said nothing* rather than *the grant permits nothing* — and refusing it
would reject connections that work today on all three forges, for a claim no forge made. What bounds a
grant's **reach** regardless is `Covers` / `AccountWide`, both of which refuse an empty value.
`TestAnEmptyScopeListIsAdmittedAndTheLimitIsStated` pins both halves, so the limit is a recorded decision
rather than an oversight, and the compensating control is checkable rather than reassuring.

### Fences, and that they can go red

| Fence | Where |
|---|---|
| The three full-access nouns refused on every forge, with a refusal that says **write** | `sourceingest.TestAFullAccessScopeIsRefusedOnEveryForge` |
| The declared read scopes are what the consent screen states, both directions | `sourceingest.TestTheDeclaredReadScopesAreWhatTheConsentScreenStates` |
| The explainer decides nothing | `sourceingest.TestTheExplainerDecidesNothing` |
| A scope from another forge is refused | `sourceingest.TestAScopeFromAnotherForgeIsRefused` |
| Every forge declares a non-empty list, and its own entries are admitted | `sourceingest.TestEveryForgeDeclaresItsReadOnlyScopes` |
| P35's own side: §6.3's separation, with `repo` promoted into the fenced table | `forgedelivery.TestAReadConnectionCannotCarryAWriteScope` |

Proved able to fail, by three mutations:

- re-admitting `repo` on GitHub → **three** fences red, across two packages;
- making the allowlist stop deciding (denylist-only semantics) → three red, including P32's own
  pre-existing `TestConnectRefusesAGrantBroaderThanOneRepository`;
- emptying one forge's list → the anti-vacuity fence red, naming the outage that would cause.

🔴 The P35 fence did the job it was built for. It was written as a `t.Log` with an **inverted**
assertion — reporting the gap and failing the day it closed — and it failed on the first run after the
fix, with a message telling whoever closed it to promote the case. It did not stay silent in either
direction.

---

## F2 · ⚠️ The requirement says five `proposalgen` states and there are eight

**Where.** PRD §2.2, PRD §6.2 FR7, `tasks.md` 3.5 and 7.12 all say **five** "nothing to propose" states.
`internal/proposalgen` declares **eight** states — `generated` plus **seven** ways to find nothing.

**What happened.** The prose was written from P30's account, which listed three and an ellipsis. Two
states arrived afterwards: `no_model_menu` and `revision_mismatch`.

**What was built.** **All seven**, not five. Implementing five would leave two states falling through to
a default, and a default *is* the generic empty result FR7 forbids — the P30 defect with a smaller blast
radius.

**What was added so it cannot drift again.** `proposalgen.States()`, `State.Valid()` and
`State.FoundNothing()`, so every consumer reads the closed set from its owning package rather than
retyping a count. `improvementrun.TestEveryNothingToProposeStateHasItsOwnSentence` ranges over
`States()` and asserts no two render the same sentence, so an eighth is caught the day it is added
rather than the day a customer meets it.

**No fix is owed.** This is a documentation correction, made in
[`internal/improvementrun/state.go`](../../../internal/improvementrun/state.go)'s header and here. The
count in the prose is what went stale; the code is right.
