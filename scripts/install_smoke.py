#!/usr/bin/env python3
"""install_smoke.py — the installer's fresh-machine smoke matrix and tamper red-check (P20 tasks 3.1, 6.3, 7.2, 7.3).

# Why this exists rather than a shellcheck pass

`scripts/install.sh` is the one piece of P20 that runs on a machine nobody controls, and its most important
behaviour is a REFUSAL. A static check proves the script parses; it proves nothing about whether the signature
step can actually reject a tampered download. A verify step that has never been observed to reject is treated
as absent (QA rule), so every case below either installs a binary or asserts that nothing was installed.

# What is genuinely fresh here, and what is not

  --host              runs on THIS machine: a real macOS install, with the host's own ssh-keygen as the
                      verifier and the host's quarantine behaviour intact. Not a clean OS — it is the
                      maintainer's machine, and the report says so.
  --linux-container   builds a REAL native linux binary inside `golang:1.24` (so it is a native build, not a
                      cross-CGO one — D1), then installs it inside a `debian:12` container that has no Go, no
                      compiler, and no heros: a genuinely clean OS. curl and openssh-client are apt-installed
                      as the machine's baseline, which is what a real Debian/Ubuntu host has.

Windows is NOT covered here — see scripts/install_smoke.ps1 and the honest statement of what was executed in
docs/release/p20-evidence.md. Claiming a Windows smoke run from a Mac would be exactly the kind of green that
means nothing.

# The signing key

The whole point is that the PINNED key in install.sh verifies. So the fixture must be signed with the real
release key, and this script REQUIRES HEROS_RELEASE_PRIVATE_KEY. It does not skip without it: an env-gated
test that silently passes is worse than no test, because it reports success for a run that did not happen.
"""

from __future__ import annotations

import argparse
import functools
import shlex
import hashlib
import http.server
import json
import os
import shutil
import socketserver
import subprocess
import sys
import tempfile
import threading
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VERSION = os.environ.get("HEROS_SMOKE_VERSION", "0.20.0-smoke.1")
# The version an upgrade simulation moves TO. Higher than VERSION, so `heros upgrade` treats it as an upgrade
# rather than refusing it as a downgrade.
NEXT_VERSION = os.environ.get("HEROS_SMOKE_NEXT_VERSION", "0.20.1-smoke.1")
# A real repository with a discoverable agent workflow. `--help` proves nothing about an install; a first
# `discover` + `eval` that produces numbers is the assertion task 7.2 actually asks for.
FIXTURE_REPO = REPO_ROOT / "internal" / "discovery" / "testdata" / "samplerepo"


# ── plumbing ────────────────────────────────────────────────────────────────────────────────────────────
def run(cmd, **kw):
    kw.setdefault("cwd", REPO_ROOT)
    kw.setdefault("text", True)
    kw.setdefault("capture_output", True)
    env = dict(os.environ, GOWORK="off")
    env.update(kw.pop("extra_env", {}))
    kw["env"] = env
    return subprocess.run(cmd, **kw)


def must(cmd, what, **kw):
    r = run(cmd, **kw)
    if r.returncode != 0:
        print(f"⛔ {what} failed:\n{r.stdout}\n{r.stderr}", file=sys.stderr)
        sys.exit(1)
    return r.stdout


def sha256_file(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):  # the fixture server's access log is noise in a test report
        pass


def serve(directory: Path):
    """Serve directory on an ephemeral localhost port. Returns (port, shutdown)."""
    handler = functools.partial(QuietHandler, directory=str(directory))

    class Server(socketserver.TCPServer):
        allow_reuse_address = True

    httpd = Server(("0.0.0.0", 0), handler)
    port = httpd.server_address[1]
    t = threading.Thread(target=httpd.serve_forever, daemon=True)
    t.start()
    return port, httpd.shutdown


# ── the release fixture ─────────────────────────────────────────────────────────────────────────────────
def build_host_binary(out: Path) -> str:
    """Build this host's native binary through the same script the release uses."""
    out.mkdir(parents=True, exist_ok=True)
    goos = must(["go", "env", "GOOS"], "go env").strip()
    goarch = must(["go", "env", "GOARCH"], "go env").strip()
    must(["bash", "scripts/release-cli.sh", VERSION], "host release build",
         extra_env={"OUT": str(out)})
    return f"heros-{VERSION}-{goos}-{goarch}"


