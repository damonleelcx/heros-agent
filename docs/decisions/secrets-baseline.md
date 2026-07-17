# Secrets-Management Baseline (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 · **amended at P2 / M2** (tasks 4.5, 6.1) |
| Owner | DevOps (support to System Designer) |
| Status | Frozen at M0; §1.1 added at M2 when the first real manager was wired |
| Tasks | 4.4 (this doc), 4.5 + 6.1 (§1.1) |
| Cross-refs | `docs/decisions/otel-genai-conventions.md` (no secrets in spans); `internal/config/config.go`; `internal/providergateway/secrets.go`; `internal/providergateway/awssecrets.go`; `.gitleaks.toml`; `config.example.json` |

The baseline for provider and backing-service credentials, set **before any provider call exists** (no
live calls in P0) — least privilege by default. Core rule (arbitrated at level 1, *safety*, on the
cost/complexity ladder — it outranks every convenience):

> **Provider secrets are sourced from a secrets manager, and NEVER appear in the repo, logs, CI echo,
> or traces.** (NFR6)

---

## 1. Where secrets come from — the precedence

> ⚠️ **Scope of this section.** It describes the **backing-service** credentials the agentd process
> reads at boot (OpenAI-for-embeddings, Qdrant, Neo4j, the inbox HMAC key) — the `config.Config`
> fields. It described **provider** credentials too, by assumption, until M2. It no longer does:
> provider credentials go through the gateway's `Secrets` seam, and **§1.1 is their truth source.**
> Read §1.1 before applying anything below to a provider key.

Secrets are injected as **environment variables** by a secrets manager (Vault, AWS/GCP Secrets Manager,
Kubernetes Secrets, or a local `.env` not committed). Precedence, highest first:

1. **Environment variable** (secrets-manager-injected) — the production path.
2. **`config.json`** field — dev-only convenience; the file is **gitignored** and never committed.
3. Empty — the harness degrades (e.g. no `OPENAI_API_KEY` ⇒ features needing it are disabled, loudly).

`internal/config/config.go` `applySecretEnv()` implements this: a non-empty env var **wins over** the
file value. Mapping:

| Secret | Env var (secrets-manager-injected) | Config field |
|---|---|---|
| OpenAI API key | `OPENAI_API_KEY` | `openai_api_key` |
| Qdrant API key | `QDRANT_API_KEY` | `qdrant_api_key` |
| Neo4j password | `NEO4J_PASSWORD` | `neo4j_password` |
| Inbox HMAC key | `HEROS_INBOX_SIGNING_KEY` | `inbox_signing_key` |

Proven by `internal/config/secretenv_test.go` (env overrides file; empty env does not clobber).

## 1.1 Provider credentials — the real manager (M2, tasks 4.5 / 6.1)

**What changed, and why this section exists.** Until M2 this document said provider secrets were
"sourced from a secrets manager" while the code read `os.Getenv`. The gap was not a lie so much as an
*assumption*: the design assumed something upstream — a Vault agent, a Kubernetes secret, an ECS task
definition — had already put the value in the environment. That assumption is legitimate and is how
most deployments work, but it made the claim unfalsifiable: **an operator could not tell, from the
running system, whether a manager was involved at all.** A security property nobody can check is a
security property nobody has.

**What is true now.**

| | |
|---|---|
| **The seam** | `providergateway.Secrets` — `Credential(ctx, provider)` + `Describe()`. Provider credentials do **not** flow through `config.Config` and are **not** in the table above. A `ModelEntry` carries *which* provider, never *how to authenticate*: a credential in an entry would be hashed into its `version_id` and live forever. |
| **Implementations** | `EnvSecrets` (the pre-M2 path, unchanged, still correct for local dev) · **`AWSSecretsManager` (real; `aws-sdk-go-v2`, `GetSecretValue` at call time)** · `StaticSecrets` (tests, single-tenant). |
| **Choosing one** | `providergateway.NewSecretsFromEnv` — the single decision point. `HEROS_SECRETS_SOURCE=env` (default) or `aws-secrets-manager`. Resolved once, at **boot**, in `launch.StartAgentd`. |
| **Which one is live** | **`GET /readyz` → `secrets_source: {kind, detail}`.** This is the part that closes the gap above: the claim is now externally checkable, by a monitor, on the box in question. |

### Why AWS Secrets Manager first

Arbitrated on [八级法则](../../../aikeylabs-skills/shared/00-核心法则.md). At **L1 (safety)** the
candidates tie — Vault, GCP SM and AWS SM all hold a secret properly, so per **L2** the decision moves
down. It settles at **L4 (operations)**, not L8: Vault adds an agent to run, a token to renew and a
seal to unseal; AWS SM adds a client to a dependency (`aws-sdk-go-v2`) that the Bedrock adapter
already signs with. L8 (implementation cost) agrees, but per **L3** it is not permitted to be the
reason and it is not.

The L1 tiebreak that *did* matter: AWS SM authenticates **ambiently** (IRSA / task role / instance
profile / SSO), so wiring it creates **no bootstrap secret**. A manager reached with a long-lived key
in an env var has moved the secret, not removed it.

**This does not make AWS SM the only supported manager.** Vault or GCP SM is a new file implementing
the same interface — no gateway change, no caller change. That the seam predates the implementation is
why (`secrets.go:21`, written at 4.5: *"it is the boundary a real one plugs into"*).

### The three properties that are load-bearing

1. **Call-time resolution, bounded staleness.** Fetched per call, cached in **memory** for
   `DefaultSecretTTL` (5 min). Never on disk, never in a row, never in a log — so the DB sweep stays
   true by construction. The cost is rotation latency, bounded at 5 min and stated rather than
   discovered. Without the cache every model call is a `GetSecretValue` — a per-call charge and a
   throttling ceiling on P4's fan-out.
