#!/usr/bin/env bash
# Stand up the P27 account system locally and print where to click.
#
# Postgres (docker) + agentd (this branch) + the customer console (production build), wired together
# with self-serve sign-up ON. Everything lands in one scratch directory; nothing touches your repo.
#
#   bash scripts/run-local-auth-demo.sh           start it (paste-a-credential sign-in)
#   bash scripts/run-local-auth-demo.sh --oidc    start it with a REAL local OpenID Connect provider,
#                                                 so sign-in is the button-and-redirect flow a hosted
#                                                 customer actually meets
#   bash scripts/run-local-auth-demo.sh --stop    tear it down
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUN="${HEROS_LOCAL_DEMO_DIR:-$HOME/.heros-local-demo}"
PGNAME="heros-local-demo-pg"

say() { printf "\n\033[1m%s\033[0m\n" "$*"; }

MODE="configured"
[ "${1:-}" = "--oidc" ] && MODE="oidc"

if [ "${1:-}" = "--stop" ]; then
  say "stopping"
  [ -f "$RUN/agentd.pid" ]  && kill "$(cat "$RUN/agentd.pid")"  2>/dev/null || true
  [ -f "$RUN/console.pid" ] && kill "$(cat "$RUN/console.pid")" 2>/dev/null || true
  [ -f "$RUN/idp.pid" ]     && kill "$(cat "$RUN/idp.pid")"     2>/dev/null || true
  docker rm -f "$PGNAME" >/dev/null 2>&1 || true
  echo "stopped. state kept in $RUN (delete it to start clean)"
  exit 0
fi

command -v docker >/dev/null || { echo "docker is required (for Postgres)"; exit 1; }
command -v go     >/dev/null || { echo "go is required"; exit 1; }
mkdir -p "$RUN/data"

freeport() { python3 -c "import socket;s=socket.socket();s.bind(('127.0.0.1',0));print(s.getsockname()[1]);s.close()"; }

# ── 1 · Postgres ─────────────────────────────────────────────────────────────────────────────────────
say "1/4  Postgres"
docker rm -f "$PGNAME" >/dev/null 2>&1 || true
docker run -d --name "$PGNAME" -e POSTGRES_PASSWORD=demo -e POSTGRES_DB=demo -e POSTGRES_USER=postgres \
  -p 127.0.0.1:0:5432 postgres:16-alpine >/dev/null
PGPORT="$(docker port "$PGNAME" 5432/tcp | head -1 | sed 's/.*://')"
for _ in $(seq 1 60); do docker exec "$PGNAME" pg_isready -U postgres -q 2>/dev/null && break; sleep 0.5; done
echo "     ready on 127.0.0.1:$PGPORT"

# ── 2 · configuration ────────────────────────────────────────────────────────────────────────────────
# 🔴 auth_mode MUST be "required". It defaults to "off", and with it off no principal is ever resolved —
# every authenticated route answers 401 and the console cannot even record a session.
say "2/4  configuration"
API_PORT="$(freeport)"
BFF_KEY="heros_local_demo_console_credential"
ASSERTION="demo-assertion"
TENANT="org_demo_seed"
cat > "$RUN/config.json" <<EOF
{
  "data_dir": "$RUN/data",
  "listen_addr": "127.0.0.1:$API_PORT",
  "auth_mode": "required",
  "tenant_credentials": [
    { "tenant_id": "$TENANT", "api_key": "$BFF_KEY", "role": "owner", "key_id": "key_console" }
  ]
}
EOF
# A plan catalog is required for the ACCOUNT SURFACE to mount: a members page that cannot state the seat
# allowance is a page that invites a support ticket on its first use, so it stays absent without one.
cat > "$RUN/plans.json" <<'EOF'
{
  "version": "local-demo-1",
  "plans": [
    { "plan_id": "free", "display_name": "Free", "rank": 0,
      "features": ["cli", "discovery", "dashboard"],
      "limits": { "seats": 2 }, "price_refs": {} },
    { "plan_id": "team", "display_name": "Team", "rank": 1,
      "features": ["cli", "discovery", "dashboard", "assisted_pr"],
      "limits": { "seats": 5 }, "price_refs": { "subscription": "price_demo_team" } }
  ]
}
EOF

# ── 3 · the platform ─────────────────────────────────────────────────────────────────────────────────
say "3/4  platform (agentd)"
( cd "$ROOT" && GOWORK=off go build -o "$RUN/agentd" ./cmd/agentd )
DATABASE_URL="postgres://postgres:demo@127.0.0.1:$PGPORT/demo?sslmode=disable" \
PLAN_CATALOG_PATH="$RUN/plans.json" \
HEROS_SELF_SERVE_SIGNUP=1 \
HEROS_CONFIG="$RUN/config.json" \
  nohup "$RUN/agentd" > "$RUN/agentd.log" 2>&1 &
