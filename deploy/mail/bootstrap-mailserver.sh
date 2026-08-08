#!/usr/bin/env bash
# Stand up this platform's own SMTP relay on ONE fresh Ubuntu 24.04 host.
#
# What it builds:
#   Postfix    — submission on 587 (STARTTLS + SASL, the only thing agentd talks to), and inbound 25
#                for the domain so bounces, DMARC reports and a reply to support@ have somewhere to land.
#   OpenDKIM   — signs everything leaving on 587. Without it Gmail files us under "unauthenticated".
#   Dovecot    — SASL for Postfix, LMTP delivery, and IMAPS so the mailboxes are readable by a person.
#   certbot    — a real certificate for mail.<domain>, auto-renewing, reloading both daemons.
#
# Usage (as root on the mail host):
#   bash bootstrap-mailserver.sh <domain> [smarthost|direct]
#
# Example — smarthost, which is the DEFAULT and this deployment's chosen mode:
#   SMARTHOST_HOST=… SMARTHOST_USER=… SMARTHOST_PASS=… SMARTHOST_SPF=… \
#     bash bootstrap-mailserver.sh heros-agent.space smarthost
#
# Re-running is idempotent. It never regenerates the DKIM key or the submission password once they
# exist — rotating either under a live deployment breaks signing or breaks agentd, silently in both
# cases, and a re-run is the thing you do when something is already broken.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# 🔴 THE TWO MODES, AND WHY THIS SCRIPT HAS A SWITCH AT ALL
# ─────────────────────────────────────────────────────────────────────────────────────────────────
#
# `direct`    Postfix delivers to each recipient's MX itself. This is the point of self-hosting and it
#             requires AWS to have removed the outbound port-25 block on this instance — a Support
#             request, SEPARATE from SES production access, granted per-account and tied to the
#             Elastic IP. Until it is granted, every outbound connection to a real MX hangs and times
#             out. It does not refuse; it HANGS, and Postfix then queues quietly for five days, which
#             is why `verify-mail.sh` tests port 25 egress first and treats it as a hard gate.
#
# `smarthost` ← THE DEFAULT, and this deployment's choice. Postfix hands every message to a
#             third-party relay on 587 with its own credential. The port-25 block is irrelevant
#             because nothing here ever opens 25 outbound, and the relay's IP pool is already warm,
#             so none of the reputation cold-start applies.
#
# ⚠️ Everything agentd sees is IDENTICAL in both modes: same host, same port, same credential, same
# From. The mode is a property of this box's egress and nothing above it knows or cares. That is
# deliberate — the AWS decision that would otherwise block this whole build becomes one variable on
# one host, changeable after the fact with `MODE=direct bash bootstrap-mailserver.sh <domain>`.
#
# DKIM signing happens HERE in both modes, so the domain's authentication story does not change when
# the mode does. A smarthost that re-signs with its own domain is a smarthost you have to trust with
# your alignment; signing before handoff means DMARC passes on OUR key either way.
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# 🔴 THE ONE THING SMARTHOST MODE CHANGES ABOVE THIS BOX: SPF, AND HOW DMARC PASSES
# ─────────────────────────────────────────────────────────────────────────────────────────────────
#
# In `direct` mode our Elastic IP is the sending IP, so `v=spf1 ip4:<eip> -all` authorises it and SPF
# aligns with the From domain. **In smarthost mode neither of those is true.** The connecting IP
# belongs to the relay, so an `ip4:` record listing our EIP authorises an address that never sends —
# every message fails SPF. And most relays REWRITE the envelope sender to their own bounce domain,
# so even with `include:` published, SPF authenticates THEIR domain and is not aligned with ours.
#
# What that means in practice, and it is not a problem so long as it is known:
#
#   - SPF must publish the relay's `include:`, NOT our IP. `dns-records.sh` reads the live Postfix
#     configuration and emits whichever is correct, so it cannot get out of step with this switch.
#   - 🔴 DMARC then passes on **DKIM alignment alone**. Our own signature, applied here before
#     handoff, is the only thing holding it up. In `direct` mode a broken OpenDKIM is a degradation;
#     in smarthost mode it is a DMARC FAILURE — which, once the policy moves past `p=none`, is a
#     rejection. `milter_default_action = accept` (below) therefore trades away more here than it
#     does in direct mode, and `verify-mail.sh` checks the milter explicitly for that reason.
set -euo pipefail

