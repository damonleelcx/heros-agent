---
title: CLI reference
tier: reference
summary: A deliberately wrong exit-code table — 1 and 2 are swapped, which is the failure with opposite remedies.
platform_version: 0.20.0
boundary: This is a test fixture and is never published.
---

## Exit codes

| Code | Name | Means | Your remedy |
|---|---|---|---|
| `0` | ok | success | nothing |
| `1` | operational-error | the tool broke | retry |
| `2` | configured-gate-failed | your gate failed | fix the regression |

## Commands

### help
### version
### init
### doctor
### discover
### apply
### author
### eval
### coverage
### status
### verify-release
### login
### link
### upgrade
