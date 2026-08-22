#!/usr/bin/env python3
"""Red-check: prove P33's fences can actually FAIL (tasks 7.1, 7.2, 7.6, 7.7, 7.9).

# Why this exists

Task 7.1 says it in these words: **"Mutate the extractor to return a default; the test must fail."**
It says it because the whole product of this phase is reporting ABSENCE HONESTLY, and every one of the
ways that erodes is invisible in a green suite:

  7.1  an extractor returns a plausible default instead of naming what it lacked
  7.2  a zero-edge graph is reported as a fact about the repository rather than about our parser
  7.6  a composite score is emitted from somewhere
  7.7  a budget refusal shrinks the report instead of degrading a state
  7.9  a `not_measured` finding is constructed with no missing input

Each is a few lines. Weaken any one of them and every test in this repository still passes except the
one that names it — so the only way to know that test is doing work is to break the rule and watch it
go red.

# 🔴 EVERY MUTATION MUST COMPILE

A mutation that does not compile also exits non-zero, and a drill that accepted that would report a
fence as proven when the fence was never run. So each replacement below is a change a person could
plausibly ship — a default filled in to unblock a demo, a condition inverted while "simplifying", a
branch deleted in a refactor — and the drill asserts the package still BUILDS before it trusts the
test result.

# 🔴 It restores in a `finally`, and it refuses to run on a dirty tree

A crash mid-run would leave a weakened honesty check in the working tree, which is worse than the
outcome this script prevents. The originals are held in memory and written back unconditionally; the
script refuses to start if the files it is about to edit already differ from HEAD, because it cannot
tell somebody's work in progress from a mutation a previous crash failed to clean up.

Run: make p33-fence-redcheck
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PKG = os.path.join(ROOT, "internal", "assessment")
EXTRACT = os.path.join(PKG, "extract.go")
FINDING = os.path.join(PKG, "finding.go")
REPORT = os.path.join(PKG, "report.go")
RUNNER = os.path.join(PKG, "runner.go")
HEALTH = os.path.join(PKG, "health.go")

# (name, file, find, replace, test) — `find` must appear EXACTLY ONCE, or the mutation is ambiguous and
# the script refuses rather than guessing which occurrence to weaken.
MUTATIONS = [
    # ── 7.1 · an extractor returns a default ────────────────────────────────────────────────────
    #
    # The FLOOR read as an observation. `discovery` emits `memory: none` for every node in every
    # repository and documents it as "the absence of evidence, not evidence of absence"; reading it as
    # a finding produces "this repository has no memory strategy" for every customer on earth, with a
    # measurement's confidence, and every fixture-based test still passes.
    (
        "7.1 memory-floor-read-as-observation",
        EXTRACT,
        '''		if v := n.MemoryDefault(); v != "none" {''',
        '''		if v := n.MemoryDefault(); v != "" {''',
        "TestMemoryAndHarnessRefuseTheFloor",
    ),
    (
        "7.1 harness-floor-read-as-observation",
        EXTRACT,
        '''		if v := n.HarnessDefault(); v != "single-shot" {''',
        '''		if v := n.HarnessDefault(); v != "" {''',
        "TestMemoryAndHarnessRefuseTheFloor",
    ),
    # The majority reported as the answer. "Three of your four call sites use gpt-4o-mini" invites a
    # reader to conclude something about the fourth, which is the one we could not read.
    (
        "7.1 model-reports-the-resolved-majority",
        EXTRACT,
        """	if len(unresolved) > 0 {
		return NotMeasured(AxisModel, MissingUnresolvedField, fmt.Sprintf(""",
        """	if false && len(unresolved) > 0 {
		return NotMeasured(AxisModel, MissingUnresolvedField, fmt.Sprintf(""",
        "TestAPartiallyResolvedAxisIsNotMeasured",
    ),
    # ── 7.2 · design D6, the inversion this phase is named after ────────────────────────────────
    (
        "7.2 zero-edges-as-a-fact-about-the-repository",
        EXTRACT,
        """	if len(s.IR.Edges) > 0 {""",
        """	if len(s.IR.Edges) >= 0 {""",
        "TestAZeroEdgeRepositoryNamesTheFrontend",
    ),
    # ── 7.9 · the conditional requirement, made a default ───────────────────────────────────────
    (
        "7.9 not-measured-gets-a-default-missing-input",
        FINDING,
        """func NotMeasured(axis Axis, missing MissingInput, claim string, ref EvidenceRef) (Finding, error) {
	return finish(""",
        """func NotMeasured(axis Axis, missing MissingInput, claim string, ref EvidenceRef) (Finding, error) {
	if missing == "" {
		missing = MissingUnresolvedField
	}
	return finish(""",
        "TestANotMeasuredFindingWithNoMissingInputCannotBeConstructed",
    ),
    # ── FR1 · a report that drops what it could not say ─────────────────────────────────────────
    (
        "FR1 an-omitted-axis-is-tolerated",
        REPORT,
        """	if len(missing) > 0 {
		return fmt.Errorf(""",
        """	if false && len(missing) > 0 {
		return fmt.Errorf(""",
        "TestAnOmittedAxisFailsTheReport",
    ),
    # ── 7.7 · a budget refusal that shrinks the report ──────────────────────────────────────────
    #
    # The plausible regression: "we ran out of money, so stop adding findings". It produces a shorter
    # report that renders perfectly and presents a partial answer as a complete one.
    (
        "7.7 budget-exhaustion-drops-the-remaining-axes",
        RUNNER,
        """			findings[i] = degraded
			continue""",
        """			_ = degraded
			continue""",
        "TestTheBudgetStopsBeforeTheCallAndDegradesTheRest",
    ),
    # ── 7.6 · the composite ─────────────────────────────────────────────────────────────────────
    #
    # 🔴 This one mutates the SUBJECT rather than the fence: a composite is ADDED, and the fence must
    # notice. Every other drill here removes a check; this one proves the check catches an addition,
    # which is the direction R4 is actually exposed to.
    (
        "7.6 a-composite-is-added",
        REPORT,
        """// AllNotMeasured reports whether""",
        """// Score is a composite this drill adds so the fence can catch it.
func (a Assessment) Score() float64 {
	return float64(a.Tally().Observed) / float64(len(Axes()))
}

// AllNotMeasured reports whether""",
        "TestNoFunctionReducesAnAssessmentToANumber",
    ),
    # ── 8.1 · the copy that turns an absence into a shrug ───────────────────────────────────────
    #
    # 🔴 The most likely erosion in the whole phase, and the one with no functional symptom. Somebody
    # adds an axis in a hurry and writes "the memory strategy is unknown" — grammatical, honest, and it
    # tells the reader nothing they can do. The report gets one row worse and nothing goes red.
    (
        "8.1 absence-written-as-a-shrug",
        EXTRACT,
        """		"a memory strategy is a store read and written between turns, and static call-site extraction "+
			"sees one call at a time; nothing here says this repository has no memory — only that we "+
			"have not looked between its turns yet", s.Evidence())""",
        """		"the memory strategy is unknown", s.Evidence())""",
        "TestAbsenceReadsAsATaskAndNotAsAFailure",
    ),
    # ── 6.2 · the alarm that must not fire on the ordinary case ─────────────────────────────────
    (
        "6.2 refusals-counted-as-absences",
        HEALTH,
        """	if a.AllNotMeasured() {
		m.allNotMeasured++
	}""",
        """	absent := 0
	for _, f := range a.Findings {
		if f.State() == StateNotMeasured || f.State() == StateRefused {
			absent++
		}
	}
	if absent == len(Axes()) {
		m.allNotMeasured++
	}""",
        "TestTheAlertFiresWhenTheProductSilentlyStopsSayingAnything",
    ),
]


def go(*args):
    env = dict(os.environ, GOWORK="off")
    return subprocess.run(["go", *args], cwd=ROOT, capture_output=True, text=True, env=env)


def run_test(name: str) -> subprocess.CompletedProcess:
    """Run one test. `-count=1` because a cached PASS would report a fence as red-capable when the
    mutation was never compiled — a drill failure this repository has already had once."""
    return go("test", "-count=1", "-run", f"^{name}$", "./internal/assessment/")


def main() -> int:
    files = sorted({m[1] for m in MUTATIONS})

    dirty = subprocess.run(
        ["git", "diff", "--quiet", "--"] + files, cwd=ROOT, capture_output=True
    ).returncode != 0
    if dirty and not os.environ.get("P33_REDCHECK_ALLOW_DIRTY"):
        print("p33-fence-redcheck: refusing to run — these files already differ from HEAD:")
        for f in files:
            print(f"  {os.path.relpath(f, ROOT)}")
        print("\nThis script rewrites them and restores from memory. It cannot tell your work in progress")
        print("from a mutation a previous crash failed to clean up, and guessing wrong would either lose")
        print("your changes or leave a weakened check in the tree. Commit or stash first, or set")
        print("P33_REDCHECK_ALLOW_DIRTY=1 if you are sure.")
        return 2

    originals = {f: open(f, encoding="utf-8").read() for f in files}

    # 🔴 Baseline FIRST. A test that is already failing would make every mutation below look like a
    # working fence, which is the drill reporting success for a suite that is red.
    print("p33-fence-redcheck: baseline")
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

    failures = []
    try:
        print("\np33-fence-redcheck: mutations")
        for name, path, find, repl, test in MUTATIONS:
            source = originals[path]
            if source.count(find) != 1:
                print(f"  ✖ {name}: the pattern appears {source.count(find)} times in "
                      f"{os.path.relpath(path, ROOT)}; the mutation is ambiguous")
                failures.append(name)
                continue
            open(path, "w", encoding="utf-8").write(source.replace(find, repl, 1))

            # 🔴 THE COMPILE CHECK. A mutation that does not build exits non-zero for a reason that has
            # nothing to do with the fence, and accepting it would report a fence as proven when it was
            # never run.
            build = go("vet", "./internal/assessment/")
            if build.returncode != 0:
                open(path, "w", encoding="utf-8").write(source)
                print(f"  ✖ {name}: the MUTATION DOES NOT COMPILE, so this drill proves nothing about "
                      f"the fence. Rewrite it as a change somebody could ship.")
                print("    " + build.stderr.strip().splitlines()[-1] if build.stderr.strip() else "")
                failures.append(name)
                continue

            result = run_test(test)
            open(path, "w", encoding="utf-8").write(source)

            if result.returncode == 0:
                print(f"  ✖ {name}: the rule was BROKEN and {test} still passed. "
                      f"That test is not fencing this rule.")
                failures.append(name)
            else:
                print(f"  ✓ {name}: breaking the rule turned {test} red")
    finally:
        # Unconditional. A weakened honesty check left in the tree is worse than the failure this
        # prevents.
        for path, source in originals.items():
            open(path, "w", encoding="utf-8").write(source)

    if failures:
        print(f"\np33-fence-redcheck FAILED — {len(failures)} fence(s) cannot go red: "
              + ", ".join(failures))
        return 1
    print(f"\np33-fence-redcheck PASSED — {len(MUTATIONS)} rule(s) proven capable of failing.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