DOMAIN="${1:?usage: bootstrap-mailserver.sh <domain> [smarthost|direct]}"
MODE="${2:-${MODE:-smarthost}}"
MAILHOST="mail.${DOMAIN}"
SELECTOR="${DKIM_SELECTOR:-heros}"
STATE="/var/lib/heros-mail"
CERT_DIR="/etc/letsencrypt/live/${MAILHOST}"

case "$MODE" in
  direct|smarthost) ;;
  *) echo "mode must be 'direct' or 'smarthost', got '${MODE}'" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 2; }

log() { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
mkdir -p "$STATE"
chmod 700 "$STATE"

# keep_secret FILE GENERATOR — writes GENERATOR's output to FILE only when FILE is absent.
#
# 🔴 The whole reason this is a function and not an inline `openssl rand`: a re-run that rotates the
# submission password leaves agentd holding a credential the relay no longer accepts. agentd does not
# crash on that — `mailer.New` succeeds, `Configured()` answers true, and every send fails at AUTH with
# the platform reporting itself healthy. A silent rotation is worse than no re-run at all.
keep_secret() {
  local file="$1"; shift
  if [ ! -s "$file" ]; then
    ( umask 077; "$@" > "$file" )
  fi
  cat "$file"
}

# ── 1. Identity of the host ─────────────────────────────────────────────────────────────────────
#
# Postfix derives myhostname, HELO and the Message-ID domain from this. A box still calling itself
# `ip-10-0-1-42` HELOs as that name, and a HELO that does not resolve to the connecting IP is one of
# the cheapest spam signals there is — several large receivers reject on it alone.
log "Hostname → ${MAILHOST}"
hostnamectl set-hostname "$MAILHOST"
printf '%s\n' "$MAILHOST" > /etc/mailname
grep -q "$MAILHOST" /etc/hosts || printf '127.0.1.1 %s %s\n' "$MAILHOST" "mail" >> /etc/hosts

# ── 2. Packages ─────────────────────────────────────────────────────────────────────────────────
#
# The debconf preseed matters: without it the Postfix package opens an interactive dialog and a
# cloud-init run blocks forever on a prompt nobody is watching.
log "Installing packages"
export DEBIAN_FRONTEND=noninteractive
debconf-set-selections <<EOF
postfix postfix/main_mailer_type select Internet Site
postfix postfix/mailname string ${MAILHOST}
EOF
apt-get update -qq
apt-get install -y -qq \
  postfix postfix-pcre opendkim opendkim-tools \
  dovecot-core dovecot-imapd dovecot-lmtpd \
  certbot ca-certificates dnsutils netcat-openbsd swaks

# ── 3. Certificate ──────────────────────────────────────────────────────────────────────────────
#
# ⚠️ PRECONDITION A SCRIPT CANNOT SATISFY: an A record for ${MAILHOST} must already point at this
# host's Elastic IP, and port 80 must be reachable from the internet. certbot's standalone challenge
# is an HTTP request Let's Encrypt makes TO this box; there is no other way for it to succeed, and its
# failure here is the failure you want — a self-signed fallback would make `smtpd_tls_security_level
# = encrypt` succeed against a certificate no client should have accepted.
if [ ! -d "$CERT_DIR" ]; then
  log "Obtaining certificate for ${MAILHOST}"
  systemctl stop postfix 2>/dev/null || true
  certbot certonly --standalone --non-interactive --agree-tos \
    --preferred-challenges http \
    -d "$MAILHOST" \
    -m "postmaster@${DOMAIN}"
else
  log "Certificate for ${MAILHOST} already present — leaving it alone"
fi

# Renewal must reload the daemons. certbot renews at 03:00 and by default tells nobody; Postfix goes
# on presenting the expired certificate until somebody restarts it for an unrelated reason.
mkdir -p /etc/letsencrypt/renewal-hooks/deploy
cat > /etc/letsencrypt/renewal-hooks/deploy/reload-mail.sh <<'EOF'
#!/bin/sh
systemctl reload postfix  2>/dev/null || true
systemctl reload dovecot  2>/dev/null || true
EOF
chmod +x /etc/letsencrypt/renewal-hooks/deploy/reload-mail.sh

# ── 4. DKIM ─────────────────────────────────────────────────────────────────────────────────────
log "OpenDKIM (selector ${SELECTOR})"
KEYDIR="/etc/opendkim/keys/${DOMAIN}"
mkdir -p "$KEYDIR"
if [ ! -s "${KEYDIR}/${SELECTOR}.private" ]; then
  opendkim-genkey -b 2048 -d "$DOMAIN" -D "$KEYDIR" -s "$SELECTOR" -v
  chown -R opendkim:opendkim /etc/opendkim/keys
  chmod 600 "${KEYDIR}/${SELECTOR}.private"
else
  log "DKIM key already exists — NOT regenerating (the published TXT record matches the old one)"
fi

cat > /etc/opendkim.conf <<EOF
Syslog                  yes
SyslogSuccess           yes
Canonicalization        relaxed/simple
Mode                    sv
SubDomains              no
OversignHeaders         From
UserID                  opendkim
# 🔴 inet on loopback, never a unix socket under /var/spool/postfix here: Postfix runs chrooted in
# Debian's default master.cf and a chrooted smtpd cannot see a socket outside its root. The failure is
# "Milter: connect failed" on every message — that is loud, but the reflex fix is to disable the
# milter, which turns it into unsigned mail that delivers fine today and lands in spam next month.
Socket                  inet:8891@localhost
PidFile                 /run/opendkim/opendkim.pid
KeyTable                /etc/opendkim/KeyTable
SigningTable            refile:/etc/opendkim/SigningTable
ExternalIgnoreList      /etc/opendkim/TrustedHosts
InternalHosts           /etc/opendkim/TrustedHosts
EOF

cat > /etc/opendkim/KeyTable <<EOF
${SELECTOR}._domainkey.${DOMAIN} ${DOMAIN}:${SELECTOR}:${KEYDIR}/${SELECTOR}.private
EOF
cat > /etc/opendkim/SigningTable <<EOF
*@${DOMAIN} ${SELECTOR}._domainkey.${DOMAIN}
EOF
# 🔴 Loopback ONLY. TrustedHosts is the list of senders OpenDKIM will sign FOR, and anything in it is
# effectively permitted to have our domain's signature applied to its mail. The subnet is not trusted:
# on a VPC that is every instance in it.
cat > /etc/opendkim/TrustedHosts <<EOF
127.0.0.1
localhost
${MAILHOST}
EOF
chown -R opendkim:opendkim /etc/opendkim
mkdir -p /run/opendkim && chown opendkim:opendkim /run/opendkim

# ── 5. Mailboxes and the submission credential ──────────────────────────────────────────────────
#
# Two DIFFERENT kinds of account, and conflating them is the mistake this section is arranged to
# prevent:
#
#   agentd@   authenticates on 587 and has NO mailbox. It is the platform's credential.
#   the rest  have mailboxes and exist so that mail ARRIVING has somewhere to go.
#
# A single account doing both means the credential agentd carries can also read the domain's mail.
log "Accounts"
SUBMIT_USER="agentd@${DOMAIN}"
SUBMIT_PASS="$(keep_secret "${STATE}/submission.password" openssl rand -base64 30)"

# Mailboxes that must exist. postmaster and abuse are not optional decoration — RFC 2142 requires
# them, several receivers probe them, and dmarc@ is where the aggregate reports this domain asks for
# in its own DMARC record are delivered. Publishing rua= at a mailbox that does not exist generates a
# bounce to every reporting provider on the internet, daily.
MAILBOXES="postmaster abuse dmarc support"

install -d -m 0770 -o vmail -g vmail /var/mail/vhosts 2>/dev/null || {
  groupadd -g 5000 vmail 2>/dev/null || true
  useradd -g vmail -u 5000 vmail -d /var/mail/vhosts -m -s /usr/sbin/nologin 2>/dev/null || true
  install -d -m 0770 -o vmail -g vmail /var/mail/vhosts
}

if [ ! -s /etc/dovecot/users ]; then
  : > /etc/dovecot/users
  chmod 600 /etc/dovecot/users
fi
add_user() { # add_user local-part password
  local addr="$1@${DOMAIN}" pw="$2" hash
  grep -q "^${addr}:" /etc/dovecot/users && return 0
  hash="$(doveadm pw -s SHA512-CRYPT -p "$pw")"
  printf '%s:%s:5000:5000::/var/mail/vhosts/%s/%s::\n' "$addr" "$hash" "$DOMAIN" "$1" >> /etc/dovecot/users
}
grep -q "^${SUBMIT_USER}:" /etc/dovecot/users || {
  printf '%s:%s:5000:5000::/nonexistent::\n' \
    "$SUBMIT_USER" "$(doveadm pw -s SHA512-CRYPT -p "$SUBMIT_PASS")" >> /etc/dovecot/users
}
for mb in $MAILBOXES; do
  add_user "$mb" "$(keep_secret "${STATE}/mailbox.${mb}.password" openssl rand -base64 24)"
done
chown root:dovecot /etc/dovecot/users && chmod 640 /etc/dovecot/users

# virtual_mailbox_maps — the accounts with a MAILBOX, which is deliberately not the same list as the
# accounts that can AUTHENTICATE. agentd@ is absent, so mail addressed to it is rejected at RCPT
# rather than accepted and dropped into a directory nobody opens.
: > /etc/postfix/vmailbox
for mb in $MAILBOXES; do
  printf '%s@%s %s/%s/\n' "$mb" "$DOMAIN" "$DOMAIN" "$mb" >> /etc/postfix/vmailbox
done
postmap /etc/postfix/vmailbox

# 🔴 smtpd_sender_login_maps. Without this, ANY account that authenticates may put ANY address in
# From: — so the submission credential agentd carries could send as postmaster@, and a leaked one
# could send as anybody at this domain with our own DKIM signature on it. This map says which login
# owns which sender, and the submission restriction below rejects a mismatch.
: > /etc/postfix/sender_login
printf 'support@%s %s\n' "$DOMAIN" "$SUBMIT_USER" >> /etc/postfix/sender_login
printf '%s %s\n' "$SUBMIT_USER" "$SUBMIT_USER" >> /etc/postfix/sender_login
for mb in $MAILBOXES; do
  printf '%s@%s %s@%s\n' "$mb" "$DOMAIN" "$mb" "$DOMAIN" >> /etc/postfix/sender_login
done
postmap /etc/postfix/sender_login

# ── 6. Dovecot ──────────────────────────────────────────────────────────────────────────────────
log "Dovecot (SASL for Postfix, LMTP delivery, IMAPS for people)"
cat > /etc/dovecot/dovecot.conf <<EOF
protocols = imap lmtp
listen = *
mail_location = maildir:/var/mail/vhosts/%d/%n
mail_uid = 5000
mail_gid = 5000
namespace inbox {
  inbox = yes
}

disable_plaintext_auth = yes
auth_mechanisms = plain login
passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users
}
userdb {
  driver = static
  args = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n
}

