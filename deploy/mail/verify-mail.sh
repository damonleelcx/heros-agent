#!/usr/bin/env bash
# Prove this mail host works, in the order the failures actually happen.
#
#   bash verify-mail.sh <domain> [you@example.com]
#
# Run it ON THE MAIL HOST. With a recipient address it also sends one real message and tells you what
# to look for in it.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# 🔴 WHY THIS SCRIPT EXISTS RATHER THAN A CHECKLIST
#
# Every check below is one that FAILS SILENTLY in production. Port 25 does not refuse, it hangs. A
# missing PTR does not error, it changes somebody else's spam score. An unsigned message delivers
# perfectly to the one inbox you tested and gets filed by Gmail next month. An open relay works
# beautifully right up until it is on a blocklist. None of these produce a log line on this box.
#
# ⚠️ AND WHAT IT STILL DOES NOT PROVE. This runs on the MAIL HOST. It proves the relay. It does not
# prove that agentd — in its pod, behind its NetworkPolicy — can reach 587 here. That has already
# been the bug once (see the prod overlay's 587 egress rule) and it is invisible from this side of
# the connection. `make mail-proof TO=…` from a build host does not cover it either. The layer that
# covers it is a real sign-up against the deployed console.
set -uo pipefail

DOMAIN="${1:?usage: verify-mail.sh <domain> [you@example.com]}"
TO="${2:-}"
MAILHOST="mail.${DOMAIN}"
SELECTOR="${DKIM_SELECTOR:-heros}"
STATE="/var/lib/heros-mail"

