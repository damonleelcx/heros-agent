#!/usr/bin/env bash
# heros-ci.sh is the P11 CI integration's brain: it runs the local heros workflow, decides the build
# outcome from the CLI's EXIT CODES, and enforces the two rules that make a CI check trustworthy:
#
#   🔴 build-safety (task 8.3): platform UNAVAILABILITY never fails the build. `link` and `login` are
#      the only platform-facing steps; each runs under a BOUNDED TIMEOUT and its failure — unreachable,
#      slow, or a 5xx — is REPORTED and the build CONTINUES. A slow dependency is an outage with extra
#      steps, so the timeout matters as much as the non-failure.
#   ✅ gate-bite (task 8.4): a customer-configured quality gate failing (exit 1) FAILS the build and
#      NAMES the gate. A check that never fails is decoration.
#
# It is usable with NO linking (task 8.6): with no token, the link steps are skipped and nothing is
# transmitted. It exposes the P12 delivery hook (task 8.7) without defining delivery.
#
# Env inputs:
#   HEROS_BIN            path to the heros binary (required)
#   HEROS_REPO           repo to analyze (default .)
#   HEROS_OUT            artifact output dir (default heros-out)
#   HEROS_EVAL_ARGS      extra args to `heros eval` (e.g. "--min-quality 0.7 --seeds 5")
#   HEROS_PLATFORM_TOKEN if set, linking is enabled; consumed from the CI SECRET mechanism, never logged
#   HEROS_LINK_TIMEOUT   seconds to bound each platform call (default 20)
#   HEROS_DELIVERY_HOOK  optional command P12 CI-mediated delivery runs (task 8.7); we invoke it and
#                        surface its result but define nothing about it
set -uo pipefail

HEROS_BIN="${HEROS_BIN:?HEROS_BIN is required}"
REPO="${HEROS_REPO:-.}"
OUT="${HEROS_OUT:-heros-out}"
EVAL_ARGS="${HEROS_EVAL_ARGS:-}"
LINK_TIMEOUT="${HEROS_LINK_TIMEOUT:-20}"
SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/stderr}"

mkdir -p "${OUT}"
note() { echo "heros-ci: $*" >&2; }
summary() { echo "$*" >> "${SUMMARY}" 2>/dev/null || true; }

# report_platform surfaces a platform-facing failure WITHOUT failing the build (task 8.3). It
# distinguishes the three conditions the spec names — slow (timeout), unreachable/degraded (other
# non-zero) — so the report says which, and the build continues either way.
report_platform() {
  local step="$1" code="$2"
  if [ "${code}" = "124" ]; then
    note "PLATFORM SLOW: ${step} exceeded ${LINK_TIMEOUT}s — reported, build NOT failed"
    summary "- linking: ⚠️ platform slow (${step} timed out after ${LINK_TIMEOUT}s) — build not failed"
  else
    note "PLATFORM UNAVAILABLE/DEGRADED: ${step} exit ${code} — reported, build NOT failed"
    summary "- linking: ⚠️ platform unavailable (${step} exit ${code}) — build not failed"
  fi
  # Deliberately no exit: our availability must never break a customer's build.
}

# run_bounded <seconds> <cmd...> — a portable timeout: prefers `timeout`/`gtimeout`, else a background
# kill. Returns 124 on timeout, matching coreutils, so the caller can tell "slow" from "errored".
run_bounded() {
  local secs="$1"; shift
  if command -v timeout >/dev/null 2>&1; then timeout "${secs}" "$@"; return $?; fi
  if command -v gtimeout >/dev/null 2>&1; then gtimeout "${secs}" "$@"; return $?; fi
  "$@" &
  local pid=$!
  ( sleep "${secs}"; kill -TERM "${pid}" 2>/dev/null ) &
  local watcher=$!
  wait "${pid}" 2>/dev/null; local code=$?
  kill -TERM "${watcher}" 2>/dev/null
  # If the watcher killed it, bash reports 143 (128+SIGTERM); normalize to 124 (timeout).
  [ "${code}" = "143" ] && code=124
  return "${code}"
}