ssl = required
ssl_cert = <${CERT_DIR}/fullchain.pem
ssl_key  = <${CERT_DIR}/privkey.pem
ssl_min_protocol = TLSv1.2

# The SASL socket Postfix authenticates against. It lives INSIDE Postfix's chroot, which is why the
# path is relative to /var/spool/postfix and not to /.
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
# IMAPS only. 143 is not opened at all rather than opened-with-STARTTLS-required: a port that exists
# is a port a misconfigured client will try in the clear before it is told not to.
service imap-login {
  inet_listener imap { port = 0 }
  inet_listener imaps { port = 993 }
}
EOF

# ── 7. Postfix ──────────────────────────────────────────────────────────────────────────────────
log "Postfix (mode: ${MODE})"

# Strip the Received header on submission. Otherwise every confirmation mail carries the private IP
# of the pod that generated it, published to whoever receives it — an internal topology leak in the
# one message type we send to strangers.
cat > /etc/postfix/submission_header_checks <<'EOF'
/^Received:/            IGNORE
/^X-Originating-IP:/    IGNORE
/^User-Agent:/          IGNORE
EOF

postconf -e "myhostname = ${MAILHOST}"
postconf -e "myorigin = ${DOMAIN}"
postconf -e "mydomain = ${DOMAIN}"
# 🔴 mydestination stays EMPTY and the domain is served as a VIRTUAL mailbox domain instead. With the
# domain in mydestination, Postfix would also accept mail for every UNIX account on the box — root,
# ubuntu, vmail — as ${user}@${DOMAIN}. Virtual domains accept exactly what /etc/postfix/vmailbox
# lists and reject the rest.
postconf -e "mydestination = localhost"
postconf -e "virtual_mailbox_domains = ${DOMAIN}"
postconf -e "virtual_mailbox_maps = hash:/etc/postfix/vmailbox"
postconf -e "virtual_transport = lmtp:unix:private/dovecot-lmtp"
postconf -e "inet_interfaces = all"
postconf -e "inet_protocols = ipv4"
# ⚠️ ipv4 only, deliberately. An instance with an IPv6 address will PREFER it outbound, and that
# address has no PTR record and is not in our SPF — so the same message that passes every check over
# IPv4 is rejected outright by Gmail over IPv6. This is the single most common way a correctly
# configured mail server fails only for Gmail.

