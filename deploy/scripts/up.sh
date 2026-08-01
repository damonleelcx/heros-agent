#!/usr/bin/env bash
# One command: backend + customer console + operator console, up and verified, on one host.
#
#   make deploy-up            (or: bash deploy/scripts/up.sh)
#
# # What this is for
#
# The two-substrate artifacts in deploy/ describe a REPRODUCIBLE deploy: digest-pinned images published
# by a release pipeline, secrets injected from an operator's store. That is right for a cluster and it is
# unusable before a release exists — the platform-image digests in images.env are placeholders, and four
# of the five store digests are too. This script is the other path: build the three images from this
# checkout, generate the credentials once, and bring the whole thing up with no registry and no secret
# store. Same compose files, same topology, same /readyz.
#
# # The two rules it will not bend
#
#   1. FIRST INSTALL DETERMINES CREDENTIALS. It generates deploy/.env.local exactly once and REFUSES to
#      overwrite it. Rotating a credential under a running deployment would leave the console holding a
#      key the platform no longer honours, which presents as "the console is broken" rather than as
#      "the credential changed" — and on a deployment with real data, an unreadable store.
#   2. IT NEVER REPORTS SUCCESS IT DID NOT OBSERVE. It waits for the aggregated /readyz, then proves the
#      platform is actually refusing unauthenticated calls. A bring-up script that prints URLs because
#      `docker compose up` exited 0 is reporting that a container was created, not that a platform works.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
deploy="$root/deploy"
envfile="$deploy/.env.local"
imagesfile="$deploy/.env.images.local"
configdir="$deploy/config"
logdir="${TMPDIR:-/tmp}/heros-deploy-up"

READY_TIMEOUT="${READY_TIMEOUT:-300}"
AGENTD_PORT="${AGENTD_PORT:-4321}"
CONSOLE_PORT="${CONSOLE_PORT:-4320}"
ADMIN_CONSOLE_PORT="${ADMIN_CONSOLE_PORT:-4310}"

mkdir -p "$logdir"

# fail prints the six-tuple an operator needs and exits. One line of "error:" sends somebody to read
# container logs to learn what this script already knew.
fail() {
  local step="$1" reason="$2" causes="$3" fix="$4" next="$5"
  {
    echo ""
    echo "╭─ deploy/scripts/up.sh FAILED"
    echo "│  step          $step"
    echo "│  reason        $reason"
    echo "│  likely cause  $causes"
    echo "│  how to fix    $fix"
    echo "│  logs          $logdir"
    echo "│  next command  $next"
    echo "╰─"
  } >&2
  exit 1
}

say() { printf '\033[1m==\033[0m %s\n' "$*"; }

# ── 10_preflight ────────────────────────────────────────────────────────────────────────────────────
say "preflight"

command -v docker >/dev/null 2>&1 || fail "10_preflight" "docker is not on PATH" \
  "Docker Desktop / Colima is not installed, or not started" \
  "install Docker with the Compose plugin, then start it" "docker version"

docker compose version >/dev/null 2>&1 || fail "10_preflight" "the docker compose plugin is missing" \
  "an old standalone docker-compose, or a Docker install without the v2 plugin" \
  "install Docker Compose v2 (the 'docker compose' subcommand, not 'docker-compose')" "docker compose version"

docker info >/dev/null 2>&1 || fail "10_preflight" "the docker daemon is not reachable" \
  "Docker Desktop / Colima is installed but not running" \
  "start the Docker daemon and re-run" "docker info"

command -v curl >/dev/null 2>&1 || fail "10_preflight" "curl is not on PATH" \
  "a minimal base image or a stripped PATH" "install curl" "curl --version"

