#!/usr/bin/env python3
"""agent_drills.py — defeat each P30 fence, confirm it goes RED, restore.

P30 task 9.6, and the reason it is a script rather than a paragraph in a commit message: a fence that
was proved red once, by hand, six weeks ago is a fence nobody has checked since. This re-proves them.

🔴 EVERY RUN USES `-count=1`. A mutation followed by a same-second `go test` reads a CACHED PASS and
reports a live fence as dead — which is the worst possible outcome here, because it produces a
confident "NOT A FENCE" about machinery that is working. This repository has been bitten by exactly
that before.

🔴 AND IT RESTORES THE TREE, then re-runs green. A drill that left a mutation behind would be a script
whose failure mode is corrupting the thing it verifies, so the restore is checked against the content
this run started with — not against git, which is non-empty whenever the work is uncommitted.
"""

from __future__ import annotations

import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


@dataclass(frozen=True)
class Drill:
    """One fence, the edit that would defeat it, and the test that must go red."""

    name: str
    # file, old, new — the DEFEAT. Each is written as the plausible edit somebody would actually make,
    # not as vandalism: a fence that only catches obvious damage catches nothing.
    path: str
    old: str
    new: str
    package: str
    test: str


DRILLS: list[Drill] = [
    Drill(
        name="the placement gate moved below the cache read",
        path="internal/herosagent/runner.go",
        old="\tif err := r.host.MayRun(placement); err != nil {\n\t\tcode := CodeDisabled",
        new="\tif false {\n\t\tcode := CodeDisabled",
        package="./internal/herosagent/",
        test="TestPlatformRunnerRunsNothingForACustomerPlacedTenant",
    ),
    Drill(
        name="the ingest trusts the submitter's confidence floor",
        path="internal/herosagent/ingest.go",
        old="\t\tcase c < i.floor:",
        new="\t\tcase false:",
        package="./internal/herosagent/",
        test="TestIngestAppliesTheConfidenceFloorToASubmittedFact",
    ),
    Drill(
        name="the cap is checked AFTER the provider call",
        path="internal/herosagent/runner.go",
        old="\tif r.caps != nil {\n\t\tverdict, err := r.caps.Check(ctx, in.TenantID)",
        new="\tif false {\n\t\tverdict, err := r.caps.Check(ctx, in.TenantID)",
        package="./internal/herosagent/",
        test="TestACapIsEnforcedBeforeTheProviderCall",
    ),
    Drill(
        name="readiness asserts from configuration instead of resolving",
        path="internal/herosagent/readiness.go",
        old="\t\tif err := in.Credentials.Resolve(ctx, active.Definition.CredentialRef); err != nil {",
        new="\t\tif false {",
        package="./internal/herosagent/",
        test="TestReadinessResolvesTheCredentialRatherThanAssertingIt",
    ),
    Drill(
        name="a rollout stage may be skipped",
        path="internal/herosagent/rollout.go",
        old="\tif wantIdx > fromIdx+1 {",
        new="\tif false {",
        package="./internal/herosagent/",
        test="TestARolloutStageCannotBeSkipped",
    ),
    Drill(
        name="disabling deletes stored inferences instead of marking them stale",
        path="internal/herosagent/meminferencestore.go",
        # The plausible edit: "disabling should clean up", written as a delete in the loop that marks.
        old="\t\tst.StaleReason, st.StaleAtMS = reason, atMS\n\t\ts.m[k] = st",
        new="\t\tdelete(s.m, k)",
        package="./internal/herosagent/",
        test="TestDisablingMarksStaleRatherThanDeleting",
    ),
    Drill(
        name="the composition dispatches a metric set",
        path="internal/patternclassifier/composition.go",
        old="\tsort.SliceStable(c.Patterns, func(i, j int) bool {",
        new=(
            "\tif len(c.Patterns) > 0 {\n"
            "\t\tif ms, ok := MetricSetFor(c.Patterns[0].Pattern); ok {\n"
            "\t\t\t_ = ms\n"
            "\t\t}\n"
            "\t}\n"
            "\tsort.SliceStable(c.Patterns, func(i, j int) bool {"
        ),
        package="./internal/patternclassifier/",
        test="TestTheCompositionIsNotADispatcher",
    ),
]


def run(args: list[str]) -> tuple[int, str]:
    proc = subprocess.run(args, cwd=ROOT, capture_output=True, text=True, env=_env())
    return proc.returncode, proc.stdout + proc.stderr


def _env() -> dict[str, str]:
    import os

    env = dict(os.environ)
    # GOWORK=off, because this repository sits in a workspace whose other modules do not build here.
    env["GOWORK"] = "off"
    return env


def drill(d: Drill) -> bool:
    """Returns True when the fence went red on the defeat."""
    path = ROOT / d.path
    original = path.read_text()
    if d.old not in original:
        print(f"  ✖ {d.name}\n      the drill's anchor is not in {d.path} — this drill is STALE and is "
              f"proving nothing. Fix the anchor or delete the drill; a drill that cannot find its own "
              f"target silently stops testing.")
        return False

    path.write_text(original.replace(d.old, d.new, 1))
    try:
        # 🔴 -count=1. See the module docstring.
        code, out = run(["go", "test", d.package, "-count=1", "-run", d.test])
        went_red = code != 0
        # A `-run` that matches NOTHING exits 0 and looks identical to a pass. Catching it here is the
        # difference between "the fence held" and "no test ran at all" — a distinction this repository
        # has paid for.
        if "no tests to run" in out or "testing: warning: no tests to run" in out:
            print(f"  ✖ {d.name}\n      `-run {d.test}` matched no test. An unmatched -run exits 0, so "
                  f"this drill would report a dead fence as green forever.")
            return False
    finally:
        path.write_text(original)

    if not went_red:
        print(f"  ✖ {d.name}\n      the fence did NOT go red. {d.test} passes against a tree where the "
              f"property is defeated, so it is not testing what it claims.")
        return False
    print(f"  ✓ {d.name}")
    return True


def main() -> int:
    print(f"P30 mutation drills — {len(DRILLS)} fences\n")
    # Read every target BEFORE anything is mutated, so the restore check compares against the state
    # this run actually found rather than against a commit.
    baseline = {d.path: (ROOT / d.path).read_text() for d in DRILLS}
    results = [drill(d) for d in DRILLS]

    # 🔴 The restore is CHECKED against the CONTENT this run started with, not against git.
    #
    # The first version of this compared `git diff` and reported "THE TREE WAS NOT RESTORED" on a
    # correctly-restored tree — because the files carried uncommitted work, so the diff was non-empty
    # before any drill ran. A restore check that fails whenever the developer running it has
    # uncommitted changes is a check that gets ignored, which is worse than not having one.
    for path, before in baseline.items():
        if (ROOT / path).read_text() != before:
            print(f"\n🔴 THE TREE WAS NOT RESTORED: {path} differs from how this run found it.")
            return 2

    code, out = run(["go", "test", "./internal/herosagent/", "./internal/patternclassifier/", "-count=1"])
    if code != 0:
        print(f"\n🔴 the restored tree is not green:\n{out}")
        return 2

    failed = results.count(False)
    print(f"\n{results.count(True)}/{len(DRILLS)} fences went red; tree restored and green.")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
