#!/usr/bin/env python3
"""Red-check: prove P31's server-side refusals can actually FAIL (tasks 6.1, 6.12, 6.13).

# Why this exists

Three tasks in §6 say the same thing in the same words: **"Mutate the check; the test must fail."** They
say it because a fence that cannot go red is decoration, and the three checks they name are the ones a
green suite is least able to distinguish from a broken one:

  6.1   a `finding` with no evidence reference is refused
  6.12  a `plan` missing any of its four limits is refused
  6.13  a `result` that leaves a planned step unreconciled is refused

Each is a few lines inside `internal/conversation`. Delete any one of them and every test in this
repository still passes except the one that names it — so the only way to know that test is doing work
is to remove the check and watch it go red.

# How it works

For each mutation: weaken ONE check in the real source, run the test that claims to catch it, and assert
it FAILS. Then restore. A mutation whose test still passes is reported as a fence that is not fencing.

Every mutation below is a REAL regression somebody could plausibly ship — a condition inverted while
"simplifying", a required field made optional to unblock a demo, a loop deleted during a refactor:

  finding-evidence   `if f.EvidenceRef == ""` -> a condition that is never true
  plan-budget        `if !p.Budget.Complete()` -> the same
  result-unreconciled  the "every declared step has an entry" loop, deleted

# 🔴 It restores in a `finally`, and it refuses to run on a dirty tree

A crash mid-run would leave a weakened security check in the working tree, which is a worse outcome
than the one this script exists to prevent. The originals are held in memory and written back
unconditionally; and the script refuses to start if the files it is about to edit already differ from
HEAD, because it cannot tell somebody's work in progress from a mutation it failed to clean up.

Run: make p31-fence-redcheck
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EMITTER = os.path.join(ROOT, "internal", "conversation", "emitter.go")
BUDGET = os.path.join(ROOT, "internal", "conversation", "budget.go")

# (name, file, find, replace, test) — `find` must appear EXACTLY ONCE, or the mutation is ambiguous and
# the script refuses rather than guessing which occurrence to weaken.
MUTATIONS = [
    (
        "6.1 finding-evidence",
        EMITTER,
        'if f.EvidenceRef == "" {',
        'if f.EvidenceRef == "\\x00never" {',
        "TestFindingWithNoEvidenceIsRefusedBeforeTheTransport",
    ),
    (
        "6.12 plan-budget",
        EMITTER,
        "if !p.Budget.Complete() {",
        "if false {",
        "TestPlanMissingAnyLimitIsRefused",
    ),
    (
        "6.12 progress-phase",
        EMITTER,
        "if !p.Phase.Valid() {",
        "if false {",
        "TestProgressWithNoPhaseIsRefused",
    ),
    (
        "6.13 result-unreconciled",
        EMITTER,
        """	for _, s := range e.Plan.Steps {
		if !declared[s.ID] {""",
        """	for _, s := range e.Plan.Steps {
		if false && !declared[s.ID] {""",
        "TestResultMissingAReconciliationEntryIsRefused",
    ),
    # 6.12's other half: the LIMITS themselves. Forcing each of the four and asserting the terminal
    # message names that specific limit is worthless if the accountant is not what decides — so each
    # check is removed in turn and the attribution test must go red for it.
    (
        "6.12 wall-clock-limit",
        BUDGET,
        "if b.now().Sub(b.startedAt) >= time.Duration(b.envelope.WallClockSeconds)*time.Second {",
        "if false {",
        "TestEachLimitIsSeparatelyAttributable",
    ),
    (
        "6.12 token-budget-limit",
        BUDGET,
        "if cost.Tokens > b.remaining.Tokens {",
        "if false {",
        "TestEachLimitIsSeparatelyAttributable",
    ),
    (
        "6.12 tool-call-limit",
        BUDGET,
        "if cost.ToolCalls > b.remaining.ToolCalls {",
        "if false {",
        "TestEachLimitIsSeparatelyAttributable",
    ),
    (
        "6.12 turn-ceiling-limit",
        BUDGET,
        "if cost.Turns > b.remaining.Turns {",
        "if false {",
        "TestEachLimitIsSeparatelyAttributable",
    ),
    # 6.16 — the loop guard. Removing it does not merely change an answer: the drill's own test would
    # hang forever, so the test carries its own bound and fails on it.
    (
        "6.16 step-re-entry-ceiling",
        BUDGET,
        "if b.entries[stepID] >= StepReEntryCeiling {",
        "if false {",
        "TestStepReEntryTerminatesNamingTheStep",
    ),
    (
        "6.14 result-verdict",
        EMITTER,
        "if m.Kind == KindResult && m.Result.VerifiedClaim {",
        "if false {",
        "TestAResultCitingANonExistentVerdictIsRefusedWithoutDetection",
    ),
]


def run_test(name: str) -> subprocess.CompletedProcess:
    """Run one test. `-count=1` because a cached PASS would report a fence as red-capable when the
    mutation was never compiled — the drill failure this repository has already had once."""
    env = dict(os.environ, GOWORK="off")
    return subprocess.run(
        ["go", "test", "-count=1", "-run", f"^{name}$", "./internal/conversation/"],
        cwd=ROOT, capture_output=True, text=True, env=env,
    )


def main() -> int:
    files = sorted({m[1] for m in MUTATIONS})

    dirty = subprocess.run(
        ["git", "diff", "--quiet", "--"] + files, cwd=ROOT, capture_output=True
    ).returncode != 0
    if dirty and not os.environ.get("P31_REDCHECK_ALLOW_DIRTY"):
        print("p31-fence-redcheck: refusing to run — these files already differ from HEAD:")
        for f in files:
            print(f"  {os.path.relpath(f, ROOT)}")
        print("\nThis script rewrites them and restores from memory. It cannot tell your work in progress")
        print("from a mutation a previous crash failed to clean up, and guessing wrong would either lose")
        print("your changes or leave a weakened check in the tree. Commit or stash first, or set")
        print("P31_REDCHECK_ALLOW_DIRTY=1 if you are sure.")
        return 2

    originals = {f: open(f, encoding="utf-8").read() for f in files}

    # 🔴 Baseline FIRST. A test that is already failing would make every mutation below look like a
    # working fence, which is the drill reporting success for a suite that is red.
    print("p31-fence-redcheck: baseline")
    failures = []
    for name, _f, _find, _repl, test in MUTATIONS:
        result = run_test(test)
        if result.returncode != 0:
            print(f"  ✖ {test} is ALREADY FAILING — fix that first")
            print(result.stdout[-1500:])
            return 2
        if "no tests to run" in result.stdout or "no test files" in result.stdout:
            # A `-run` pattern that matches nothing exits 0. Silently.
            print(f"  ✖ {test} MATCHED NO TEST — the drill would pass while measuring nothing")
            return 2
        print(f"  ✓ {test}")

    try:
        for name, path, find, repl, test in MUTATIONS:
            source = originals[path]
            if source.count(find) != 1:
                print(f"  ✖ {name}: the pattern appears {source.count(find)} times in "
                      f"{os.path.relpath(path, ROOT)}; the mutation is ambiguous")
                failures.append(name)
                continue
            open(path, "w", encoding="utf-8").write(source.replace(find, repl, 1))
            result = run_test(test)
            open(path, "w", encoding="utf-8").write(source)

            if result.returncode == 0:
                print(f"  ✖ {name}: the check was REMOVED and {test} still passed. "
                      f"That test is not fencing this rule.")
                failures.append(name)
            else:
                print(f"  ✓ {name}: removing the check turned {test} red")
    finally:
        # Unconditional. A weakened check left in the tree is worse than the failure this prevents.
        for path, source in originals.items():
            open(path, "w", encoding="utf-8").write(source)

    if failures:
        print(f"\np31-fence-redcheck FAILED — {len(failures)} fence(s) cannot go red: "
              + ", ".join(failures))
        return 1
    print(f"\np31-fence-redcheck PASSED — {len(MUTATIONS)} refusal(s) proven capable of failing.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