echo $! > "$RUN/agentd.pid"
for _ in $(seq 1 60); do curl -sS -m 2 "http://127.0.0.1:$API_PORT/readyz" >/dev/null 2>&1 && break; sleep 0.5; done
echo "     http://127.0.0.1:$API_PORT   (log: $RUN/agentd.log)"

# ── 4 · the console ──────────────────────────────────────────────────────────────────────────────────
# The PRODUCTION build under `next start`, not `next dev`: dev clobbers .next, and the console's own
# suite fakes ~117 sign-in failures against a stray dev server.
IDENTITY_ENV=()
if [ "$MODE" = "oidc" ]; then
  say "3.5  identity provider (local OIDC)"
  ( cd "$ROOT" && nohup node scripts/local-idp.mjs > "$RUN/idp.log" 2>&1 & echo $! > "$RUN/idp.pid" )
  for _ in $(seq 1 60); do grep -q '"issuer"' "$RUN/idp.log" 2>/dev/null && break; sleep 0.5; done
  ISSUER="$(head -1 "$RUN/idp.log" | python3 -c 'import json,sys; print(json.load(sys.stdin)["issuer"])')"
  echo "     $ISSUER   (signs in as dana@northwind.test)"
fi

say "4/4  console"
CONSOLE_PORT="$(freeport)"
( cd "$ROOT/web/console" && npm run build >"$RUN/console-build.log" 2>&1 ) || {
  echo "console build failed — see $RUN/console-build.log"; exit 1; }
if [ "$MODE" = "oidc" ]; then
  # A federated deployment MUST carry a tenant map, or there is no rule that resolves a verified claim
  # to an organization — the console refuses to boot without one rather than authenticate nobody.
  IDENTITY_ENV=(
    CONSOLE_TENANT_IDENTITY=oidc
    CONSOLE_IDP_ISSUER="$ISSUER"
    CONSOLE_IDP_CLIENT_ID=heros-console-local
    CONSOLE_IDP_REDIRECT_ALLOWLIST="http://127.0.0.1:$CONSOLE_PORT/auth/callback"
    CONSOLE_IDP_TENANT_MAP="{\"strategy\":\"issuer\",\"issuers\":{\"$ISSUER\":{\"tenant_id\":\"$TENANT\"}}}"
  )
else
  IDENTITY_ENV=(
    CONSOLE_TENANT_IDENTITY=configured
    CONSOLE_TENANT_ASSERTIONS="{\"$ASSERTION\":\"$TENANT\"}"
  )
fi

( cd "$ROOT/web/console" && \
  env NODE_ENV=production \
  PLATFORM_API_BASE="http://127.0.0.1:$API_PORT" \
  CONSOLE_PLATFORM_CREDENTIAL="$BFF_KEY" \
  CONSOLE_SESSION_STORE="platform" \
  CONSOLE_UPSTREAM_TIMEOUT_MS="8000" \
  "${IDENTITY_ENV[@]}" \
  nohup npx next start --port "$CONSOLE_PORT" > "$RUN/console.log" 2>&1 & echo $! > "$RUN/console.pid" )
for _ in $(seq 1 60); do curl -sS -m 2 "http://127.0.0.1:$CONSOLE_PORT/api/health" >/dev/null 2>&1 && break; sleep 0.5; done

if [ "$MODE" = "oidc" ]; then
  SIGNIN_HELP="  Sign in   press \"Continue with your organization\".
            You are sent to a DIFFERENT origin ($ISSUER),
            you choose an identity there, and you come back signed in.
            You type nothing. This is the hosted product's flow."
else
  SIGNIN_HELP="  Sign in   paste:  $ASSERTION

            It is an ASSERTION, not a password. This install runs the
            \`configured\` seam — the single-customer / air-gapped shape,
            where whoever runs the install hands out this value.
            For the federated flow your customers meet, use --oidc."
fi

cat <<EOF

────────────────────────────────────────────────────────────────────────
  Open      http://127.0.0.1:$CONSOLE_PORT/signin
$SIGNIN_HELP

  Then try
    /signup                   name an organization (self-serve is ON here)
    /app/settings/members     members, roles, and both seat numbers
    /app/device               approve a terminal login (\`heros login\`)
    /app/runs                 runs, incl. the pre-ownership state

  Sign out from the top-right, then open /app/settings/members again to
  watch it fail closed.

  Test \`heros login\` (the device flow, P27 §13) — in a second terminal:

    go run ./cmd/heroslocallink -device -addr 127.0.0.1:$API_PORT

  It prints a code and waits. Approve it at /app/device in the browser,
  and the terminal stores a PERSONAL credential. Then remove yourself
  from Members and run it again: the next request is refused.

  Stop everything:  bash scripts/run-local-auth-demo.sh --stop
────────────────────────────────────────────────────────────────────────
EOF
