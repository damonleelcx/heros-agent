#!/usr/bin/env python3
"""Red-check: prove the sandbox proof can actually FAIL (P1 task 7.2).

A fence that cannot go red is decoration. discovery_sandbox_proof.py passing tells you nothing until
you know it is capable of failing — a proof with an inverted assertion, a typo'd field name, or a
probe that silently measures nothing would print PASS forever and nobody would notice.

So this deliberately WEAKENS the compose spec, one guarantee at a time, and asserts the proof goes
RED for that specific claim. It is the same discipline internal/discovery/noexec_test.go follows
("Verified red-able: an implementation that shelled out would create the sentinel and fail"), applied
to the deployment half.

Each mutation is a REAL regression someone could plausibly ship:
  - deleting `network_mode: none`            -> the worker gets a default bridge network (egress)
  - flipping the /repo mount `:ro` -> `:rw`  -> the worker can rewrite the customer's repo
  - adding `environment: [OPENAI_API_KEY]`   -> the operator's provider key lands in the worker

The weakened spec is written into deploy/ (not /tmp) on purpose: the compose `build.context` is
resolved relative to the file's own location, so a copy elsewhere would change what is built and the
red-check would no longer be exercising the real image.

Run: make discovery-sandbox-proof-redcheck     (needs: Docker)
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
COMPOSE = os.path.join(ROOT, "deploy", "docker-compose.discovery.yml")
PROOF = os.path.join(ROOT, "scripts", "discovery_sandbox_proof.py")
# Same directory as the real spec => identical build context. Dot-prefixed + cleaned up in finally.
MUTANT = os.path.join(ROOT, "deploy", ".redcheck-compose.discovery.yml")


def mutate_drop_network(src: str) -> str:
    """Regression: someone deletes the no-egress line."""
    out = [ln for ln in src.splitlines(keepends=True) if "network_mode: none" not in ln]
    return "".join(out)


def mutate_repo_writable(src: str) -> str:
    """Regression: someone flips the repo mount to read-write."""
    return src.replace(":/repo:ro", ":/repo:rw")


def mutate_inject_creds(src: str) -> str:
    """Regression: someone forwards the operator's provider key into the worker."""
    return src.replace(
        "environment: {}",
        "environment:\n      OPENAI_API_KEY: ${OPENAI_API_KEY:-}",
    )


MUTATIONS = [
    ("no-egress", "network_mode: none deleted", mutate_drop_network),
    ("read-only-repo", "/repo mount flipped to :rw", mutate_repo_writable),
    ("no-ambient-creds", "OPENAI_API_KEY forwarded into the worker", mutate_inject_creds),
]


def run_proof() -> subprocess.CompletedProcess:
    env = dict(os.environ)
    env["DISCOVERY_COMPOSE_FILE"] = MUTANT
    # The proof itself exports poison creds; for the cred mutation to be detectable the variable must
    # exist in the invoking shell, which the proof guarantees. Nothing to set here.
    return subprocess.run(
        [sys.executable, PROOF], env=env, capture_output=True, text=True, timeout=2400, cwd=ROOT,
    )


def main() -> int:
    with open(COMPOSE, encoding="utf-8") as fh:
        original = fh.read()

    print("== red-check: each weakened spec MUST turn the sandbox proof red ==")
    failures = []
    try:
        for claim, description, mutate in MUTATIONS:
            mutated = mutate(original)
            if mutated == original:
                # The mutation did not apply => the spec drifted and this red-check is testing
                # nothing. That is itself a failure: a no-op mutation would "prove" red-ability
                # while changing nothing at all.
                failures.append(f"{claim}: mutation '{description}' did not modify the spec "
                                f"(red-check is inert — the compose file has drifted)")
                print(f"  [FAIL] {claim}: mutation was a no-op — nothing was tested")
                continue

            with open(MUTANT, "w", encoding="utf-8") as fh:
                fh.write(mutated)
            r = run_proof()

            if r.returncode == 0:
                failures.append(f"{claim}: proof PASSED on a spec where {description} — "
                                f"the fence cannot detect this regression")
                print(f"  [FAIL] {claim}: proof stayed GREEN despite '{description}'")
                continue

            # Going red is necessary but not sufficient: it must go red for the RIGHT claim.
            # Otherwise an unrelated crash would masquerade as a working fence.
            blamed = claim in (r.stdout + r.stderr)
            if not blamed:
                failures.append(f"{claim}: proof failed on '{description}' but never named the "
                                f"'{claim}' claim — it may be failing for an unrelated reason")
                print(f"  [FAIL] {claim}: went red, but not attributably ('{claim}' not in output)")
                continue

            print(f"  [PASS] {claim}: '{description}' -> proof correctly went RED (rc={r.returncode})")
    finally:
        if os.path.exists(MUTANT):
            os.remove(MUTANT)

    if failures:
        print(f"\nredcheck: FAIL — the sandbox fence is not trustworthy:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("\nredcheck: PASS — every weakened guarantee is detected by the proof")
    return 0


if __name__ == "__main__":
    sys.exit(main())
