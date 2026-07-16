#!/usr/bin/env python3
"""Discovery throughput soft-signal (P1 task 8.3).

Generates a synthetic Go repo of ~DISCOVERY_LOC lines (default 200,000), runs the built `discover`
CLI over it, and reports elapsed time against the ≤60 s / ~200k-LOC budget (NFR3).

This is a SOFT signal: it prints PASS/WARN and ALWAYS exits 0. Throughput is environment-sensitive
(CI runner speed varies), so it must inform, not block — a hard gate here would be a flaky fence.
"""
import os
import subprocess
import sys
import tempfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DISCOVER = os.environ.get("DISCOVER_BIN", os.path.join(ROOT, "bin", "discover"))
TARGET_LOC = int(os.environ.get("DISCOVERY_LOC", "200000"))
BUDGET_S = float(os.environ.get("DISCOVERY_BUDGET_S", "60"))

# ~40 LOC per generated file, one LLM call site each.
FILE_TEMPLATE = '''package p{pkg}

import "github.com/anthropics/anthropic-sdk-go"

// generated call site {i}
func call{i}(client *anthropic.Client) {{
\tclient.Messages.New(nil, anthropic.MessageNewParams{{Model: anthropic.ModelClaudeOpus4_6}})
}}
'''
FILLER = "\n".join(f"// filler line {n} to pad line count" for n in range(30))


def gen_repo(root, target_loc):
    os.makedirs(root, exist_ok=True)
    with open(os.path.join(root, "go.mod"), "w") as f:
        f.write("module example.com/bench\n\ngo 1.22\n")
    loc = 0
    i = 0
    files = 0
    while loc < target_loc:
        pkg = i // 50  # 50 files per package dir
        d = os.path.join(root, f"pkg{pkg}")
        os.makedirs(d, exist_ok=True)
        body = FILE_TEMPLATE.format(pkg=pkg, i=i) + FILLER + "\n"
        with open(os.path.join(d, f"f{i}.go"), "w") as f:
            f.write(body)
        loc += body.count("\n") + 1
        i += 1
        files += 1
    return files, loc


def main():
    if not os.path.exists(DISCOVER):
        print(f"discovery-throughput: {DISCOVER} not built — skipping (soft signal)", file=sys.stderr)
        return
    with tempfile.TemporaryDirectory() as tmp:
        repo = os.path.join(tmp, "bench")
        files, loc = gen_repo(repo, TARGET_LOC)
        out = os.path.join(tmp, "ir.json")
        rep = os.path.join(tmp, "report.json")
        t0 = time.monotonic()
        proc = subprocess.run(
            [DISCOVER, "--repo", repo, "--out", out, "--report", rep,
             "--repo-url", "local://bench", "--commit", "0000000"],
            capture_output=True, text=True,
        )
        elapsed = time.monotonic() - t0

        if proc.returncode != 0:
            print(f"discovery-throughput: CLI failed (soft): {proc.stderr.strip()}")
            return
        rate = loc / elapsed if elapsed else float("inf")
        verdict = "PASS" if elapsed <= BUDGET_S else "WARN"
        print(f"discovery-throughput: {verdict} — {loc:,} LOC / {files:,} files in {elapsed:.1f}s "
              f"({rate/1000:.0f}k LOC/s; budget {BUDGET_S:.0f}s)")
        if verdict == "WARN":
            print("  note: over budget on this runner — soft signal only, build not failed (NFR3).")


if __name__ == "__main__":
    main()
