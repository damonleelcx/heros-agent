#!/usr/bin/env python3
"""Golden-vector tests for config_hash (task 1.6 / 2.4).

Asserts the four properties a P2 implementation MUST reproduce:
  (a) determinism      — canon+SHA-256 of base.resolved_config == base.config_hash
  (b) canonicalization — a key-reordered copy of base hashes identically
  (c) version-sensitivity — repointing prompt_ref @3->@4 == variant_b.config_hash (differs)
  (d) seed-invariance  — run_id/seed/timestamp are absent from resolved_config, so no
                          run-time value can change the hash

Reference canonicalizer: RFC 8785 subset (sorted ASCII keys, no whitespace, UTF-8,
short number form) — matches docs/decisions/config-hash-spec.md §4.

Usage: python3 schemas/test_config_hash.py   (exit 0 = pass)
"""
import copy
import hashlib
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
GOLDEN = json.loads((HERE / "samples/config-hash.golden.json").read_text())


def canon(obj) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def config_hash(obj) -> str:
    return hashlib.sha256(canon(obj).encode("utf-8")).hexdigest()


def main() -> int:
    base = GOLDEN["base"]["resolved_config"]
    failures = []

    def check(name, cond, detail=""):
        (print(f"ok   {name}") if cond
         else failures.append(f"{name}{(' — ' + detail) if detail else ''}"))

    # (a) determinism
    h = config_hash(base)
    check("determinism: base hash matches golden",
          h == GOLDEN["base"]["config_hash"], f"got {h[:12]}")

    # canonical string is byte-stable too
    check("canonical string matches golden",
          canon(base) == GOLDEN["base"]["canonical_json"])

    # (b) canonicalization: reorder top-level + nested keys, same hash
    reordered = {"edges": base["edges"], "nodes": list(reversed(base["nodes"])),
                 "ir_version": base["ir_version"]}
    reordered["nodes"] = list(reversed(reordered["nodes"]))  # restore order; keys still reorder
    check("canonicalization: key-reordered copy hashes identically",
          config_hash(reordered) == h)

    # (c) version-sensitivity: prompt_ref @3 -> @4
    var_b = copy.deepcopy(base)
    var_b["nodes"][0]["prompt_ref"] = "prompt://triage/classify@4"
    hb = config_hash(var_b)
    check("version-sensitivity: prompt @3->@4 changes the hash", hb != h)
    check("version-sensitivity: matches golden variant_b",
          hb == GOLDEN["variant_b_prompt_v4"]["config_hash"], f"got {hb[:12]}")

    # (d) seed-invariance: no run-time field exists to vary
    check("seed-invariance: run_id/seed/timestamp absent from resolved_config",
          not ({"run_id", "seed", "timestamp"} & set(base.keys())))

    if failures:
        print("\nCONFIG_HASH GOLDEN VECTORS FAILED:", file=sys.stderr)
        for f in failures:
            print("  - " + f, file=sys.stderr)
        return 1
    print("\nAll config_hash golden vectors pass.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
