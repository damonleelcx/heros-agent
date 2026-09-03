# Deploying herosd

`eval.heros-agent.space` — one container on the k3s node `i-05f4712279b04fac5`, sharing that node's
Postgres and mail relay with the other products hosted there.

## What this is, and what it is not

This directory deploys **one** thing: the `herosd` binary from `cmd/herosd`, serving the console in
`web/static` and driving goals against Postgres. There is no separate frontend service, no queue
service and no object store — the durable kernel keeps its state in Postgres and its working files
on one volume.

It is deliberately **not** a Kustomize tree with overlays. There is one deployment of this product
and one environment; overlays for environments that do not exist are three files to keep in sync for
no reader's benefit. The image reference is the only value that changes between deploys, and
`make deploy` substitutes it.

## The shared parts live somewhere else

Postgres, the nightly backup CronJob and the mail relay are in the **`heros` namespace** and their
manifests are on this repository's `main` branch under `deploy/k8s/`. They are shared infrastructure
serving three tenants, so they are not vendored here — a second copy is a second truth, and the one
that is wrong is always the one somebody applies.

Three things over there are **coupled to this deployment** and will break it silently if they drift:

| In `main`'s `deploy/k8s` | What it must say | What happens if it does not |
|---|---|---|
| `base/networkpolicy.yaml`, postgres ingress | an item naming namespace `heros-eval` + pod `heros-eval` | pods start, migrations hang, `connection refused` |
| `overlays/prod/mail.yaml`, mail ingress | the same, on port 587 | boots fine; invitations and resets silently never send |
| `base/postgres.yaml`, backup CronJob `for db in …` | includes `heros_eval` | **the database is not backed up, and the job still reports OK** |

The third is the dangerous one. A database missing from that loop is unprotected behind a green
backup job, and nobody looks until a restore is needed.

## First-time setup

```bash
# 1. Database and role on the shared instance. Idempotent; --dry-run prints the SQL.
ROLE_PASSWORD=… deploy/bootstrap-db.sh --dry-run
ROLE_PASSWORD=… deploy/bootstrap-db.sh

# 2. Credentials. One JSON secret; every property below must be present or External Secrets
#    fails the whole secret rather than one key.
aws secretsmanager create-secret --name heros/eval --secret-string '{
  "qwen-api-key": "…",
  "database-url": "postgres://heros_eval:…@postgres.heros.svc.cluster.local:5432/heros_eval?sslmode=disable",
  "bootstrap-email": "…",
  "bootstrap-password": "…",
  "smtp-username": "support@heros-agent.space",
  "smtp-password": "…"
}'

# 3. DNS: an A record for eval.heros-agent.space at the node's public IP, BEFORE applying —
#    cert-manager's HTTP-01 challenge resolves the name and fails quietly if it does not exist.
```

`HEROS_BOOTSTRAP_EMAIL` / `HEROS_BOOTSTRAP_PASSWORD` are read **once**, when no user exists. After
the first account is created they are inert; herosd refuses to start with neither them nor an
existing user, and there is no default password anywhere in the source.

## Deploying a change

```bash
make deploy            # build arm64, push to ECR, apply the digest ECR reports, wait for rollout
```

The node is **aarch64** (t4g.large), so the image must be built `--platform linux/arm64`. A local
Docker on an Apple Silicon Mac matches natively.

Deploys are pinned **by digest**, not by tag. A tag says what somebody called a build; a digest says
what is running. `make deploy` reads the digest back **from ECR** rather than from the local build:
what a build called itself and what is addressable in the registry are two different facts, and only
the second is what the cluster will pull — so reading it back also proves the push landed.

To stop between steps — to look at the `kubectl diff` before committing to it, or to redeploy a
digest that is already in ECR:

```bash
make deploy-push                          # prints the digest
make deploy-apply DIGEST=sha256:…         # apply.sh diffs against the live cluster before applying
make deploy-status                        # pods, ingress, certificate
```

### If the build cannot reach the module proxy

`go mod download` runs **inside the container**, which does not inherit your shell's proxy settings.
On a machine where `proxy.golang.org` is unreachable the build fails after about two minutes with:

```
dial tcp 142.251.45.145:443: i/o timeout
```

That reads like a network blip and is not one. The Makefile now defaults `GOPROXY` to whatever your
own `go env GOPROXY` says, so a machine already configured with a mirror works without any flag. If
you need to override it for one build:

```bash
make deploy GOPROXY=https://goproxy.cn,direct
```

## Access

The instance has **no SSH key pair**. Reach it with SSM:

```bash
aws ssm send-command --instance-ids i-05f4712279b04fac5 --document-name AWS-RunShellScript \
  --parameters 'commands=["k3s kubectl -n heros-eval get pods"]'
```

Multi-line scripts must be base64-framed (`echo <b64> | base64 -d | bash`) — passing newlines
through `--parameters` collapses them and the script fails on its first line. `kubectl` on the box
is `k3s kubectl`; there is no Docker there (containerd under k3s) and no checkout of this repo.