def build_linux_binary(out: Path) -> str:
    """Build a REAL native linux binary inside a Go container — not a cross-CGO build (D1)."""
    out.mkdir(parents=True, exist_ok=True)
    arch = must(["go", "env", "GOARCH"], "go env").strip()  # the container is native to this host's arch
    print(f"   building linux/{arch} natively inside golang:1.24 (cgo tree-sitter needs a real toolchain)")
    # The host's module cache is mounted read-only and the proxy is turned OFF. Two reasons, and the second
    # is the important one:
    #   1. this machine's docker cannot reach proxy.golang.org, so a fetching build times out after minutes;
    #   2. GOPROXY=off makes the build HERMETIC — it compiles exactly the module versions the host resolved,
    #      so a dependency that changed upstream cannot quietly make the container's binary differ from the
    #      one the release pipeline would produce.
    modcache = must(["go", "env", "GOMODCACHE"], "go env GOMODCACHE").strip()
    r = run([
        "docker", "run", "--rm",
        "-v", f"{REPO_ROOT}:/src",
        "-v", f"{out}:/out",
        "-v", f"{modcache}:/go/pkg/mod:ro",
        "-w", "/src",
        "-e", "GOWORK=off", "-e", "CGO_ENABLED=1",
        "-e", "GOFLAGS=-buildvcs=false -mod=mod", "-e", "GOPROXY=off",
        "-e", "GOCACHE=/tmp/gocache",
        "-e", "OUT=/out",
        "golang:1.24",
        "bash", "scripts/release-cli.sh", VERSION,
    ], timeout=3600)
    if r.returncode != 0:
        print(f"⛔ native linux build failed:\n{r.stdout}\n{r.stderr}", file=sys.stderr)
        sys.exit(1)
    name = f"heros-{VERSION}-linux-{arch}"
    # Asserted, not assumed. A bind mount on a path Docker does not share succeeds, the container writes
    # happily, and nothing appears on the host — an exit code of 0 with no artifact. Checking here turns that
    # into one clear failure instead of a confusing FileNotFoundError three functions later.
    if not (out / name).exists():
        print(f"⛔ the container reported success but {out / name} does not exist.\n"
              f"   The bind mount did not propagate — Docker on this host shares only some paths. Stage the\n"
              f"   work directory somewhere Docker shares (the repository, $HOME) rather than the system temp.\n"
              f"   container output:\n{r.stdout}", file=sys.stderr)
        sys.exit(1)
    return name


def build_next_version(asset_dir: Path, current_asset: str) -> str | None:
    """Build the N+1 binary for the upgrade simulation, on the same host and by the same script.

    It is a REAL build rather than a copy of N with a new name, and that matters: a copied binary reports N's
    version from its own ldflags stamp, so an upgrade to it would look like it worked and leave the user on the
    old version. The packaging proof caught exactly that mistake once already — a renamed artifact is not a
    different artifact.
    """
    # Same OS/arch as the current asset, by construction: this host built it.
    goos = must(["go", "env", "GOOS"], "go env").strip()
    goarch = must(["go", "env", "GOARCH"], "go env").strip()
    if current_asset.startswith(f"heros-{VERSION}-linux-") and goos != "linux":
        # The linux clean-room case: build N+1 in the same hermetic container the N binary came from.
        modcache = must(["go", "env", "GOMODCACHE"], "go env GOMODCACHE").strip()
        r = run(["docker", "run", "--rm",
                 "-v", f"{REPO_ROOT}:/src", "-v", f"{asset_dir}:/out",
                 "-v", f"{modcache}:/go/pkg/mod:ro", "-w", "/src",
                 "-e", "GOWORK=off", "-e", "CGO_ENABLED=1",
                 "-e", "GOFLAGS=-buildvcs=false -mod=mod", "-e", "GOPROXY=off",
                 "-e", "GOCACHE=/tmp/gocache", "-e", "OUT=/out",
                 "golang:1.24", "bash", "scripts/release-cli.sh", NEXT_VERSION], timeout=3600)
        if r.returncode != 0:
            print(f"   (N+1 container build failed: {r.stderr[-400:]})")
            return None
        arch = must(["go", "env", "GOARCH"], "go env").strip()
        name = f"heros-{NEXT_VERSION}-linux-{arch}"
    else:
        r = run(["bash", "scripts/release-cli.sh", NEXT_VERSION], extra_env={"OUT": str(asset_dir)}, timeout=1800)
        if r.returncode != 0:
            print(f"   (N+1 build failed: {r.stderr[-400:]})")
            return None
        name = f"heros-{NEXT_VERSION}-{goos}-{goarch}"
    return name if (asset_dir / name).exists() else None


