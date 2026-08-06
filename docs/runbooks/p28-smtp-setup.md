# Runbook — mail for confirmation and password-reset links (P28)

- **Applies to:** the hosted deployment (`deploy/k8s/overlays/prod`, k3s on `i-05f4712279b04fac5`).
- **Relay:** Amazon SES in `us-east-1`, sending as `support@heros-agent.space`.
- **State:** the relay is **configured and proven**, and the P28 code is **DEPLOYED** as of 2026-08-06
  (§5.2 — image `a4b854c`, `/readyz` reports `mail_configured: true`). The sandbox request was
  **submitted and DENIED** (§5.1), so mail still reaches separately-verified addresses only. The
  console seam has **not** been flipped (§6).

---

## 1. What was done, and how each part was checked

| Step | State | Evidence |
|---|---|---|
| SES domain identity for `heros-agent.space` | ✅ done | `VerifiedForSendingStatus: true` |
| Easy DKIM (3 CNAMEs in Route 53 `Z085320975OZ7TS42GL1`) | ✅ done | `DkimAttributes.Status: SUCCESS` |
| SPF at the apex — `v=spf1 include:amazonses.com ~all` | ✅ done | the zone had **no TXT record at all** before |
| DMARC — `v=DMARC1; p=none; fo=1` | ✅ done | monitor mode; see §4 before tightening |
| IAM user `heros-ses-smtp`, send-only, `ses:FromAddress` locked to `support@` | ✅ done | it cannot send as any other address |
| SMTP credentials in `heros/platform` | ✅ done | 12 → 14 keys, read-modify-write, **never printed** |
| Operator IdP issuer + client id in `heros/platform` | ✅ done | 14 → 16 keys, copied from the live cluster |
| All 16 keys projected into the cluster Secret | ✅ done | `kubectl get secret heros-platform` → `count=16` |
| A real message through `internal/mailer` | ✅ **sent** | `make mail-proof TO=…` → accepted by the relay |
| SES production access | 🔴 **denied** | automated, case `178599389200025` — §5.1 |

**Repeat the last one any time:**

```bash
make mail-proof TO=you@example.com
```

It uses the production code path — `mailer.New(mailer.ConfigFromEnv(), nil)` and a real `ResetPassword`
body — and takes credentials from the store as environment, never on a command line. A proof that sent a
hand-written SMTP message would establish that the relay works and nothing about whether our code can
reach it, which is the half that has actually been wrong before.

## 2. 🔴 The ordering trap this uncovered — read before any `kubectl apply`

The `ExternalSecret` reported **`Ready=True`, `secret synced`** while projecting only the original **12**
keys. It is not lying and it is not broken: `spec.data` is an explicit list, and the live copy did not
declare the four new keys, so it synced exactly what it knew about. **A green sync status describes the
keys it declares, not the keys the workload needs.**

That matters because the manifest now reads `admin-idp-issuer` and `admin-idp-client-id` from that Secret
with `optional: true`. Applying the Deployment before the Secret carries them gives agentd
`ADMIN_IDENTITY_MODE=oidc` with an **empty issuer**, and `adminlaunch` refuses the boot — a rollout that
never completes, and on a single-replica deployment a surface that does not come back if the old pod is
gone.

The four keys have already been patched into the live ExternalSecret and verified at `count=16`, so the
trap is disarmed for this deployment. **The rule stands for the next one:** apply
`externalsecrets.yaml`, confirm the key count, *then* the Deployments.

```bash
sudo kubectl -n heros get secret heros-platform -o json | python3 -c "import json,sys; print(len(json.load(sys.stdin)['data']))"
```

Expect `16`.

## 3. Verify — four layers, and layer 3 is the one people skip

1. **The platform says it is configured.** Not a log line; the readiness surface:

   ```bash
   sudo kubectl -n heros exec deploy/agentd -- wget -qO- localhost:4321/readyz | grep -o '"mail_configured":[a-z]*'
   ```

   ⚠️ This only becomes meaningful once the P28 image is deployed (§5) — the current image does not read
   `HEROS_SMTP_*` and does not report the field.