# A port already in use is the single most common first-run failure, and compose reports it as a driver
# error forty lines into a build. Say it up front, and name the port.
for p in "$AGENTD_PORT" "$CONSOLE_PORT" "$ADMIN_CONSOLE_PORT"; do
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    # Our OWN previous deployment holding the port is not a conflict — `up -d` is idempotent and will
    # reuse or replace those containers. Anything else is.
    #
    # 🔴 Asked via the container LABEL, not via `docker compose ps`. The obvious version of this check
    # called compose without the --env-file arguments, so the compose file's `${VAR:?}` interpolation
    # failed, `ps` printed nothing, and the guard concluded no stack was running — making this script
    # refuse to re-run against its OWN deployment. That is the idempotency it advertises, broken by the
    # check meant to protect it, and it only shows up on the second run.
    if [ -z "$(docker ps -q --filter "label=com.docker.compose.project=$(basename "$deploy")" 2>/dev/null)" ]; then
      fail "10_preflight" "port $p is already in use by another process" \
        "a previous non-compose run, or another service on that port" \
        "stop it, or re-run with a different port: AGENTD_PORT/CONSOLE_PORT/ADMIN_CONSOLE_PORT" \
        "lsof -nP -iTCP:$p -sTCP:LISTEN"
    fi
  fi
done

# ── 20_credentials (once, and only once) ────────────────────────────────────────────────────────────
# gen prints n random alphanumerics, or fails LOUDLY. It never prints a short or empty string.
#
# 🔴 Two traps, both hit while writing this:
#
#   `tr -dc … < /dev/urandom | head -c N`  — head closes the pipe at N bytes, tr takes SIGPIPE, and
#   under `set -o pipefail` the pipeline returns non-zero. Under `set -e` that killed this script
#   mid-generation. `dd` bounds the input instead, and `cut` drains it, so nothing is ever signalled.
#
#   The near-miss is worse than the crash: a variant that swallowed the error would have written
#   `POSTGRES_PASSWORD=` — an EMPTY password — into a file that looks generated and correct. So the
#   length is CHECKED, and a short read is a hard failure rather than a weak credential nobody notices.
gen() {
  local n="${1:-40}" out
  out="$(dd if=/dev/urandom bs=1024 count=8 2>/dev/null | LC_ALL=C tr -dc 'a-zA-Z0-9' | cut -c1-"$n")"
  if [ "${#out}" -ne "$n" ]; then
    fail "20_credentials" "the random generator produced ${#out} characters, wanted $n" \
      "/dev/urandom is unreadable, or dd/tr/cut behaved unexpectedly on this platform" \
      "this is refused rather than padded: a short or empty credential written into a file that looks generated is worse than a failed install" \
      "head -c 32 /dev/urandom | od -c | head -2"
  fi
  printf '%s' "$out"
}

if [ -f "$envfile" ]; then
  say "credentials: reusing $(basename "$envfile") — first install determined them, and this script never rewrites it"
else
  say "credentials: generating $(basename "$envfile") (once; 0600; git-ignored)"
  console_key="hc_$(gen 40)"
  admin_key="ha_$(gen 40)"
  console_assertion="$(gen 24)"
  umask 077
  cat > "$envfile" <<EOF
# GENERATED by deploy/scripts/up.sh on first install. 0600, git-ignored.
#
# 🔴 THIS FILE CONTAINS REAL CREDENTIALS. It is the one file under deploy/ that ever will — every other
# file there documents NAMES only. Do not commit it, do not paste it, and do not regenerate it under a
# running deployment: the platform would stop honouring the key the consoles still hold, and the symptom
# is "the console is broken", not "the credential changed".
#
# To rotate deliberately: stop the stack, delete this file AND deploy/config/config.json, re-run
# 'make deploy-up'. Your data survives (it lives in named volumes); only the credentials change.

POSTGRES_DB=heros
POSTGRES_USER=heros
POSTGRES_PASSWORD=$(gen 40)

OBJECT_STORE_ROOT_USER=heros
OBJECT_STORE_ROOT_PASSWORD=$(gen 40)

NEO4J_AUTH=neo4j/$(gen 32)

# The customer console BFF's own key for the platform API, and the operator BFF's — DISTINCT, so
# neither console can act with the other's credential. Both are registered in config/config.json.
CONSOLE_PLATFORM_CREDENTIAL=$console_key
ADMIN_PLATFORM_CREDENTIAL=$admin_key

# The assertion -> tenant map the customer console resolves a session against (ADR-008 'configured').
CONSOLE_TENANT_ASSERTIONS={"$console_assertion":"local"}
CONSOLE_TENANT_IDENTITY=configured

# Aggregate the operator console into the platform's /readyz — this bring-up ships one, so /readyz must
# know about it. Unset, readiness would report green while the operator surface was dead.
ADMIN_CONSOLE_HEALTH_URL=http://admin-console:4310/api/health