# ── 1. discover (local, offline) ─────────────────────────────────────────────
note "discover ${REPO}"
"${HEROS_BIN}" discover --repo "${REPO}" --out "${OUT}/ir.json" --report "${OUT}/discovery-report.json" > "${OUT}/discover.json" 2>>"${OUT}/heros.log"
dcode=$?
if [ "${dcode}" -ne 0 ]; then
  note "discover failed (exit ${dcode}) — a local tool/config failure fails the build"
  summary "### heros check: ❌ discovery failed (exit ${dcode})"
  exit "${dcode}"
fi

# ── 2. eval (local, offline) → the gate decision ─────────────────────────────
note "eval ${REPO} ${EVAL_ARGS}"
# shellcheck disable=SC2086
"${HEROS_BIN}" eval --repo "${REPO}" ${EVAL_ARGS} > "${OUT}/eval.json" 2>>"${OUT}/heros.log"
ecode=$?
case "${ecode}" in
  0) note "eval passed (no gate failure)"; summary "### heros check: ✅ passed" ;;
  1)
    gate="$(grep -o '"name": *"[^"]*"' "${OUT}/eval.json" | head -1 | cut -d'"' -f4)"
    note "GATE FAILED: ${gate:-configured gate} — failing the build (task 8.4)"
    summary "### heros check: ❌ quality gate \`${gate:-configured}\` failed"
    exit 1
    ;;
  3) note "invalid configuration (exit 3) — failing the build"; summary "### heros check: ❌ invalid configuration"; exit 3 ;;
  *) note "eval operational error (exit ${ecode}) — a local failure fails the build"; summary "### heros check: ❌ eval error (exit ${ecode})"; exit "${ecode}" ;;
esac

# ── 3. link (platform-facing, build-safe) ────────────────────────────────────
run_id="$(grep -o '"run_id": *"[^"]*"' "${OUT}/eval.json" | head -1 | cut -d'"' -f4)"
if [ -z "${HEROS_PLATFORM_TOKEN:-}" ]; then
  note "no HEROS_PLATFORM_TOKEN — linking disabled; nothing is transmitted (task 8.6)"
  summary "- linking: disabled (nothing transmitted)"
else
  note "linking run ${run_id} (bounded ${LINK_TIMEOUT}s; platform unavailability will NOT fail the build)"
  # login, then link — each bounded and each non-fatal. The token is passed by env, never echoed.
  HEROS_PLATFORM_TOKEN="${HEROS_PLATFORM_TOKEN}" run_bounded "${LINK_TIMEOUT}" "${HEROS_BIN}" login >/dev/null 2>>"${OUT}/heros.log"
  lcode=$?
  if [ "${lcode}" -ne 0 ]; then
    report_platform "login" "${lcode}"
  else
    run_bounded "${LINK_TIMEOUT}" "${HEROS_BIN}" link --repo "${REPO}" --run "${run_id}" > "${OUT}/link.json" 2>>"${OUT}/heros.log"
    lcode=$?
    if [ "${lcode}" -ne 0 ]; then
      report_platform "link" "${lcode}"
    else
      url="$(grep -o '"run_url": *"[^"]*"' "${OUT}/link.json" | head -1 | cut -d'"' -f4)"
      note "linked · ${url}"
      summary "- linking: ✅ [view run](${url})"
    fi
  fi
fi

# ── 4. P12 delivery hook (task 8.7) — invoked, never defined here ─────────────
if [ -n "${HEROS_DELIVERY_HOOK:-}" ]; then
  note "invoking P12 delivery hook (its contract is P12's, not ours)"
  HEROS_RUN_ID="${run_id}" HEROS_OUT="${OUT}" bash -c "${HEROS_DELIVERY_HOOK}" || note "delivery hook returned non-zero (P12 owns that outcome)"
fi

note "done — build outcome: PASS"
exit 0
