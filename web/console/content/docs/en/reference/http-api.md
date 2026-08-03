---
title: HTTP API reference
tier: reference
summary: This tier is absent, and this page says why rather than leaving you to conclude there is no API.
platform_version: 0.21.0
boundary: There is no HTTP API reference. This page documents that absence and its reason; it is not a partial reference and it does not list endpoints.
generated: true
order: 4
---

## Status: absent

There is no OpenAPI document in this repository, and the HTTP API reference is generated or it is not published at all. A hand-written endpoint list is a copy of the truth that begins drifting the day it is written, and it defeats the fence — which can only check documentation against an artifact.

## Why you are reading this instead of an endpoint list

Reference documentation is **generated from a shipped artifact**, or it is not published. The CLI reference is generated from the command registry; the schema reference from the JSON Schemas; the metric reference from the metric catalogue. Each of those has an artifact a fence can check documentation against.

The HTTP API has no such artifact. A hand-written endpoint list would be a **copy of the truth that begins drifting the day it is written**, and — worse — it would defeat the fence, because a fence can only compare a page against an artifact. A wrong endpoint list that passes a check is more dangerous than no endpoint list, because a reader trusts it.

So this tier is marked absent and the API fence **refuses** any documented endpoint, method or field anywhere in this documentation rather than passing vacuously. That refusal is the honest behaviour: the fence says "I cannot check this", instead of saying nothing and being mistaken for approval.

## What to use instead, today

The `heros` CLI is the supported programmatic surface, and it **is** fully documented — see the [CLI reference](/docs/reference/cli). It covers discovery, applying a change, evaluation and linking a run, and its exit codes are a public contract your pipeline can branch on.

When an OpenAPI document ships, this page is replaced by a generated reference and the fence starts checking pages against it. Until then, nothing here describes an endpoint.

