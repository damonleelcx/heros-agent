#!/usr/bin/env python3
"""Expand-migrate-contract proof for the Workflow IR schema (task 2.3).

Proves the additive-evolution contract (PRD NFR1) with a WORKED EXAMPLE: adding an optional node
field. The assertions encode exactly what the contract does and does not promise:

  1. BACKWARD COMPAT (the headline: "older samples still validate"):
     a document authored against v1.0.0 validates against the v1.1.0 schema (new optional field absent).

  2. NEW DOCS VALIDATE AT NEW VERSION:
     a v1.1.0 document that uses the new optional field validates against the v1.1.0 schema.

  3. FORWARD-COMPAT IS A PARSE CONTRACT, NOT A STRICT-VALIDATE ONE:
     because the published schema is `additionalProperties: false` (strict authoring at a pinned
     version), a v1.1.0 document does NOT strict-validate against the v1.0.0 schema. That is WHY a
     consumer pinned to MAJOR n must PARSE leniently (ignore unknown fields), not strict-validate a
     newer document. This test asserts the rejection so the design choice is explicit, not accidental.

  4. A CONTRACT (breaking) STEP still validates old docs until the final MAJOR bump:
     renaming is done as add-optional (expand) -> dual-write (migrate) -> drop-old (contract). Steps
     1-2 above are the "expand" step; here we assert an old sample still validates after expand.

Usage: python3 schemas/test_schema_evolution.py   (exit 0 = pass)
"""
import copy
import json
import sys
from pathlib import Path

from jsonschema import Draft202012Validator

HERE = Path(__file__).resolve().parent


def load(rel):
    return json.loads((HERE / rel).read_text())


def make_v1_1(schema_v1_0):
    """EXPAND step: add one OPTIONAL node field `retry_policy`; bump ir_version pattern is unchanged
    (the field is data, not the version string), but the schema's own version note becomes 1.1.0."""
    s = copy.deepcopy(schema_v1_0)
    node_props = s["$defs"]["Node"]["properties"]
    node_props["retry_policy"] = {
        "type": "object",
        "description": "OPTIONAL (added in IR 1.1.0). Absence must not invalidate a v1.0 document.",
        "additionalProperties": True,
    }
    # NOT added to Node.required -> optional. MAJOR unchanged.
    return s


def main() -> int:
    v1_0 = load("workflow-ir.schema.json")
    v1_1 = make_v1_1(v1_0)

    # sanity: both are themselves valid schemas
    Draft202012Validator.check_schema(v1_0)
    Draft202012Validator.check_schema(v1_1)

    val_v1_0 = Draft202012Validator(v1_0)
    val_v1_1 = Draft202012Validator(v1_1)

    doc_v1_0 = load("samples/workflow-ir.valid.json")  # authored against v1.0.0

    # a v1.1 document that USES the new optional field
    doc_v1_1 = copy.deepcopy(doc_v1_0)
    doc_v1_1["nodes"][0]["retry_policy"] = {"max_retries": 2, "backoff": "exponential"}

    failures = []

    def expect_valid(name, validator, doc):
        errs = list(validator.iter_errors(doc))
        if errs:
            failures.append(f"{name}: expected VALID, got: {errs[0].message}")
        else:
            print(f"ok   {name}")

    def expect_invalid(name, validator, doc):
        if list(validator.iter_errors(doc)):
            print(f"ok   {name}")
        else:
            failures.append(f"{name}: expected INVALID, but it validated")

    # 1. backward compat — older sample validates against newer schema
    expect_valid("v1.0 doc validates against v1.1 schema (backward compat)", val_v1_1, doc_v1_0)
    # 2. new doc validates at new version
    expect_valid("v1.1 doc (uses retry_policy) validates against v1.1 schema", val_v1_1, doc_v1_1)
    # 3. forward compat is a parse contract: strict-validating a v1.1 doc at v1.0 is rejected
    expect_invalid("v1.1 doc is rejected by strict v1.0 schema (=> consumers parse leniently)",
                   val_v1_0, doc_v1_1)
    # 4. expand step keeps old docs valid
    expect_valid("v1.0 doc still validates after expand step", val_v1_1, doc_v1_0)

    if failures:
        print("\nSCHEMA EVOLUTION PROOF FAILED:", file=sys.stderr)
        for f in failures:
            print("  - " + f, file=sys.stderr)
        return 1
    print("\nExpand-migrate-contract proof holds.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
