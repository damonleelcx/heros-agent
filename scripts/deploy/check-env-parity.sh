#!/usr/bin/env bash
# CI gate: Docker Compose and the Kubernetes base declare the SAME ENVIRONMENT CONTRACT, per workload.
#
# P19 Decision 2 promised both halves — "a CI check asserts the two substrates reference the same digest
# set AND the same env-var contract" — and only the digest half was ever written. The two had already
# drifted by the time anyone looked: ADMIN_CONSOLE_HEALTH_URL was set on Kubernetes and missing on
# Compose, so Compose /readyz silently never aggregated the operator console at all. That is exactly the
# failure this gate exists to make impossible: a variable that exists on one substrate and not the other
# does not fail anything, it just quietly changes what the deployment does.
#
# What it compares: the set of environment variable NAMES each workload declares. Not values — the
# values differ legitimately (a Compose interpolation, a secretKeyRef, a service DNS name), and
# comparing them would produce a gate nobody could keep green. Names are the contract; values are the
# environment.
#
# Written for bash 3.2 (the macOS system bash): no associative arrays, no mapfile.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail=0

# names_from_compose <file> <service> — the env var names one compose service declares.
#
# Reads the `environment:` mapping block of one service: enter at `  <service>:`, leave at the next
# key at the same indent, and take `KEY:` lines from inside `environment:`. A YAML parser would be
# better; a YAML parser is not present on every machine that runs this, and a gate that only runs
# where a dependency is installed is a gate that does not run.
names_from_compose() {
  awk -v svc="$2" '
    # service header at 2-space indent
    /^  [a-zA-Z0-9_-]+:[[:space:]]*$/ {
      name = $0; sub(/^  /, "", name); sub(/:.*$/, "", name)
      in_svc = (name == svc); in_env = 0; next
    }
    !in_svc { next }
    # environment: block header at 4-space indent
    /^    environment:[[:space:]]*$/ { in_env = 1; next }
    # any other 4-space key ends the environment block
    /^    [a-zA-Z0-9_-]+:/ { if (in_env) in_env = 0 }
    in_env && /^      [A-Za-z_][A-Za-z0-9_]*:/ {
      k = $0; sub(/^      /, "", k); sub(/:.*$/, "", k); print k
    }
  ' "$1" | sort -u
}

# names_from_k8s <file> <container> — the env var names one k8s container declares.
#
# Handles both forms the base uses: the inline `- { name: X, value: "y" }` flow mapping and the block
# `- name: X` / `valueFrom:` form.
names_from_k8s() {
  awk -v c="$2" '
    $0 ~ ("^        - name: " c "$") { in_c = 1; next }
    # a following sibling list item at the same indent ends this container
    in_c && /^        - name: / { in_c = 0 }
    !in_c { next }
    /^          env:/ { in_env = 1; next }
    # any other 10-space key ends the env list
    in_env && /^          [a-zA-Z]/ && !/^          env:/ { in_env = 0 }
    in_env && /^            - \{ name: [A-Za-z_][A-Za-z0-9_]*/ {
      k = $0; sub(/^.*\{ name: /, "", k); sub(/[,}].*$/, "", k); gsub(/[[:space:]]/, "", k); print k
    }
    in_env && /^            - name: [A-Za-z_][A-Za-z0-9_]*/ {
      k = $0; sub(/^.*- name: /, "", k); gsub(/[[:space:]]/, "", k); print k
    }
  ' "$1" | sort -u
}

# compare <label> <compose-names-file> <k8s-names-file>
compare() {
  local label="$1" a="$2" b="$3"
  local only_compose only_k8s
  only_compose="$(comm -23 "$a" "$b")"
  only_k8s="$(comm -13 "$a" "$b")"
  if [ -z "$only_compose" ] && [ -z "$only_k8s" ]; then
    echo "check-env-parity: OK — $label declares the same $(wc -l < "$a" | tr -d ' ') variables on both substrates"
    return 0
  fi
  echo "FAIL: $label declares a different environment contract on the two substrates." >&2
  if [ -n "$only_compose" ]; then
    echo "--- only in Docker Compose (missing from the Kubernetes base) ---" >&2
    printf '%s\n' "$only_compose" | sed 's/^/  /' >&2
  fi
  if [ -n "$only_k8s" ]; then
    echo "--- only in the Kubernetes base (missing from Docker Compose) ---" >&2
    printf '%s\n' "$only_k8s" | sed 's/^/  /' >&2
  fi
  echo "  A variable on one substrate only does not fail anything at apply — it silently changes what" >&2
  echo "  that deployment does. Add it to both, or remove it from both." >&2
  fail=1
}

# ── agentd ──────────────────────────────────────────────────────────────────────────────────────────
names_from_compose "$root/deploy/docker-compose.platform.yml" agentd > "$tmp/agentd.compose"
names_from_k8s "$root/deploy/k8s/base/agentd.yaml" agentd > "$tmp/agentd.k8s"
compare "agentd" "$tmp/agentd.compose" "$tmp/agentd.k8s"

# ── customer console ────────────────────────────────────────────────────────────────────────────────
names_from_compose "$root/deploy/docker-compose.platform.yml" console > "$tmp/console.compose"
names_from_k8s "$root/deploy/k8s/base/console.yaml" console > "$tmp/console.k8s"
compare "console" "$tmp/console.compose" "$tmp/console.k8s"

# ── operator console (its own unit on Compose, a workload in the same base on Kubernetes) ───────────
names_from_compose "$root/deploy/docker-compose.admin-console.yml" admin-console > "$tmp/admin.compose"
names_from_k8s "$root/deploy/k8s/base/admin-console.yaml" admin-console > "$tmp/admin.k8s"
compare "admin-console" "$tmp/admin.compose" "$tmp/admin.k8s"

# ── The gate must be able to FAIL. A parity check that finds nothing because its extractor returns
#    nothing is worse than no check: it reports green forever. Assert each side actually parsed.
for f in agentd.compose agentd.k8s console.compose console.k8s admin.compose admin.k8s; do
  if [ ! -s "$tmp/$f" ]; then
    echo "FAIL: extracted ZERO environment variables for $f — the extractor did not match the file's" >&2
    echo "  shape, so this gate was about to pass without comparing anything." >&2
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "check-env-parity: PASS — the two substrates declare the same environment contract"
