#!/usr/bin/env python3
"""Validate P0 sample fixtures against the frozen JSON Schemas.

Proves the M0 gate (PRD NFR8): every `*.valid.json` sample MUST validate against
its schema, and every `*.invalid-*.json` fixture MUST be rejected. Exit non-zero
on any surprise so CI fails the build.

Usage: python3 schemas/validate.py
Requires: pip install jsonschema  (draft 2020-12 capable, >= 4.18)
"""
import json
import sys
from pathlib import Path

from jsonschema import Draft202012Validator

HERE = Path(__file__).resolve().parent

# (schema file, [valid samples], [invalid samples])
CASES = [
    (
        "workflow-ir.schema.json",
        [
            "samples/workflow-ir.valid.json",
            "samples/workflow-ir.with-subgraphs.valid.json",
            # A REAL P3.5 classifier output (regenerate with P35_WRITE_SAMPLE=1 go test
            # ./internal/patternclassifier -run TestGenerateLabelledSample). It proves the additive
            # claim end to end: labels written into the P0-reserved field still validate against the
            # frozen schema, at the same ir_version MAJOR as an unlabelled document.
            "samples/workflow-ir.p35-labelled.valid.json",
        ],
        [
            "samples/workflow-ir.invalid-missing-io-contract.json",
            "samples/workflow-ir.invalid-missing-model.json",
        ],
    ),
    (
        "metric-event.schema.json",
        ["samples/metric-event.valid.json"],
        [
            "samples/metric-event.invalid-missing-tag.json",
            "samples/metric-event.invalid-missing-unit.json",
        ],
    ),
    (
        "runtime-invocation.schema.json",
        ["samples/runtime-invocation.valid.json"],
        [],
    ),
]


def load(rel: str):
    return json.loads((HERE / rel).read_text())


def main() -> int:
    failures = []

    # Every schema in this directory must itself be a valid draft 2020-12 schema — including ones with
    # no fixtures, which the CASES loop below never reaches.
    #
    # `console-view.schema.json` is the reason this exists. It is GENERATED from the Go view structs
    # (cmd/consoletypes, ADR-007), and a generated schema is exactly the kind that goes subtly wrong
    # without anyone noticing: nothing hand-writes it, so nothing hand-reads it either. Its drift gate
    # proves it matches the Go types; this proves it is a schema at all.
    for schema_path in sorted(HERE.glob("*.schema.json")):
        try:
            Draft202012Validator.check_schema(json.loads(schema_path.read_text()))
            print(f"ok   {schema_path.name} is a valid draft 2020-12 schema")
        except Exception as exc:  # noqa: BLE001 - the message is the finding
            failures.append(f"{schema_path.name} is not a valid draft 2020-12 schema: {exc}")

    for schema_file, valids, invalids in CASES:
        schema = load(schema_file)
        # Meta-check: the schema itself must be a valid draft 2020-12 schema.
        Draft202012Validator.check_schema(schema)
        validator = Draft202012Validator(schema)

        for rel in valids:
            errs = sorted(validator.iter_errors(load(rel)), key=lambda e: e.path)
            if errs:
                failures.append(f"VALID sample {rel} was REJECTED by {schema_file}:")
                failures += [f"    - {e.message}" for e in errs]
            else:
                print(f"ok   {rel} validates against {schema_file}")

        for rel in invalids:
            errs = list(validator.iter_errors(load(rel)))
            if not errs:
                failures.append(
                    f"INVALID sample {rel} was ACCEPTED by {schema_file} (schema fails to reject it)"
                )
            else:
                print(f"ok   {rel} is correctly rejected by {schema_file} ({errs[0].message})")

    if failures:
        print("\nSCHEMA VALIDATION FAILED:", file=sys.stderr)
        for line in failures:
            print("  " + line, file=sys.stderr)
        return 1
    print("\nAll schema fixtures behaved as expected.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
