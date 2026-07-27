#!/usr/bin/env bash
# CI gate: no plaintext `kind: Secret` is committed, and no secret VALUE appears in any deploy manifest,
# env-example, or the image set (P19: "a committed plaintext Secret fails CI"; "no secret in any
# bundle/manifest/log"). Secrets are referenced via ExternalSecret; the value lives only in the
# operator's store, materialised at apply time.
#
# It fails LOUD on:
#   (a) any Kubernetes `kind: Secret` carrying `data:` or `stringData:` in the committed tree;
#   (b) a non-empty assignment to a known-secret env var in any deploy/.env*.example or deploy/images.env
#       (those files document NAMES only — a value there is a leak).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
fail=0

# (a) Plaintext Secret objects. A Secret manifest that carries no data/stringData (rare) is allowed; one
#     that carries either is a committed credential.
while IFS= read -r file; do
  # Look at each YAML doc: if it declares kind: Secret AND has data:/stringData:, it is plaintext.
  if awk '
    /^---/ {k=""; d=0}
    /^kind:[[:space:]]*Secret[[:space:]]*$/ {k=1}
    /^[[:space:]]*(data|stringData):/ {d=1}
    END {exit (k && d) ? 0 : 1}
  ' "$file"; then
    echo "FAIL: committed plaintext Secret with data/stringData: $file" >&2
    fail=1
  fi
done < <(grep -rlE '^kind:[[:space:]]*Secret[[:space:]]*$' "$root/deploy" 2>/dev/null || true)

# (b) A secret VALUE assigned in an env-example or the image set. These carry names only.
secret_vars='POSTGRES_PASSWORD|OBJECT_STORE_ROOT_PASSWORD|NEO4J_PASSWORD|NEO4J_AUTH|CONSOLE_PLATFORM_CREDENTIAL|CONSOLE_TENANT_ASSERTIONS|ADMIN_PLATFORM_CREDENTIAL|OPENAI_API_KEY|ANTHROPIC_API_KEY'
for f in "$root"/deploy/.env*.example "$root"/deploy/images.env; do
  [ -f "$f" ] || continue
  while IFS= read -r hit; do
    # An explicit placeholder (change-me / replace-me / replace_me) is not a real secret — example
    # files that use the "fill this in" convention rather than a blank are allowed to keep it.
    case "$hit" in
      *change-me*|*replace-me*|*replace_me*|*CHANGE_ME*|*REPLACE_ME*) continue ;;
    esac
    echo "FAIL: a secret value is assigned in an example/manifest file (must be name-only or a change-me placeholder): $hit" >&2
    fail=1
  done < <(grep -nE "^[[:space:]]*($secret_vars)=[^[:space:]].*" "$f" 2>/dev/null || true)
done

# (c) A LITERAL secret value in a compose file. The compose templates must reference secrets by
# `${VAR...}` interpolation only — a compose key like `POSTGRES_PASSWORD:` or `NEO4J_AUTH:` whose value
# is not a `${...}` reference is a hardcoded credential. This is the gate that makes the "no secret in
# any manifest" promise true for the compose files specifically (and covers what an external scanner's
# generic-password heuristic would otherwise be the only thing watching).
compose_keys='POSTGRES_PASSWORD|PGPASSWORD|MINIO_ROOT_PASSWORD|OBJECT_STORE_ROOT_PASSWORD|NEO4J_AUTH|NEO4J_PASSWORD|CONSOLE_PLATFORM_CREDENTIAL|ADMIN_PLATFORM_CREDENTIAL'
for f in "$root"/deploy/docker-compose*.yml; do
  [ -f "$f" ] || continue
  while IFS= read -r hit; do
    val="${hit#*:}"
    case "$val" in
      *'${'*) : ;;                         # a ${VAR...} interpolation — no literal secret
      *) echo "FAIL: a compose file assigns a literal secret value (must be \${VAR...}): $f: $hit" >&2; fail=1 ;;
    esac
  done < <(grep -nE "^[[:space:]]*($compose_keys):[[:space:]]*[^[:space:]]" "$f" 2>/dev/null || true)
done

if [ "$fail" -eq 0 ]; then
  echo "check-no-plaintext-secrets: OK — no committed Secret value; ExternalSecret references only"
fi
exit "$fail"