AGENTD_PORT=$AGENTD_PORT
CONSOLE_PORT=$CONSOLE_PORT
ADMIN_CONSOLE_PORT=$ADMIN_CONSOLE_PORT
EOF
  chmod 600 "$envfile"
fi

# shellcheck disable=SC1090
CONSOLE_KEY="$(grep '^CONSOLE_PLATFORM_CREDENTIAL=' "$envfile" | cut -d= -f2-)"
ADMIN_KEY="$(grep '^ADMIN_PLATFORM_CREDENTIAL=' "$envfile" | cut -d= -f2-)"

# ── 30_platform_config ──────────────────────────────────────────────────────────────────────────────
# The credential SET agentd authenticates against. It is a file rather than an environment variable
# because it is a list of records, and it is written from the same generated keys the consoles get — the
# two halves of one credential cannot be configured independently without eventually disagreeing.
mkdir -p "$configdir"
if [ ! -f "$configdir/config.json" ]; then
  say "platform config: writing config/config.json with auth REQUIRED"
  umask 077
  cat > "$configdir/config.json" <<EOF
{
  "auth_mode": "required",
  "tenant_credentials": [
    { "tenant_id": "local", "api_key": "$CONSOLE_KEY", "role": "member", "key_id": "customer-console" },
    { "tenant_id": "local", "api_key": "$ADMIN_KEY", "role": "admin", "key_id": "operator-console" }
  ]
}
EOF
  chmod 600 "$configdir/config.json"
else
  say "platform config: reusing config/config.json"
fi

# ── 40_bring_up ─────────────────────────────────────────────────────────────────────────────────────
compose() {
  docker compose \
    --project-directory "$deploy" \
    --env-file "$imagesfile" \
    --env-file "$envfile" \
    -f "$deploy/docker-compose.platform.yml" \
    -f "$deploy/docker-compose.platform.build.yml" \
    -f "$deploy/docker-compose.admin-console.yml" \
    "$@"
}

# The Go module source, passed INTO the build. A container inherits none of this host's network
# configuration: a machine that reaches modules through a regional mirror, or through a local proxy on
# 127.0.0.1 that no container can address, builds fine on the command line and then dies inside the
# image against the public default. Preference order: an explicit GOPROXY in this environment, then
# whatever `go` on this host is actually configured to use, then nothing (the Dockerfile's public
# default) — because a deploy host is not required to have Go installed at all.
goproxy="${GOPROXY:-}"
if [ -z "$goproxy" ] && command -v go >/dev/null 2>&1; then
  goproxy="$(go env GOPROXY 2>/dev/null || true)"
fi
build_args=""
if [ -n "$goproxy" ]; then
  build_args="--build-arg GOPROXY=$goproxy"
  say "go modules will be fetched via $goproxy"
fi

say "building the three platform images from this checkout, and starting every service"
echo "   (first run pulls the store images and compiles Go + two Next.js apps — several minutes)"
# shellcheck disable=SC2086  # build_args is deliberately word-split: it is empty or one --build-arg pair
if ! compose build $build_args > "$logdir/up.log" 2>&1 || ! compose up -d >> "$logdir/up.log" 2>&1; then
  tail -30 "$logdir/up.log" >&2
  fail "40_bring_up" "docker compose up failed (last 30 lines above)" \
    "a build error, an unpullable store image, or a port conflict" \
    "read the full log; if a build broke, fix it and re-run — this script is idempotent" \
    "less $logdir/up.log"
fi

# ── 50_verify ───────────────────────────────────────────────────────────────────────────────────────
# The platform's OWN aggregated verdict, not a per-container guess. /readyz is not-ready until every
# wired component answers, so waiting on it waits on all of them at once — and names the one that is
# holding things up.
say "waiting for the aggregated /readyz (timeout ${READY_TIMEOUT}s)"
deadline=$(( $(date +%s) + READY_TIMEOUT ))
last=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  if body="$(curl -fsS --noproxy '*' --max-time 5 "http://127.0.0.1:$AGENTD_PORT/readyz" 2>/dev/null)"; then
    printf '   /readyz: ready\n'
    last="$body"
    break
  fi
  # A 503 body still carries the verdict, and it names the component. Show it while waiting so a stuck
  # bring-up says WHICH dependency is stuck instead of counting down in silence.
  body="$(curl -sS --noproxy '*' --max-time 5 "http://127.0.0.1:$AGENTD_PORT/readyz" 2>/dev/null || true)"
  if [ -n "$body" ] && [ "$body" != "$last" ]; then
    last="$body"
    # python3, not sed: the `t` branch this used is a GNU extension, and BSD sed (macOS) rejects it
    # with `undefined label` — printing an error into a progress line on the platform this script is
    # most often run from. A bring-up script that emits sed diagnostics while it waits reads as broken.
    printf '   waiting: %s\n' "$(printf '%s' "$body" | python3 -c '
