#!/usr/bin/env bash
# Print the exact DNS records this mail host needs, with its real values filled in.
#
# Run it ON THE MAIL HOST, after bootstrap-mailserver.sh — the DKIM record is generated from the
# private key that lives here, so a record copied from anywhere else is a record that will not verify.
#
#   bash dns-records.sh <domain> [public-ip]
#
# The IP is read from instance metadata when omitted.
#
# 🔴 These four records are not "best practice" in the optional sense. A message from an unknown IP
# with no SPF, no DKIM and no DMARC is delivered to the spam folder by Gmail and refused outright by
# several corporate filters. For a product whose only mail is a password-reset link somebody is
# waiting on, the spam folder and /dev/null are the same outcome.
set -euo pipefail

DOMAIN="${1:?usage: dns-records.sh <domain> [public-ip]}"
MAILHOST="mail.${DOMAIN}"
SELECTOR="${DKIM_SELECTOR:-heros}"
KEYFILE="/etc/opendkim/keys/${DOMAIN}/${SELECTOR}.txt"

IP="${2:-}"
if [ -z "$IP" ]; then
  # IMDSv2. A plain GET against IMDSv1 returns 401 on any instance with the metadata options
  # hardened, and the error looks like "no network" rather than "wrong protocol version".
  TOKEN="$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 60" || true)"
  IP="$(curl -sf -H "X-aws-ec2-metadata-token: ${TOKEN}" \
    http://169.254.169.254/latest/meta-data/public-ipv4 || true)"
fi
[ -n "$IP" ] || { echo "could not determine the public IP — pass it as the second argument" >&2; exit 2; }

if [ ! -s "$KEYFILE" ]; then
  echo "no DKIM record at ${KEYFILE} — run bootstrap-mailserver.sh on this host first" >&2
  exit 2
fi

