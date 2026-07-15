# Secrets-Management Baseline (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | DevOps (support to System Designer) |
| Status | Draft — freeze at M0 |
| Tasks | 4.4 (this doc) |
| Cross-refs | `docs/decisions/otel-genai-conventions.md` (no secrets in spans); `internal/config/config.go`; `.gitleaks.toml`; `config.example.json` |

The baseline for provider and backing-service credentials, set **before any provider call exists** (no
live calls in P0) — least privilege by default. Core rule (arbitrated at level 1, *safety*, on the
cost/complexity ladder — it outranks every convenience):

> **Provider secrets are sourced from a secrets manager, and NEVER appear in the repo, logs, CI echo,
> or traces.** (NFR6)

---

## 1. Where secrets come from — the precedence

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
- [ ] (P2.5) Wire the real secrets manager for the deployed service; add the exporter-middleware
      redaction test once instrumentation exists.

## 6. What is deferred (with owner)

- Concrete secrets-manager product and its injection mechanism per deployment form — **P2.5 / DevOps**.
- Exporter-middleware redaction **test** (asserting off-allow-list attributes are dropped) — **P2.5**,
  when telemetry is emitted. The *rule* is set here; the runtime test needs a runtime.
