# Fleet skill worker (`fleet-skill-worker`)

Reference program: subscribe to **NATS** subject **`heros.fleet.proposals.approved`** (same event `collectived` publishes after `POST /v1/ingest/approved-mutation`), parse **`prompt_engineering`** proposals, and write **`SKILL.md`** files under:

`data_dir/skills/<tenant>/_global or slug/<skill>/SKILL.md`

matching agentd layout. Then optionally:

- **`git -C <dir> pull --ff-only`** — treat a git repo as extra source of truth
- **`POST /api/catalog/reindex`** on local agentd — refresh SQLite / Neo4j skill index

## Build & run

```bash
go build -o fleet-skill-worker ./cmd/fleet-skill-worker
./fleet-skill-worker -nats nats://127.0.0.1:4222 -data-dir "$HOME/.heros-agent" \
  -agentd-reindex-url http://127.0.0.1:8787/api/catalog/reindex
```

Flags:

| Flag | Meaning |
|------|--------|
| `-nats` | NATS URL (or `NATS_URL`) |
| `-subject` | Default `heros.fleet.proposals.approved` |
| `-queue` | Queue group name for horizontal scale (optional) |
| `-data-dir` | Same as agentd **`data_dir`** |
| `-state-file` | Dedup applied proposal IDs (default: OS config dir / `heros-fleet-worker/applied.json`) |
| `-apply-system-prompt` | Also write **`system/prompt.md`** if diff contains `### SYSTEM_PROMPT` |
| `-git-pull-dir` | After apply, run **`git pull --ff-only`** in that directory |
| `-agentd-reindex-url` | Trigger catalog reindex after each apply |
| `-api-key` | `X-API-Key` if agentd requires it |

## Storage model: NATS vs Git vs S3

| Approach | Pros | Cons |
|----------|------|------|
| **NATS payload (this worker)** | No extra store; uses full proposal **`diff_text`** already emitted by agentd/collectived; lowest latency. | Large diffs inflate messages; broker retention is not a durable archive by default; replay needs JetStream or logging. |
| **Git as SoT** | History, PR review, branch policies, familiar ops; **`git pull`** on nodes is the real “sync”. | Approving in Heros does not push git by itself—you need a CI job or bot that commits on `approved` events, or humans push skills manually. |
| **S3 / object store** | Good for large artifacts, versioning, compliance; Lambda can react to `PutObject`. | More moving parts; you still need a signal (NATS/SNS) or polling. |

**Practical recommendation**

- **Small org / fast iteration:** this worker (**NATS carries the diff**) + optional **reindex** — simplest.
- **Enterprise “golden repo”:** use NATS only as a **signal** (`{"git_ref":"…"}`) and run **`git pull`** (or a dedicated sync sidecar); store skills only in git.
- **Heavy binaries / bundles:** **S3** (+ manifest in NATS); worker downloads and unpacks — out of scope for this minimal binary but the pattern fits.

`collectived` today publishes the **same JSON shape** as `approval.Proposal` on `heros.fleet.proposals.approved`, so this worker stays compatible without S3/Git changes.

## Security

Approved payloads **write local files**. Run the worker only on trusted networks; restrict NATS auth; validate in production with your own signing or allowlist if needed.