# mynetworks is loopback and nothing else. The temptation is to add the VPC CIDR so agentd can relay
# without a credential; that makes every instance in the VPC — including anything an attacker lands
# on — an authenticated sender for this domain.
postconf -e "mynetworks = 127.0.0.0/8 [::ffff:127.0.0.0]/104 [::1]/128"
postconf -e "smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, defer_unauth_destination"
postconf -e "smtpd_recipient_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination"

postconf -e "smtpd_tls_cert_file = ${CERT_DIR}/fullchain.pem"
postconf -e "smtpd_tls_key_file = ${CERT_DIR}/privkey.pem"
# `may` on port 25 and `encrypt` on 587 (set in master.cf). Requiring TLS on 25 would refuse inbound
# mail from every sender that does not offer it — including bounces we need to see.
postconf -e "smtpd_tls_security_level = may"
postconf -e "smtpd_tls_mandatory_protocols = >=TLSv1.2"
postconf -e "smtp_tls_security_level = may"
postconf -e "smtp_tls_mandatory_protocols = >=TLSv1.2"
postconf -e "smtp_tls_CAfile = /etc/ssl/certs/ca-certificates.crt"

postconf -e "smtpd_sasl_type = dovecot"
postconf -e "smtpd_sasl_path = private/auth"
postconf -e "smtpd_sasl_auth_enable = no"
postconf -e "smtpd_sender_login_maps = hash:/etc/postfix/sender_login"

