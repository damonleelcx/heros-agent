#!/bin/sh
# check-external-origins.sh — the AIR-GAPPED package's zero-external-origin gate (P24 task 1.8, D1).
#
# WHY THIS RUNS AT PACKAGE-BUILD TIME AND NOT AT INSTALL TIME.
# The claim "this package references no external origin" is the one an air-gapped customer cannot check
# for themselves — they have no egress with which to discover that something tried to leave, and by the
# time anything is observable the package is already inside their machine room. So the claim is produced
# by the SAME RUN that produces the artifact: it is a property of the tarball, established while the
# tarball is being cut, rather than a README somebody updates. A check that runs on the far side would
# be checking a package that has already been shipped.
#
# WHY IT EXISTS AT ALL. P24 installs three third-party products — an analytics tag, a session recorder
# and an error reporter — into a platform that ships to three substrates: our own hosted deployment, a
# customer's Compose/Kubernetes install, and an air-gapped network. All three integrations are configured
# for the FIRST ONE ONLY. Absence everywhere else is the default, and it is silent. The failure this
# gate is written against is not malice; it is a Helm value, an example env, or an overlay patch that
# carries a measurement id or an ingest host into a bundle nobody expected to phone home. The blast
# radius of that mistake is a customer's network, and they find out before we do.
#
# WHAT COUNTS AS EXTERNAL. An absolute http(s) URL whose host is reachable off the customer's own
# network: anything that is not loopback, not an RFC1918 / link-local address, not a cluster-internal
# name (`*.svc`, `*.svc.cluster.local`, `*.internal`, `*.local`), not a Compose service name (a bare
# host with no dot), and not a shell/Kustomize placeholder the operator fills in with their own value.
# Those exclusions are the point rather than leniency: a health probe against `http://console:4320` is
# the package talking to itself, and flagging it would teach somebody to switch the gate off.
#
# It also flags the two SHAPES that would carry a reporting identity even without a full URL: a GA4
# measurement id and a Sentry DSN. Those are the values that turn an absent integration into a present
# one, and both are recognisable by shape.
#
# WHY `--allow` EXISTS AND WHY IT DEFAULTS TO NOTHING.
# The air-gapped package's answer is ZERO, with no exceptions, and that is the default. The P20
# installable-package build has a different and equally honest answer: its installer must reach the
# public forge the customer is downloading FROM, and hiding that behind a proxy would make a third-party
# flow look first-party — the manoeuvre P24's design rejects by name for exactly this reason. So each
# permitted host is named on the command line by the build that permits it, one flag per decision,
# rather than being written into a shared allowance nobody re-reads.
#
# USAGE:
#   deploy/scripts/check-external-origins.sh <root>                        # zero external origins
#   deploy/scripts/check-external-origins.sh --allow github.com <root>     # zero, except a named host
#   deploy/scripts/check-external-origins.sh --self-test                   # prove it goes red
#
# Exit 0 = zero external origins. Exit 1 = findings, each naming file, line and origin. Exit 2 = usage.
set -eu

die() { echo "check-external-origins: FATAL: $*" >&2; exit 2; }

# ALLOWED is a space-separated host list. A host is matched exactly or as a suffix after a dot, so
# `--allow github.com` covers `api.github.com` without covering `evilgithub.com`.
ALLOWED=""