# 🔴 THE MODE IS READ FROM THE LIVE POSTFIX CONFIGURATION, never passed in. The SPF record below is
# the one value that differs between the two modes, and it differs in the direction that fails
# silently: an `ip4:` record naming our Elastic IP is correct in direct mode and authorises an
# address that never sends in smarthost mode, so every message fails SPF while the DNS looks
# complete. Deriving it from `relayhost` means this script cannot disagree with the box it runs on —
# which a flag, re-typed on a later day by somebody who did not set the mode, absolutely can.
RELAYHOST="$(postconf -h relayhost 2>/dev/null | tr -d ' ')"
if [ -n "$RELAYHOST" ]; then
  MODE="smarthost"
  SMARTHOST_SPF="$(cat /var/lib/heros-mail/smarthost.spf 2>/dev/null || true)"
  if [ -z "$SMARTHOST_SPF" ]; then
    echo "this host relays through ${RELAYHOST} but no SPF mechanism was recorded for it." >&2
    echo "re-run the bootstrap with SMARTHOST_SPF set, or there is no correct SPF record to print." >&2
    exit 2
  fi
  SPF_RECORD="v=spf1 ${SMARTHOST_SPF} -all"
  SPF_NOTE="the relay's own mechanism, because the connecting IP is THEIRS. \`ip4:${IP}\` here would
     authorise an address that never sends a thing and fail SPF on every message."
  # 🔴 RELAXED SPF ALIGNMENT, and this is not a loosening — it is the difference between DMARC
  # passing on two mechanisms and passing on one. Relays bounce-handle by rewriting the envelope
  # sender to a SUBDOMAIN of ours (Resend uses \`send.<domain>\`, which is what its required MX and
  # SPF records sit on). SPF is evaluated against that envelope domain, so under \`aspf=s\` —
  # STRICT, requiring an exact match with the From domain — \`send.heros-agent.space\` does not
  # align with \`heros-agent.space\` and SPF contributes NOTHING to DMARC. Under \`aspf=r\` the two
  # share an organizational domain and it aligns. Strict alignment here does not buy security; it
  # discards the only SPF result available and leaves DKIM carrying DMARC alone.
  DMARC_ALIGN="adkim=s; aspf=r"
  DMARC_NOTE="\`aspf=r\` (relaxed) because the relay rewrites the envelope sender to a subdomain of
     ${DOMAIN}. Under \`aspf=s\` that would not align and SPF would contribute nothing."
else
  MODE="direct"
  SPF_RECORD="v=spf1 ip4:${IP} -all"
  SPF_NOTE="this host's Elastic IP, because in direct mode it is the connecting address."
  # Strict is correct here: this host sets the envelope sender itself, to the From domain exactly.
  DMARC_ALIGN="adkim=s; aspf=s"
  DMARC_NOTE="\`aspf=s\` (strict) because in direct mode this host sets the envelope sender itself,
     to ${DOMAIN} exactly. Nothing rewrites it, so there is no reason to accept a subdomain."
fi

# opendkim-genkey writes the record split across quoted strings for BIND. Most DNS consoles want one
# unbroken value, so join it. (Route 53 in particular accepts >255-char values only when the console
# itself splits them, which it does — pasting the BIND form with its embedded quotes stores the quotes
# as data and the record silently fails to verify.)
#
# 🔴 THE FINAL `tr -d` MUST INCLUDE `\n`, and this is not defensive tidying. `grep -o` prints each
# match on its own line, so the two quoted chunks (`v=DKIM1; h=sha256; k=rsa; ` and `p=MIIBIjAN…`)
# come back separated by a newline that survives every later filter. Without it this prints a record
# broken across two lines — and the first line alone, `v=DKIM1;h=sha256;k=rsa;`, is a syntactically
# VALID DKIM record with no key in it. Paste that and every signature fails verification while the
# DNS console shows a record that looks right. Verified by checking the emitted value is one line
# and contains `p=`.
DKIM_VALUE="$(sed -e 's/^.*(//' -e 's/).*$//' "$KEYFILE" \
  | tr -d '\n\t' | grep -o '"[^"]*"' | tr -d '"' | tr -d ' \n')"
case "$DKIM_VALUE" in
  *p=*) ;;
  *) echo "refusing to print a DKIM record with no p= key: parsed '${DKIM_VALUE}' from ${KEYFILE}" >&2
     exit 2 ;;
esac

cat <<EOF

  DNS records for ${DOMAIN}        —  this host is in ${MODE} mode
  ─────────────────────────────────────────────────────────────────────────────────────────────

  1. A     ${MAILHOST}
           ${IP}

     Must exist BEFORE bootstrap-mailserver.sh can get a certificate. In DIRECT mode its name must
     also match the PTR record exactly — a forward/reverse mismatch is read as forgery by most
     filters. In SMARTHOST mode no receiver ever sees this address, so PTR does not apply.

  2. MX    ${DOMAIN}
           10 ${MAILHOST}

     Not optional even for a send-only product, and in smarthost mode it is the record that stays
     OURS: the relay carries mail out, this host takes mail in. Without it, bounces have nowhere to
     return, the DMARC reports requested in §5 are undeliverable, and a reply to support@ vanishes.
     That last one is a gap this deployment has carried and this record closes.

  3. TXT   ${DOMAIN}
           ${SPF_RECORD}

     ${SPF_NOTE}

     🔴 \`-all\`, not \`~all\`. Softfail asks receivers to accept forgeries and mark them; hardfail asks
     them to refuse. Use \`~all\` ONLY while another sender is still live for this domain — if SES,
     a marketing tool or a helpdesk also sends as ${DOMAIN}, publishing \`-all\` without listing them
     stops their mail dead. List every one of them, then tighten.

  4. TXT   ${SELECTOR}._domainkey.${DOMAIN}
           ${DKIM_VALUE}

     ⚠️ One unbroken value, no surrounding quotes. This is generated from the private key on THIS
     host; regenerating the key without republishing this breaks every signature.

     🔴 IN SMARTHOST MODE THIS RECORD IS LOAD-BEARING IN A WAY IT IS NOT IN DIRECT MODE. Most relays
     rewrite the envelope sender to their own bounce domain, so SPF authenticates THEIR domain and
     is not aligned with ${DOMAIN} under either \`aspf=s\` or \`aspf=r\`. DMARC passes if EITHER SPF or
     DKIM aligns — so here it passes on DKIM alone, and this record is the whole of it. A broken
     OpenDKIM is a degradation in direct mode and a DMARC FAILURE in this one.

     ⚠️ THIS DOES NOT REPLACE THE RELAY'S OWN RECORDS, and an earlier draft of this script said to
     decline them. That was wrong: a relay will not send as ${DOMAIN} until you have published the
     records ITS dashboard generates, which is how it proves you own the domain. Publish both. They
     use different selectors and do not collide, the message carries two signatures, and DMARC
     passes on whichever aligns. Ours is the one that keeps working the day you change relays.

  5. TXT   _dmarc.${DOMAIN}
           v=DMARC1; p=none; rua=mailto:dmarc@${DOMAIN}; ruf=mailto:dmarc@${DOMAIN}; fo=1; ${DMARC_ALIGN}

     ${DMARC_NOTE}

     🔴 Start at \`p=none\` and MEAN it. p=none asks receivers to report, not to act — it is how you
     find the sender you forgot before that sender's mail starts being rejected. Read the reports in
     dmarc@ for two weeks, then move to \`p=quarantine\` and later \`p=reject\`. Publishing p=reject on
     day one is how a company discovers its invoicing system was sending as the domain, by having it
     stop.

  ─────────────────────────────────────────────────────────────────────────────────────────────
EOF

if [ "$MODE" = "smarthost" ]; then
cat <<EOF
  6. THE RELAY'S OWN RECORDS — from its dashboard, NOT from this script.

     🔴 THE FIVE ABOVE ARE NOT SUFFICIENT AND THIS IS THE STEP THAT LOOKS DONE WHEN IT IS NOT. A
     relay will not send as ${DOMAIN} until you publish the records it generates for you, because
     that is how it proves you own the domain. Until you do, this host authenticates, hands the
     message over successfully, and the relay drops it — visible in \`mailq\` and nowhere else.

     For Resend (${RELAYHOST}) that is three records, all on a \`send\` subdomain it uses as the
     bounce/envelope domain, plus its own DKIM:

       MX    send.${DOMAIN}              feedback-smtp.<region>.amazonses.com   (priority 10)
       TXT   send.${DOMAIN}              v=spf1 include:amazonses.com ~all
       TXT   resend._domainkey.${DOMAIN} <the value its dashboard shows>

     ⚠️ Copy the exact values from the dashboard — the region in the MX host and the DKIM key are
     account-specific, and the ones written here are the SHAPE, not your values.

     ⚠️ \`send.${DOMAIN}\` does NOT collide with the MX in §2. That one is on the bare domain and is
     how mail reaches this host; this one is on a subdomain and is how the relay handles bounces.
     Different names, both needed, neither replaces the other.

     🔴 AND NOTE WHAT \`include:amazonses.com\` MEANS: Resend runs on Amazon SES. This deployment
     still delivers through SES infrastructure — under RESEND's account, which has production
     access, rather than this one, which was refused it. That is a legitimate way through and it is
     worth knowing rather than rediscovering: a future SES-wide problem is still your problem.

  ─────────────────────────────────────────────────────────────────────────────────────────────
  🔴 NO PTR RECORD IS NEEDED. Receivers see the relay's address, not ${IP}, so rDNS on this host
  authenticates nothing. That is the AWS Support case you do not have to wait on.

EOF
else
cat <<EOF
  Then: PTR for ${IP} → ${MAILHOST}, which on EC2 is requested through AWS, not set in DNS. §2 of
  README.md. Nothing above compensates for its absence.

EOF
fi
