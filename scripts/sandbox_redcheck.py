#!/usr/bin/env python3
"""Red-check: prove the P3 sandbox proof can actually FAIL (sandbox spec §3).

A fence that cannot go red is decoration. scripts/sandbox_proof.py passing tells you nothing until you
know it is capable of failing — a proof with an inverted assertion, a typo'd field name, or a probe
that silently measures nothing would print PASS forever and nobody would notice.

So this deliberately WEAKENS deploy/docker-compose.sandbox.yml, one guarantee at a time, and asserts
the proof goes RED for that specific claim. Same discipline as the discovery red-check, applied to the
P3 isolate posture.

Each mutation is a REAL regression someone could plausibly ship:
  - deleting `network_mode: none`             -> the isolate gets a default bridge network (egress)
  - flipping the /work mount `:ro` -> `:rw`   -> untrusted tool code can rewrite the working set
  - adding `environment: [OPENAI_API_KEY]`    -> the operator's provider key lands in the isolate
  - deleting `pids_limit`                      -> a fork bomb is no longer contained

The weakened spec is written into deploy/ (not /tmp) on purpose: compose `build.context` is resolved
relative to the file's own location, so a copy elsewhere would change what is built and the red-check
would no longer exercise the real image.

Run: make sandbox-proof-redcheck     (needs: Docker)
"""
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
COMPOSE = os.path.join(ROOT, "deploy", "docker-compose.sandbox.yml")
PROOF = os.path.join(ROOT, "scripts", "sandbox_proof.py")
# Same directory as the real spec => identical build context. Dot-prefixed + cleaned up in finally.
MUTANT = os.path.join(ROOT, "deploy", ".redcheck-compose.sandbox.yml")


def mutate_drop_network(src: str) -> str:
    return "".join(ln for ln in src.splitlines(keepends=True) if "network_mode: none" not in ln)


def mutate_workset_writable(src: str) -> str:
    return src.replace(":/work:ro", ":/work:rw")


def mutate_inject_creds(src: str) -> str:
    return src.replace(
        "environment: {}",
        "environment:\n      OPENAI_API_KEY: ${OPENAI_API_KEY:-}",
    )


def mutate_drop_pids_limit(src: str) -> str:
    return "".join(ln for ln in src.splitlines(keepends=True) if "pids_limit:" not in ln)


MUTATIONS = [
    ("no-egress", "network_mode: none deleted", mutate_drop_network),
    ("read-only-workset", "/work mount flipped to :rw", mutate_workset_writable),
    ("no-ambient-creds", "OPENAI_API_KEY forwarded into the isolate", mutate_inject_creds),
    ("resource-bounds", "pids_limit deleted", mutate_drop_pids_limit),
]


def run_proof() -> subprocess.CompletedProcess:
    env = dict(os.environ)
    env["SANDBOX_COMPOSE_FILE"] = MUTANT
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
        print("\nredcheck: FAIL — the sandbox fence is not trustworthy:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("\nredcheck: PASS — every weakened guarantee is detected by the proof")
    return 0


if __name__ == "__main__":
    sys.exit(main())