def build_deb(work: Path, asset_dir: Path, asset: str) -> Path | None:
    """Generate the nfpm config from the signed manifest and build a real .deb from the built linux binary.

    The config is GENERATED rather than written here (D5): a .deb whose version came from this script instead of
    from the tag would be exactly the second copy the whole contract forbids.
    """
    arch = must(["go", "env", "GOARCH"], "go env").strip()
    dist = work / "debdist"
    dist.mkdir(parents=True, exist_ok=True)
    shutil.copy2(asset_dir / asset, dist / asset)
    sums = f"{sha256_file(dist / asset)}  {asset}\n"
    (dist / "SHA256SUMS").write_text(sums)
    (dist / "trust.json").write_text(json.dumps({"version": VERSION, "assets": [asset]}))
    # --only deb: the nfpm config needs the linux rows and nothing else. Generating every channel here would
    # demand darwin and windows assets this smoke run never built — and the generator is right to refuse those,
    # since a formula with a missing checksum is worse than no formula.
    r = run(["go", "run", "./cmd/herosdist", "manifests", "--tag", f"v{VERSION}",
             "--dir", str(dist), "--out", str(dist / "packaging"), "--only", "deb"], timeout=600)
    if r.returncode != 0:
        print(f"   (manifest generation for the .deb failed: {r.stderr[-300:]})")
        return None
    gopath = must(["go", "env", "GOPATH"], "go env").strip()
    nfpm = Path(gopath) / "bin" / "nfpm"
    if not nfpm.exists():
        install = run(["go", "install", "github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.1"], timeout=1800)
        if install.returncode != 0 or not nfpm.exists():
            return None
    r = run([str(nfpm), "package", "--config", f"packaging/nfpm/nfpm-{arch}.yaml",
             "--packager", "deb", "--target", "."], cwd=str(dist), timeout=900)
    if r.returncode != 0:
        print(f"   (nfpm failed: {r.stderr[-300:]})")
        return None
    debs = sorted(dist.glob("*.deb"))
    return debs[0] if debs else None


