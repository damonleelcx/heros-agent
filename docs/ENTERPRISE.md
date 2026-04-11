# Enterprise stack (Qdrant + Neo4j + NATS)

This daemon integrates **three production tiers** (vector, graph, messaging) behind a **Go control plane + HTTP API**. Skills/tools/memory are **folder-first**; see `docs/AGENT_LAYOUT.md`.

## 1. Start infrastructure

```bash
cp deploy/.env.enterprise.example deploy/.env.enterprise
# edit NEO4J_AUTH if needed
docker compose -f deploy/docker-compose.enterprise.yml --env-file deploy/.env.enterprise up -d
```

Services:

- **NATS** `nats://127.0.0.1:4222` (enable JetStream with `-js` in compose)
- **Qdrant** `http://127.0.0.1:6333`
- **Neo4j** Bolt `neo4j://127.0.0.1:7687`, Browser `http://127.0.0.1:7474`

## 2. Configure agentd

Copy `config.enterprise.example.json`, set `neo4j_password` to match `NEO4J_AUTH`, and add `openai_api_key` if you want OpenAI embeddings (recommended with Qdrant). Vector dimension must match the embedder (`embedding_dims` 256 pairs with `text-embedding-3-small` + `dimensions` in the API).

```bash
go build -o agentd ./cmd/agentd
./agentd -config config.enterprise.example.json
```

## 3. Health and APIs

- `GET /health` — SQLite + optional Qdrant / Neo4j / NATS status
- `POST /api/memory/consolidate` — promote episodic → semantic (**Qdrant** + SQLite audit)
- `POST /api/memory/retrieve` — vector search (**Qdrant** when configured)
- `POST /api/graph/entity`, `POST /api/graph/link`, `GET /api/graph/neighbors` — **Neo4j** (+ SQLite mirror)
- Proposals: `POST /api/proposals` also publishes **`heros.fleet.proposals.pending`** on NATS; approve publishes **`heros.fleet.proposals.approved`**; memory promotion publishes **`heros.fleet.memory.promoted`**.

## 4. NATS subject layout

| Subject | Purpose |
|--------|---------|
| `heros.fleet.proposals.pending` | New mutation awaiting human approval |
| `heros.fleet.proposals.approved` | Committed after approval |
| `heros.fleet.memory.promoted` | Episodic chunk indexed to Qdrant |
| `heros.node.<node_id>.proposals.pending` | Same event, node-scoped |

## 5. Optional HTTP collective

`cmd/collectived` remains a minimal HTTP ingest stub; in production, prefer NATS + your own worker pool consuming `heros.fleet.*`.

## 6. JetStream persistence

Set `jetstream_enabled: true` in config (NATS must run with `-js`). Sidecar ensures stream `HEROS` (configurable) over subject `heros.>` with file storage and `jetstream_max_age_hours` retention. Publishes use JetStream when enabled so consumers can use durable pull/push subscribers.

## 7. Multi-tenant API auth

- `auth_mode`: `off` (default) or `required`.
- When `required`, every `/api/*` call needs `X-API-Key: <key>` or `Authorization: Bearer <key>` matching `tenant_credentials` in config.
- `role: admin` may list all tenants’ proposals / jobs; `member` is scoped to `tenant_id`.
- Proposals and episodic memory carry `tenant_id`; semantic chunks are tenant-filtered.
- Public without key: `GET /health`, `GET /metrics` (if enabled), static `/` UI.

## 8. Scheduler + observability

- SQLite table `scheduled_jobs`; background tick publishes `heros.fleet.scheduler.fired` on NATS (with payload). API: `GET /api/schedule/jobs`, `POST /api/schedule/jobs`.
- `metrics_enabled: true` → `GET /metrics` (Prometheus text counter `heros_http_requests_total`). All responses get `X-Request-ID`.

## 9. MCP bridge (stdio)

Build `go build -o heros-mcp ./cmd/heros-mcp`. Configure your IDE or any MCP host to run:

`heros-mcp -agentd-url=http://127.0.0.1:8787 -api-key=<key>`

Tools: `heros_health`, `heros_memory_retrieve`, `heros_submit_proposal` (HTTP to agentd — **no direct shell from MCP**; sandbox stays on the agentd side, e.g. existing CLI risk tiers).
