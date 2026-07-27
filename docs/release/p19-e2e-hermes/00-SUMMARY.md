# End-to-end test — heros-agent × nousresearch/hermes-agent

Target under test: `/Users/damon/Downloads/repos/heros-agent`
Test corpus: `github.com/nousresearch/hermes-agent` @ `528e335` (3,333 Python files, checked out at `/tmp/hermes-agent`)
Date: 2026-07-26 · heros CLI `0.11.0-dev` · contract `p11.link.v1` / `p11.cli.v1`

Every artifact in this directory was produced live by running the platform against the real hermes-agent repo.

## 1. CLI — offline, no account (P11)
| Step | Command | Result | Proof |
|---|---|---|---|
| version | `heros version` | `0.11.0-dev`, link endpoint `https://heros-agent.space` | `01-version.txt` |
| discover | `heros discover --repo /tmp/hermes-agent` | **40 nodes** (37 openai, 3 bedrock), 0 edges, rev `528e335` | `02-discover.json`, `02-ir.json`, `02-discovery-report.json` |
| eval (pass) | `heros eval --seeds 3 --cases 4 --min-quality 0.7` | quality **0.7875** [CI 0.7375–0.8313], cost/latency/tokens + per-node; **exit 0** | `04-eval.json` |
| eval (gate fail) | `… --min-quality 0.99` | gate FAILED, **exit 1** (gate is a real contract) | `05-eval-gate-fail.*` |

## 2. Apply / codemod — source-transformation, reviewable diff (P2, ADR-001/002/003)
- `internal/transform` Python codemod suite: **all pass** (`03b-transform-tests.txt`).
- **Real hermes-agent checkout** (`realrepo` tag, `03c-realrepo-hermes.txt`): of 40 discovered nodes,
  **4 honestly rewritable**, **33 refused (kwargs/args splat)**, **3 refused (bedrock provider-swap, ADR-002)** —
  the engine refuses to mis-rewrite a `**kwargs` splat or a Bedrock SDK call rather than emit a green-badged broken diff.
- Full **persist→generate→apply→verify** on a Python repo (pgproof, real Postgres): **PASS** (`03d-pgproof-python-e2e.txt`).

## 3. Linking to heros-agent.space — opt-in, allowlisted (P11)
- `heros link --run … --dry-run`: prints the **exact** payload, transmits nothing (`06-link-dryrun.json`).
  Payload = metrics + IR structure (node_ids) + config_hash + source_revision + scores. **No source, no prompts, no keys** (verified).
- Real `heros login` / `heros link`: `https://heros-agent.space` **is not deployed** — DNS resolves to a parking IP
  (162.255.119.200) but TLS times out; the CLI **fails closed** with exit code **2** (operational-error) (`07-*`).

## 4. Customer web console (P9) — Next.js + BFF, no key in browser
Served by `cmd/p9hermes` (:4321) + `web/console` BFF (:4320), tenant `nousresearch/hermes-agent`. See `console-views/`.
- Sign-in exchanges a tenant credential for a server-held session; browser gets an HttpOnly cookie, never a platform key.
- Graph renders all 40 real nodes with correct provider labels; Board degrades gracefully ("subsystem not mounted").

## 5. Operator/admin console (P8) — second origin, SSO+MFA, audited
Served by `cmd/p8hermes` (admin API :4311, BFF-gated) + `web/admin-console` BFF (:4310). See `admin-views/`.
- SSO + MFA sign-in as `adm-superadmin`; admin API refuses non-BFF requests.
- Tenants (5, named plans), Audit Log **hash-chain INTACT** — no role can alter an entry without breaking the chain.
- Separate origin + disjoint cookie jar from the customer console.

## Environment note
`https://heros-agent.space` is **not deployed yet** (confirmed above). This is exactly the P19 (Deployment & Delivery)
workstream: stand the platform up as a thing you can deploy. Proof of the not-deployed state is in `07-*`.