postconf -e "milter_default_action = accept"
postconf -e "milter_protocol = 6"
postconf -e "smtpd_milters = inet:localhost:8891"
postconf -e "non_smtpd_milters = inet:localhost:8891"
# ⚠️ milter_default_action = accept means a DEAD OpenDKIM sends UNSIGNED mail rather than no mail.
# `tempfail` is the safer-looking choice and it is the wrong one here: it would take the platform's
# password resets down for a signing daemon that has crashed, and unsigned-but-delivered beats
# not-delivered for a link somebody is waiting on. verify-mail.sh checks the signature explicitly so
# this cannot degrade unnoticed.

postconf -e "message_size_limit = 10485760"
postconf -e "smtpd_helo_required = yes"
postconf -e "disable_vrfy_command = yes"
postconf -e "biff = no"
postconf -e "append_dot_mydomain = no"

if [ "$MODE" = "smarthost" ]; then
  # ⚠️ Read from the environment, never prompted for and never taken as an argument. A password in
  # argv is in the shell history and in every process listing on the box for as long as this runs.
  : "${SMARTHOST_HOST:?smarthost mode needs SMARTHOST_HOST, the SMTP endpoint of the relay}"
  : "${SMARTHOST_PORT:=587}"
  : "${SMARTHOST_USER:?smarthost mode needs SMARTHOST_USER}"
  : "${SMARTHOST_PASS:?smarthost mode needs SMARTHOST_PASS}"

  # 🔴 SMARTHOST_SPF is the relay's SPF mechanism — `include:spf.example-relay.com` or whatever that
  # provider documents. It is REQUIRED rather than defaulted, because there is no safe default: the
  # `ip4:` record that is correct in direct mode authorises an address that never sends in this mode,
  # so every message fails SPF while the DNS looks fully configured. Refusing to boot without it is
  # louder than emitting a record that is wrong in a way nothing checks.
  #
  # It is persisted so `dns-records.sh` prints the right SPF without being told again — a record
  # generated from a value re-typed on a later day is how the two drift.
  # ⚠️ NO APOSTROPHE OR QUOTE IN THIS MESSAGE. Bash quote-parses the word inside ${VAR:?word} even
  # within double quotes, so a lone `'` in the text opens a string that never closes and the WHOLE
  # SCRIPT fails to parse — at load, on a line the error does not name. Found the hard way.
  : "${SMARTHOST_SPF:?smarthost mode needs SMARTHOST_SPF, the SPF mechanism the relay documents, e.g. include:spf.example-relay.com}"
  printf '%s\n' "$SMARTHOST_SPF" > "${STATE}/smarthost.spf"

  printf '[%s]:%s %s:%s\n' "$SMARTHOST_HOST" "$SMARTHOST_PORT" "$SMARTHOST_USER" "$SMARTHOST_PASS" \
    > /etc/postfix/sasl_passwd
  chmod 600 /etc/postfix/sasl_passwd
  postmap /etc/postfix/sasl_passwd
  postconf -e "relayhost = [${SMARTHOST_HOST}]:${SMARTHOST_PORT}"
  postconf -e "smtp_sasl_auth_enable = yes"
  postconf -e "smtp_sasl_password_maps = hash:/etc/postfix/sasl_passwd"
  postconf -e "smtp_sasl_security_options = noanonymous"
  # 🔴 `encrypt`, not `may`, to a smarthost. Opportunistic TLS to a relay we chose and authenticate to
  # is a downgrade we would never notice: our own credential crosses that connection.
  postconf -e "smtp_tls_security_level = encrypt"
