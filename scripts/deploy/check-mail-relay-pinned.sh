#!/usr/bin/env bash
# CI gate: the mail relay's SMTP submission rule names its peers by SELECTOR, never by address —
# and it EXISTS.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# 🔴 WHAT THIS GATE IS FOR
#
# Everything either side of the relay runs under default-deny. If the submission rule is missing or
# wrong, every confirmation link, password reset and invitation is dropped by the NetworkPolicy —
# with no event, no log line and no metric. A sender's own readiness stays green, because "mail is
# configured" describes configuration and not reachability. The symptom that reaches a human is a
# customer saying the email never arrived, weeks later.
#
# This deployment has been through THREE shapes of that rule, and two were wrong in ways nothing
# caught:
#
#   1. `ipBlock: 0.0.0.0/0` on 587 — correct for SES, which publishes no stable address, but it gave
#      the only workload with external egress a path to any host on the internet on that port.
#   2. `ipBlock: <elastic ip>/32` — narrower, and it named an address on a dedicated mail host that
#      was subsequently terminated and RELEASED. An egress rule pointing at a released public
#      address is worse than a wide one: it points at whatever AWS hands the next customer.
#   3. A podSelector — the relay is a pod in this cluster, so no address is involved at all.
#
# An `ipBlock` pinned to the NODE'S OWN public address would also pass any test of "can this thing
# send mail" — by hairpinning out through the internet gateway and back to a pod one virtual hop
# away. That version works, and is the one this gate exists to reject: it is indistinguishable from
# correct at runtime, and it puts a credential-bearing connection on the public internet.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# ⚠️ WHAT CHANGED, AND WHY THIS GATE MOVED RATHER THAN BEING DELETED
#
# It used to read the SENDER'S egress rule in overlays/prod/kustomization.yaml, because the sender —
# agentd — was deployed from this repository. It no longer is: the platform was removed from this
# deployment, and the things that send mail now live in their own repositories, each carrying its own
# egress rule.
#
# So the gate now reads the half of the connection this repository still owns: the RELAY'S INGRESS in
# overlays/prod/mail.yaml. The reasoning above is unchanged — selector, never address — and it
# applies to an ingress `from:` for exactly the reasons it applied to an egress `to:`.
#
# 🔴 Deleting it instead would have been the wrong move even though its original subject is gone. A
# gate removed because the thing it watched moved is a gate that stops watching the thing.
#
# Written for bash 3.2 (the macOS system bash).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
file="$root/deploy/k8s/overlays/prod/mail.yaml"
fail=0

[ -f "$file" ] || { echo "check-mail-relay-pinned: $file not found" >&2; exit 1; }

# 🔴 SCOPED TO THE NetworkPolicy INGRESS, and the scoping is load-bearing in both directions.
#
# A bare grep for `port: 587` in this file matches two things that must NOT be judged by this rule:
#
#   - the Service port definition, which is a port number and names no peer at all; and
#   - the relay's own EGRESS to the upstream smart host, which is CORRECTLY an ipBlock. That peer is
#     smtp.resend.com, outside the cluster, so there is no selector that could describe it. A gate
#     that demanded a selector there would be demanding something impossible, and the way that gets
#     resolved at 2am is by deleting the gate.
#
# Both were flagged by the first version of this check. The rule is about how the relay names peers
# INSIDE the cluster; that is the ingress, and only the ingress.
#
# 🔴 AND THE INGRESS RULE MUST EXIST. An earlier version asserted only "if there is a 587 rule, it
# must look like X" — so a tree with NO 587 rule satisfied every assertion and the gate reported
# PASS. No rule and a wrong rule produce the identical silent timeout. An empty set passing every
# test about its members is not evidence of anything.
awk '
  /^---/                                { doc = ""; sect = ""; next }
  /^kind: NetworkPolicy/                { doc = "np"; next }
  doc == "np" && /^  ingress:/          { sect = "ingress"; next }
  doc == "np" && /^  egress:/           { sect = "egress";  next }
  { line[NR] = $0 }
  doc == "np" && sect == "ingress" && /port: 587/ {
    found = 1
    shape = ""
    for (i = NR; i >= NR - 8 && i >= 1; i--) {
      if (line[i] ~ /ipBlock/)     { shape = "ipBlock"; break }
      if (line[i] ~ /podSelector/) { shape = "podSelector"; break }
    }
    if (shape == "ipBlock") {
      # NOTE: no apostrophes anywhere inside this awk program. It is single-quoted, so a lone
      # apostrophe closes it and the shell then fails to parse the REST OF THE FILE, reporting a
      # syntax error on a later line that has nothing wrong with it.
      print "  x the SMTP submission INGRESS rule (line " NR ") is an ipBlock, not a selector."
      print "    Every sender is a workload in this cluster. An address here is either stale, or the"
      print "    public IP of the node itself - which works by hairpinning out through the internet"
      print "    gateway and back, and is indistinguishable from correct at runtime."
      bad = 1
    } else if (shape == "") {
      print "  x could not determine how the SMTP ingress rule at line " NR " names its peer."
      print "    Expected a podSelector within the preceding few lines."
      bad = 1
    }
  }
  END {
    if (!found) {
      print "  x the relay NetworkPolicy declares NO ingress rule for SMTP submission (587)."
      print "    The relay runs under default-deny. With no ingress rule every message any tenant"
      print "    tries to send is refused - silently, with the sender healthy and nothing in any log."
      bad = 1
    }
    exit bad ? 1 : 0
  }
' "$file" >&2 || fail=1

# 🔴 namespaceSelector and podSelector must be ANDed, which means living in ONE list item: the
# namespaceSelector carries the leading dash and the podSelector does not. Written as two dashed
# items they are ORed - admitting every pod in that namespace AND every pod anywhere carrying the
# label. The two forms render identically in `kubectl get`, the wrong one is strictly wider than
# intended, and this codebase documents having got it wrong before.
awk '
  /^[[:space:]]*-[[:space:]]*namespaceSelector/ { pending = NR; next }
  /^[[:space:]]*-[[:space:]]*podSelector/ {
    if (pending == NR - 1) {
      print "  x line " NR ": podSelector is its own list item directly after a namespaceSelector."
      print "    Two items are ORed, not ANDed. Drop the dash so both live in one item."
      bad = 1
    }
    pending = 0
    next
  }
  { pending = 0 }
  END { exit bad ? 1 : 0 }
' "$file" >&2 || fail=1

# The placeholder must be gone. It is the RFC 5737 documentation address the ipBlock version shipped
# with; if it survives anywhere in this file, some rule still carries it.
if grep -q 'MAIL_RELAY_EIP' "$file" || grep -q '203\.0\.113\.10' "$file"; then
  echo "  ✗ the RFC 5737 mail-relay placeholder (203.0.113.10 / MAIL_RELAY_EIP) is still in the tree." >&2
  fail=1
fi

# And the relay the rules select must actually be declared, or the selectors match nothing and the
# rules are no-ops that read as configured.
if ! grep -q 'kind: Deployment' "$file"; then
  echo "  ✗ overlays/prod/mail.yaml declares no Deployment for the relay these rules govern." >&2
  echo "    A selector matching no pod denies exactly as silently as a missing rule." >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "== check-mail-relay-pinned: FAIL ==" >&2
  exit 1
fi
echo "== check-mail-relay-pinned: PASS — the relay names its peers by selector, not by address =="
