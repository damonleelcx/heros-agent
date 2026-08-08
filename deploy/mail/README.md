# The platform's own mail server

> 🔴 **THE DEPLOYED SHAPE IS NOT THE ONE THIS FILE DESCRIBES.** The relay runs as a **workload in the
> k3s cluster on `heros-prod`** — see [`../k8s/overlays/prod/mail.yaml`](../k8s/overlays/prod/mail.yaml).
> The dedicated EC2 host below was built and then torn down: the platform already runs on one node,
> and a second instance bought nothing that cluster does not provide. cert-manager issues the
> certificate certbot would have had to fight Traefik for, external-secrets carries the credential,
> and `agentd` reaches the relay by `podSelector` instead of an egress rule pinned to a public
> address it would hairpin out to and back.
>
> **What in this file still applies:** everything about delivery — §1 (the two modes), §1b (SPF, DKIM
> and how DMARC actually passes), the DNS records, and the layered proof in §5. `dns-records.sh` and
> `verify-mail.sh` still work; run them against the workload rather than a host.
>
> **What does not:** `bootstrap-mailserver.sh` (apt/Debian throughout — `heros-prod` is Amazon Linux
> 2023 on aarch64) and `provision-ec2.sh` (it built the host that was removed). Both are kept because
> the Postfix, OpenDKIM and Dovecot configuration in them is the reasoning behind the container's
> settings, and because `direct` mode on a standalone box is still a real option if the relay is ever
> dropped.

## The original standalone-host design

This directory stands up an SMTP relay this platform owns, to replace the Amazon SES path that
`overlays/prod` was configured for. It exists because **SES production access was denied**, which
leaves the deployment in the sandbox — a sandbox account delivers only to separately-verified
addresses, so a stranger signing up receives nothing. `mailer.New` would report the deployment
`Configured() == true` throughout, because from the platform's side the relay is answering. That is
the failure this replaces.

