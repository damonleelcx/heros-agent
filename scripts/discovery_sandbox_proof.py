#!/usr/bin/env python3
"""Least-privilege sandbox proof for the Discovery worker (P1 task 7.2 / NFR7 / invariant I8).

Task 7.2 claims three things: read-only repo mount, no network egress, no ambient provider creds.
`internal/discovery/noexec_test.go` proves the *code* cannot ask for them. This proves the *runtime*
does not grant them — the half docs/discovery/10-hardening-review.md §7.2 deferred to DevOps.

Why both halves are needed: `discover` parses untrusted source with tree-sitter's C runtime via cgo
(CGO_ENABLED=0 does not link). A Go import guard says nothing about what that C code does on a
hostile input. The import guard constrains our intent; the sandbox bounds someone else's bug.

THE RULE THIS FILE OBEYS: assert the SHIPPED spec, never a hand-rolled `docker run`.
Every probe below runs `docker compose -f deploy/docker-compose.discovery.yml`, so deleting
`network_mode: none` from that file turns this proof RED. A proof that built its own hardened
command line would only prove Docker works — it would stay green while the shipped spec rotted.

Each claim is checked twice, and both are load-bearing:
  STATIC  — the field is present in the resolved compose spec (catches deletion/typo)
  DYNAMIC — the thing the field forbids actually fails (catches "field present but inert")
A static check alone would pass on a field Docker silently ignores; a dynamic check alone would pass
if a probe were accidentally testing nothing. Neither is sufficient.

Run: make discovery-sandbox-proof     (needs: Docker)
Self-test the fence: make discovery-sandbox-proof-redcheck
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# Overridable so discovery_sandbox_redcheck.py can point this proof at a deliberately-weakened copy
# of the spec and assert it goes RED. Defaults to the real shipped spec.
COMPOSE = os.environ.get(
    "DISCOVERY_COMPOSE_FILE", os.path.join(ROOT, "deploy", "docker-compose.discovery.yml")
)
FIXTURE = os.path.join(ROOT, "internal", "discovery", "testdata", "fixtures", "golden")
SERVICE = "discover"

# Poison values exported into the invoking environment. If any reaches the container, claim 3 is
# false. These are the real variable names internal/providergateway/secrets.go reads.
POISON = {
    "OPENAI_API_KEY": "sk-poison-must-not-reach-worker",
    "ANTHROPIC_API_KEY": "sk-ant-poison-must-not-reach-worker",
    "AWS_SECRET_ACCESS_KEY": "poison-aws-must-not-reach-worker",
    "QDRANT_API_KEY": "poison-qdrant-must-not-reach-worker",
    "NEO4J_PASSWORD": "poison-neo4j-must-not-reach-worker",
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


def compose_env(repo: str, out: str) -> dict:
    """Invoking environment: poison creds + the two paths the spec requires.

    The poison is exported on PURPOSE. The container must not see it despite it being right here in
    the parent environment — that is exactly the regression (`env_file:`, `environment: - OPENAI_API_KEY`)
    this proof exists to catch.
    """
    env = dict(os.environ)
    env.update(POISON)
    env["DISCOVER_REPO"] = repo
    env["DISCOVER_OUT"] = out
    return env


def run(args: list[str], env: dict, timeout: int = 900) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["docker", "compose", "-f", COMPOSE] + args,
        env=env, capture_output=True, text=True, timeout=timeout, cwd=ROOT,
    )


def probe(env: dict, shell_cmd: str) -> subprocess.CompletedProcess:
    """Run a shell probe INSIDE the shipped service definition.

    --entrypoint changes only the command; compose still applies network_mode, read_only, volumes,
    cap_drop and user from the file under test. So the probe measures the shipped posture.
    """
    return run(["run", "--rm", "--entrypoint", "/bin/sh", SERVICE, "-c", shell_cmd], env)


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

    # The repo volume must be read-only. Compose resolves the long form, so check the parsed entry.
    repo_vols = [v for v in (svc.get("volumes") or []) if v.get("target") == "/repo"]
    if not repo_vols:
        record(False, "read-only-repo", "static", "no volume mounted at /repo")
    else:
        record(repo_vols[0].get("read_only") is True, "read-only-repo", "static",
               f"/repo read_only={repo_vols[0].get('read_only')!r} (want True)")

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
        print("discovery-sandbox-proof: docker not found on PATH — this proof needs Docker.",
              file=sys.stderr)
        return 2

    print("== discovery sandbox proof: asserting deploy/docker-compose.discovery.yml ==")
    # Scratch lives under $HOME, NOT the default temp dir, and this is load-bearing on macOS.
    # A VM-backed Docker (colima/lima/Docker Desktop) only shares certain host paths — $HOME among
    # them. macOS's TMPDIR (/var/folders/...) is NOT shared, so bind-mounting a path there does not
    # fail: Docker silently creates an EMPTY root-owned directory in the VM instead. The probes then
    # "pass" against an empty mount while proving nothing. $HOME is shared by every common Docker
    # backend and is trivially local on Linux CI, so it needs no detection and has no fallback.
    scratch_base = os.path.join(os.path.expanduser("~"), ".cache", "heros-agent")
    os.makedirs(scratch_base, exist_ok=True)
    tmp = tempfile.mkdtemp(prefix="discover-sandbox-", dir=scratch_base)
    repo = os.path.join(tmp, "repo")
    out = os.path.join(tmp, "out")
    shutil.copytree(FIXTURE, repo)
    os.makedirs(out)
    # World-writable: the container runs as uid 65532, which does not exist on the host. Loosening
    # the OUTPUT dir is safe and is not what is under test; /repo stays read-only via the mount.
    os.chmod(out, 0o777)
    env = compose_env(repo, out)

    try:
        # ---- resolve the shipped spec -----------------------------------------------------------
        cfg = run(["config", "--format", "json"], env)
        if cfg.returncode != 0:
            print(f"discovery-sandbox-proof: `compose config` failed:\n{cfg.stderr}", file=sys.stderr)
            return 1
        spec = json.loads(cfg.stdout)

        print("\n-- static: the shipped spec declares the posture --")
        static_checks(spec)

        print("\n-- build --")
        b = run(["build", SERVICE], env, timeout=1800)
        if b.returncode != 0:
            print(f"discovery-sandbox-proof: build failed:\n{b.stdout[-3000:]}\n{b.stderr[-3000:]}",
                  file=sys.stderr)
            return 1
        print("  image built")

        # ---- CLAIM 1: read-only repo mount ------------------------------------------------------
        print("\n-- dynamic: CLAIM 1 read-only repo mount --")
        # PRECONDITION, and it is not ceremony: every read-only probe below is vacuous if the bind
        # mount did not actually deliver the fixture. A write to an empty auto-created mount also
        # "fails", so the claim would look proven while nothing was tested. This exact false-green
        # happened during development (macOS TMPDIR is not shared into the Docker VM). Assert the
        # fixture is really visible before believing anything the probes say about it.
        p = probe(env, "test -f /repo/go.mod && echo VISIBLE || echo EMPTY_MOUNT")
        mounted = "VISIBLE" in p.stdout
        record(mounted, "read-only-repo", "precondition",
               "fixture is really visible inside /repo (probes are not vacuous)" if mounted
               else "/repo does NOT contain the fixture — the bind mount did not deliver it, so every "
                    "read-only probe below would pass without testing anything")
        p = probe(env, "echo mutated > /repo/PROOF_WRITE 2>&1; echo rc=$?")
        record("rc=0" not in p.stdout, "read-only-repo", "dynamic",
               f"write to /repo rejected ({p.stdout.strip().splitlines()[-1] if p.stdout.strip() else 'no output'})")
        record(not os.path.exists(os.path.join(repo, "PROOF_WRITE")), "read-only-repo", "dynamic",
               "no file appeared in the host repo")
        # Deleting is a mutation too, and a different syscall path than create.
        p = probe(env, "rm -f /repo/go.mod 2>&1; echo rc=$?")
        record("rc=0" not in p.stdout or os.path.exists(os.path.join(repo, "go.mod")),
               "read-only-repo", "dynamic", "delete inside /repo did not remove the host file")

        # ---- CLAIM 2: no network egress ---------------------------------------------------------
        # Probes read /proc/net/dev and use bash's /dev/tcp: both are already in the base image, so
        # the proof does not install iproute2/netcat into the very image whose minimality it asserts.
        print("\n-- dynamic: CLAIM 2 no network egress --")
        p = probe(env, r"grep -c ':' /proc/net/dev | tail -1; grep -oE '^ *[a-z0-9]+:' /proc/net/dev | tr -d ' :'")
        lines = [l for l in p.stdout.strip().splitlines() if l]
        ifaces = [l for l in lines[1:] if l]
        non_lo = [i for i in ifaces if i != "lo"]
        record(bool(ifaces) and not non_lo, "no-egress", "dynamic",
               f"only loopback exists (interfaces={ifaces or 'UNREADABLE'})")
        # An actual connect() must not succeed. With network_mode:none there is no route at all.
        p = probe(env, "(timeout 5 bash -c 'echo > /dev/tcp/1.1.1.1/443' && echo REACHED) 2>&1; echo done")
        record("REACHED" not in p.stdout, "no-egress", "dynamic",
               "outbound TCP connect to 1.1.1.1:443 failed (no route)")

        # ---- CLAIM 3: no ambient provider creds -------------------------------------------------
        print("\n-- dynamic: CLAIM 3 no ambient provider creds --")
        p = probe(env, "env")
        container_env = p.stdout
        for var, value in POISON.items():
            present = (f"{var}=" in container_env) or (value in container_env)
            record(not present, "no-ambient-creds", "dynamic",
                   f"{var} absent from worker environment"
                   if not present else f"{var} LEAKED into the worker despite being unset in the spec")

        # ---- the posture must not break the actual job ------------------------------------------
        # A sandbox that also breaks discovery would "pass" every check above while being useless.
        print("\n-- dynamic: the real worker still does its job under this posture --")
        r = run(["run", "--rm", SERVICE], env)
        ir = os.path.join(out, "ir.json")
        ok = r.returncode == 0 and os.path.exists(ir) and os.path.getsize(ir) > 0
        record(ok, "worker-functional", "dynamic",
               "discover ran read-only + network-less + cred-less and emitted a non-empty IR"
               if ok else f"discover FAILED under the posture (rc={r.returncode}): "
                          f"{(r.stderr or r.stdout)[-500:]}")

    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    print(f"\n== {checks_run} checks run ==")
    if failures:
        print(f"discovery-sandbox-proof: FAIL — {len(failures)} violation(s):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("discovery-sandbox-proof: PASS — read-only mount, no egress, no ambient creds all enforced")
    return 0


if __name__ == "__main__":
    sys.exit(main())
