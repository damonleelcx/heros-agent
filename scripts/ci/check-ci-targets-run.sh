#!/usr/bin/env bash
# check-ci-targets-run.sh — every prerequisite of `make ci` is actually invoked by a workflow.
#
# WHY THIS EXISTS. `make ci` named five prerequisites. The workflows ran three of them. The other two —
# `docs-facts-check` and `console-types-check` — were invoked by nothing, so `make ci` was green in
# nobody's hands and the gates inside it had no effect on any pull request.
#
# That is how P28 merged with stale docs facts: the `heros login` command surface grew three flags, the
# generated reference did not, and the gate built to catch exactly that (P23 Decision 14 — "adding a
# subcommand is a normal Tuesday, and remembering the reference is not") never ran. Three pull requests
# went green over the top of it.
#
# 🔴 The failure mode is silence, which is why this is a script and not a comment. A target listed in
# `make ci` LOOKS wired — the aggregate target is right there in the Makefile, and reading it tells you
# the gate runs. Only reading the workflows tells you it does not, and nobody re-reads the workflows
# when they add a target. The same shape cost a day elsewhere in this repo: the admin-console image was
# unbuildable because no job builds that Dockerfile, and main stayed green throughout.
#
# WHAT IT DOES NOT CHECK. That the job is required, or that it ran on this commit. It checks the weakest
# useful property — that some workflow mentions the target at all — because that is the property that was
# false, and a check that goes red for the real reason beats a thorough one nobody lands.
set -euo pipefail

cd "$(dirname "$0")/../.."

WORKFLOWS=.github/workflows
[ -d "$WORKFLOWS" ] || { echo "check-ci-targets-run: FATAL: no $WORKFLOWS directory"; exit 1; }

# The prerequisites of `ci:`, read from the Makefile rather than restated here — a second copy of this
# list is a second thing to forget, which is the defect this script exists to catch.
targets="$(awk '/^ci:[[:space:]]/ { sub(/^ci:[[:space:]]*/, ""); print; exit }' Makefile)"
[ -n "$targets" ] || { echo "check-ci-targets-run: FATAL: no 'ci:' target found in Makefile"; exit 1; }

missing=""
for t in $targets; do
  # `make <target>` anywhere in any workflow. Matches `run: make go`, `run: PATH=… make deploy-lint`,
  # and a multi-line `run:` block alike.
  if grep -rqE "make([[:space:]]+[A-Za-z0-9_=./\"'\$(){}-]+)*[[:space:]]+${t}([[:space:]]|\$)" "$WORKFLOWS"; then
    printf '  ✓ %s\n' "$t"
  else
    printf '  🔴 %s — in `make ci`, invoked by no workflow\n' "$t"
    missing="$missing $t"
  fi
done

if [ -n "$missing" ]; then
  echo ""
  echo "check-ci-targets-run: FAIL —${missing}"
  echo "  These are gates that pass locally and gate nothing. Either add a step that runs each, or"
  echo "  remove it from 'make ci' so the aggregate target stops claiming coverage it does not have."
  exit 1
fi

echo "check-ci-targets-run: OK — every 'make ci' prerequisite is invoked by a workflow"
