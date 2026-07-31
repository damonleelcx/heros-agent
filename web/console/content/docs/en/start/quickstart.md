---
title: Quickstart
tier: quickstart
summary: From an installed CLI to a discovery graph of your own repository, with no config file to edit and no account to create.
platform_version: 0.20.0
boundary: The quickstart proves the tool can read your repository. It changes no code, calls no model, spends nothing, and sends nothing anywhere.
claims: discover
order: 2
---

One path, one command, no options. Everything you might want to choose between lives in the
[guides](/docs) — this page exists to get you from an installed binary to a real result on **your own
code**, and then to stop.

## Before you start

You need two things, and neither of them is an account.

- The `heros` binary. If you do not have it, [install it](/docs/start/install) — the install path checks
  the download's checksum and the release manifest's signature before anything reaches your `PATH`.
- A repository that calls a language model somewhere, in Go, Python, JavaScript, TypeScript, Java,
  Kotlin or Rust.

You do **not** need a config file, a provider key, an account, or a network connection.

## Build the graph

Run this in your repository:

```bash
heros discover --repo . --out ir.json --report discovery.json
```

That is the whole quickstart. There is no step where you edit a config file first — the defaults work,
and a first run that requires configuration is a first run most people never finish.

### What you get

Two files, and two lines on your terminal. The lines go to **stderr** and the machine-readable result
goes to **stdout**, so a pipeline can consume one without parsing the other.

```
discover: analyzing . (offline, no account)…
discover: 26 nodes, 0 edges → ir.json
```

```json
{
  "contract_version": "p11.cli.v1",
  "command": "discover",
  "ok": true,
  "exit_code": 0,
  "data": {
    "workflow_id": "workflow",
    "ir_path": "ir.json",
    "report_path": "discovery.json",
    "nodes": 26,
    "edges": 0,
    "source_revision": "98105f31f46d3de58a8f69a2a439cee3f7a5e389"
  }
}
```

**This is a captured run, not an illustration.** It is `heros` 0.20.0 reading
[nousresearch/hermes-agent](https://github.com/nousresearch/hermes-agent) at commit `98105f31`, on
2026-07-31. Your numbers will differ; the shape will not.

## Reading the result

Three fields answer the three questions people actually have.

| Field | What it means |
|---|---|
| `nodes` | How many LLM call sites were found. Each becomes a node you can configure and measure on its own. |
| `edges` | How many connections between them the **static** reader could prove. |
| `source_revision` | The commit this graph describes. Every later artifact is anchored to it, so a result can always be traced back to the code that produced it. |

### `edges: 0` is a finding, not a failure

In the run above, 26 call sites were found and **zero edges between them**. That is not the tool giving
up — it is the tool reporting something true about the repository: those call sites are not wired to each
other in a way a source reader can prove. Control flows through a runtime dispatcher, through data, or
through a framework's own machinery.

This is the most important thing to understand about a first result. The graph states what it **proved**,
and it states that separately from what might be true at runtime. Edges that only appear while the program
runs arrive with tracing, and the model keeps the two apart rather than merging them into one confident
picture.

A tool that guessed those edges would draw a prettier graph and a less useful one, because you could no
longer tell which parts of it were measured.

## Where the files go

Both outputs land where you asked, beside your repository. Nothing was written into your source tree, no
branch was created, and no working-tree file changed.

- `ir.json` — the Workflow IR, the structural model everything else is computed from. Its shape is a
  published contract; see the [schema reference](/docs/reference/schemas).
- `discovery.json` — the discovery report: what was read, what was found, and what was skipped.

## What just did not happen

Worth stating plainly, because for a tool of this kind the usual answer is the opposite:

- **No model was called.** Discovery is static analysis. It costs nothing and needs no provider key.
- **No network request was made.** The package this command lives in does not link a network stack at
  all — that is a property of the build, not a promise in a document.
- **No account exists.** Nothing was uploaded and nothing here is attached to a customer record.

## Next

- **Not sure a term means what you think it means?** The [glossary](/docs/concepts/glossary) defines the
  product's own nouns.
- **Got a refusal instead of a result?** Read [refusals](/docs/concepts/refusals) first. A refusal is a
  designed outcome with a name, not an error, and it is the easiest thing on this surface to misread.
- **Want to change something and measure it?** [Configure a variant and read the
  diff](/docs/guides/configure-a-variant).
- **Want this on every pull request?** [Wire it into CI](/docs/guides/wire-ci).
