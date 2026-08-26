#!/usr/bin/env python3
"""Red-check: prove P35's gate fences can actually FAIL (tasks.md §7, §9.7).

# Why this exists

design.md's organising question is *"which existing gate could this new path go around, and what makes
that impossible rather than merely unlikely."* Every answer is a few lines at a call site. Deleting one
leaves the whole suite green except the fence that names it — and the fence that names it is a test
somebody wrote at the same time as the line, in the same frame of mind, which is exactly when a test
that asserts nothing looks correct.

§9.7's title is the requirement: **green is worth having only if green can be red.** So the drill breaks
each gate and asserts the named fence goes red.

The gates, and what each one is standing in front of:

  G1  typed I/O contract       a contract violation is verified, gets a verdict row, and the delivery
                               oracle reads that row
  G2  verified delta           an unverified change is offered to somebody with an approve button
  G3  entitlement              a customer gets a write to their repository they did not contract for
  G4  transform refusal        a configuration the transform refuses is materialised by a caller-side
                               value
  G5  human approval           a change reaches a repository with nobody's name on it
  G6  never merge below        auto-merge, which is Enterprise-only, on every plan
      Autonomous

  and the four P35-specific ones:

  W   withdrawal               a change that failed to reproduce is delivered anyway
  I   idempotency              two pull requests for one change
  C   cancellation             a branch pushed by a run somebody stopped
  B   bound reporting          a truncated run reported as converged, so nobody raises the cap

# 🔴 EVERY MUTATION MUST COMPILE

A mutation that does not compile exits non-zero for a reason that has nothing to do with the fence, and
a drill that accepted that would report a fence as proven when the fence was never run. Each replacement
below is a change somebody could plausibly ship — a condition relaxed while "simplifying", a check moved
"because a lower layer already does it", a default filled in to unblock a demo.

# 🔴 `-count=1` on every run

A cached PASS reports a fence as red-capable when the mutation was never compiled. This repository has
had that drill failure once already.

# 🔴 It restores in a `finally`, and it refuses to run on a dirty tree

A crash mid-run would leave a weakened gate in the working tree, which is worse than the outcome this
script prevents. Originals are held in memory and written back unconditionally; the script refuses to
start if the files it is about to edit already differ from HEAD, because it cannot tell work in progress
from a mutation a previous crash failed to clean up.

Run: make p35-fence-redcheck
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RUN = os.path.join(ROOT, "internal", "improvementrun")
PROPOSAL = os.path.join(RUN, "proposal.go")
SERVICE = os.path.join(RUN, "service.go")
DELIVER = os.path.join(RUN, "deliver.go")
REMEASURE = os.path.join(RUN, "remeasure.go")
BOUND = os.path.join(RUN, "bound.go")
ENUMERATE = os.path.join(RUN, "enumerate.go")
FORGE = os.path.join(ROOT, "internal", "forgedelivery")
HOSTEDAPP = os.path.join(FORGE, "hostedapp.go")
SURFACE = os.path.join(FORGE, "surfacedefault.go")

PKG_RUN = "./internal/improvementrun/"
PKG_FORGE = "./internal/forgedelivery/"

# (name, package, file, find, replace, test) — `find` must appear EXACTLY ONCE, or the mutation is
# ambiguous and the script refuses rather than guessing which occurrence to weaken.
MUTATIONS = [
    # ── G1 · the typed I/O contract, checked BEFORE verification ────────────────────────────────
    #
    # "The composed verifier already checks the contract" is the most defensible-sounding deletion in
    # this package, and it moves the check to after a provider call has been spent and a verdict row
    # written — and the delivery oracle reads that row.
    (
        "G1 the-run-level-contract-check-is-removed-because-a-lower-layer-has-one",
        PKG_RUN, SERVICE,
        '''	if err := RejectBeforeVerification(v.contract, req.Candidate); err != nil {''',
        '''	if err := error(nil); err != nil {''',
        "TestConversationalRun_ContractViolationRejectedBeforeVerification",
    ),
    # A nil checker admitting rather than refusing: the gate's ABSENCE becomes indistinguishable from
    # its success, which is design.md's worry in one line of code.
    (
        "G1 a-missing-contract-checker-admits-instead-of-refusing",
        PKG_RUN, PROPOSAL,
        '''		return fmt.Errorf("%w: no typed-contract checker is configured, so no candidate can be admitted",
			ErrContractViolation)''',
        '''		return nil''',
        "TestAMissingContractCheckerRefusesRatherThanAdmits",
    ),
    # ── G2 · the held-out verification gate ─────────────────────────────────────────────────────
    #
    # `MergeReady()` is contract AND build AND gate. Dropping to `ContractOK` is the shape of a
    # "simplification" by somebody who read the three as redundant.
    #
    # 🔴 The fence is `TestACandidateThatDidNotBuildIsNotSurfaced`, NOT the gate-failing-high-scorer one.
    # The drill originally pointed at that and it stayed green with this guard removed, because
    # `Validate()`'s own gate check caught the case as well — defence in depth working, and a drill that
    # could prove neither guard. The build case is caught by `MergeReady()` and by nothing else.
    (
        "G2 an-unbuilt-candidate-is-surfaced-because-the-merge-ready-check-is-relaxed",
        PKG_RUN, PROPOSAL,
        '''	if !vr.MergeReady() {''',
        '''	if !vr.ContractOK {''',
        "TestACandidateThatDidNotBuildIsNotSurfaced",
    ),
    # The second guard, isolated: a proposal read back from a STORE never ran the constructor's checks.
    (
        "G2 a-stored-proposal-is-trusted-about-its-own-verdict",
        PKG_RUN, PROPOSAL,
        '''	if p.GateResult != verification.GatePass {''',
        '''	if p.GateResult != verification.GatePass && false {''',
        "TestAStoredProposalWithAFailedGateIsRefusedOnRead",
    ),
    # The composite outranking the constraint — FR9's exact wording is "however high its composite".
    (
        "G2 a-high-composite-is-allowed-past-a-failed-gate",
        PKG_RUN, SERVICE,
        '''	if res.MergeReady() {
		v.verified = append(v.verified, verifiedCandidate{cand: req.Candidate, result: res})
	}''',
        '''	if res.MergeReady() || res.Metrics.Composite.Mean > 0.5 {
		v.verified = append(v.verified, verifiedCandidate{cand: req.Candidate, result: res})
	}''',
        "TestConversationalRun_GateFailingHighScorerNotDelivered",
    ),
    # ── G4 · the transform refusal, unliftable by any caller-side value ─────────────────────────
    #
    # An exemption for a "trusted" origin is the shape this arrives in: the console is ours, so surely
    # it may. FR14 says no plan, role, entitlement, flag or parameter.
    (
        # The plausible shipped form: "only enforce the contract when we do not know which axis this is
        # — the person asked for this one." It compiles, it reads as a narrowing, and it makes the
        # refusal liftable by a caller-side value, which is exactly what FR14 forbids.
        "G4 the-transform-refusal-is-lifted-for-an-axis-the-plan-named",
        PKG_RUN, PROPOSAL,
        '''	if ok, reason := check.Check(cand); !ok {''',
        '''	if ok, reason := check.Check(cand); !ok && cand.Dimension == "" {''',
        "TestConversationalRun_NoOverrideMaterialisesARefusedConfiguration",
    ),
    # ── G5 · human approval, bound to a hash ────────────────────────────────────────────────────
    #
    # "The revision only moved forward, the diff still applies" is the argument for this one, and it
    # produces an approval for a diff nobody saw.
    (
        "G5 an-approval-survives-its-revision-moving",
        PKG_RUN, SERVICE,
        '''	if !want.Equal(current) {''',
        '''	if !want.Equal(current) && false {''',
        "TestConversationalRun_ApprovalVoidWhenRevisionMoves",
    ),
    # Delivery reaching a repository with nobody's name on it.
    (
        "G5 delivery-stops-requiring-an-approval",
        PKG_RUN, DELIVER,
        '''	if d := run.DecisionFor(proposalID); d.State != DecisionApproved {''',
        '''	if d := run.DecisionFor(proposalID); d.State == "impossible" {''',
        "TestAnUnapprovedProposalIsNotDelivered",
    ),
    # ── G6 · never merge below Autonomous ───────────────────────────────────────────────────────
    #
    # Requesting the level from the plan is the plausible "make it configurable" change, and it makes
    # the merge branch inside forgedelivery.Prepare reachable from a phase whose non-goal is merging.
    (
        "G6 the-delivery-level-becomes-autonomous",
        PKG_RUN, DELIVER,
        '''		Level:      entitlement.LevelAssisted,''',
        '''		Level:      entitlement.LevelAutonomous,''',
        "TestConversationalRun_NeverMergesBelowAutonomous",
    ),
    # ── W · re-measurement disagreement withdraws ───────────────────────────────────────────────
    #
    # 🔴 The single most likely "improvement" to this phase: make the second observation confirm. It is
    # one operator, and it turns a gate with teeth into a ritual that cannot fail.
    (
        "W re-measurement-can-only-confirm",
        PKG_RUN, REMEASURE,
        '''	if !Reproduced(verified, remeasured) {''',
        '''	if !Reproduced(verified, remeasured) && false {''',
        "TestConversationalRun_RemeasurementDisagreementWithdraws",
    ),
    # A provider that moved, blamed on the change. Deleting this check reads as removing a redundant
    # comparison; it produces a withdrawal that says a customer's change is bad on a day a vendor
    # shipped a model.
    (
        "W a-provider-that-moved-is-blamed-on-the-change",
        PKG_RUN, REMEASURE,
        '''	if verified.ProviderModelVersion != remeasured.ProviderModelVersion {''',
        '''	if false {''',
        "TestAProviderThatMovedIsNotReportedAsAChangeThatFailed",
    ),
    # A single-seed measurement admitted: its interval has width zero, so nothing ever reproduces.
    (
        "W a-single-seed-measurement-is-accepted",
        PKG_RUN, REMEASURE,
        '''	if m.Delta.NSeeds <= 1 {''',
        '''	if m.Delta.NSeeds < 0 {''',
        "TestASingleSeedMeasurementIsRefused",
    ),
    # A withdrawn change delivered anyway.
    (
        "W a-withdrawn-change-is-delivered",
        PKG_RUN, DELIVER,
        '''	if w, withdrawn := run.WithdrawalFor(proposalID); withdrawn {''',
        '''	if w, withdrawn := run.WithdrawalFor(proposalID); withdrawn && false {''',
        "TestAWithdrawnChangeIsNotDelivered",
    ),
    # ── C · cancellation pushes nothing (decisions.md D-35.6) ───────────────────────────────────
    #
    # Moving the cancel check "up front where the other guards are" reads as tidying and re-opens the
    # window: the run is cancelled after the guard and before the push, and a branch is left behind
    # that P12 forbids anybody from deleting.
    (
        "C the-cancellation-check-at-the-delivery-gate-is-removed",
        PKG_RUN, DELIVER,
        '''	if s.Cancelled != nil && s.Cancelled(run.RunID) {''',
        '''	if s.Cancelled == nil && s.Cancelled != nil {''',
        "TestConversationalRun_CancelPushesNothing",
    ),
    # ── B · which bound stopped the run ─────────────────────────────────────────────────────────
    #
    # The cap-over-stopping-condition preference looks like a cosmetic ordering choice. Without it a
    # truncated run reports as converged, and nobody raises the cap that was the actual constraint.
    # 🚫 There is deliberately NO mutation for a cap-over-stopping-condition override. One existed and
    # this drill proved it unreachable — `Plan.Constraints` maps the cap onto `MaxIterations` and the
    # loop consumes one candidate per iteration, so `max_iter` always fires first. The override was
    # DELETED rather than kept and excused; see `service.go` for why keeping it would have been wrong.
    (
        # The mapping itself is the thing that has to hold, so the drill breaks THAT.
        "B the-candidate-cap-stops-reaching-the-loop",
        PKG_RUN, os.path.join(RUN, "plan.go"),
        '''		MaxIterations:    p.CandidateCap,''',
        '''		MaxIterations:    p.CandidateCap * 100,''',
        "TestConversationalRun_ReportsWhichBoundStoppedIt",
    ),
    # A fault reported as a bound: an outage rendered as a limit the customer reached.
    (
        "B a-dependency-failure-is-reported-as-a-bound",
        PKG_RUN, BOUND,
        '''		o.Fault = res.StopReason''',
        '''		o.Bound = BoundKillSwitch''',
        "TestAFaultIsNeverReportedAsABound",
    ),
    # ── the candidate cap ───────────────────────────────────────────────────────────────────────
    (
        "B the-candidate-cap-stops-capping",
        PKG_RUN, ENUMERATE,
        '''		if len(e.admitted) >= e.Plan.CandidateCap {''',
        '''		if len(e.admitted) >= e.Plan.CandidateCap && false {''',
        "TestOneIterationConsumesOneCandidate",
    ),
    # ── credential posture · revocation is immediate ────────────────────────────────────────────
    #
    # 🔴 The plausible shipped version of this is a cache "so we do not hit the store on every write".
    # It is the exact latency FR25 exists to remove, and nothing about it looks wrong.
    (
        "R revocation-waits-for-something-other-than-the-next-call",
        PKG_FORGE, HOSTEDAPP,
        '''			if i.Active {
				return i, nil
			}
			revoked = true''',
        '''			if i.Active || true {
				return i, nil
			}
			revoked = true''',
        "TestRevocationStopsPushesImmediately",
    ),
    # An installation selecting nothing read as covering everything.
    (
        "R an-installation-selecting-no-repositories-validates",
        PKG_FORGE, HOSTEDAPP,
        '''	if len(i.Repositories) == 0 {''',
        '''	if len(i.Repositories) < 0 {''',
        "TestABroaderInstallationIsRefused",
    ),
    # ── R3 · the surface-scoped default ─────────────────────────────────────────────────────────
    #
    # "One code path, simplest story" — design D3 names this and rejects it: it makes a write-scoped
    # forge credential a permanent platform holding for every customer, including the ones ADR-005 was
    # written for.
    (
        # 🔴 The FENCE lives in `improvementrun` and the MUTATION lives in `forgedelivery`, which is the
        # point: the property is P12's and the caller that depends on it is P35's, so the drill has to
        # cross the package boundary the way a real regression would.
        "R3 the-hosted-app-becomes-the-default-everywhere",
        PKG_RUN, SURFACE,
        '''	case SurfaceCLI, SurfaceCI:
		return ModeCI, nil''',
        '''	case SurfaceCLI, SurfaceCI:
		return ModeApp, nil''',
        "TestOnlyTheConsoleSurfaceReachesForACredential",
    ),
]


def go(*args):
    env = dict(os.environ, GOWORK="off")
    return subprocess.run(["go", *args], cwd=ROOT, capture_output=True, text=True, env=env)


def run_test(pkg: str, name: str) -> subprocess.CompletedProcess:
    """Run one test. `-count=1` because a cached PASS would report a fence as red-capable when the
    mutation was never compiled."""
    return go("test", "-count=1", "-run", f"^{name}$", pkg)


def main() -> int:
    files = sorted({m[2] for m in MUTATIONS})

    dirty = subprocess.run(
        ["git", "diff", "--quiet", "--"] + files, cwd=ROOT, capture_output=True
    ).returncode != 0
    if dirty and not os.environ.get("P35_REDCHECK_ALLOW_DIRTY"):
        print("p35-fence-redcheck: refusing to run — these files already differ from HEAD:")
        for f in files:
            print(f"  {os.path.relpath(f, ROOT)}")
        print("\nThis script rewrites them and restores from memory. It cannot tell your work in progress")
        print("from a mutation a previous crash failed to clean up, and guessing wrong would either lose")
        print("your changes or leave a weakened GATE in the tree. Commit or stash first, or set")
        print("P35_REDCHECK_ALLOW_DIRTY=1 if you are sure.")
        return 2

    originals = {f: open(f, encoding="utf-8").read() for f in files}

    # 🔴 Baseline FIRST. A test that is already failing would make every mutation below look like a
    # working fence, which is the drill reporting success for a suite that is red.
    print("p35-fence-redcheck: baseline")
    seen = set()
    for name, pkg, _f, _find, _repl, test in MUTATIONS:
        if test in seen:
            continue
        seen.add(test)
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
        print("\np35-fence-redcheck: mutations")
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
                print(f"  ✖ {name}: the gate was BROKEN and {test} still passed. "
                      f"That test is not fencing this gate.")
                failures.append(name)
            else:
                print(f"  ✓ {name}: breaking the gate turned {test} red")
    finally:
        # Unconditional. A weakened gate left in the tree is worse than the failure this prevents.
        for path, source in originals.items():
            open(path, "w", encoding="utf-8").write(source)

    if failures:
        print(f"\np35-fence-redcheck FAILED — {len(failures)} gate(s) cannot go red: "
              + ", ".join(failures))
        return 1
    print(f"\np35-fence-redcheck PASSED — {len(MUTATIONS)} gate(s) proven capable of failing.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
