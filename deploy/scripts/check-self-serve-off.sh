#!/bin/sh
# check-self-serve-off.sh — the AIR-GAPPED package's self-serve-sign-up gate (P27 task 10.4).
#
# Sibling of check-external-origins.sh, and it runs beside it in package-airgapped.sh for the same
# reason: the claim is about the TARBALL, so it is established by the run that cuts the tarball rather
# than asserted by a README somebody updates.
#
# WHAT IT REFUSES. Any file in the staged package that turns `HEROS_SELF_SERVE_SIGNUP` on. P27 makes a
# verified identity that maps to no organization able to CREATE one — which is right for the hosted
# product and wrong for a machine room. An air-gapped install has one customer by construction, its
# identity provider is the customer's own, and every person who can authenticate is already inside the
# perimeter. Self-serve there does not onboard a customer; it lets anybody the IdP admits mint an
# organization on a platform nobody is watching, on a network with no egress to alert over it.
#
# 🔴 AND IT REFUSES A PACKAGE THAT SAYS NOTHING. Absence is already OFF — `launch.SelfServeEnabled`
# treats unset as off, deliberately — so a gate that only looked for an affirmative value would pass a
# package in which the posture is nowhere stated. That is the failure this whole phase's readiness work
# is written against: a configured value visible only as a missing line is a value nobody checks before
# an incident, and "it is off because nobody wrote it down" survives exactly until somebody writes it
# down. So the package must DECLARE the posture, and the declaration must be off.
#
# The difference matters on upgrade. An operator diffing two air-gapped packages can see a declaration
# change from "0" to "1"; they cannot see a line that was always absent start being absent for a
# different reason.
#
# USAGE:
#   deploy/scripts/check-self-serve-off.sh <root>...     # declared, and declared off
#   deploy/scripts/check-self-serve-off.sh --self-test   # prove it goes red
#
# Exit 0 = declared off. Exit 1 = on somewhere, or never declared. Exit 2 = usage.
set -eu

VAR="HEROS_SELF_SERVE_SIGNUP"

die() { echo "check-self-serve-off: FATAL: $*" >&2; exit 2; }

# ── The classifier. One function, so the gate and its self-test cannot disagree. ──────────────────────
#
# Reads "file:line:text" triples naming the variable on stdin and prints a verdict per line:
#   "on   <file>:<line>: <text>"   an affirmative value
#   "off  <file>:<line>: <text>"   an explicit negative
# A line that mentions the name without assigning a value — prose, a comment, a lookup in code — is
# neither, and is dropped. That is not leniency: this file's own header says the name, and so does
# deploy/README.md, and a gate that counted those as declarations would report the posture as declared
# by the document describing it.
classify() {
  awk -F: '
    {
      file = $1; line = $2;
      text = $0; sub(/^[^:]*:[^:]*:/, "", text);

      # The assigned value, in the three shapes this package carries: `NAME=value` (shell/.env),
      # `name: NAME, value: "v"` (k8s inline), and `- NAME=v` (compose). Everything after the name up to
      # the first separator, unquoted.
      v = text;
      sub(/^.*'"$VAR"'/, "", v);
      sub(/^["'"'"']?[[:space:]]*[,}]?[[:space:]]*/, "", v);        # k8s: close the name, reach `value:`
      sub(/^value[[:space:]]*:[[:space:]]*/, "", v);
      sub(/^[=:][[:space:]]*/, "", v);
      gsub(/["'"'"']/, "", v);
      sub(/[[:space:]]*[},].*$/, "", v);
      sub(/[[:space:]]+#.*$/, "", v);
      sub(/[[:space:]]+$/, "", v);
      lv = tolower(v);

      # 🔴 The same set launch.SelfServeEnabled accepts, and nothing else. A gate that recognised more
      # affirmatives than the code does would refuse packages that are already off; one that recognised
      # fewer would pass a package that is on. They must be the same list.
      if (lv == "1" || lv == "true" || lv == "yes" || lv == "on")   { print "on  " file ":" line ": " text; next }
      if (lv == "0" || lv == "false" || lv == "no" || lv == "off")  { print "off " file ":" line ": " text; next }
      # Anything else assigns nothing readable. Reported as neither — and since a package with no
      # readable declaration fails below, an unparseable value cannot pass by being ignored.
    }
  '
}