2. **The request is accepted.** `POST /api/v1/auth/password/forgot` always answers `{"ok":true,…}` — by
   design, so it discloses nothing about who has an account. **Layer 2 alone proves nothing.**

3. **The mail arrives.** The inbox. This is the layer the operator fallback cannot fake, and the layer
   `make mail-proof` stops one short of: it reports the *relay accepting* the message, which is not the
   same as an inbox receiving it.

4. **The link works.** Follow it, set a password, and confirm the completion screen lists the machine
   credentials it did **not** revoke. That is the whole reset contract, end to end.

If layer 1 is true and layer 3 never arrives: `kubectl -n heros logs deploy/agentd | grep 'WARN mail'`. A
send failure is logged there and never fails the request that caused it.

## 4. ⚠️ Things that are true and easy to misread

- **SES is not literally free.** $0.10 per 1,000 messages with a monthly free allowance. At sign-up volume
  that is pennies, and it was chosen over a genuinely-free third party because it needed **no new account
  and no new vendor** — the domain was already in this account's Route 53, so DKIM published and verified
  in under a minute. Swapping later is three env values plus two store keys; nothing in the platform knows
  which relay it talks to.
- **`support@heros-agent.space` is send-only.** The domain has **no MX record**, so a reply bounces. The
  message bodies do not invite a reply, so nothing we send makes a promise this breaks — but somebody will
  reply anyway and nobody receives it. Two ways to fix, neither done: an MX + SES receipt rule into
  S3/Lambda forwarding, or point MX at a free forwarder.
- **DMARC is `p=none`.** Monitor mode, deliberately: tightening to `quarantine`/`reject` before there is
  any reporting in place turns a misconfiguration into silently discarded mail. Tighten after real traffic,
  and add `rua=` once an address can receive.
- **The SMTP credential can only send as `support@`.** The IAM policy carries a
  `ses:FromAddress` condition, so a leaked credential cannot spoof another address on the domain.

## 5. 🔴 Two things that are NOT done

### 5.1 🔴 The SES sandbox is not lifted — the request was SUBMITTED and DENIED

```bash
aws sesv2 get-account --region us-east-1 --query '{Prod:ProductionAccessEnabled,Review:Details.ReviewDetails}'
```

| Field | Value |
|---|---|
| `ProductionAccessEnabled` | `false` |
| `ReviewDetails.Status` | **`DENIED`** |
| `ReviewDetails.CaseId` | `178599389200025` |
| Submitted | `TRANSACTIONAL`, `https://heros-agent.space`, 1045-character use case, contact `damonlee1020@gmail.com` |

The denial was **immediate and automated** — it came back within the same API call, so no human read the
description. That is common for a young account with no sending history and it is not a verdict on the
use case.

**🚫 It cannot be resubmitted through the API.** A second `put-account-details` returns
`ConflictException`, because account details now exist. (Verified — and the failed call left the original
description intact, which was checked rather than assumed.)

**🚫 The Support API cannot read the case either:** `describe-cases` returns
`SubscriptionRequiredException` on a Basic support plan.

So the only route is the console, and the useful next step is the reason:

1. **Check `damonlee1020@gmail.com`.** AWS emails the denial, and unlike the API response it usually names
   a reason. That reason decides what the appeal should say.
2. **Reply to case `178599389200025`** in Support Center → *Your support cases* (include resolved). A reply
   reopens it for a human, which is the normal and usually successful path after an automated denial.

⚠️ Until it is granted, the account sends **200/day to separately verified addresses only**. Your own test
arrives. **A stranger signing up receives nothing and gets no error** — SES accepts the message and drops
it, so the platform reports the send as successful. That is the most misleading state in this whole setup,
so do not read a green `make mail-proof` as "sign-up mail works".

**What is usable meanwhile:** a private beta. Verify each early user's address
(`aws sesv2 create-email-identity --email-identity them@example.com --region us-east-1`; they click a
link) and everything works for exactly those people. That is an honest launch shape for a handful of
users; it does not scale past the 200/day cap or reach strangers.