else
  postconf -e "relayhost ="
  postconf -e "smtp_sasl_auth_enable = no"
  # 🔴 Clear the persisted smarthost SPF when switching BACK to direct. Leaving it would make
  # `dns-records.sh` keep printing `include:<relay>` for a box that now sends from its own IP — an
  # SPF record that authorises a relay we no longer use and omits the address we do. The switch is
  # the moment that goes wrong, and it goes wrong in the direction where DNS looks configured.
  rm -f "${STATE}/smarthost.spf"
fi

# master.cf: submission on 587 and smtps on 465.
python3 - "$DOMAIN" <<'PY'
import re, sys
path = "/etc/postfix/master.cf"
src = open(path).read()
# Drop any block this script wrote before, so a re-run replaces rather than stacks.
src = re.sub(r"\n# >>> heros-mail submission >>>.*?# <<< heros-mail submission <<<\n", "\n", src, flags=re.S)

# 🔴 Comment out any PRE-EXISTING submission/smtps service before appending ours. Postfix refuses to
# start on a duplicate service definition — "master.cf: duplicate service: submission" — and it
# refuses at RESTART, which on a re-run means the mail server that was working thirty seconds ago is
# now down and the script that took it down reported success. Debian ships these lines commented, so
# on a fresh host this matches nothing; it exists for the host somebody configured by hand first.
out, skipping = [], False
for line in src.split("\n"):
    if re.match(r"^(submission|smtps)\s+inet\b", line):
        skipping = True
        out.append("# (disabled by heros-mail bootstrap; replaced by the block below) " + line)
        continue
    if skipping:
        if line.startswith((" ", "\t")) and line.strip():
            out.append("# " + line)
            continue
        skipping = False
    out.append(line)