scan_root() {
  root="$1"
  [ -d "$root" ] || die "no such directory: $root"
  # Text-bearing config only. A `docker save` tarball inside the staging directory would otherwise be
  # grepped as a 400MB binary and could match the name by coincidence.
  find "$root" -type f \
    \( -name '*.yaml' -o -name '*.yml' -o -name '*.sh' -o -name '*.env' -o -name '.env.*' \
       -o -name '*.example' -o -name '*.conf' -o -name '*.json' \) -print 2>/dev/null |
  while IFS= read -r f; do
    grep -n "$VAR" "$f" 2>/dev/null | sed "s|^|$f:|" || true
  done | classify
}

# ── Self-test. A gate nobody has watched go red is a gate nobody knows the polarity of. ───────────────
self_test() {
  tmp=$(mktemp -d) || die "cannot create a temp dir"
  trap 'rm -rf "$tmp"' EXIT
  fails=0

  mkdir -p "$tmp/on"
  printf 'env:\n  - { name: %s, value: "1" }\n' "$VAR" > "$tmp/on/deploy.yaml"
  if verdicts=$(scan_root "$tmp/on") && echo "$verdicts" | grep -q '^on '; then
    echo "self-test: an affirmative value is detected — ok"
  else
    echo "self-test: FAILED to detect an affirmative value" >&2; fails=1
  fi

  mkdir -p "$tmp/off"
  printf 'env:\n  - { name: %s, value: "0" }\n' "$VAR" > "$tmp/off/deploy.yaml"
  verdicts=$(scan_root "$tmp/off")
  if echo "$verdicts" | grep -q '^off ' && ! echo "$verdicts" | grep -q '^on '; then
    echo "self-test: an explicit negative is detected as off — ok"
  else
    echo "self-test: FAILED on an explicit negative (got: $verdicts)" >&2; fails=1
  fi

  # The case this gate exists for beyond the obvious one: nothing said at all.
  mkdir -p "$tmp/silent"
  printf 'env:\n  - { name: NODE_ENV, value: "production" }\n' > "$tmp/silent/deploy.yaml"
  if [ -z "$(scan_root "$tmp/silent")" ]; then
    echo "self-test: a package that declares nothing yields no declaration — ok"
  else
    echo "self-test: FAILED — a silent package appeared to declare something" >&2; fails=1
  fi

  # Prose must not count. This file and deploy/README.md both name the variable.
  mkdir -p "$tmp/prose"
  printf '# Self-serve sign-up is off unless %s says otherwise.\n' "$VAR" > "$tmp/prose/README.md"
  printf 'echo "read %s"\n' "$VAR" > "$tmp/prose/doc.sh"
  if [ -z "$(scan_root "$tmp/prose")" ]; then
    echo "self-test: prose naming the variable is not a declaration — ok"
  else
    echo "self-test: FAILED — prose was read as a declaration" >&2; fails=1
  fi

  [ "$fails" -eq 0 ] || exit 1
  echo "check-self-serve-off: self-test passed"
  exit 0
}

ROOTS=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --self-test) self_test ;;
    -*) die "unknown flag: $1" ;;
    *) ROOTS="$ROOTS $1" ;;
  esac
  shift
done
[ -n "$ROOTS" ] || die "usage: $0 <root>... | --self-test"

VERDICTS=""
for root in $ROOTS; do
  found=$(scan_root "$root" || true)
  [ -z "$found" ] || VERDICTS="$VERDICTS$found
"
done

ON=$(echo "$VERDICTS" | grep '^on ' || true)
OFF=$(echo "$VERDICTS" | grep '^off ' || true)

if [ -n "$ON" ]; then
  echo "check-self-serve-off: FAILED — this package enables self-serve sign-up." >&2
  echo "$ON" | sed 's/^on  /    /' >&2
  cat >&2 <<EOF

An air-gapped install has ONE customer by construction. Its identity provider is the customer's own, so
everybody it admits is already inside the perimeter — and with self-serve on, every one of them can mint
an organization on a platform with no egress to alert over it. Set $VAR to 0 in the
air-gapped overlay and re-cut the package.
EOF
  exit 1
fi

if [ -z "$OFF" ]; then
  echo "check-self-serve-off: FAILED — this package never declares $VAR." >&2
  cat >&2 <<EOF

Unset already means off, so this package is not currently dangerous — and that is exactly why it must
say so. A posture visible only as a missing line is one nobody checks before an incident and nobody can
diff across two releases: an operator can see "0" become "1", but cannot see an absent line start being
absent for a different reason. Declare it: $VAR: "0" in the air-gapped overlay.
EOF
  exit 1
fi

echo "check-self-serve-off: self-serve sign-up is declared OFF"