### 5.2 ✅ The P28 code IS deployed (2026-08-06)

All three images built from `a4b854c` for `linux/arm64`, pushed to ECR and pinned by digest. `/readyz`
reports `mail_configured: true`, `self_serve_signup: true`, `store: postgres`, `status: ready`.

**🔴 Three things this release found, each of which had been invisible:**

1. **agentd was forbidden the port its own manifest configured.** The overlay set `HEROS_SMTP_PORT=587`
   and its egress allowlist opened **443 only**, so the first real send failed with
   `dial tcp …:587: connect: connection refused`. Fixed in the overlay (a separate 587 rule).

   ⚠️ **`make mail-proof` cannot catch this and never could** — it runs on a build host, not in the
   pod, so it crosses no NetworkPolicy. **A send proven from somewhere the product does not run is not
   a proven send.** §3's layer 3 calls the inbox the layer people skip; this is the layer before it.

2. **The admin-console image had been unbuildable since P27.** `scan:ledger` reads every change in
   `GOVERNED_CHANGES`; P27 added one without adding the matching `COPY`. No CI job builds that
   Dockerfile, so main stayed green and the release stopped at the first `docker build`.

3. **The live cluster had drifted from these manifests.** agentd and console were running digests
   pinned nowhere in the repo, and `ADMIN_IDP_ISSUER`/`ADMIN_IDP_CLIENT_ID` were literal values from an
   out-of-band `kubectl set env`. That drift makes `kubectl apply` **fail outright** — a strategic merge
   keeps the existing `value` and adds the manifest's `valueFrom`, which the API rejects as
   `may not be specified when value is not empty`.

   ⚠️ `--server-side --force-conflicts` does **not** rescue this: `env` merges by `name` there too. The
   fix is one **atomic JSON patch** replacing each drifted entry with the manifest's, guarded by `test`
   ops on the index — atomic because a two-step "clear, then apply" leaves an intermediate template with
   an empty issuer, and `adminlaunch` refuses that boot.

## 6. And the step deliberately left for a decision

The customer console still runs `CONSOLE_TENANT_IDENTITY=configured`, so `/create-account` and
`/forgot-password` redirect to `/signin` regardless of the self-serve flag.

The lockout that made this a decision is now solvable: `HEROS_BOOTSTRAP_OWNER_TENANT` adopts a named
address into an organization that **already exists**, as an owner, with a single-use password-set link —
so flipping the seam no longer strands the tenant holding the data. It refuses to create an organization,
refuses a suspended one, refuses to promote an existing member, and mints nothing for an account that
already has a password.

**Step 1 is done as of 2026-08-06.** `damonlee1020@gmail.com` is an **owner of `heros`**
(`usr_e2204f72f2ee3a237bd56f97`, active), and a single-use link was minted and **accepted by the relay**
with no send warning. Step 3 is still pending: it waits on that password actually working.

⚠️ **`kubectl set env` for step 1 does not survive an apply.** `HEROS_BOOTSTRAP_OWNER_EMAIL` is declared
in the manifest with an empty value, so the next `kubectl apply` silently sets it back and no link is
minted — observed on this deployment. `HEROS_BOOTSTRAP_OWNER_TENANT` survives only because the manifest
does not declare it at all. Set the pair **after** the apply, never before, and remove both once the
password exists.

To flip, in this order:

```bash
# 1. name the first owner and the organization they adopt
sudo kubectl -n heros set env deploy/agentd HEROS_BOOTSTRAP_OWNER_EMAIL=you@example.com HEROS_BOOTSTRAP_OWNER_TENANT=<existing tenant id>
# 2. watch the boot line naming the adoption, then collect the link from the inbox
sudo kubectl -n heros logs deploy/agentd | grep 'account system'
# 3. ONLY after signing in with the new password, switch the console's seam
sudo kubectl -n heros set env deploy/console CONSOLE_TENANT_IDENTITY=password
```

⚠️ Step 3 is the one-way moment: the shared assertion stops working for everybody the instant it lands.
Do not run it until step 2's password works.