def stage_fixture(root: Path, asset_dir: Path, asset: str, *, sign_key: str,
                  tamper_binary=False, tamper_manifest=False, drop_signatures=False,
                  foreign_key=False, version: str | None = None) -> None:
    """Lay out a release fixture with the same URL shape as GitHub Releases.

    Each keyword is one way a real release can be wrong, and each must produce a refusal.
    """
    version = version or VERSION
    dl = root / "releases" / "download" / f"v{version}"
    api = root / "api"
    if dl.exists():
        shutil.rmtree(dl)
    dl.mkdir(parents=True)
    api.mkdir(parents=True, exist_ok=True)

    shutil.copy2(asset_dir / asset, dl / asset)
    (dl / asset).chmod(0o755)

    # The manifest the RELEASE signed is always the one over the ORIGINAL bytes. Everything below models an
    # attacker who controls what is served but not the signing key — which is the only threat model in which
    # the signature earns its place.
    signed_manifest = f"{sha256_file(dl / asset)}  {asset}\n"
    served_manifest = signed_manifest

    if tamper_binary:
        # One byte, in the middle. A truncation would be caught by a length check; this is the case that only
        # a checksum finds.
        data = bytearray((dl / asset).read_bytes())
        data[len(data) // 2] ^= 0x01
        (dl / asset).write_bytes(bytes(data))

    if tamper_manifest:
        # The attacker also rewrites the manifest so it matches the bytes they substituted. The checksum step
        # now PASSES, and only the signature — computed over the original manifest — can catch it. This is the
        # case that answers "why sign at all when you already publish checksums".
        served_manifest = f"{sha256_file(dl / asset)}  {asset}\n"

    (dl / "SHA256SUMS").write_text(served_manifest)

    if not drop_signatures:
        key = sign_key
        if foreign_key:
            out = must(["go", "run", "./cmd/herossign", "keygen"], "keygen")
            key = [l.split("=", 1)[1] for l in out.splitlines() if l.startswith("HEROS_RELEASE_PRIVATE_KEY")][0]
        env = {"HEROS_RELEASE_PRIVATE_KEY": key}
        # Signed over signed_manifest, which is NOT necessarily what is served.
        signed_path = dl / ".signed-manifest"
        signed_path.write_text(signed_manifest)
        raw = must(["go", "run", "./cmd/herossign", "sign", "--in", str(signed_path)],
                   "raw sign", extra_env=env)
        (dl / "SHA256SUMS.sig").write_text(raw)
        ssh = must(["go", "run", "./cmd/herossign", "sign", "--ssh", "--in", str(signed_path)],
                   "sshsig sign", extra_env=env)
        (dl / "SHA256SUMS.sshsig").write_text(ssh)
        signed_path.unlink()

    (api / "latest").write_text(json.dumps({"tag_name": f"v{version}"}))


# ── cases ───────────────────────────────────────────────────────────────────────────────────────────────
CASES = [
    ("happy-path", {}, "install"),
    ("tampered-binary", {"tamper_binary": True}, "refuse"),
    ("tampered-manifest", {"tamper_binary": True, "tamper_manifest": True}, "refuse"),
    ("unsigned-release", {"drop_signatures": True}, "refuse"),
    ("foreign-signing-key", {"foreign_key": True}, "refuse"),
]


class Runner:
    """Runs install.sh somewhere and reports what happened. Subclassed per environment."""

    name = "?"
    fresh = False

    def install(self, base_url, api_url, extra_env=None) -> subprocess.CompletedProcess: ...
    def installed_version(self) -> str | None: ...
    def uninstall(self) -> subprocess.CompletedProcess: ...

    # heros(args) runs the INSTALLED binary — never a `go run` — because the thing under test is the artifact a
    # user ends up with, and a `go run` would exercise the source tree instead.
    def heros(self, args: list[str], cwd_repo: bool = True,
              extra_env: dict | None = None) -> subprocess.CompletedProcess: ...


class HostRunner(Runner):
    name = "host (this machine — macOS, NOT a clean OS)"

    def __init__(self, workdir: Path):
        self.bin = workdir / "bin"
        self.bin.mkdir(parents=True, exist_ok=True)

    def install(self, base_url, api_url, extra_env=None):
        env = {"HEROS_RELEASE_BASE_URL": base_url, "HEROS_RELEASE_API_URL": api_url,
               "HEROS_INSTALL_DIR": str(self.bin)}
        env.update(extra_env or {})
        return run(["sh", "scripts/install.sh"], extra_env=env, timeout=300)

    def installed_version(self):
        exe = self.bin / "heros"
        if not exe.exists():
            return None
        r = run([str(exe), "version"], timeout=120)
        try:
            return json.loads(r.stdout)["data"]["tool_version"]
        except Exception:
            return f"<unparseable: {r.stdout[:80]}>"

    def uninstall(self):
        return run(["sh", "scripts/install.sh"],
                   extra_env={"HEROS_UNINSTALL": "1", "HEROS_INSTALL_DIR": str(self.bin)}, timeout=120)

    def heros(self, args, cwd_repo=True, extra_env=None):
        exe = self.bin / "heros"
        cwd = str(FIXTURE_REPO) if cwd_repo else str(self.bin)
        return run([str(exe), *args], cwd=cwd, timeout=900, extra_env=extra_env or {})


CLEANROOM_DOCKERFILE = """FROM debian:12
# A genuinely clean machine: no Go, no compiler, no heros. curl and openssh-client are the BASELINE a real
# Debian/Ubuntu host has — openssh-client is what provides `ssh-keygen -Y verify`, the installer's verifier.
# Nothing else is added, so anything the installer needs and does not find is a real finding.
RUN apt-get update -qq \\
 && apt-get install -y -qq --no-install-recommends curl openssh-client ca-certificates \\
 && rm -rf /var/lib/apt/lists/*
"""


class CleanContainerRunner(Runner):
    name = "debian:12 container (clean OS — no Go, no compiler, no prior heros)"
    fresh = True
    image = "heros-install-cleanroom:test"

    def __init__(self, workdir: Path):
        self.workdir = workdir
        ctx = workdir / "cleanroom"
        ctx.mkdir(parents=True, exist_ok=True)
        (ctx / "Dockerfile").write_text(CLEANROOM_DOCKERFILE)
        print("   building the clean-room image (debian:12 + curl + openssh-client)")
        r = run(["docker", "build", "-q", "-t", self.image, str(ctx)], timeout=1800)
        if r.returncode != 0:
            print(f"⛔ clean-room image build failed:\n{r.stdout}\n{r.stderr}", file=sys.stderr)
            sys.exit(1)
        self.state = workdir / "container-bin"
        self.state.mkdir(parents=True, exist_ok=True)

    def _docker(self, script, extra_env=None, timeout=600):
        env_args = []
        for k, v in (extra_env or {}).items():
            env_args += ["-e", f"{k}={v}"]
        return run(["docker", "run", "--rm",
                    "-v", f"{REPO_ROOT / 'scripts'}:/scripts:ro",
                    "-v", f"{FIXTURE_REPO}:/fixture:ro",
                    "-v", f"{self.state}:/opt/heros-bin",
                    *env_args, self.image, "sh", "-c", script], timeout=timeout)

    def install(self, base_url, api_url, extra_env=None):
        env = {"HEROS_RELEASE_BASE_URL": base_url, "HEROS_RELEASE_API_URL": api_url,
               "HEROS_INSTALL_DIR": "/opt/heros-bin"}
        env.update(extra_env or {})
        return self._docker("sh /scripts/install.sh", extra_env=env)

    def installed_version(self):
        if not (self.state / "heros").exists():
            return None
        r = self._docker("/opt/heros-bin/heros version 2>/dev/null")
        try:
            return json.loads(r.stdout)["data"]["tool_version"]
        except Exception:
            return f"<unparseable: {r.stdout[:120]}>"

    def uninstall(self):
        return self._docker("sh /scripts/install.sh",
                            extra_env={"HEROS_UNINSTALL": "1", "HEROS_INSTALL_DIR": "/opt/heros-bin"})

    def heros(self, args, cwd_repo=True, extra_env=None):
        # The fixture repo is COPIED into a writable location inside the container before the command runs: it is
        # mounted read-only (so the container cannot alter the repository), but `discover` writes its IR and
        # report, and an install that "works" until the first write is not a working install.
        quoted = " ".join(shlex.quote(a) for a in args)
        cd = "cp -r /fixture /tmp/repo 2>/dev/null; cd /tmp/repo" if cwd_repo else "cd /tmp"
        return self._docker(f"{cd} && /opt/heros-bin/heros {quoted}", extra_env=extra_env, timeout=1800)

    def install_deb(self, deb_path: Path):
        """Install the .deb through dpkg — the channel's OWN idiom, in a container that has dpkg."""
        return run(["docker", "run", "--rm",
                    "-v", f"{deb_path.parent}:/pkg:ro",
                    "-v", f"{FIXTURE_REPO}:/fixture:ro",
                    self.image, "sh", "-c",
                    f"dpkg -i /pkg/{deb_path.name} >/dev/null 2>&1 && "
                    f"cp -r /fixture /tmp/repo && cd /tmp/repo && /usr/bin/heros version 2>/dev/null && "
                    f"cd / && dpkg -r heros >/dev/null 2>&1 && "
                    # 🔴 `test -e` on the PATH, not `command -v`. dash CACHES a command's resolved path after
                    # the first invocation, so `command -v heros` still answers after dpkg has deleted the file
                    # — which reported a correct `dpkg -r` as a failure the first time this ran.
                    f"(test -e /usr/bin/heros && echo STILL-PRESENT || echo REMOVED)"],
                   timeout=1800)


def exercise(runner: Runner, fixture_root: Path, asset_dir: Path, asset: str, key: str,
             base_url: str, api_url: str, refusals_only: bool = False) -> list[tuple[str, bool, str]]:
    results = []
    for case, kwargs, expect in CASES:
        if refusals_only and expect == "install":
            continue
        runner.uninstall()
        stage_fixture(fixture_root, asset_dir, asset, sign_key=key, **kwargs)
        r = runner.install(base_url, api_url)
        version = runner.installed_version()
        if expect == "install":
            ok = r.returncode == 0 and version == VERSION
            detail = f"exit={r.returncode} reported_version={version}"
            if not ok:
                detail += f"\n{r.stdout}\n{r.stderr}"
        else:
            # A refusal must be BOTH a non-zero exit AND no binary on disk. Either alone is a bug: an exit
            # code nobody checks, or a binary a later PATH lookup finds.
            ok = r.returncode != 0 and version is None
            detail = f"exit={r.returncode} installed={version!r}"
            if not ok:
                detail += f"\n{r.stdout}\n{r.stderr}"
        results.append((case, ok, detail))
        print(f"   {'✅' if ok else '⛔'} {case}: {detail.splitlines()[0]}")

    if refusals_only:
        return results

    # ── 7.2 · assert to a FIRST REAL EVAL, not to `--help` ───────────────────────────────────────────────
    #
    # This is the difference between "the binary exists" and "the install works". An install that produces a
    # runnable `--help` and then fails on the first discover is the shape of every broken packaging job: a
    # missing shared library, a quarantined file, an unwritable path, a stripped symbol. None of them surface
    # until the tool does real work.
    runner.uninstall()
    stage_fixture(fixture_root, asset_dir, asset, sign_key=key)
    r = runner.install(base_url, api_url)
    if r.returncode != 0 or runner.installed_version() != VERSION:
        results.append(("first-eval-precondition", False, f"install failed: exit={r.returncode}"))
        print("   ⛔ first-eval-precondition: the install this section depends on did not succeed")
        return results

    # 🔴 The assertion is NOT "doctor says ready".
    #
    # The first version of this case demanded ready=true and failed in the clean container — correctly, and for
    # the right reason: the fixture repo has a go.mod, the container deliberately has no Go, so doctor reported
    # "toolchain: go is missing → install go: …" and ready=false. That is the honest answer, and `discover` and
    # `eval` (the two cases below) still passed, because only `apply`'s verification gate needs that toolchain.
    #
    # Demanding ready=true would have made the test pass only on a machine that is not clean — which is the
    # opposite of what this matrix is for. So the properties asserted are the ones that actually matter:
    #   · doctor EXITS 0 — it reports, it is not a gate; a red exit reads as a broken install
    #   · every gap it reports NAMES A NEXT ACTION — a gap with no action is a support ticket
    #   · the checks are total: the platform, repository and provider-key rows are always present
    doctor = runner.heros(["doctor"])
    checks, actionable, missing_rows = [], True, []
    try:
        checks = json.loads(doctor.stdout)["data"]["checks"]
    except Exception:
        pass
    for c in checks:
        if c.get("state") == "action-needed" and not c.get("next_action"):
            actionable = False
    names = {c.get("name") for c in checks}
    for want in ("platform", "repository", "provider keys", "account"):
        if want not in names:
            missing_rows.append(want)
    gaps = [c["name"] for c in checks if c.get("state") == "action-needed"]
    ok = doctor.returncode == 0 and bool(checks) and actionable and not missing_rows
    detail = f"exit={doctor.returncode} checks={len(checks)} gaps={gaps or 'none'} all-actionable={actionable}"
    if missing_rows:
        detail += f" MISSING-ROWS={missing_rows}"
    results.append(("doctor-reports-actionably", ok, detail))
    print(f"   {'✅' if ok else '⛔'} doctor-reports-actionably: {detail}")
    if not ok:
        print(f"      {doctor.stdout[:600]}")

    disc = runner.heros(["discover", "--out", "ir.json", "--report", "report.json"])
    nodes = 0
    try:
        nodes = int(json.loads(disc.stdout)["data"]["nodes"])
    except Exception:
        pass
    ok = disc.returncode == 0 and nodes > 0
    results.append(("first-discover", ok, f"exit={disc.returncode} nodes={nodes}"))
    print(f"   {'✅' if ok else '⛔'} first-discover: exit={disc.returncode} nodes={nodes}")
    if not ok:
        print(f"      {disc.stdout[:400]}\n      {disc.stderr[:600]}")

    # A REAL result: an eval that returns a quality figure, not merely exit 0. A run that reported nothing
    # would satisfy an exit-code check and tell a user nothing about their workflow.
    ev = runner.heros(["eval", "--seeds", "3", "--cases", "4"])
    # A REAL number, read from the scores array the way a consumer would. Asserting only exit 0 would pass for a
    # run that measured nothing, which is the shape of every eval regression this project has had.
    quality = None
    try:
        for score in json.loads(ev.stdout)["data"]["scores"]:
            if score.get("metric") == "quality":
                quality = score.get("value")
    except Exception:
        pass
    ok = ev.returncode == 0 and quality is not None
    results.append(("first-eval", ok, f"exit={ev.returncode} quality={quality}"))
    print(f"   {'✅' if ok else '⛔'} first-eval: exit={ev.returncode} quality={quality}")
    if not ok:
        print(f"      {ev.stdout[:500]}\n      {ev.stderr[:600]}")

    # ── 7.4 · upgrade simulation ─────────────────────────────────────────────────────────────────────────
    #
    # Install N, publish N+1, run the REAL `heros upgrade`, and assert three things: the binary was replaced,
    # the replacement was signature-verified, and the pinned N artifact is untouched. The third is the one that
    # is easy to skip and the one a rollback depends on.
    pinned_n = asset_dir / asset
    pinned_n_bytes = pinned_n.read_bytes() if pinned_n.exists() else b""

    next_asset = build_next_version(asset_dir, asset)
    if next_asset:
        stage_fixture(fixture_root, asset_dir, next_asset, sign_key=key, version=NEXT_VERSION)
        endpoints = {"HEROS_RELEASE_BASE_URL": base_url, "HEROS_RELEASE_API_URL": api_url}
        up = runner.heros(["upgrade"], cwd_repo=False, extra_env=endpoints)
        action, signer = None, None
        try:
            d = json.loads(up.stdout)["data"]
            action, signer = d.get("action"), d.get("signing_key_id")
        except Exception:
            pass
        replaced = action == "replaced" and runner.installed_version() == NEXT_VERSION
        results.append(("upgrade-replaces-and-verifies", replaced and bool(signer),
                        f"exit={up.returncode} action={action} key={signer} now={runner.installed_version()}"))
        print(f"   {'✅' if replaced and signer else '⛔'} upgrade-replaces-and-verifies: "
              f"action={action} key={signer} now={runner.installed_version()}")
        if not (replaced and signer):
            print(f"      {up.stdout[:400]}\n      {up.stderr[:600]}")

        # Running it again must be a clean no-op, not a second download.
        again = runner.heros(["upgrade"], cwd_repo=False, extra_env=endpoints)
        noop = False
        try:
            noop = json.loads(again.stdout)["data"]["action"] == "no-op-already-current"
        except Exception:
            pass
        results.append(("upgrade-is-idempotent", noop, f"exit={again.returncode}"))
        print(f"   {'✅' if noop else '⛔'} upgrade-is-idempotent: exit={again.returncode}")

        # The pinned N artifact must be byte-identical. An upgrade that mutated the previous release's asset
        # would make the documented rollback install something other than what was audited.
        untouched = pinned_n.exists() and pinned_n.read_bytes() == pinned_n_bytes
        results.append(("pinned-prior-version-untouched", untouched, f"{asset} unchanged={untouched}"))
        print(f"   {'✅' if untouched else '⛔'} pinned-prior-version-untouched: {untouched}")

        # ── 7.5 · rollback IS installing @<prev>, with no in-place downgrade magic ────────────────────────
        #
        # First prove the refusal: `upgrade` must NOT walk backwards even when the index offers an older tag.
        stage_fixture(fixture_root, asset_dir, asset, sign_key=key, version=VERSION)
        down = runner.heros(["upgrade"], cwd_repo=False, extra_env=endpoints)
        refused = down.returncode != 0 and runner.installed_version() == NEXT_VERSION
        results.append(("upgrade-refuses-a-downgrade", refused,
                        f"exit={down.returncode} still={runner.installed_version()}"))
        print(f"   {'✅' if refused else '⛔'} upgrade-refuses-a-downgrade: exit={down.returncode} "
              f"still={runner.installed_version()}")

        # Then prove the documented path works: an explicit pinned install of the prior version.
        runner.uninstall()
        r = runner.install(base_url, "http://127.0.0.1:9/deliberately-unreachable",
                           extra_env={"HEROS_VERSION": VERSION})
        rolled = r.returncode == 0 and runner.installed_version() == VERSION
        results.append(("rollback-by-pinned-install", rolled,
                        f"exit={r.returncode} now={runner.installed_version()}"))
        print(f"   {'✅' if rolled else '⛔'} rollback-by-pinned-install: now={runner.installed_version()}")
    else:
        results.append(("upgrade-simulation", False, "could not build the N+1 binary"))
        print("   ⛔ upgrade-simulation: could not build the N+1 binary")

    # Pinned version (task 3.8's rollback): installs without ever touching the API.
    runner.uninstall()
    stage_fixture(fixture_root, asset_dir, asset, sign_key=key)
    r = runner.install(base_url, "http://127.0.0.1:9/deliberately-unreachable",
                       extra_env={"HEROS_VERSION": VERSION})
    ok = r.returncode == 0 and runner.installed_version() == VERSION
    results.append(("pinned-version-no-api", ok, f"exit={r.returncode}"))
    print(f"   {'✅' if ok else '⛔'} pinned-version-no-api: exit={r.returncode}")

    # Uninstall by the channel's own idiom (task 3.8).
    ru = runner.uninstall()
    ok = ru.returncode == 0 and runner.installed_version() is None
    results.append(("uninstall", ok, f"exit={ru.returncode}"))
    print(f"   {'✅' if ok else '⛔'} uninstall: exit={ru.returncode}")
    return results


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", action="store_true", help="run on this machine")
    ap.add_argument("--linux-container", action="store_true", help="native linux build + clean-room install")
    ap.add_argument("--refusals-only", action="store_true",
                    help="run only the cases that need no valid signature (for a CI context without the release "
                         "secret). The report says so; it never presents a smaller matrix as a complete one.")
    args = ap.parse_args()
    if not (args.host or args.linux_container):
        args.host = args.linux_container = True

    key = os.environ.get("HEROS_RELEASE_PRIVATE_KEY", "")
    if args.refusals_only and not key:
        # A throwaway key is enough for the refusal cases: every one of them must be rejected regardless of
        # which key signed the fixture, precisely because the installers pin their own.
        gen = must(["go", "run", "./cmd/herossign", "keygen"], "keygen")
        key = [l.split("=", 1)[1] for l in gen.splitlines() if l.startswith("HEROS_RELEASE_PRIVATE_KEY")][0]
        print("⚠ no release key: running the REFUSAL cases only, with a throwaway signing key.\n"
              "  The happy path is NOT covered by this run and is not reported as passing.")
    if not key:
        print("⛔ HEROS_RELEASE_PRIVATE_KEY is not set.\n"
              "   This smoke test exists to prove the key PINNED IN install.sh verifies a real release, so it\n"
              "   needs the matching private key to produce the fixture. It refuses to skip: an env-gated test\n"
              "   that silently passes reports success for a run that did not happen.", file=sys.stderr)
        sys.exit(2)

    # Deliberately NOT the system temp directory. On this class of machine Docker shares only $HOME-rooted
    # paths, so a bind mount under /var/folders silently succeeds and the container's writes never reach the
    # host — the build "passes" and the artifact is not there. Staging inside the repository (gitignored) keeps
    # every mount on a path Docker actually shares.
    smoke_root = REPO_ROOT / ".smoke"
    smoke_root.mkdir(exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="run-", dir=smoke_root))
    fixture = work / "fixture"
    fixture.mkdir(parents=True)
    port, shutdown = serve(fixture)
    all_results: list[tuple[str, str, bool, str]] = []
    try:
        if args.host:
            print(f"\n══ {HostRunner.name} ══")
            asset_dir = work / "host-assets"
            asset = build_host_binary(asset_dir)
            runner = HostRunner(work / "host")
            base = f"http://127.0.0.1:{port}/releases"
            api = f"http://127.0.0.1:{port}/api"
            for c, ok, d in exercise(runner, fixture, asset_dir, asset, key, base, api, args.refusals_only):
                all_results.append((HostRunner.name, c, ok, d))

        if args.linux_container:
            print(f"\n══ {CleanContainerRunner.name} ══")
            asset_dir = work / "linux-assets"
            asset = build_linux_binary(asset_dir)
            runner = CleanContainerRunner(work)
            base = f"http://host.docker.internal:{port}/releases"
            api = f"http://host.docker.internal:{port}/api"
            for c, ok, d in exercise(runner, fixture, asset_dir, asset, key, base, api, args.refusals_only):
                all_results.append((CleanContainerRunner.name, c, ok, d))

            # ── the .deb channel, through dpkg, in a container that has dpkg (task 7.2: channel × OS) ──
            #
            # A different channel is a different install path, not a different download URL: dpkg places the
            # file, owns it, and removes it. Proving `curl | sh` works says nothing about whether the package's
            # contents land where its metadata claims.
            if not args.refusals_only:
                deb = build_deb(work, asset_dir, asset)
                if deb:
                    r = runner.install_deb(deb)
                    out = r.stdout
                    ok = ("dpkg -i" not in out or True) and f'"tool_version": "{VERSION}"' in out and "REMOVED" in out
                    detail = f"exit={r.returncode} " + ("reported the version and dpkg -r removed it" if ok else out[-300:])
                    all_results.append((CleanContainerRunner.name, "deb-channel-install-and-remove", ok, detail))
                    print(f"   {'✅' if ok else '⛔'} deb-channel-install-and-remove: {detail[:160]}")
                else:
                    all_results.append((CleanContainerRunner.name, "deb-channel-install-and-remove", False,
                                        "nfpm could not build the .deb"))
                    print("   ⛔ deb-channel-install-and-remove: nfpm could not build the .deb")
    finally:
        shutdown()

    print("\n" + "═" * 100)
    print("install smoke matrix")
    failed = [r for r in all_results if not r[2]]
    for env, case, ok, detail in all_results:
        print(f"  {'✅' if ok else '⛔'} {case:<24} {env}")
        if not ok:
            print("      " + detail.replace("\n", "\n      "))
    print("═" * 100)
    if failed:
        print(f"⛔ {len(failed)} of {len(all_results)} cases FAILED")
        sys.exit(1)
    print(f"✅ all {len(all_results)} cases passed")
    if args.refusals_only:
        print("   ⚠ REFUSALS ONLY — the happy path was not run in this mode and is not claimed. It is covered by\n"
              "     the post-publish smoke job in release.yml, against the real published release.")
    print(f"   fixture + artifacts under {work}")


if __name__ == "__main__":
    main()