src = "\n".join(out)
block = """
# >>> heros-mail submission >>>
submission inet n       -       y       -       -       smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_tls_auth_only=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
  -o smtpd_relay_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject
  -o smtpd_sender_restrictions=reject_sender_login_mismatch
  -o cleanup_service_name=submission-cleanup
  -o milter_macro_daemon_name=ORIGINATING
smtps     inet  n       -       y       -       -       smtpd
  -o syslog_name=postfix/smtps
  -o smtpd_tls_wrappermode=yes
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
  -o smtpd_relay_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject
  -o smtpd_sender_restrictions=reject_sender_login_mismatch
  -o cleanup_service_name=submission-cleanup
  -o milter_macro_daemon_name=ORIGINATING
# A cleanup service used ONLY by submission. Attaching the header_checks to the shared `cleanup`
# would strip Received: from INBOUND mail too, destroying the delivery path of every bounce and
# report that arrives — the one evidence trail this box exists to keep.
submission-cleanup unix n       -       y       -       0       cleanup
  -o header_checks=pcre:/etc/postfix/submission_header_checks
# <<< heros-mail submission <<<
"""
src = src.rstrip("\n") + "\n" + block
open(path, "w").write(src)
PY

newaliases 2>/dev/null || true

# ── 8. Start ────────────────────────────────────────────────────────────────────────────────────
log "Starting"
systemctl enable --now opendkim
systemctl restart opendkim
systemctl enable --now dovecot
systemctl restart dovecot
systemctl enable --now postfix
systemctl restart postfix

# ── 9. What the operator has to do next ─────────────────────────────────────────────────────────
cat > "${STATE}/agentd.env" <<EOF
HEROS_SMTP_HOST=${MAILHOST}
HEROS_SMTP_PORT=587
HEROS_SMTP_TLS=starttls
HEROS_SMTP_USERNAME=${SUBMIT_USER}
HEROS_SMTP_PASSWORD=${SUBMIT_PASS}
HEROS_SMTP_FROM=support@${DOMAIN}
EOF
chmod 600 "${STATE}/agentd.env"

log "Done. Mode=${MODE}."
cat <<EOF

  The platform's mail configuration was written to ${STATE}/agentd.env — it is NOT printed here,
  because a password echoed by a cloud-init run is a password in the console log. Read it over SSH.

  Still to do, in order:
    1. bash dns-records.sh ${DOMAIN}      — publish MX, SPF, DKIM, DMARC
    2. bash verify-mail.sh ${DOMAIN}      — nothing is proven until this passes
EOF
if [ "$MODE" = "smarthost" ]; then
cat <<EOF
    3. verify the sending DOMAIN at ${SMARTHOST_HOST}

  ⚠️ THAT THIRD STEP IS NOT OPTIONAL AND IS EASY TO READ AS DONE. Almost every relay refuses to
  send as a domain until you have proved you own it, in THEIR console, separately from the DNS
  above. Until then this box authenticates fine, hands the message over fine, and the relay
  rejects it — a failure that appears only in \`mailq\` on a machine nobody is watching.

  🔴 And if that relay offers to publish its own DKIM record for ${DOMAIN}: you do not need it and
  should not use it. This box already signs with a key it holds. Two signatures is not an error,
  but it makes "which key did DMARC pass on" a question with no answer you control.
EOF
else
cat <<EOF
    3. request rDNS + port-25 removal     — README.md §2. Nothing delivers in direct mode until it lands.
EOF
fi
echo