import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("starting"); raise SystemExit
bad = d.get("degraded_components") or [k for k,v in (d.get("components") or {}).items()
                                       if isinstance(v,dict) and v.get("status") not in (None,"ready")]
print("degraded: " + ", ".join(bad) if bad else "starting")' 2>/dev/null || echo starting)"
  fi
  sleep 3
done

if ! curl -fsS --noproxy '*' --max-time 5 "http://127.0.0.1:$AGENTD_PORT/readyz" >/dev/null 2>&1; then
  compose ps > "$logdir/ps.log" 2>&1 || true
  compose logs --tail 40 > "$logdir/services.log" 2>&1 || true
  printf '   last /readyz: %s\n' "${last:-<no response>}" >&2
  fail "50_verify" "the platform did not reach an aggregated healthy /readyz within ${READY_TIMEOUT}s" \
    "a component is still starting on a slow first run, or one is genuinely failing — the body above NAMES it" \
    "read the named component's log; raise READY_TIMEOUT=600 if the box is just slow" \
    "cat $logdir/services.log"
fi

# Both console origins, each on its own health endpoint — never a rendered page.
for pair in "customer console:$CONSOLE_PORT" "operator console:$ADMIN_CONSOLE_PORT"; do
  name="${pair%%:*}"; port="${pair##*:}"
  curl -fsS --noproxy '*' --max-time 5 "http://127.0.0.1:$port/api/health" >/dev/null 2>&1 \
    || fail "50_verify" "the $name is not healthy on :$port" \
       "its container failed to start, or it cannot reach the platform API" \
       "read that container's log" "docker compose --project-directory $deploy logs ${name// /-}"
  printf '   %s: healthy\n' "$name"
done

# 🔴 Prove auth is ENFORCED, not merely configured. "the config file did not load" and "the config said
# auth off" are indistinguishable from outside, and exactly one of them is intended. An unauthenticated
# call to a tenant-scoped route must be refused.
code="$(curl -sS --noproxy '*' --max-time 5 -o /dev/null -w '%{http_code}' \
  "http://127.0.0.1:$AGENTD_PORT/api/v1/prompts" 2>/dev/null || echo 000)"
if [ "$code" = "200" ]; then
  fail "50_verify" "the platform served a tenant-scoped route to an UNAUTHENTICATED caller" \
    "config/config.json did not load, so agentd fell back to auth_mode=off — and :$AGENTD_PORT is published" \
    "check that deploy/config/config.json exists and is valid JSON, then re-run" \
    "docker compose --project-directory $deploy exec agentd cat /etc/heros/config.json"
fi
printf '   auth: enforced (unauthenticated /api/v1/prompts -> %s)\n' "$code"

# ── 60_report ───────────────────────────────────────────────────────────────────────────────────────
cat <<EOF

╭─ up
│
│  backend (platform API)   http://127.0.0.1:$AGENTD_PORT       /readyz  /healthz
│  customer console         http://127.0.0.1:$CONSOLE_PORT
│  operator console         http://127.0.0.1:$ADMIN_CONSOLE_PORT       (its own origin — a disjoint cookie jar)
│
│  credentials              deploy/.env.local            (0600, git-ignored, generated once)
│  platform config          deploy/config/config.json    (the tenant credentials both consoles use)
│
│  what is SERVED vs registered-but-not-mounted:
│    docker compose --project-directory deploy logs agentd | grep -E 'served|not mounted'
│  stop, keeping data:      make deploy-down
│  stop, DELETING data:     make deploy-down-hard
╰─
EOF
