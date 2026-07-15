#!/usr/bin/env python3
"""Spike: is the typed I/O contract sufficient for P4? (AI-Engineer task 3.2)

Two downstream P4 capabilities read a node's io_contract:
  (A) schema-driven eval-set synthesis  — generate VALID / BOUNDARY / INVALID inputs from input_schema
  (B) output-contract-adherence metric  — validate a node's actual output against output_schema

AI-engineer discipline: prove it, print the process data, let the reader recompute the conclusion.
This does NOT ship a generator (that's P4); it PROVES the P0 contract carries enough signal to drive
one, using jsonschema as the oracle. Exit 0 = the contract is sufficient and discriminating.

Requires: pip install jsonschema
"""
import sys
from jsonschema import Draft202012Validator

# A representative constrained input_schema (the kind Discovery can infer for a classifier node).
INPUT_SCHEMA = {
    "type": "object",
    "properties": {
        "ticket_text": {"type": "string", "minLength": 1, "maxLength": 4000},
        "priority": {"type": "integer", "minimum": 1, "maximum": 5},
        "channel": {"type": "string", "enum": ["email", "chat", "phone"]},
    },
    "required": ["ticket_text", "priority"],
    "additionalProperties": False,
}

OUTPUT_SCHEMA = {
    "type": "object",
    "properties": {"intent": {"type": "string", "enum": ["billing", "bug", "how_to", "other"]}},
    "required": ["intent"],
    "additionalProperties": False,
}

# (A) What a schema-driven generator would emit, hand-authored here so the reader sees the intent.
VALID_CASES = [
    {"ticket_text": "card declined", "priority": 3, "channel": "email"},
    {"ticket_text": "x", "priority": 1},                       # minimal required
]
BOUNDARY_CASES = [
    {"ticket_text": "x" * 4000, "priority": 5, "channel": "phone"},  # at maxLength / max
    {"ticket_text": "y", "priority": 1, "channel": "chat"},           # at minLength / min
]
INVALID_CASES = [
    ({"priority": 3}, "missing required ticket_text"),
    ({"ticket_text": "hi", "priority": 9}, "priority above maximum"),
    ({"ticket_text": "hi", "priority": 2, "channel": "sms"}, "channel not in enum"),
    ({"ticket_text": "", "priority": 2}, "ticket_text below minLength"),
    ({"ticket_text": "hi", "priority": 2, "extra": 1}, "additionalProperties"),
]

# (B) Outputs to score for contract adherence.
GOOD_OUTPUT = {"intent": "billing"}
BAD_OUTPUTS = [
    ({"intent": "unknown"}, "intent not in enum"),
    ({}, "missing required intent"),
]

PERMISSIVE = {"type": "object"}  # the P1 allowance: static analysis couldn't infer a shape


def main():
    iv = Draft202012Validator(INPUT_SCHEMA)
    ov = Draft202012Validator(OUTPUT_SCHEMA)
    fails = []

    print("== (A) schema-driven eval-set synthesis: input_schema discriminates ==")
    for c in VALID_CASES:
        ok = iv.is_valid(c)
        print(f"  {'ok  ' if ok else 'FAIL'} VALID    accepted: {c}")
        if not ok:
            fails.append(f"valid case rejected: {c}")
    for c in BOUNDARY_CASES:
        ok = iv.is_valid(c)
        print(f"  {'ok  ' if ok else 'FAIL'} BOUNDARY accepted: {c}")
        if not ok:
            fails.append(f"boundary case rejected: {c}")
    for c, why in INVALID_CASES:
        ok = not iv.is_valid(c)
        print(f"  {'ok  ' if ok else 'FAIL'} INVALID  rejected ({why}): {c}")
        if not ok:
            fails.append(f"invalid case accepted ({why}): {c}")

    print("\n== (B) output-contract-adherence: output_schema flags violations ==")
    ok = ov.is_valid(GOOD_OUTPUT)
    print(f"  {'ok  ' if ok else 'FAIL'} conformant output accepted: {GOOD_OUTPUT}")
    if not ok:
        fails.append("conformant output rejected")
    for o, why in BAD_OUTPUTS:
        ok = not ov.is_valid(o)
        print(f"  {'ok  ' if ok else 'FAIL'} non-conformant output flagged ({why}): {o}")
        if not ok:
            fails.append(f"non-conformant output accepted ({why}): {o}")

    # adherence rate = fraction of outputs that validate — the actual P4 metric.
    outputs = [GOOD_OUTPUT] + [o for o, _ in BAD_OUTPUTS]
    adherent = sum(1 for o in outputs if ov.is_valid(o))
    print(f"  -> adherence rate over sample = {adherent}/{len(outputs)} = {adherent/len(outputs):.0%}")

    print("\n== (C) permissive-schema allowance (P1): degrades gracefully, not silently wrong ==")
    pv = Draft202012Validator(PERMISSIVE)
    # A permissive schema accepts ANY object => no discrimination possible => property/fuzz synthesis
    # is impossible and adherence is vacuously 100%. This is EXPECTED and must be surfaced as a
    # low-constraint signal, not mistaken for a passing contract.
    accepts_all = pv.is_valid({"anything": [1, 2, 3]}) and pv.is_valid({})
    print(f"  ok   permissive {{'type':'object'}} accepts arbitrary objects: {accepts_all}")
    if not accepts_all:
        fails.append("permissive schema unexpectedly discriminated")
    # constraint score: 0 for permissive, >0 for constrained (a proxy P4 can report as eval-set quality)
    def constraint_score(s):
        keys = ("properties", "required", "enum", "minimum", "maximum", "minLength", "maxLength")
        return sum(1 for k in keys if k in s) + len(s.get("properties", {}))
    print(f"  ok   constraint score: constrained={constraint_score(INPUT_SCHEMA)}  permissive={constraint_score(PERMISSIVE)}")
    print("       => synthesis falls back to LLM-driven when score is 0; adherence reported as "
          "'unconstrained', never as a false pass.")

    if fails:
        print("\nI/O-CONTRACT SUFFICIENCY: FAILED", file=sys.stderr)
        for f in fails:
            print("  - " + f, file=sys.stderr)
        return 1
    print("\nI/O-contract is SUFFICIENT for schema-driven synthesis + adherence, and the permissive "
          "P1 allowance degrades gracefully.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