# ── The classifier. One function, so the gate and its self-test cannot disagree. ─────────────────────
#
# Reads "file:line:url" triples on stdin, writes the EXTERNAL ones to stdout unchanged.
classify() {
  awk -F: -v allowed="$ALLOWED" '
    BEGIN { n = split(allowed, allow, " ") }
    {
      # Rebuild the URL: awk split the "file:line:" prefix and the URL contains colons of its own.
      file = $1; line = $2;
      url = $0; sub(/^[^:]*:[^:]*:/, "", url);

      host = url;
      sub(/^https?:\/\//, "", host);
      sub(/[\/?#].*$/, "", host);
      sub(/:[0-9{$][^:]*$/, "", host);   # port, incl. a {placeholder} or ${VAR} port
      sub(/^[^@]*@/, "", host);          # strip userinfo, which is where a DSN key lives

      if (host == "") next;
      if (host ~ /\$/) next;             # a placeholder the operator fills in
      if (host ~ /\{\{/) next;           # a template placeholder
      if (host == "localhost") next;
      if (host ~ /^127\./) next;
      if (host ~ /^0\.0\.0\.0$/) next;
      if (host ~ /^10\./) next;
      if (host ~ /^192\.168\./) next;
      if (host ~ /^172\.(1[6-9]|2[0-9]|3[01])\./) next;
      if (host ~ /^169\.254\./) next;
      if (host ~ /^\[/) next;            # bracketed IPv6 literal, incl. [::1]
      if (host ~ /\.svc$/ || host ~ /\.svc\.cluster\.local$/) next;
      if (host ~ /\.internal$/ || host ~ /\.local$/) next;
      if (host !~ /\./) next;            # a bare name is a Compose service, not a host on the internet

      # A host named by the build that permits it. Exact, or a subdomain of it — never a substring, so
      # `--allow github.com` does not quietly permit `evilgithub.com`.
      for (i = 1; i <= n; i++) {
        if (allow[i] == "") continue;
        if (host == allow[i]) next;
        if (length(host) > length(allow[i]) &&
            substr(host, length(host) - length(allow[i])) == "." allow[i]) next;
      }

      printf "%s:%s:%s\n", file, line, url;
    }
  '
}

# ── The scan over a package root. ────────────────────────────────────────────────────────────────────
#
# Comments are stripped first, for the reason every other fence in this repository strips them: a URL in
# prose is documentation, and a gate that cannot tell code from commentary either cries wolf or gets
# "fixed" by loosening the pattern until it stops catching the real thing. `images/` is skipped because
# a saved container image is a binary blob whose contents are governed by the image build, not by this
# packager — grepping it would produce findings nobody can act on from here.
scan_root() {
  root="$1"
  [ -e "$root" ] || die "no such package root: $root"

  find "$root" -type f \
    ! -path "*/images/*" \
    ! -name "SHA256SUMS" \
    ! -name "*.tar" ! -name "*.tar.gz" ! -name "*.gz" ! -name "*.zip" \
    | LC_ALL=C sort \
    | while IFS= read -r f; do
        # Skip anything that is not text; a binary that happens to contain "http://" is not a manifest.
        case "$(file -b --mime-type "$f" 2>/dev/null || echo text/plain)" in
          text/*|application/json|application/x-sh|inode/x-empty|application/javascript) ;;
          *) continue ;;
        esac
        rel=${f#"$root"/}
        sed -e 's/#.*$//' "$f" \
          | grep -noE 'https?://[^ "'"'"'`)<>,]+' \
          | sed "s|^|$rel:|" \
          | sed 's|:\([0-9]*\):|:\1:|'
      done | classify
}

# ── Reporting identities that carry no host of their own. ────────────────────────────────────────────
scan_ids() {
  root="$1"
  # A GA4 measurement id, and a Sentry DSN's distinctive userinfo@host shape. Both are recognisable by
  # shape, and both turn "absent by default" into "configured" with a single line in an overlay.
  grep -rnoE '\bG-[A-Z0-9]{8,12}\b|https://[0-9a-f]{16,}@[a-z0-9.-]+' "$root" \
    --exclude-dir=images --exclude=SHA256SUMS 2>/dev/null || true
}

self_test() {
  tmp=$(mktemp -d) || die "cannot create a temp dir"
  trap 'rm -rf "$tmp"' EXIT
  mkdir -p "$tmp/pkg/deploy"

  # The permitted shapes must NOT be flagged, or the gate cries wolf and gets disabled.
  cat > "$tmp/pkg/deploy/compose.yml" <<'EOF'
services:
  console:
    healthcheck:
      test: ["CMD", "curl", "-f", "http://127.0.0.1:4320/api/health"]
  agentd:
    environment:
      PEER: http://agentd:4321
      VAULT: http://vault.heros.svc:8200
      SITE: https://$DOMAIN
# see https://example.com/design for the reasoning behind the probe
EOF
  if [ -n "$(scan_root "$tmp/pkg")" ]; then
    echo "check-external-origins: SELF-TEST FAILED: a loopback, service-name, cluster-internal," >&2
    echo "  placeholder or commented URL was flagged. A gate that cries wolf gets switched off." >&2
    echo "$(scan_root "$tmp/pkg")" >&2
    exit 1
  fi

  # And the thing it is for MUST be flagged.
  cat > "$tmp/pkg/deploy/leak.yml" <<'EOF'
services:
  console:
    environment:
      TAG_SRC: https://www.googletagmanager.com/gtag/js
EOF
  if ! scan_root "$tmp/pkg" | grep -q googletagmanager; then
    echo "check-external-origins: SELF-TEST FAILED: an analytics host in an overlay was not caught." >&2
    exit 1
  fi

  # A SYNTHETIC id. It only has to match the shape this gate looks for, and a live measurement id in
  # the source tree makes a test fixture the one place this repository names one — which is how an id
  # ends up copied into a manifest by somebody looking for an example.
  printf 'HEROS_GA_MEASUREMENT_ID=G-FIXTURE0000\n' > "$tmp/pkg/deploy/.env.example"
  if ! scan_ids "$tmp/pkg" | grep -q 'G-'; then
    echo "check-external-origins: SELF-TEST FAILED: a GA4 measurement id was not caught." >&2
    exit 1
  fi

  echo "check-external-origins: self-test passed — an external host and a measurement id are caught;"
  echo "  loopback, Compose service names, cluster-internal names, placeholders and prose are not."
}

ROOTS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --self-test) self_test; exit 0 ;;
    --allow) shift; [ $# -gt 0 ] || die "--allow needs a host"; ALLOWED="$ALLOWED $1" ;;
    -h|--help) sed -n '1,45p' "$0"; exit 0 ;;
    -*) die "unknown option: $1" ;;
    *) ROOTS="$ROOTS $1" ;;
  esac
  shift
done
[ -n "$ROOTS" ] || die "usage: $0 [--allow <host>]... <root>... | --self-test"
FINDINGS=""
IDS=""
for root in $ROOTS; do
  found=$(scan_root "$root" || true)
  [ -z "$found" ] || FINDINGS="$FINDINGS$found
"
  ids=$(scan_ids "$root" || true)
  [ -z "$ids" ] || IDS="$IDS$ids
"
done

if [ -n "$FINDINGS" ] || [ -n "$IDS" ]; then
  echo "check-external-origins: FAILED — this package references something outside the customer's network." >&2
  [ -z "$FINDINGS" ] || { echo "  external origins:" >&2; echo "$FINDINGS" | sed 's/^/    /' >&2; }
  [ -z "$IDS" ] || { echo "  reporting identities:" >&2; echo "$IDS" | sed 's/^/    /' >&2; }
  cat >&2 <<'EOF'

An air-gapped package carries no measurement id, no project id, no DSN and no external host — not as a
default, not as an example, not commented out in an overlay. All three P24 integrations are configured
for the platform's own hosted deployment only; absence is the default on every other substrate and it
is silent. Remove the reference and re-cut the package.
EOF
  exit 1
fi

echo "check-external-origins: passed — 0 external origins, 0 reporting identities in$ROOTS"
