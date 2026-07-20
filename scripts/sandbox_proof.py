#!/usr/bin/env python3
"""Least-privilege sandbox proof for the P3 isolate (sandbox spec §3; tasks 3.2–3.5).

internal/sandbox proves, hermetically, the controls a process enforces on itself: scrubbed env, hard
resource bounds, working-set-scoped scratch, and the fail-closed gate. It reports OS-level network
egress denial and filesystem-namespace scoping as UNAVAILABLE on a bare host (so a node requiring them
fails closed). THIS proof is the runtime half those two controls were deferred to: it asserts that
deploy/docker-compose.sandbox.yml — the concrete sandbox.NewContainedEnforcer posture — actually
denies egress and scopes the filesystem, plus enforces no-ambient-creds and resource bounds.

THE RULE THIS FILE OBEYS (same as the discovery proof): assert the SHIPPED spec, never a hand-rolled
`docker run`. Every probe runs `docker compose -f deploy/docker-compose.sandbox.yml`, so deleting
`network_mode: none` from that file turns this proof RED. A proof that built its own hardened command
line would only prove Docker works — it would stay green while the shipped spec rotted.

Each claim is checked twice, both load-bearing:
  STATIC  — the field is present in the resolved compose spec (catches deletion/typo)
  DYNAMIC — the thing the field forbids actually fails (catches "field present but inert")

Run: make sandbox-proof                (needs: Docker)
Self-test the fence: make sandbox-proof-redcheck
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# Overridable so sandbox_redcheck.py can point this proof at a deliberately-weakened copy of the spec
# and assert it goes RED. Defaults to the real shipped spec.
COMPOSE = os.environ.get(
    "SANDBOX_COMPOSE_FILE", os.path.join(ROOT, "deploy", "docker-compose.sandbox.yml")
)
SERVICE = "isolate"

# Poison values exported into the invoking environment. If any reaches the isolate, the no-ambient-creds
# claim is false. These are the real variable names internal/providergateway reads.
POISON = {
    "OPENAI_API_KEY": "sk-poison-must-not-reach-isolate",
    "ANTHROPIC_API_KEY": "sk-ant-poison-must-not-reach-isolate",
    "AWS_SECRET_ACCESS_KEY": "poison-aws-must-not-reach-isolate",
    "QDRANT_API_KEY": "poison-qdrant-must-not-reach-isolate",
    "NEO4J_PASSWORD": "poison-neo4j-must-not-reach-isolate",
}

failures: list[str] = []
checks_run = 0


def record(ok: bool, claim: str, kind: str, detail: str) -> None:
    global checks_run
    checks_run += 1
    tag = "PASS" if ok else "FAIL"
    print(f"  [{tag}] {claim} ({kind}): {detail}")
    if not ok:
        failures.append(f"{claim} ({kind}): {detail}")


def compose_env(workset: str) -> dict:
    """Invoking environment: poison creds + the working-set path the spec requires.

    The poison is exported on PURPOSE. The isolate must not see it despite it being right here in the
    parent environment — that is exactly the regression (`env_file:`, `environment: - OPENAI_API_KEY`)
    this proof exists to catch.
    """
    env = dict(os.environ)
    env.update(POISON)
    env["SANDBOX_WORKSET"] = workset
    return env


def run(args: list[str], env: dict, timeout: int = 900) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["docker", "compose", "-f", COMPOSE] + args,
        env=env, capture_output=True, text=True, timeout=timeout, cwd=ROOT,
    )


def probe(env: dict, shell_cmd: str) -> subprocess.CompletedProcess:
    """Run a shell probe INSIDE the shipped service definition.

    The image's entrypoint is already /bin/sh, so `run ... SERVICE -c '<cmd>'` executes the probe under
    the file's network_mode, read_only, volumes, cap_drop, resource limits and user. The probe measures
    the shipped posture, not a look-alike.
    """
    return run(["run", "--rm", SERVICE, "-c", shell_cmd], env)


def static_checks(spec: dict) -> None:
    """Assert the resolved spec still declares the posture. Catches a weakened file."""
    svc = spec.get("services", {}).get(SERVICE)
    if not svc:
        record(False, "spec", "static", f"service {SERVICE!r} missing from resolved compose spec")
        return

    record(svc.get("network_mode") == "none", "no-egress", "static",
           f"network_mode={svc.get('network_mode')!r} (want 'none')")
    record(svc.get("read_only") is True, "read-only-rootfs", "static",
           f"read_only={svc.get('read_only')!r} (want True)")
    record(svc.get("cap_drop") == ["ALL"], "capabilities", "static",
           f"cap_drop={svc.get('cap_drop')!r} (want ['ALL'])")
    record("no-new-privileges:true" in (svc.get("security_opt") or []), "no-new-privileges", "static",
           f"security_opt={svc.get('security_opt')!r}")
    record(bool(svc.get("user")) and not str(svc.get("user")).startswith("0:"), "non-root", "static",
           f"user={svc.get('user')!r}")

    # The working-set volume must be read-only.
    work_vols = [v for v in (svc.get("volumes") or []) if v.get("target") == "/work"]
    if not work_vols:
        record(False, "read-only-workset", "static", "no volume mounted at /work")
    else:
        record(work_vols[0].get("read_only") is True, "read-only-workset", "static",
               f"/work read_only={work_vols[0].get('read_only')!r} (want True)")

    # Resource bounds must be declared (task 3.5). compose config resolves mem_limit to a byte count and
    # pids_limit to an int; either being absent means the isolate could exhaust the host.
    record(bool(svc.get("mem_limit")), "resource-bounds", "static",
           f"mem_limit={svc.get('mem_limit')!r}" if svc.get("mem_limit") else "mem_limit not set")
    record(bool(svc.get("pids_limit")), "resource-bounds", "static",
           f"pids_limit={svc.get('pids_limit')!r}" if svc.get("pids_limit") else "pids_limit not set")

    # No credential-shaped variable may be declared, and env_file must not be used at all.
    declared = svc.get("environment") or {}
    leaked = sorted(k for k in declared if k in POISON)
    record(not leaked, "no-ambient-creds", "static",
           f"compose declares credential vars {leaked}" if leaked else "no credential vars declared")
    record(not svc.get("env_file"), "no-ambient-creds", "static",
           f"env_file={svc.get('env_file')!r} would forward a secrets file" if svc.get("env_file")
           else "no env_file")


def main() -> int:
    if not shutil.which("docker"):
        print("sandbox-proof: docker not found on PATH — this proof needs Docker.", file=sys.stderr)
        return 2

    print("== sandbox isolate proof: asserting deploy/docker-compose.sandbox.yml ==")
    # Scratch under $HOME, NOT the default temp dir: a VM-backed Docker (colima/lima/Desktop) shares
    # $HOME but not macOS's /var/folders TMPDIR, and bind-mounting an unshared path silently yields an
    # EMPTY root-owned dir in the VM — the probes then "pass" against nothing. $HOME needs no detection.
    scratch_base = os.path.join(os.path.expanduser("~"), ".cache", "heros-agent")
    os.makedirs(scratch_base, exist_ok=True)
    tmp = tempfile.mkdtemp(prefix="isolate-sandbox-", dir=scratch_base)
    workset = os.path.join(tmp, "work")
    os.makedirs(workset)
    # A file the isolate legitimately sees in its working set — proves the read-only probes are not
    # vacuous (a write to an empty auto-created mount also "fails").
    with open(os.path.join(workset, "input.txt"), "w") as f:
        f.write("working-set-visible\n")
    env = compose_env(workset)

    try:
        cfg = run(["config", "--format", "json"], env)
        if cfg.returncode != 0:
            print(f"sandbox-proof: `compose config` failed:\n{cfg.stderr}", file=sys.stderr)
            return 1
        spec = json.loads(cfg.stdout)

        print("\n-- static: the shipped spec declares the posture --")
        static_checks(spec)

        print("\n-- build --")
        b = run(["build", SERVICE], env, timeout=1800)
        if b.returncode != 0:
            print(f"sandbox-proof: build failed:\n{b.stdout[-3000:]}\n{b.stderr[-3000:]}", file=sys.stderr)
            return 1
        print("  image built")

        # ---- CLAIM: read-only, working-set-scoped filesystem (task 3.4) -------------------------
        print("\n-- dynamic: read-only working-set-scoped filesystem --")
        p = probe(env, "test -f /work/input.txt && echo VISIBLE || echo EMPTY_MOUNT")
        mounted = "VISIBLE" in p.stdout
        record(mounted, "read-only-workset", "precondition",
               "working set is really visible inside /work (probes are not vacuous)" if mounted
               else "/work does NOT contain the working set — the bind mount did not deliver it, so the "
                    "read-only probes below would pass without testing anything")
        p = probe(env, "echo mutated > /work/PROOF_WRITE 2>&1; echo rc=$?")
        record("rc=0" not in p.stdout, "read-only-workset", "dynamic",
               "write to /work rejected (working set is read-only)")
        record(not os.path.exists(os.path.join(workset, "PROOF_WRITE")), "read-only-workset", "dynamic",
               "no file appeared in the host working set")
        # A path outside the working set must not be writable either (read-only rootfs). /scratch is the
        # single writable surface and it is ephemeral.
        p = probe(env, "echo x > /etc/PROOF_ESCAPE 2>&1; echo rc=$?")
        record("rc=0" not in p.stdout, "read-only-rootfs", "dynamic",
               "write outside the working set (/etc) rejected — rootfs is read-only")
        p = probe(env, "echo x > /scratch/ok 2>&1 && echo WROTE_SCRATCH")
        record("WROTE_SCRATCH" in p.stdout, "ephemeral-scratch", "dynamic",
               "the ephemeral /scratch is the single writable surface")

        # ---- CLAIM: no network egress (task 3.3) ------------------------------------------------
        print("\n-- dynamic: no network egress --")
        p = probe(env, r"grep -oE '^ *[a-z0-9]+:' /proc/net/dev | tr -d ' :'")
        ifaces = [l for l in p.stdout.strip().splitlines() if l]
        non_lo = [i for i in ifaces if i != "lo"]
        record(bool(ifaces) and not non_lo, "no-egress", "dynamic",
               f"only loopback exists (interfaces={ifaces or 'UNREADABLE'})")
        p = probe(env, "(timeout 5 sh -c 'exec 3<>/dev/tcp/1.1.1.1/443' && echo REACHED) 2>&1; echo done")
        record("REACHED" not in p.stdout, "no-egress", "dynamic",
               "outbound TCP connect to 1.1.1.1:443 failed (no route)")
        # The cloud metadata endpoint specifically must be unreachable (credential-theft path).
        p = probe(env, "(timeout 5 sh -c 'exec 3<>/dev/tcp/169.254.169.254/80' && echo REACHED) 2>&1; echo done")
        record("REACHED" not in p.stdout, "no-egress", "dynamic",
               "cloud metadata endpoint 169.254.169.254 unreachable")

        # ---- CLAIM: no ambient provider creds (task 3.2) ----------------------------------------
        print("\n-- dynamic: no ambient provider creds --")
        p = probe(env, "env")
        container_env = p.stdout
        for var, value in POISON.items():
            present = (f"{var}=" in container_env) or (value in container_env)
            record(not present, "no-ambient-creds", "dynamic",
                   f"{var} absent from the isolate environment"
                   if not present else f"{var} LEAKED into the isolate despite being unset in the spec")

        # ---- CLAIM: resource bounds contain a runaway (task 3.5) --------------------------------
        print("\n-- dynamic: resource bounds are applied to the isolate --")
        # Primary, deterministic check: the cgroup's own pids.max reflects the enforced limit. Reading it
        # from inside the isolate proves the cgroup pids controller is applied to THIS container (not that
        # a limit exists somewhere). cgroup v2 exposes it at /sys/fs/cgroup/pids.max; a finite number
        # (not "max") means the fork ceiling is in force.
        p = probe(env, "cat /sys/fs/cgroup/pids.max 2>/dev/null || cat /sys/fs/cgroup/pids/pids.max 2>/dev/null || echo UNREADABLE")
        pmax = (p.stdout.strip().splitlines() or ["UNREADABLE"])[-1].strip()
        record(pmax.isdigit() and int(pmax) > 0, "resource-bounds", "dynamic",
               f"cgroup pids.max = {pmax} (a finite fork ceiling is enforced on the isolate)"
               if pmax.isdigit() else f"pids.max = {pmax!r} — no finite fork ceiling is applied")
        # Secondary: a fork bomb actually hits that ceiling. The container is --rm, so survivors die with
        # it. Counting fork failures is best-effort (shell-buffering dependent), so this only STRENGTHENS
        # the verdict — the pids.max read above is the load-bearing check.
        p = probe(env, "i=0; while [ $i -lt 600 ]; do sleep 30 & i=$((i+1)); done 2>&1 | "
                        "grep -ci 'resource temporarily\\|cannot allocate\\|fork' || true")
        try:
            forks_blocked = int((p.stdout.strip().splitlines() or ["0"])[-1])
        except ValueError:
            forks_blocked = 0
        print(f"  [info] fork-bomb secondary signal: {forks_blocked} fork failure line(s) observed")

        # ---- the posture must not break a legitimate tool ---------------------------------------
        print("\n-- dynamic: a legitimate tool still runs under this posture --")
        p = probe(env, "cat /work/input.txt > /scratch/out.txt && cat /scratch/out.txt")
        record("working-set-visible" in p.stdout, "isolate-functional", "dynamic",
               "a benign tool read its working set and wrote its scratch under the full posture"
               if "working-set-visible" in p.stdout else f"benign tool failed: {p.stdout!r} {p.stderr!r}")

    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    print(f"\n== {checks_run} checks run ==")
    if failures:
        print(f"sandbox-proof: FAIL — {len(failures)} violation(s):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("sandbox-proof: PASS — no egress, read-only working-set FS, no ambient creds, resource bounds "
          "all enforced by the shipped isolate posture")
    return 0


if __name__ == "__main__":
    sys.exit(main())
