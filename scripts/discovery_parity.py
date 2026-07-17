#!/usr/bin/env python3
"""Discovery output parity harness (import-parser-research-validation).

Runs the BUILT `discover` CLI over EVERY fixture repo and records a SHA-256 of both emitted
artifacts, so a change to the parsing path can be proven output-equivalent rather than claimed to be
("没有纯重构例外：'等价'是声称，不是证明").

  snapshot <dir>   run every fixture, write <dir>/parity.json (the baseline)
  verify   <dir>   run every fixture again, diff against <dir>/parity.json, exit non-zero on drift

BOTH artifacts are hashed, and that is the point of this harness rather than an IR-only diff:
framework subgraphs, dedup merges, ambiguity flags and file diagnostics travel in the REPORT and
never reach the IR, so an IR-only comparison is blind to exactly the parts of the parse a frontend
change is most likely to move. A harness that cannot see the regression it exists to catch is
decoration.

Fixtures are discovered from the filesystem, not a list, so a fixture added later is covered without
anyone remembering to add it here.
"""
import hashlib
import json
import os
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FIXTURES = os.path.join(ROOT, "internal", "discovery", "testdata", "fixtures")
DISCOVER = os.environ.get("DISCOVER_BIN", os.path.join(ROOT, "bin", "discover"))


def fixtures():
    return sorted(
        d for d in os.listdir(FIXTURES)
        if os.path.isdir(os.path.join(FIXTURES, d))
    )


def run_fixture(name, tmp):
    repo = os.path.join(FIXTURES, name)
    ir_path = os.path.join(tmp, f"{name}.ir.json")
    report_path = os.path.join(tmp, f"{name}.report.json")
    cmd = [DISCOVER, "--repo", repo, "--out", ir_path, "--report", report_path]
    cfg = os.path.join(repo, "llm-eval.yaml")
    if os.path.exists(cfg):
        cmd += ["--config", cfg]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    entry = {"exit": proc.returncode}
    for label, path in (("ir", ir_path), ("report", report_path)):
        if os.path.exists(path):
            with open(path, "rb") as f:
                entry[label] = hashlib.sha256(f.read()).hexdigest()
        else:
            entry[label] = None
    return entry


def collect():
    out = {}
    with tempfile.TemporaryDirectory() as tmp:
        for name in fixtures():
            out[name] = run_fixture(name, tmp)
    return out


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("snapshot", "verify"):
        print(__doc__, file=sys.stderr)
        return 2
    mode, dest = sys.argv[1], sys.argv[2]
    path = os.path.join(dest, "parity.json")
    got = collect()

    if mode == "snapshot":
        os.makedirs(dest, exist_ok=True)
        with open(path, "w") as f:
            json.dump(got, f, indent=2, sort_keys=True)
        print(f"discovery-parity: snapshot of {len(got)} fixtures -> {path}")
        return 0

    if not os.path.exists(path):
        print(f"discovery-parity: no baseline at {path}; run `snapshot` first", file=sys.stderr)
        return 2
    with open(path) as f:
        want = json.load(f)

    drift = []
    for name in sorted(set(want) | set(got)):
        if want.get(name) != got.get(name):
            drift.append((name, want.get(name), got.get(name)))
    if drift:
        print(f"discovery-parity: FAIL — {len(drift)} fixture(s) drifted")
        for name, w, g in drift:
            print(f"  {name}:\n    before {w}\n    after  {g}")
        return 1
    print(f"discovery-parity: PASS — {len(got)} fixtures byte-identical (ir.json AND report.json)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
