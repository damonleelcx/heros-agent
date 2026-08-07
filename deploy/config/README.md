# `deploy/config/`

Mounted read-only into the platform container at `/etc/heros`. `deploy/scripts/up.sh` writes
`config.json` here on a first install — the tenant credentials the two console BFFs authenticate with —
and never rewrites it.

`config.json` is **git-ignored**, because it holds real credentials. This directory is tracked (and this
file is why) so the bind mount has a source in a fresh clone: without it, Docker would create a
root-owned directory in its place and the mount would silently carry nothing.

Rotating deliberately: stop the stack, delete this file *and* `deploy/.env.local`, re-run
`make deploy-up`. Data in the named volumes survives; only the credentials change.

## The two published catalogs

`agentd` reads two more files from this directory, and neither is in git — both carry **prices**, and
`internal/plancfg` and `internal/modelcatalog` each ship a fence that enumerates the whole git index and
fails the build if a catalog is ever committed. Both are also **git-ignored** by exact path, beside
`config.json`: the fences read the *index*, so they fire after a `git add -A` has already staged the
prices, and the ignore is what keeps that from happening in the first place.

| File | Gates | Without it |
|---|---|---|
| `plans.json` | P7 billing **and** P12 delivery (entitlement) | billing cannot say which plan a customer is on; delivery cannot decide whether a tenant may have a pull request opened for them |
| `models.json` | the P5.5 proposal generator | the registry records no capability tier or cost per run, so "cheaper" is not expressible and nothing can be proposed |

The deployment declares **where** they go (`PLAN_CATALOG_PATH`, `MODEL_CATALOG_PATH`); you supply the
files. A path with no file behind it reads as *not published* — agentd stats it at boot — so a fresh
install with neither gets a clean "not configured, here is the path" rather than a mounted billing page
that fails on its first read. Publishing one later needs a restart.

`models.json` is a list of `{name, tier, cost_per_run, latency_ms, provider, model_id}`, where `name` is
this deployment's name for the model and `tier` orders capability from 1 upward:

```json
{"models": [
  {"name": "Claude Sonnet 5",  "tier": 3, "cost_per_run": 0.05,  "latency_ms": 900,
   "provider": "anthropic", "model_id": "claude-sonnet-5"},
  {"name": "Claude Haiku 4.5", "tier": 1, "cost_per_run": 0.004, "latency_ms": 200,
   "provider": "anthropic", "model_id": "claude-haiku-4-5"}
]}
```

🔴 **Declare `provider` and `model_id`, or this file gates nothing.** They are what agentd registers at
boot so that `name` has something to match; without them the model registry stays empty, and three
surfaces go quiet at once for a reason none of them states: `/api/v1/models` returns nothing, the studio
matrix renders a workflow's node columns over **no rows**, and the proposal generator emits no
candidates — which reads to a customer as *"we looked at your workflow and found nothing to improve"*.

They are omitted only when some other route already registered the model; an entry without them is a
judgement about somebody else's registry row, and it is counted and reported as such at boot
(`model registry: seeded from …; N registered, M published without a provider/model_id`).

Seeding is idempotent — model entries are content-addressed, so a restart publishes nothing new, and
editing this file mints a new version on the next boot while the old one stays resolvable for any
configuration that pinned it. Nothing is ever deprecated or deleted from this file's contents: a model
you remove stops being **offered** and stays **resolvable**, because a `config_hash` somewhere may
depend on it.

A registered model with no entry here is **skipped and reported**, never defaulted to tier 0 — tier 0 is
cheaper than everything, so an unjudged model would silently become the first downgrade offered.