2. **Fail closed, everywhere, with no env fallback.** An unknown `HEROS_SECRETS_SOURCE`, an unmapped
   provider, a denied fetch, a malformed payload — all errors. **There is deliberately no fallback
   from AWS SM to `EnvSecrets`**: a manager that silently degraded to an env var would keep serving
   calls while `/readyz` still claimed AWS, and the deployment would be lying about its own posture.
   That is 禁止静默回落 at L1.
3. **Scrubbing is unchanged and now re-proved for the new source.** `scrubErr` breaks the `Unwrap`
   chain so the unredacted message is unreachable. `parseSecretPayload` is the one place holding a
   plaintext payload next to an error path, and it never interpolates it —
   `TestAWSSecrets_AMalformedPayloadNeverAppearsInTheError` is the guard, verified to go red.

### Secret payload shape

JSON object, `SecretString`. Two shapes, matching `Credential`'s two:

```json
{"api_key": "sk-..."}                                        // openai, anthropic
{"access_key_id": "...", "secret_access_key": "...",         // bedrock
 "session_token": "...", "region": "us-east-1"}
```

A raw non-JSON string is **rejected**, not guessed at: malformed JSON would be indistinguishable from
a raw key, so a typo'd payload would be handed to the provider as a credential and come back as a 401
that says nothing about the actual mistake (失败要显眼).

### Deployment

| Variable | Meaning |
|---|---|
| `HEROS_SECRETS_SOURCE` | `env` (default) or `aws-secrets-manager`. Anything else: **boot fails**. |
| `HEROS_SECRETS_AWS_REGION` | Region. Falls back to the SDK chain (`AWS_REGION`, profile). Required overall. |
| `HEROS_SECRETS_AWS_PREFIX` | Map by convention: `<prefix><provider>` → e.g. `heros/prod/openai`. |
| `HEROS_SECRETS_AWS_IDS` | Map explicitly: `openai=arn:...,anthropic=heros/anthropic`. Wins over the prefix. |

IAM: `secretsmanager:GetSecretValue` on those secrets only, plus `kms:Decrypt` on their key if
customer-managed. Least privilege per §4 — one role per service, not one role for all secrets.

## 2. Never in the repo

- **`.gitignore`** already excludes `config.json` (the only file that may hold a real key in dev).
- **`config.example.json`** is the committed template — **placeholders only**, each pointing at the env
  var to use. Copy it to `config.json` locally; never paste a real key into the example.
- **CI `secret-scan` job** runs **gitleaks** (`.gitleaks.toml`) on every push/PR and **fails the build**
  if a secret pattern is committed. The allowlist covers only the known placeholder strings, so a real
  key cannot hide behind the template.

## 3. Never in logs / CI echo / traces

- **Traces/metrics:** the OTel conventions doc forbids any credential in a span attribute, metric label,
  or log line; the exporter middleware is deny-by-default on attribute keys. See
  `otel-genai-conventions.md` §2.
- **Logs:** secret values are never logged. Log the *presence* ("OpenAI key: set/unset"), never the value.
- **CI echo:** the CI never `echo`s a secret. GitHub Actions masks `secrets.*` in logs, and no workflow
  step prints an env secret. The `db-proof` job uses a throwaway Postgres password (`postgres`) that is
  not a real credential.

## 4. Least privilege

- Each service gets only the keys it needs (a read-only embedding key ≠ a full provider key).
- Keys are rotated at the secrets manager; because nothing pins a key into a committed file, rotation is
  an env change, not a code change.
- No key is shared across editions/tenants by default.

## 5. Checklist (M0)

- [x] Secrets sourced from env (secrets-manager path), env-wins-over-file — `applySecretEnv` + test.
- [x] `config.json` gitignored; `config.example.json` is placeholders only.
- [x] CI `secret-scan` (gitleaks) blocks committed secrets.
- [x] No secret in logs / CI echo (masking + presence-not-value logging rule).
- [x] "No secrets in span attributes" rule stated and owned by the OTel conventions doc.
- [x] **(M2, was P2.5) A real secrets manager is wired** — `AWSSecretsManager` behind the `Secrets`
      seam, selected by `NewSecretsFromEnv`, reported at `GET /readyz`. See §1.1.
- [ ] (P2.5) Exporter-middleware redaction test once instrumentation exists.

## 6. What is deferred (with owner)

- **A live-AWS proof** — **P2.5 / DevOps**. 🔴 The integration is real (the real SDK client, real
  SigV4, real AWS JSON 1.1), and is tested against an httptest endpoint replaying AWS's documented
  wire shapes. **What no test here proves: that a real IAM policy grants `GetSecretValue`, that a real
  ARN resolves, that a real KMS key decrypts, or that endpoint TLS is right.** Those need an AWS
  account. The first deployment to set `HEROS_SECRETS_SOURCE=aws-secrets-manager` is the proof, and it
  fails closed and loudly at boot if any of the above is wrong — which is the design, not a
  consolation.
- **Vault / GCP Secrets Manager** — unscheduled, and *not* blocked: a new file implementing
  `providergateway.Secrets`. Explicitly **not** built ahead of a customer asking (禁止建了等未来用).
- **The `run_queue` / P4 fan-out call-volume profile vs `DefaultSecretTTL`** — **P4**. 5 min is a
  reasoned default, not a measured one; P4 is the first workload that will make the fetch rate
  observable.
- Exporter-middleware redaction **test** (asserting off-allow-list attributes are dropped) — **P2.5**,
  when telemetry is emitted. The *rule* is set here; the runtime test needs a runtime.
