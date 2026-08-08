#!/usr/bin/env bash
# CI gate: agentd's egress to the mail relay is a podSelector to an in-cluster relay, never an
# address — and it EXISTS.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# 🔴 WHAT THIS GATE IS FOR, AND WHY IT ASSERTS THE SHAPE RATHER THAN A VALUE
#
# agentd runs under default-deny. If its egress rule for SMTP submission is missing or wrong, every
# confirmation link, password reset and invitation is dropped by the NetworkPolicy — with no event,
# no log line and no metric. /readyz stays green, because `mailer.Configured()` describes
# configuration and not reachability. The symptom that reaches a human is a customer saying the email
# never arrived, weeks later.
#
# This deployment has now been through THREE shapes of that rule, and two of them were wrong in ways
# nothing caught:
#
#   1. `ipBlock: 0.0.0.0/0` on 587 — correct for SES, which publishes no stable address, but it gave
#      the only workload with external egress a path to any host on the internet on that port.
#   2. `ipBlock: <elastic ip>/32` — narrower, and it named an address on a dedicated mail host that
#      was subsequently terminated and RELEASED. An egress rule pointing at a released public
#      address is worse than a wide one: it points at whatever AWS hands the next customer.
#   3. `podSelector: app.kubernetes.io/name=mail` — the relay is a pod in the same namespace, so no
#      address is involved at all.
#
# An `ipBlock` pinned to the NODE'S OWN public address would also pass any test of "can agentd send
# mail" — by hairpinning out through the internet gateway and back to a pod one virtual hop away.
# That version works, and is the one this gate exists to reject: it is indistinguishable from correct
# at runtime, and it puts a credential-bearing connection on the public internet.
#
# Written for bash 3.2 (the macOS system bash).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
file="$root/deploy/k8s/overlays/prod/kustomization.yaml"
fail=0

[ -f "$file" ] || { echo "check-mail-relay-pinned: $file not found" >&2; exit 1; }

# 🔴 THE RULE MUST EXIST. An earlier version of this gate asserted only "if there is a 587 rule, it
# must look like X" — so a tree with NO 587 rule satisfied every assertion and the gate reported
# PASS. No rule and a wrong rule produce the identical silent timeout. An empty set passing every
# test about its members is not evidence of anything.
if ! grep -q 'port: 587' "$file"; then
  cat >&2 <<'EOF'

  ✗ deploy/k8s/overlays/prod/kustomization.yaml declares NO egress rule for SMTP submission (587).

    agentd runs under default-deny. With no rule, every message it sends is dropped by the
    NetworkPolicy — silently, with /readyz green and nothing in any log naming this file.

EOF
  exit 1
fi

# The 587 rule must be a podSelector. Find the line and look back a few lines for how the
# destination was expressed: `to:` immediately precedes it in this file's style.
awk '
  { line[NR] = $0 }
  /port: 587/ {
    shape = ""
    for (i = NR; i >= NR - 4 && i >= 1; i--) {
      if (line[i] ~ /ipBlock/)     { shape = "ipBlock"; break }
      if (line[i] ~ /podSelector/) { shape = "podSelector"; break }
    }
    if (shape == "ipBlock") {
      print "  ✗ the SMTP egress rule (line " NR ") is an ipBlock, not a podSelector."
      # NOTE: no apostrophes anywhere inside this awk program. It is single-quoted, so a lone
      # apostrophe closes it and the shell then fails to parse the REST OF THE FILE, reporting a
      # syntax error on a later line that has nothing wrong with it.
      print "    The relay is a workload in this namespace (overlays/prod/mail.yaml). An address here"
      print "    is either stale, or the public IP of the node itself — which works by hairpinning"
      print "    agentd out through the internet gateway and back, and is indistinguishable from"
      print "    correct at runtime."
      print "    Use: to: [{ podSelector: { matchLabels: { app.kubernetes.io/name: mail } } }]"
      bad = 1
    } else if (shape == "") {
      print "  ✗ could not determine how the SMTP egress rule at line " NR " names its destination."
      print "    Expected a podSelector within the preceding few lines."
      bad = 1
    }
  }
  END { exit bad ? 1 : 0 }
' "$file" >&2 || fail=1

# The placeholder must be gone. It is the RFC 5737 documentation address the ipBlock version shipped
# with; if it survives anywhere in this file, some rule still carries it.
if grep -q 'MAIL_RELAY_EIP' "$file" || grep -q '203\.0\.113\.10' "$file"; then
  echo "  ✗ the RFC 5737 mail-relay placeholder (203.0.113.10 / MAIL_RELAY_EIP) is still in the tree." >&2
  fail=1
fi

# And the relay the rule selects must actually be declared, or the selector matches nothing and the
# rule is a no-op that reads as configured.
mailfile="$root/deploy/k8s/overlays/prod/mail.yaml"
if [ ! -f "$mailfile" ]; then
  echo "  ✗ the egress rule selects app.kubernetes.io/name=mail, but overlays/prod/mail.yaml does not exist." >&2
  echo "    A podSelector matching no pod denies exactly as silently as a missing rule." >&2
  fail=1
elif ! grep -q 'kind: Deployment' "$mailfile"; then
  echo "  ✗ overlays/prod/mail.yaml declares no Deployment for the relay the egress rule selects." >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "== check-mail-relay-pinned: FAIL ==" >&2
  exit 1
fi
echo "== check-mail-relay-pinned: PASS — agentd reaches the relay by selector, not by address =="