Read [§1](#1-the-thing-to-decide-before-you-build-anything) before you provision. It is the one
section that can save you the whole exercise.

---

## 0. What gets built

| | |
|---|---|
| **One EC2 instance**, Ubuntu 24.04, `t3.small`, public subnet, **Elastic IP** | ~$17/month + $3.60 for the EIP |
| **Postfix** | submission on 587 (STARTTLS + SASL) — the only thing `agentd` talks to. Inbound 25 for the domain. |
| **OpenDKIM** | signs everything outbound, in both modes |
| **Dovecot** | SASL for Postfix, LMTP delivery, IMAPS on 993 so `postmaster@` `abuse@` `dmarc@` `support@` are mailboxes a person can open |
| **certbot** | real certificate for `mail.<domain>`, auto-renewing, reloading both daemons |

Nothing in the platform changes. `agentd` reads six environment variables
([`internal/mailer`](../../internal/mailer/messages.go)) and has no idea what is on the other end —
which is why swapping SES for this is a change to one overlay block and nothing else.

**It also closes a gap this deployment has been carrying.** The prod overlay says of
`support@heros-agent.space`: *"the domain has no MX record, so a reply to a confirmation or reset
mail bounces… right now nobody receives it."* This build gives it a mailbox.

---

## 1. The thing to decide before you build anything

**Sending mail from EC2 requires a second AWS approval, and this build does not route around the
first one.**

Outbound TCP 25 is blocked on every EC2 instance by default. Removing it is a Support request —
*"Request to remove email sending limitations"* — reviewed by a person, granted per-account, tied to
a specific Elastic IP. It is a **different queue** from SES production access and is granted far more
often. But it can be refused, and if it is, an instance in `direct` mode delivers nothing at all.

🔴 **The block is a silent drop, not a refusal.** Connections to a real MX hang until they time out.
Postfix then queues, retries for five days, and bounces. There is no error anywhere on the box for
those five days. `verify-mail.sh` tests this first and treats it as a hard gate for exactly that
reason.

So the build has two modes, and the mode is invisible to everything above it:

| | `direct` | `smarthost` |
|---|---|---|
| Delivery | Postfix → recipient's MX | Postfix → a third-party relay on 587 |
| Needs port 25 removed | **yes** | no — never opens 25 outbound |
| Needs rDNS on the EIP | **yes** | no |
| Reputation belongs to | your EIP, from zero | the relay's pool, already warm |
| DKIM signed by | us | **us** — signing happens before handoff in both modes |
| What `agentd` sees | identical | identical |

**This deployment is on `smarthost`**, and it is the default the bootstrap takes with no second
argument. You get the mailboxes, DKIM under our own key, an operator-owned seam, and delivery that
works on day one instead of when a Support queue clears. Switching to `direct` later is one re-run
(`MODE=direct bash bootstrap-mailserver.sh <domain>`) plus republishing SPF.

⚠️ **What smarthost mode does not buy, said plainly: independence from a provider's approval.** It
moves that dependency rather than removing it — the relay is a vendor with an account you can lose,
and one that will ask you to verify the sending domain before it sends anything. What it *does* buy
is that the vendor is replaceable in one command on one host, with no change above it, which is
exactly what SES was not.

⚠️ And the second quiet part: **in smarthost mode, this EC2 box is not what makes mail leave.** The
relay is. `agentd` could talk to the relay directly and skip this host entirely. Three things are
lost if you do, and they are the reason this host still earns its $21/month — a real MX and mailboxes
for `postmaster@` `abuse@` `dmarc@` `support@` (relays are outbound-only); DKIM signed by a key we
hold rather than one the relay holds for us; and one place to change relays without touching the
platform. If none of those matter to you, point `HEROS_SMTP_*` at the relay and delete this
directory. That is a legitimate outcome and it is cheaper.

⚠️ Finally: **if the goal is to stop depending on approvals altogether, EC2 is a strange place to do
it.** The same $17/month buys a VPS at a host that does not block 25 and sets rDNS from a control
panel in thirty seconds — i.e. `direct` mode with no Support case at all. Every script here runs
unchanged on any Ubuntu 24.04 box; only `provision-ec2.sh` and the IMDS lookups are AWS-specific, and
both degrade to a warning elsewhere.

---

## 1b. What smarthost mode changes above the box — SPF, and how DMARC passes

Three things, and the first is the one that fails silently.

**SPF must name the relay, not us.** In `direct` mode our Elastic IP is the connecting address and
`v=spf1 ip4:<eip> -all` authorises it. In `smarthost` mode the connecting address belongs to the
relay, so that same record authorises an address that never sends and **every message fails SPF**
while the DNS reads as complete. `dns-records.sh` derives the record from the live Postfix
configuration rather than from a flag, so it cannot disagree with the box it runs on — which is why
the bootstrap **refuses to run without `SMARTHOST_SPF`** rather than defaulting it.

**DMARC then passes on DKIM alone.** Most relays rewrite the envelope sender to their own bounce
domain, so SPF authenticates *their* domain and aligns with `heros-agent.space` under neither
`aspf=s` nor `aspf=r`. DMARC passes if *either* mechanism aligns, so here it is carried entirely by
the signature this box applies before handoff. 🔴 That makes a dead OpenDKIM a **DMARC failure**
rather than a degradation — and once the policy moves past `p=none`, a rejection. `verify-mail.sh`
checks the milter explicitly and warns if the policy has teeth while SPF cannot align.

**The relay will make you verify the domain, in its own console, with its own DNS records,
separately from all of the above.** Until you do, this box authenticates, hands the message over,
and the relay drops it. That failure appears in `mailq` on a machine nobody is watching and nowhere
else. `dns-records.sh` prints those records as step 6 so they are not a separate thing to remember.

⚠️ **Publish the relay's DKIM record as well as ours — an earlier draft of this runbook said to
decline it, and that was wrong.** It is how the relay proves you own the domain; declining it means
it will not send at all. The two use different selectors (`resend._domainkey` and `heros._domainkey`),
do not collide, and put two signatures on each message. DMARC passes on whichever aligns. Ours is
the one that keeps working the day you change relays.

### The Resend specifics

| | |
|---|---|
| `SMARTHOST_HOST` | `smtp.resend.com` |
| `SMARTHOST_PORT` | `587` (STARTTLS). `2587` is the same thing on an alternate port if 587 is ever filtered; `465`/`2465` are implicit TLS |
| `SMARTHOST_USER` | `resend` — the literal string, not an email address |
| `SMARTHOST_PASS` | your Resend **API key** |
| `SMARTHOST_SPF` | `include:amazonses.com` |

Plus, from the Resend dashboard, on a `send` subdomain it uses as the bounce/envelope domain:
`MX send.heros-agent.space → feedback-smtp.<region>.amazonses.com`, `TXT send.heros-agent.space →
v=spf1 include:amazonses.com ~all`, and `TXT resend._domainkey.heros-agent.space`. ⚠️ That `send.`
MX does **not** collide with the bare-domain MX in §3 — different names, both needed.

🔴 **`include:amazonses.com` is not a coincidence: Resend runs on Amazon SES.** This deployment still
delivers through SES infrastructure — under *Resend's* account, which has production access, rather
than this one, which was refused it. That is a legitimate way through and it is worth knowing now
rather than rediscovering during an SES-wide incident.

🔴 **This is also why the DMARC record says `aspf=r` in smarthost mode**, where direct mode uses
`aspf=s`. Resend rewrites the envelope sender to `send.heros-agent.space`, and SPF is evaluated
against *that* domain. Under strict alignment it would not match `heros-agent.space` and SPF would
contribute nothing to DMARC, leaving DKIM alone to carry it. Relaxed alignment accepts the
subdomain, so both mechanisms count. `dns-records.sh` emits the right one per mode.

---

## 2. The AWS request — **not needed in `smarthost` mode**

Skip this section entirely unless you switch to `direct`. Nothing in smarthost mode opens port 25
outbound, and no receiver ever sees this host's address, so neither the port-25 removal nor rDNS
authenticates anything. It is recorded here because it is the whole content of the `direct` path.

Both things come from one form. In the console: **Support → Create case → Service limit increase →
EC2 Email**, or the direct form at
`https://aws.amazon.com/forms/ec2-email-limit-rdns-request`.

Give it:

- **Elastic IP** — the one `provision-ec2.sh` prints. Not the auto-assigned public IP, which changes
  on every stop/start and would take rDNS, SPF and your accumulated reputation with it.
- **Reverse DNS record** — `mail.<domain>`. It must match the forward `A` record exactly; a
  forward/reverse mismatch is read as forgery by most filters.
- **Use case** — describe the actual traffic. Transactional only: email confirmation, password
  reset, team invitation. Recipients are people who entered their own address in our sign-up form.
  No marketing, no purchased lists, no bulk. Say how you handle bounces and complaints, because that
  is what the reviewer is actually assessing.

Expect a day or more. Request it the moment the EIP exists, not after the box is built.

⚠️ **rDNS alone is not enough for the large receivers.** Microsoft (Outlook, Hotmail, Live) runs its
own reputation system on top; enrol the EIP in
[SNDS](https://sendersupport.olc.protection.outlook.com/snds/) and the
[JMRP](https://sendersupport.olc.protection.outlook.com/pm/) feedback loop, or expect Outlook to
reject with `S3140` while Gmail accepts the same message. Google's
[Postmaster Tools](https://postmaster.google.com/) is the equivalent for the other half of the world
and costs nothing but a DNS record.

---

## 3. Build it

Order matters — each step is a precondition of the next, and skipping ahead fails in a way that
looks like something else.

```bash
export AWS_REGION=us-east-1
export KEY_NAME=your-ec2-keypair
export ADMIN_CIDR=203.0.113.4/32     # your office or VPN; SSH and IMAPS are limited to it
bash deploy/mail/provision-ec2.sh heros-agent.space
```

Publish `A mail.heros-agent.space → <the Elastic IP it printed>` and wait for it to resolve.
**certbot cannot issue a certificate until it does**, and the bootstrap does not finish without
certbot — deliberately, because the alternative is a self-signed certificate that satisfies
`smtpd_tls_security_level = encrypt` while no client should have accepted it.

```bash
scp deploy/mail/*.sh ubuntu@<eip>:~/
ssh ubuntu@<eip>
```

Then on the box. `smarthost` is the default, and the relay's credentials go in the **environment** so
they never reach argv — a password in a command line is in the shell history and in every process
listing while the script runs:

```bash
sudo SMARTHOST_HOST=smtp.resend.com \
     SMARTHOST_USER=resend \
     SMARTHOST_PASS='<your Resend API key>' \
     SMARTHOST_SPF=include:amazonses.com \
  bash bootstrap-mailserver.sh heros-agent.space
```

`SMARTHOST_USER` really is the literal string `resend`; the API key is the password.
`SMARTHOST_SPF` is **required, not defaulted** — see §1b for why a wrong SPF record here is the
failure that leaves the DNS looking complete.

<details>
<summary><code>direct</code> mode, for reference</summary>

```bash
sudo bash bootstrap-mailserver.sh heros-agent.space direct
```

Requires §2 to have been granted. Switching modes later is just this command — it rewrites Postfix,
clears the recorded smarthost SPF so `dns-records.sh` stops printing it, and leaves the DKIM key and
the submission password untouched.
</details>

Publish the records it tells you to:

```bash
sudo bash dns-records.sh heros-agent.space
```

Four records: `MX`, `SPF`, `DKIM`, `DMARC`. It reads the live Postfix configuration, so in smarthost
mode the SPF line it prints names the relay and **no PTR record is requested**. Three notes that cost
people days —

- **SPF `-all` vs `~all`.** Publish `-all` only once every sender for the domain is listed. If SES,
  a helpdesk or a marketing tool still sends as `heros-agent.space`, `-all` stops their mail dead.
- **Delete the old `ip4:` if you ever switch modes.** Leaving it makes the record longer, not
  shorter, so it reads as more complete while authorising an address that no longer sends.
  `verify-mail.sh` warns about exactly this.
- **DMARC starts at `p=none`.** That asks receivers to *report*, not to act. Read `dmarc@` for two
  weeks to find the sender you forgot, then tighten to `p=quarantine` and later `p=reject`.
  Publishing `p=reject` on day one is how a company discovers its invoicing system by having it stop.

Then prove it:

```bash
sudo bash verify-mail.sh heros-agent.space you@example.com
```

---

## 4. Point the platform at it

The bootstrap writes `/var/lib/heros-mail/agentd.env`, mode `0600`. It is not printed to the console
— a password echoed by a cloud-init run is a password in the instance log.

**Compose** — copy those six lines into `deploy/.env.platform`.

**Kubernetes** — the host, port, TLS mode and sender are plain configuration and are already declared
in [`overlays/prod/kustomization.yaml`](../k8s/overlays/prod/kustomization.yaml). The username and
password go to the secret store, under the two keys
[`base/externalsecrets.yaml`](../k8s/base/externalsecrets.yaml) already projects:

```bash
aws secretsmanager put-secret-value --secret-id heros/smtp \
  --secret-string '{"username":"agentd@heros-agent.space","password":"…"}'
```

🔴 **And check the egress policy, because this has already bitten this deployment once.** `agentd`
runs under a default-deny NetworkPolicy. The 587 rule exists — it was added after every confirmation
link failed with `dial tcp …:587: connect: connection refused` — but its `ipBlock` was written wide
*because SES publishes no stable address*. This relay has a fixed Elastic IP, so that reasoning no
longer applies and the rule is now narrowed to `<eip>/32`. **If you change the EIP, change that
rule**, or mail stops with a connection refused several layers from the thing you edited.

⚠️ If the mail host is in the **same VPC** as the cluster, the private-range exceptions in that rule
(`10.0.0.0/8` and friends) will block it and the `/32` will not help — you need a rule naming the
private address instead. Putting the mail host outside the VPC avoids the whole question and is what
these scripts assume.

---

## 5. Prove it, in layers, and know what each layer misses

| Layer | Command | What it does **not** cover |
|---|---|---|
| Unit | `go test ./internal/mailer/...` | Whether anything leaves the process |
| Relay | `verify-mail.sh <domain> you@…` on the mail host | Whether `agentd` can reach the relay |
| Path | `make mail-proof TO=you@…` | Runs on a **build host** — crosses no NetworkPolicy |
| Product | a real sign-up on the deployed console | — |

🔴 **The third row is the one that has lied before.** `make mail-proof` exercises the credential, the
relay and our own code, and never once crosses the policy that `agentd` actually runs behind. A send
proven from somewhere the product does not run is not a proven send.

⚠️ **And "accepted" is not "delivered."** `verify-mail.sh` reports what the relay accepted. Open the
message and read `Authentication-Results:` — you want `spf=pass`, `dkim=pass`, `dmarc=pass`, and it
in an **inbox**, not a spam folder. For a password-reset link, the spam folder and `/dev/null` are
the same outcome.

**On `smarthost`, the IP reputation problem is the relay's and you inherit a warm pool** — which is
most of why this mode was chosen. What you do *not* inherit is domain reputation: `heros-agent.space`
is new to receivers regardless of whose IP carries it. Send low volume, keep bounces low, and never
send to an address that has not been through the confirmation flow. A shared pool also means a noisy
neighbour can move your delivery, which is the cost side of the same trade.

<details>
<summary>On <code>direct</code>, this is the paragraph that matters most</summary>

A new IP has no reputation, and that is not something you can fix by checking configuration. Even
with all four records correct and rDNS in place, the first weeks from a fresh EC2 address are the
weeks receivers are deciding about you. Warm up slowly, and enrol in
[SNDS](https://sendersupport.olc.protection.outlook.com/snds/) and
[Postmaster Tools](https://postmaster.google.com/) before you need them.
</details>

---

## 6. Operating it

```bash
mailq                                    # the queue — anything old here is a delivery problem
journalctl -u postfix -f                 # live
postqueue -f                             # retry everything now
tail -f /var/log/mail.log | grep -i dkim # signing
```

Read the mailboxes over IMAPS on 993 (`mail.<domain>`, the addresses are full
`postmaster@<domain>` form, passwords in `/var/lib/heros-mail/mailbox.*.password`). `dmarc@` is where
the aggregate reports arrive — they are the only view you have of mail being sent *as* your domain
by somebody else.

**What this box is a single point of failure for**, stated rather than implied: one instance, one
EBS volume, no replica. If it dies, sign-ups still succeed — `mailer` never fails the act that
produced the message — but confirmation and reset links stop arriving, and the operator record on the
readiness surface will *not* catch them, because from `agentd`'s side the relay is configured. The
symptom is a growing queue on a box nobody is looking at.

⚠️ **`smarthost` mode does not remove that SPOF, it splits it.** The relay's availability is their
problem now, but this host is still in the path and still the only thing on it. If mail must survive
this instance dying, the answer is to point `HEROS_SMTP_*` straight at the relay and accept losing
the inbound mailboxes — not to run two of these. Watch `mailq`; a queue that grows is the only early
signal either failure produces.

---

## 7. If you decide against this

Nothing here is load-bearing for the platform. Delete the instance, put a relay's host and credential
into the same six variables, and the platform cannot tell the difference. That property is the reason
this was a two-day job and not a two-week one, and it is worth not spending.
