#!/usr/bin/env python3
"""Red-check: prove P34's twelve QA fences can actually FAIL (tasks.md §9).

# Why this exists

§9 is titled "fences that can go red", and the title is the requirement. Every rule this phase adds is
a few lines, and weakening any one of them leaves the whole suite green except the one test that names
it — so the only way to know that test is doing work is to break the rule and watch it fail.

The twelve, and what each one is standing in front of:

  9.1   the P0 golden vectors                     every stored config_hash means something else
  9.2   byte-identical serialisation              every existing spec re-hashes
  9.3   a pre-P34 loop-bearing spec resolves      ADR-014's orphaning chain, at the seal path
  9.4   both refs set → refused naming BOTH       two iteration policies, one silently ignored
  9.5   max_turns above ceiling → refused         a policy that is a suggestion
  9.6   missing host service → at RESOLVE         a preflight answer arrives after the codemod
  9.7   fan-in without merge → refused            the platform decides what your program means
  9.8   out-of-scope predicate → ADR-004 path     a second scope validator, and it is the looser one
  9.9   unsupported language → NOT dropped        a hash scored against source that never changed
  9.10  a Kind switch missing the new case        a consumer that silently mis-seals a loop
  9.11  attribution under overlapping spans       a nanosecond of scheduling read as evidence
  9.12  concurrency capped at BOTH gates          a limit with one entrance

# 🔴 EVERY MUTATION MUST COMPILE

A mutation that does not compile exits non-zero for a reason that has nothing to do with the fence, and
a drill that accepted that would report a fence as proven when the fence was never run. Each replacement
below is a change somebody could plausibly ship — a default filled in to unblock a demo, a condition
relaxed while "simplifying", a branch deleted in a refactor — and the drill asserts the package still
BUILDS before it trusts the test result.

🔴 9.10 IS THE EXCEPTION, AND IT IS INVERTED. Its whole claim is that a missing Kind case fails to
BUILD, so for that one a successful compile is the failure. It is run separately, below the table.

# 🔴 It restores in a `finally`, and it refuses to run on a dirty tree

A crash mid-run would leave a weakened compatibility check in the working tree, which is worse than the
outcome this script prevents. Originals are held in memory and written back unconditionally; the script
refuses to start if the files it is about to edit already differ from HEAD, because it cannot tell work
in progress from a mutation a previous crash failed to clean up.

Run: make p34-fence-redcheck
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VSPEC = os.path.join(ROOT, "internal", "variantspec")
RESOLVED = os.path.join(VSPEC, "resolved.go")
RESOLVE = os.path.join(VSPEC, "resolve.go")
GRAPH = os.path.join(VSPEC, "graph.go")
GRAPHRES = os.path.join(VSPEC, "graphresolve.go")
ENVELOPE = os.path.join(VSPEC, "envelope.go")
REGISTRY = os.path.join(ROOT, "internal", "registry", "loop.go")
TRANSFORM = os.path.join(ROOT, "internal", "transform", "engine.go")
ATTRIB = os.path.join(ROOT, "internal", "attribution", "attribution.go")
SANDBOX = os.path.join(ROOT, "internal", "sandbox", "concurrency.go")
KINDS = os.path.join(ROOT, "internal", "registry", "kinds.go")

# (name, package, file, find, replace, test) — `find` must appear EXACTLY ONCE, or the mutation is
# ambiguous and the script refuses rather than guessing which occurrence to weaken.
MUTATIONS = [
    # ── 9.1 / 9.2 / 9.3 · the compatibility fence, and the one that matters most ────────────────
    #
    # The single most likely way P34 breaks the product: a field added to the hashed shape without
    # `omitempty`. It is one word. Every existing configuration re-hashes, every measurement filed
    # under the old hash becomes unreachable, and nothing errors — the board simply has less on it.
    (
        "9.1/9.2 an-always-present-field-joins-the-hashed-shape",
        "./internal/variantspec/",
        RESOLVED,
        '''	GraphGroups []ResolvedGraphGroup `json:"graph_groups,omitempty"`''',
        '''	GraphGroups []ResolvedGraphGroup `json:"graph_groups"`''',
        "TestPreP34ConfigHashesAreReproducedExactly",
    ),
    (
        "9.1 the-golden-vector-comparison-stops-comparing",
        "./internal/variantspec/",
        RESOLVED,
        '''	Predicate string `json:"predicate,omitempty"`
}''',
        '''	Predicate string `json:"predicate"`
}''',
        "TestP34_GoldenVectorsUnchanged",
    ),
    (
        "9.3 the-legacy-path-acquires-a-resolve-time-gate",
        "./internal/variantspec/",
        RESOLVE,
        '''		if ro.Harness != nil && ro.Harness.IsLoopBearing() {''',
        '''		if ro.Harness != nil && !ro.Harness.IsLoopBearing() {''',
        "TestBothRefsSetIsRefusedNamingBoth",
    ),
    # ── 9.4 · the ambiguity refusal names BOTH refs ─────────────────────────────────────────────
    #
    # Naming one of them is the plausible shortcut: the author "obviously" meant the new one. But the
    # two may disagree, and a refusal that named the loop_ref would send them to change the ref that
    # was fine.
    (
        "9.4 the-refusal-names-only-one-ref",
        "./internal/variantspec/",
        RESOLVE,
        '''					ro.Harness.VersionID, ro.Harness.Spec.Strategy, o.LoopRef, registry.StrategyEnvelope)}''',
        '''					"the one it already had", ro.Harness.Spec.Strategy, o.LoopRef, registry.StrategyEnvelope)}''',
        "TestBothRefsSetIsRefusedNamingBoth",
    ),
    # ── 9.5 · the ceiling names BOTH values ─────────────────────────────────────────────────────
    #
    # Clamping instead of refusing is the shortcut with the best-sounding justification ("we honoured
    # the policy"). It runs a different configuration than the one recorded.
    (
        "9.5 the-ceiling-clamps-instead-of-refusing",
        "./internal/variantspec/",
        ENVELOPE,
        '''	if chosen <= ceiling {
		return nil
	}''',
        '''	if chosen <= ceiling || true {
		return nil
	}''',
        "TestMaxTurnsAboveTheEnvelopeCeilingIsRefused",
    ),
    # ── 9.6 · the host-service refusal is at RESOLVE, not at run ────────────────────────────────
    #
    # Deleting the resolve-time check "because the runtime already refuses" is the most defensible-
    # sounding change in this file, and it moves the answer to after the codemod is in the tree.
    (
        "9.6 the-host-check-moves-back-to-run-time",
        "./internal/variantspec/",
        ENVELOPE,
        '''	need := registry.HostServicesForLoop(loop.Spec.Strategy)
	if len(need) == 0 {
		return nil
	}''',
        '''	need := registry.HostServicesForLoop(loop.Spec.Strategy)
	if len(need) >= 0 {
		return nil
	}''',
        "TestMissingHostServiceIsRefusedAtResolve",
    ),
    # ── 9.7 · a fan-in without a merge is refused, never defaulted ──────────────────────────────
    #
    # The single change design D6 exists to prevent: a default that looks harmless.
    (
        "9.7 the-missing-merge-is-defaulted",
        "./internal/variantspec/",
        GRAPH,
        '''		if g.Merge == nil {
			return specErr(fanIns[0], graphDim, ErrInvalidSpec,''',
        '''		if g.Merge == nil && false {
			return specErr(fanIns[0], graphDim, ErrInvalidSpec,''',
        "TestAFanInWithNoMergeIsRefusedAtValidate",
    ),
    (
        "9.7 collect-partial-stops-checking-the-downstream-contract",
        "./internal/variantspec/",
        GRAPHRES,
        '''	if m.OnNodeFailure == CollectPartial {''',
        '''	if m.OnNodeFailure == CollectPartial && false {''',
        "TestCollectPartialAgainstARequiredFieldIsRefused",
    ),
    # ── 9.8 · the predicate goes through the ADR-004 path ───────────────────────────────────────
    #
    # 🔴 The mutation is a SECOND, LOOSER SCOPE RULE rather than a deleted check — because that is how
    # this actually fails. Nobody deletes a scope check; somebody adds "…or it looks like a literal",
    # and the second grammar is born.
    (
        "9.8 a-second-looser-predicate-rule-appears",
        "./internal/variantspec/",
        GRAPHRES,
        '''		if !from.CallSite.HasInScope(e.Predicate) {''',
        '''		if !from.CallSite.HasInScope(e.Predicate) && !strings.HasPrefix(e.Predicate, "c") {''',
        "TestAnOutOfScopePredicateIsRefusedNamingTheSymbol",
    ),
    # ── 9.9 · the unsupported language refuses, and the override is NOT dropped ─────────────────
    (
        "9.9 the-topology-override-is-silently-dropped",
        "./internal/transform/",
        TRANSFORM,
        '''	if err := checkGraphTopology(r); err != nil {
		return nil, err
	}''',
        '''	if err := checkGraphTopology(r); err != nil && false {
		return nil, err
	}''',
        "TestEveryLanguageRefusesTopologyByName",
    ),
    # ── 9.11 · attribution under overlapping spans ──────────────────────────────────────────────
    #
    # 🔴 The mutation is "ignore the declared order", which is exactly the pre-fix behaviour. If this
    # stopped turning the holdout red, the fix would be unfalsifiable.
    (
        "9.11 attribution-goes-back-to-ordering-by-the-clock",
        "./internal/attribution/",
        ATTRIB,
        '''		startOrder := executionOrderDeclared(fc.Trace, declaredOrder)''',
        '''		startOrder := executionOrder(fc.Trace)''',
        "TestBothNodesDivergingIsWhereOrderActuallyDecides",
    ),
    # ── 9.12 · the sandbox's own limit, which holds when the resolve gate is bypassed ───────────
    (
        "9.12 the-sandbox-trusts-the-width-the-spec-handed-it",
        "./internal/sandbox/",
        SANDBOX,
        '''	if declared > SandboxConcurrencyCeiling {
		return SandboxConcurrencyCeiling, true
	}''',
        '''	if declared > SandboxConcurrencyCeiling && false {
		return SandboxConcurrencyCeiling, true
	}''',
        "TestTheSandboxCapsEvenWhenTheSpecAsksForMoreThanTheCeiling",
    ),
    (
        "9.12 the-resolve-time-group-check-stops-firing",
        "./internal/variantspec/",
        ENVELOPE,
        '''	if width <= limit {
		return nil
	}''',
        '''	if width <= limit || true {
		return nil
	}''',
        "TestAGroupWiderThanTheEnvelopeLimitIsRefused",
    ),
]

# 🔴 9.10 IS INVERTED and is run on its own. Its claim is that a consumer answering per-Kind fails to
# BUILD when a Kind is added — so here, a successful compile IS the failure. Expressing it in the table
# above would require the table's compile-check to mean the opposite thing for one row, which is how a
# harness starts lying.
KIND_MUTATION = (
    "9.10 an-eighth-kind-does-not-break-the-build",
    KINDS,
    "func kindAnswers[T any](model, prompt, skill, contextKind, memory, harness, loop T) map[Kind]T {",
    "func kindAnswers[T any](model, prompt, skill, contextKind, memory, harness, loop, eighth T) map[Kind]T {",
)


def go(*args):
    env = dict(os.environ, GOWORK="off")
    return subprocess.run(["go", *args], cwd=ROOT, capture_output=True, text=True, env=env)


def run_test(pkg: str, name: str) -> subprocess.CompletedProcess:
    """Run one test. `-count=1` because a cached PASS would report a fence as red-capable when the
    mutation was never compiled — a drill failure this repository has already had once."""
    return go("test", "-count=1", "-run", f"^{name}$", pkg)


def main() -> int:
    files = sorted({m[2] for m in MUTATIONS} | {KIND_MUTATION[1]})

    dirty = subprocess.run(
        ["git", "diff", "--quiet", "--"] + files, cwd=ROOT, capture_output=True
    ).returncode != 0
    if dirty and not os.environ.get("P34_REDCHECK_ALLOW_DIRTY"):
        print("p34-fence-redcheck: refusing to run — these files already differ from HEAD:")
        for f in files:
            print(f"  {os.path.relpath(f, ROOT)}")
        print("\nThis script rewrites them and restores from memory. It cannot tell your work in progress")
        print("from a mutation a previous crash failed to clean up, and guessing wrong would either lose")
        print("your changes or leave a weakened check in the tree. Commit or stash first, or set")
        print("P34_REDCHECK_ALLOW_DIRTY=1 if you are sure.")
        return 2

    originals = {f: open(f, encoding="utf-8").read() for f in files}

    # 🔴 Baseline FIRST. A test that is already failing would make every mutation below look like a
    # working fence, which is the drill reporting success for a suite that is red.
    print("p34-fence-redcheck: baseline")
    for name, pkg, _f, _find, _repl, test in MUTATIONS:
        result = run_test(pkg, test)
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
        print("\np34-fence-redcheck: mutations")
        for name, pkg, path, find, repl, test in MUTATIONS:
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
            build = go("vet", pkg)
            if build.returncode != 0:
                open(path, "w", encoding="utf-8").write(source)
                tail = build.stderr.strip().splitlines()[-1] if build.stderr.strip() else ""
                print(f"  ✖ {name}: the MUTATION DOES NOT COMPILE, so this drill proves nothing about "
                      f"the fence. Rewrite it as a change somebody could ship.\n    {tail}")
                failures.append(name)
                continue

            result = run_test(pkg, test)
            open(path, "w", encoding="utf-8").write(source)

            if result.returncode == 0:
                print(f"  ✖ {name}: the rule was BROKEN and {test} still passed. "
                      f"That test is not fencing this rule.")
                failures.append(name)
            else:
                print(f"  ✓ {name}: breaking the rule turned {test} red")

        # ── 9.10, inverted ──────────────────────────────────────────────────────────────────────
        print("\np34-fence-redcheck: 9.10 (inverted — the fence IS the compiler)")
        name, path, find, repl = KIND_MUTATION
        source = originals[path]
        if source.count(find) != 1:
            print(f"  ✖ {name}: the pattern appears {source.count(find)} times")
            failures.append(name)
        else:
            open(path, "w", encoding="utf-8").write(source.replace(find, repl, 1))
            build = go("build", "./internal/registry/")
            open(path, "w", encoding="utf-8").write(source)
            if build.returncode == 0:
                print(f"  ✖ {name}: an eighth Kind COMPILED. Every consumer answering per-Kind would "
                      f"then be silently missing a case, which is the mis-sealed loop task 3.3 names.")
                failures.append(name)
            elif "not enough arguments" not in build.stderr:
                print(f"  ✖ {name}: the build failed for the WRONG REASON — the drill proves nothing "
                      f"unless the failure is the missing argument.\n    {build.stderr.strip().splitlines()[-1]}")
                failures.append(name)
            else:
                print(f"  ✓ {name}: adding a Kind fails to BUILD, at every call site "
                      f"(\"not enough arguments in call to kindAnswers\")")
    finally:
        # Unconditional. A weakened compatibility check left in the tree is worse than the failure this
        # prevents.
        for path, source in originals.items():
            open(path, "w", encoding="utf-8").write(source)

    if failures:
        print(f"\np34-fence-redcheck FAILED — {len(failures)} fence(s) cannot go red: "
              + ", ".join(failures))
        return 1
    print(f"\np34-fence-redcheck PASSED — {len(MUTATIONS) + 1} rule(s) proven capable of failing.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
