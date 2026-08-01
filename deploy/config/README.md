# `deploy/config/`

Mounted read-only into the platform container at `/etc/heros`. `deploy/scripts/up.sh` writes
`config.json` here on a first install — the tenant credentials the two console BFFs authenticate with —
and never rewrites it.

`config.json` is **git-ignored**, because it holds real credentials. This directory is tracked (and this
file is why) so the bind mount has a source in a fresh clone: without it, Docker would create a
root-owned directory in its place and the mount would silently carry nothing.

Rotating deliberately: stop the stack, delete this file *and* `deploy/.env.local`, re-run
`make deploy-up`. Data in the named volumes survives; only the credentials change.