PASS=0; FAIL=0; WARN=0
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[1;31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
warn() { printf '  \033[1;33m!\033[0m %s\n' "$*"; WARN=$((WARN+1)); }
head_() { printf '\n\033[1m%s\033[0m\n' "$*"; }

MODE="$(postconf -h relayhost 2>/dev/null)"
[ -n "$MODE" ] && MODE="smarthost (${MODE})" || MODE="direct"

printf '\n\033[1mmail host %s — mode: %s\033[0m\n' "$MAILHOST" "$MODE"

# ── 1. Egress ───────────────────────────────────────────────────────────────────────────────────
head_ "1. Egress — can this box hand a message to the internet at all?"
if [ "$MODE" = "direct" ]; then
  # 🔴 THE GATE. Outbound 25 is blocked on every EC2 instance until AWS removes it per-account. The
  # block is a silent drop, so this is a TIMEOUT test, not a connect test — and the timeout is short
  # on purpose: a 5-second wait that reports the truth beats a 75-second one that looks like a hang.
  if timeout 5 nc -z gmail-smtp-in.l.google.com 25 2>/dev/null; then
    ok "outbound TCP 25 is open — AWS has removed the email-sending limitation on this instance"
  else
    bad "outbound TCP 25 is BLOCKED (or filtered). In 'direct' mode NOTHING WILL EVER BE DELIVERED —"
    echo "      Postfix will queue every message for five days and then bounce it, with no error"
    echo "      until then. Either get the limitation removed (README §2) or re-run the bootstrap"
    echo "      with MODE=smarthost, which does not use port 25 at all."
  fi
else
  RH="$(postconf -h relayhost | tr -d '[]' )"; RH_HOST="${RH%%:*}"; RH_PORT="${RH##*:}"
  if timeout 5 nc -z "$RH_HOST" "${RH_PORT:-587}" 2>/dev/null; then
    ok "smarthost ${RH_HOST}:${RH_PORT} is reachable"
  else
    bad "smarthost ${RH_HOST}:${RH_PORT} is NOT reachable — every message will queue"
  fi
fi

# ── 2. Identity ─────────────────────────────────────────────────────────────────────────────────
head_ "2. Identity — does this box look like what it claims to be?"
IP="$(curl -sf -H "X-aws-ec2-metadata-token: $(curl -sf -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')" http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)"
if [ -n "$IP" ]; then
  ok "public IP ${IP}"
  FWD="$(dig +short A "$MAILHOST" | tail -1)"
  if [ "$FWD" = "$IP" ]; then ok "A ${MAILHOST} → ${IP}"
  else bad "A ${MAILHOST} resolves to '${FWD:-nothing}', not ${IP}"; fi

  PTR="$(dig +short -x "$IP" | sed 's/\.$//')"
  if [ "$PTR" = "$MAILHOST" ]; then
    ok "PTR ${IP} → ${MAILHOST} (forward and reverse agree)"
  elif [ -n "$PTR" ]; then
    bad "PTR is '${PTR}', expected '${MAILHOST}' — a mismatch is read as forgery by most filters"
  else
    if [ "$MODE" = "direct" ]; then
      bad "NO PTR record. Gmail and Outlook reject unauthenticated mail from an IP with no reverse"
      echo "      DNS. On EC2 this is set through the same AWS request as port 25 (README §2)."
    else
      warn "no PTR record — harmless in smarthost mode, since the relay's IP is what receivers see"
    fi
  fi
else
  warn "no instance metadata — not on EC2, or IMDS is disabled. Skipping IP/PTR checks."
fi

HELO="$(postconf -h myhostname)"
[ "$HELO" = "$MAILHOST" ] && ok "HELO name is ${HELO}" || bad "HELO name is '${HELO}', expected ${MAILHOST}"

# ── 3. Authentication records ───────────────────────────────────────────────────────────────────
head_ "3. SPF / DKIM / DMARC — published, and matching what this box actually holds"
SPF="$(dig +short TXT "$DOMAIN" | tr -d '"' | grep -m1 '^v=spf1' || true)"
if [ -n "$SPF" ]; then
  ok "SPF: ${SPF}"
  if [ "$MODE" = "direct" ]; then
    if [ -n "${IP:-}" ] && ! printf '%s' "$SPF" | grep -q "$IP"; then
      bad "  …but ${IP} is not in it. This host's own mail fails SPF."
    fi
  else
    # 🔴 THE SMARTHOST SPF TRAP, CHECKED IN BOTH DIRECTIONS. The record that is correct in direct
    # mode is actively wrong here: `ip4:<our EIP>` authorises an address that never connects to
    # anybody, so every message fails SPF while the DNS reads as fully configured. Publishing the
    # relay's `include:` and forgetting to REMOVE the old ip4: is the more common half of it, and it
    # is the half that looks fine — the record is longer, not shorter.
    WANT="$(cat "${STATE}/smarthost.spf" 2>/dev/null || true)"
    if [ -n "$WANT" ]; then
      if printf '%s' "$SPF" | grep -qF "$WANT"; then
        ok "  …and it carries the relay's mechanism (${WANT})"
      else
        bad "  …but it does NOT carry '${WANT}'. Mail relayed through ${RH_HOST:-the smarthost} fails SPF."
      fi
    else
      warn "  …no recorded smarthost SPF mechanism to compare against (${STATE}/smarthost.spf is absent)"
    fi
    if [ -n "${IP:-}" ] && printf '%s' "$SPF" | grep -q "ip4:${IP}"; then
      warn "  …and it still lists ip4:${IP}, this host's own address. In smarthost mode this host"
      echo "        never connects to a receiver, so that mechanism authorises nothing and is stale"
      echo "        from the direct-mode record. Harmless today; misleading the day somebody reads"
      echo "        it as proof the direct path is covered."
    fi
  fi
  printf '%s' "$SPF" | grep -q '\-all' || warn "  …ends in '~all' (softfail). Tighten to '-all' once every sender is listed."
else
  bad "no SPF record on ${DOMAIN}"
fi

DKIM_PUB="$(dig +short TXT "${SELECTOR}._domainkey.${DOMAIN}" | tr -d '"' | tr -d ' ')"
if [ -n "$DKIM_PUB" ]; then
  # Compare the PUBLISHED key against the one this host will actually sign with. A published record
  # that parses but belongs to a key that was regenerated is the failure this catches — every
  # signature fails verification and nothing on this box notices.
  LOCAL_PUB="$(openssl rsa -in "/etc/opendkim/keys/${DOMAIN}/${SELECTOR}.private" -pubout 2>/dev/null \
    | grep -v '^-----' | tr -d '\n')"
  if [ -n "$LOCAL_PUB" ] && printf '%s' "$DKIM_PUB" | grep -q "$(printf '%s' "$LOCAL_PUB" | cut -c1-40)"; then
    ok "DKIM ${SELECTOR}._domainkey published AND matches this host's private key"
  else
    bad "DKIM record is published but does NOT match the key on this host — every signature will fail"
  fi
else
  bad "no DKIM record at ${SELECTOR}._domainkey.${DOMAIN}"
fi

DMARC="$(dig +short TXT "_dmarc.${DOMAIN}" | tr -d '"' | grep -m1 '^v=DMARC1' || true)"
if [ -n "$DMARC" ]; then
  ok "DMARC: ${DMARC}"
  if [ "$MODE" != "direct" ] && printf '%s' "$DMARC" | grep -qE 'p=(quarantine|reject)'; then
    # In smarthost mode the relay rewrites the envelope sender, so SPF authenticates ITS domain and
    # aligns with nothing of ours. DMARC then passes on DKIM alignment alone — which is fine while
    # the milter is up, and is a rejection the moment it is not, once the policy has teeth.
    warn "  …policy is past p=none while SPF cannot align (the relay rewrites the envelope sender)."
    echo "        DKIM alignment is the ONLY thing passing DMARC here. Confirm the milter check below"
    echo "        is green before trusting this policy — an unsigned message is now a REJECTED one."
  fi
else
  bad "no DMARC record at _dmarc.${DOMAIN}"
fi

MX="$(dig +short MX "$DOMAIN" | head -1)"
[ -n "$MX" ] && ok "MX: ${MX}" || warn "no MX record — bounces and DMARC reports have nowhere to land"

# ── 4. Services ─────────────────────────────────────────────────────────────────────────────────
head_ "4. Services"
for svc in postfix opendkim dovecot; do
  systemctl is-active --quiet "$svc" && ok "${svc} is running" || bad "${svc} is NOT running"
done
if timeout 5 nc -z localhost 8891 2>/dev/null; then
  ok "OpenDKIM milter is listening on 8891"
else
  bad "nothing on 8891 — Postfix is configured to sign and CANNOT. milter_default_action=accept means"
  echo "      mail still goes out, UNSIGNED, and delivers today. That is the point of checking here."
fi

CERT="/etc/letsencrypt/live/${MAILHOST}/fullchain.pem"
if [ -s "$CERT" ]; then
  END="$(openssl x509 -enddate -noout -in "$CERT" | cut -d= -f2)"
  if openssl x509 -checkend 604800 -noout -in "$CERT" >/dev/null; then
    ok "certificate valid until ${END}"
  else
    bad "certificate expires within 7 days (${END}) — renewal is not working"
  fi
else
  bad "no certificate at ${CERT}"
fi

# ── 5. Submission, and the refusals ─────────────────────────────────────────────────────────────
head_ "5. Submission on 587 — and the things it must REFUSE"
SUBMIT_USER="agentd@${DOMAIN}"
SUBMIT_PASS="$(cat "${STATE}/submission.password" 2>/dev/null || true)"

if [ -z "$SUBMIT_PASS" ]; then
  warn "no submission password at ${STATE}/submission.password — skipping the authenticated checks"
else
  if swaks --to "postmaster@${DOMAIN}" --from "support@${DOMAIN}" \
      --server "${MAILHOST}:587" --tls \
      --auth-user "$SUBMIT_USER" --auth-password "$SUBMIT_PASS" \
      --quit-after AUTH >/dev/null 2>&1; then
    ok "STARTTLS + AUTH on 587 accepted for ${SUBMIT_USER}"
  else
    bad "STARTTLS + AUTH on 587 FAILED for ${SUBMIT_USER} — agentd cannot send"
  fi

  # 🔴 An authenticated user must not be able to send as anybody. If this SUCCEEDS, a leaked agentd
  # credential can send DKIM-signed mail as postmaster@ or as any employee at this domain.
  if swaks --to "postmaster@${DOMAIN}" --from "ceo@${DOMAIN}" \
      --server "${MAILHOST}:587" --tls \
      --auth-user "$SUBMIT_USER" --auth-password "$SUBMIT_PASS" \
      --quit-after RCPT >/dev/null 2>&1; then
    bad "SENDER SPOOFING IS POSSIBLE — ${SUBMIT_USER} was allowed to send as ceo@${DOMAIN}."
    echo "      smtpd_sender_login_maps / reject_sender_login_mismatch are not in effect."
  else
    ok "sender-login mismatch refused (${SUBMIT_USER} cannot send as an address it does not own)"
  fi
fi

# 🔴 Open-relay test: unauthenticated, to a domain we do not serve. If this SUCCEEDS the box is
# relaying spam within hours of the first scan and blocklisted by the end of the day.
#
# 🔴 TWO TRAPS, BOTH OF WHICH MAKE THIS CHECK LIE IF IGNORED.
#
#   1. It must NOT go to localhost. 127.0.0.0/8 is in `mynetworks` — by design, that is how a local
#      process submits — so a loopback test is PERMITTED to relay and would report a healthy server
#      as an open relay. A false alarm here gets the check deleted, which costs the real one too.
#   2. `swaks` failing is not the same as the server refusing. A connection that never opens exits
#      non-zero exactly like a rejected RCPT, so the naive `if swaks …; else ok "refuses"` reports
#      GREEN for a box it never reached — the check most likely to be wrong when it matters, since
#      what it is testing is reachable-from-outside behaviour.
#
# So: connect over the EXTERNAL name, and read the output rather than the exit status.
RELAY_OUT="$(swaks --to "relay-test@example.com" --from "probe@invalid-probe.test" \
  --server "${MAILHOST}:25" --timeout 10 --quit-after RCPT 2>&1)"
if printf '%s' "$RELAY_OUT" | grep -qiE 'Relay access denied|Recipient address rejected|^<\*\* 5[0-9][0-9]'; then
  ok "port 25 refuses to relay for an unauthenticated sender"
elif printf '%s' "$RELAY_OUT" | grep -qiE '250 .*(Ok|Accepted)[[:space:]]*$'; then
  bad "OPEN RELAY on port 25 — this box accepts mail for domains it does not serve, without auth."
  echo "      Stop it now: systemctl stop postfix. smtpd_relay_restrictions is wrong."
else
  warn "could not complete the open-relay test against ${MAILHOST}:25 — THIS IS NOT A PASS."
  echo "      Usually the instance cannot reach its own Elastic IP (hairpin), or inbound 25 is closed"
  echo "      in the security group. Re-run it from OFF the box:"
  echo "        swaks --to relay-test@example.com --from probe@invalid-probe.test --server ${MAILHOST}:25 --quit-after RCPT"
  echo "      Expect '554 Relay access denied'. Anything starting 250 is an open relay."
fi

# ── 6. One real message ─────────────────────────────────────────────────────────────────────────
if [ -n "$TO" ]; then
  head_ "6. A real message to ${TO}"
  if [ -z "${SUBMIT_PASS:-}" ]; then
    warn "no submission password — cannot send"
  elif swaks --to "$TO" --from "support@${DOMAIN}" \
      --server "${MAILHOST}:587" --tls \
      --auth-user "$SUBMIT_USER" --auth-password "$SUBMIT_PASS" \
      --h-Subject "heros-agent mail host verification" \
      --body "Sent by verify-mail.sh from ${MAILHOST}. If you are reading this in a spam folder, that is a result, not a pass." \
      >/dev/null 2>&1; then
    ok "accepted for delivery by the relay"
    echo
    echo "      ⚠️ ACCEPTED IS NOT DELIVERED. Open the message and check its headers:"
    echo "          Authentication-Results: … spf=pass … dkim=pass … dmarc=pass"
    echo "        Three passes and an INBOX — not a spam folder — is the actual result. Anything"
    echo "        less and the link a customer is waiting on is going somewhere they will not look."
    echo
    echo "      Queue right now:"
    mailq | tail -5
  else
    bad "the relay refused the message"
  fi
fi

printf '\n\033[1m%d passed, %d failed, %d warnings\033[0m\n\n' "$PASS" "$FAIL" "$WARN"
[ "$FAIL" -eq 0 ]
