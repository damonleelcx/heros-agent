#!/usr/bin/env python3
"""Discovery CI gate (P1 tasks 8.1 + 8.2).

Runs the BUILT `discover` CLI on every fixture repo and asserts, failing the build loudly on any
violation (DevOps rule: no silent-pass; the fence must be able to go red):

  8.1  the emitted IR validates against schemas/workflow-ir.schema.json
       + referential integrity (every edge endpoint references a node in the doc)
  8.2  node count matches the documented expected count (regression guard)
       + the golden fixture's emitted IR is byte-identical to the committed expected-ir.json (drift)

This exercises the actual CLI end-to-end, complementing the library-level Go tests (a library test
passing does not prove the CLI writes a schema-valid file — HTTP 200 != correct on disk).
"""
import json
import os
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA = os.path.join(ROOT, "schemas", "workflow-ir.schema.json")
FIXTURES = os.path.join(ROOT, "internal", "discovery", "testdata", "fixtures")
DISCOVER = os.environ.get("DISCOVER_BIN", os.path.join(ROOT, "bin", "discover"))

try:
    import jsonschema
except ImportError:
    print("discovery-ci: jsonschema not installed — run `pip install jsonschema`", file=sys.stderr)
    sys.exit(2)


def fail(msg):
    print(f"  FAIL  {msg}")
    return False


def check_fixture(name, spec, schema, tmp):
    repo = os.path.join(FIXTURES, name)
    ir_path = os.path.join(tmp, f"{name}.ir.json")
    report_path = os.path.join(tmp, f"{name}.report.json")
    cmd = [DISCOVER, "--repo", repo, "--out", ir_path, "--report", report_path]
    if spec.get("config"):
        cfg = os.path.join(repo, "llm-eval.yaml")
        if not os.path.exists(cfg):
            return fail(f"{name}: expected llm-eval.yaml missing")
        cmd += ["--config", cfg]

    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        return fail(f"{name}: discover CLI exited {proc.returncode}: {proc.stderr.strip()}")

    with open(ir_path) as f:
        ir = json.load(f)

    # 8.1 schema validation
    try:
        jsonschema.validate(ir, schema)
    except jsonschema.ValidationError as e:
        return fail(f"{name}: IR schema violation at {list(e.absolute_path)}: {e.message}")

    # 8.1 referential integrity (CI semantic check beyond JSON Schema)
    node_ids = {n["node_id"] for n in ir["nodes"]}
    for e in ir["edges"]:
        if e["from_node_id"] not in node_ids or e["to_node_id"] not in node_ids:
            return fail(f"{name}: edge references a node not in the document: {e}")

    # 8.2 node-count regression
    want = spec["nodes"]
    got = len(ir["nodes"])
    if got != want:
        return fail(f"{name}: node-count regression — expected {want}, got {got}")

    # 8.2 golden-IR drift (byte-identical to the committed golden)
    if spec.get("golden"):
        golden = os.path.join(repo, "expected-ir.json")
        with open(ir_path, "rb") as f:
            got_bytes = f.read()
        with open(golden, "rb") as f:
            want_bytes = f.read()
        if got_bytes != want_bytes:
            return fail(f"{name}: golden-IR drift — emitted IR differs from expected-ir.json")

    print(f"  ok    {name}: {got} node(s), IR valid" + (", golden matches" if spec.get("golden") else ""))
    return True


def main():
    if not os.path.exists(DISCOVER):
        print(f"discovery-ci: {DISCOVER} not built — run `make build-discover`", file=sys.stderr)
        sys.exit(2)
    with open(SCHEMA) as f:
        schema = json.load(f)
    with open(os.path.join(FIXTURES, "expected.json")) as f:
        manifest = json.load(f)["fixtures"]

    print("discovery-ci: validating emitted IR for every fixture against workflow-ir.schema.json")
    ok = True
    with tempfile.TemporaryDirectory() as tmp:
        for name in sorted(manifest):
            ok &= check_fixture(name, manifest[name], schema, tmp)

    if not ok:
        print("discovery-ci: FAIL")
        sys.exit(1)
    print("discovery-ci: PASS — all fixtures emit schema-valid IR with expected node counts")


if __name__ == "__main__":
    main()
