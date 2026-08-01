# P24 against nousresearch/hermes-agent

The P24 error boundary, exercised with a fixture taken from a REAL repository rather than
from strings written beside the code that filters them.

- Repository: https://github.com/nousresearch/hermes-agent
- Material extracted: **401** pieces
- Envelopes transmitted: **50** (the rest rate-limited by design)
- Pieces of that material found in the transmitted bytes: **0**

## What was taken

| Kind | Count |
|---|---|
| docstring / prompt | 141 |
| model reference | 9 |
| prompt text | 2 |
| source path | 58 |
| symbol | 191 |

## A transmitted envelope, verbatim

```
{"event_id":"ee85f7f948366eea1ac2f34932b5cd60","sent_at":"2026-08-01T08:17:18.974437Z"}
{"content_type":"application/json","length":618,"type":"event"}
{"event_id":"ee85f7f948366eea1ac2f34932b5cd60","exception":{"values":[{"stacktrace":{"frames":[{"filename":"main.go","function":"main","in_app":false,"lineno":124,"module":"main"},{"filename":"proc.go","function":"main","in_app":false,"lineno":290,"module":"runtime"},{"filename":"asm_arm64.s","function":"goexit","in_app":false,"lineno":1447,"module":"runtime"}]},"type":"*main.carrier","value":"PROVIDER_ERROR"}]},"level":"error","platform":"go","release":"erroreporthermes","tags":{"edition":"dev","error.code":"PROVIDER_ERROR","runtime":"go","surface":"platform.api","trace_id":"c0b2592a7583f888583e0c8d25c5ab4f"}}
```

## What this establishes

The boundary drops material it has never seen. What it does NOT establish is anything
about a vendor's stored copy — the assertion is on the bytes this process transmitted,
which is the side of the boundary we control.
