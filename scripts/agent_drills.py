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
    # ── task 10.1 · the frontend edge wins ────────────────────────────────────────────────────────
    Drill(
        name="10.1 · an agent edge may overwrite a frontend one",
        path="internal/herosagent/residue.go",
        # The plausible edit: "the agent is more specific, let it refine the edge."
        old="\t\tif (e.FromNodeID == from && e.ToNodeID == to) || (e.FromNodeID == to && e.ToNodeID == from) {\n\t\t\treturn false\n\t\t}",
        new="\t\tif false {\n\t\t\treturn false\n\t\t}",
        package="./internal/herosagent/",
        test="TestAGoFixtureIRIsByteIdenticalWithTheAgentOnAndOff",
    ),
    # ── task 10.15 · the six axis-authoring refusals ───────────────────────────────────────────────
    Drill(
        name="10.15a · an unsupplied harness service is accepted at save",
        path="internal/herosagent/definition.go",
        old="\t\tif a.Available {\n\t\t\treturn nil\n\t\t}",
        new="\t\tif true {\n\t\t\treturn nil\n\t\t}",
        package="./internal/herosagent/",
        test="TestAnUnsuppliedHarnessServiceIsRefusedAtSaveNamingTheService",
    ),
    Drill(
        name="10.15b · max_turns may exceed the ceiling",
        path="internal/herosagent/axiseditor.go",
        # The plausible edit: "16 is too low, customers want longer loops."
        old="const MaxTurnsCeiling = 16",
        new="const MaxTurnsCeiling = 16_000",
        package="./internal/herosagent/",
        test="TestHarnessParamsAreValidatedAtSave",
    ),
    Drill(
        name="10.15c · a network-declaring tool becomes bindable",
        path="internal/herosagent/axiseditor.go",
        old="t.DeclaresNetwork",
        new="false && t.DeclaresNetwork",
        package="./internal/herosagent/",
        test="TestNetworkDeclaringAndUnapprovedToolsAreNotBindable",
    ),
    Drill(
        name="10.15d · a remote $ref is fetched rather than rejected",
        path="internal/herosagent/axiseditor.go",
        old="\t\tcase hasRemoteRef(s.Schema):",
        new="\t\tcase false && hasRemoteRef(s.Schema):",
        package="./internal/herosagent/",
        test="TestUnselectableSkillsAreRefusedWithTheirReason",
    ),
    Drill(
        name="10.15e · malformed policy params are accepted at save",
        path="internal/herosagent/axiseditor.go",
        old="func ValidatePolicyParams(",
        new="func ValidatePolicyParams_disabled(",
        package="./internal/herosagent/",
        test="TestMalformedPolicyParamsAreRefusedAtSaveNamingPolicyAndParameter",
    ),
    Drill(
        name="10.15f · an unsupplied memory host service is accepted at save",
        path="internal/herosagent/definition.go",
        old='\t\t\ta.Available = h.Embedder && h.EmbeddingRef != ""',
        new="\t\t\ta.Available = true",
        package="./internal/herosagent/",
        test="TestMemoryHostServiceFencesRefuseAtSaveAndNeverDegrade",
    ),
    # ── task 10.17 · the no-key fence, on each of the four surfaces ────────────────────────────────
    Drill(
        name="10.17a · a heros_* column could hold a key",
        path="db/migrations/postgres/0048_p30_agent_caps_and_stale.up.sql",
        # The plausible edit: "cache the resolved key so the checker does not resolve on every call."
        old="    reason        TEXT   NOT NULL,\n    set_by        TEXT   NOT NULL,",
        new="    reason        TEXT   NOT NULL,\n    provider_api_key TEXT,\n    set_by        TEXT   NOT NULL,",
        package="./internal/herosagent/",
        test="TestNoHerosColumnCouldCarryAKey",
    ),
    Drill(
        name="10.17b · an agent console surface offers a password field",
        path="web/admin-console/src/app/agent/page.tsx",
        old="export default",
        new='const CredentialInput = () => <input type="password" name="api_key" />;\n\nexport default',
        package="./internal/herosagent/",
        test="TestNoAgentConsoleSurfaceOffersAFieldForAKey",
    ),
    Drill(
        name="10.17c · a formatting call takes a credential as an argument",
        path="internal/herosagent/readiness.go",
        old="\t\tout.CredentialSource = in.Credentials.Describe()",
        new='\t\tout.CredentialSource = in.Credentials.Describe()\n\t\tcred := active.Definition.CredentialRef\n\t\t_ = fmt.Sprintf("%v", cred)',
        package="./internal/herosagent/",
        test="TestNoLogOrErrorInThisPackageFormatsACredential",
    ),
    # ── task 10.18 · the cross-tenant memory scope ─────────────────────────────────────────────────
    Drill(
        name="10.18 · the memory session widens to the tenant id",
        path="internal/herosagent/runner.go",
        # The exact widening task 10.18 names: prove it red by scoping memory to the tenant.
        old="func MemorySessionID(inferenceID string) string { return inferenceID }",
        new='func MemorySessionID(_ string) string { return "tenant-scope" }',
        package="./internal/herosagent/",
        test="TestMemoryCannotCrossTenants",
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

    # 🔴 AN AMBIGUOUS ANCHOR IS A DRILL THAT MAY BE MUTATING A COMMENT.
    #
    # `10.15b` was written with the anchor `MaxTurnsCeiling`, whose first occurrence in the file is the
    # prose above the constant. The drill dutifully rewrote a comment, the code was untouched, the test
    # passed — and the run reported the ceiling fence as DEAD. A false "not a fence" is the most
    # expensive output this script can produce, because it sends somebody to fix working machinery.
    if original.count(d.old) != 1:
        print(f"  ✖ {d.name}\n      the anchor appears {original.count(d.old)} times in {d.path}. Only "
              f"the first is replaced, so this drill may be mutating a comment while reporting on the "
              f"code — which produces a false `not a fence`. Make the anchor unique.")
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
