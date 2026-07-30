# P21 Billing Webhook — the one inbound-from-internet path (runbook + ingress posture)

- **Status:** Accepted (2026-07-30)
- **Audience:** whoever operates a deployment, and whoever reviews its network posture.
- **Owns:** the `POST /billing/webhook` endpoint, its secret wiring, and what to do when it misbehaves.
- **Reads with:** [`deploy/README.md`](../../deploy/README.md) (the P19 ingress/egress model),
  [`openspec/changes/p21-payments/design.md`](../../openspec/changes/p21-payments/design.md) Decisions 3
  and 6, [ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md).

## 1. What this path is, and why there is exactly one

P19's network model is an **egress allowlist**: the platform reaches out to a named set of destinations
and nothing reaches in. P21 opens one door in that wall, and one is the whole point — a second inbound
path is a second thing to review, rate-limit, and get wrong.

```
POST /billing/webhook          ← the ONLY route that accepts unsolicited traffic from the internet
```

It is mounted only where a deployment calls `MountBillingWebhook`. A deployment that does not collect
payments does not have this endpoint at all; there is no flag that half-enables it.

**It is not in any customer's production request path** (ADR-002). If it is down, a customer's
transformed program keeps calling its own providers. Billing is internal commerce, and the only thing a
webhook outage delays is the platform learning what Stripe already knows — which Stripe will retry
until it lands.

## 2. The posture, in the order it runs

| Step | What it does | What it refuses |
|---|---|---|
| **Bound** | `http.MaxBytesReader` caps the body at 1 MiB before a byte is read | an unbounded POST → **400** |
| **Verify** | HMAC-SHA256 over `"<t>.<body>"` against the signing secret from the Secrets seam, `hmac.Equal`, **all** `v1=` values tried | unsigned / forged → **400**, before any parse into a decision |
| **Window** | the signed timestamp must be within `billing.WebhookMaxSkew` (5 minutes) | a captured payload replayed later → **400** |
| **Dedupe** | claim the delivery on Stripe's own event id, atomically | a redelivery → **200**, applying nothing |
| **Persist** | the mirrored state and the entitlement sync land durably | a persistence failure → **5xx**, claim released so the retry re-applies |
| **Ack** | only now, **200** | — |

**Authentication is the signature.** There is no API-key gate in front of this route, and adding one
would be a second credential Stripe does not have and cannot present. That is why verification is step
one rather than a check somewhere in the middle.

**Multiple `v1=` signatures are accepted.** That is what a secret rotation looks like on the wire —
Stripe signs with the old and the new secret during the overlap. A verifier that read only the first
would reject half the deliveries in the exact window where a rejection is hardest to diagnose.

### The status codes are the contract

```
200  applied, or a redelivery that applied nothing.  Both mean DURABLY OURS. Stripe stops retrying.
400  not from Stripe, or unprocessable however often it is sent: unsigned, forged, stale, no event id.
5xx  it IS from Stripe and was NOT recorded. This is how the platform asks for the retry.
```

🔴 **A 200 is a promise.** Stripe stops retrying an event it thinks succeeded, so a handler that acked
before it persisted would convert a redelivery into a *lost* event that never comes back. Answering
**400** for a persistence failure is the same mistake with a delay: Stripe treats it as permanently
unprocessable and eventually stops.

## 3. Rate posture

The endpoint is *rate-aware* in the only sense that is honest: it does the cheap refusals first. A body
that is too large never reaches the verifier; an unsigned body never reaches the parser; a forged one
never reaches the dedupe table. The expensive work — a durable claim and a durable effect — happens only
after a valid signature, which an attacker cannot produce without the signing secret.

If a deployment fronts the platform with a proxy, rate-limit this route there as well. Do **not** put a
limiter in front of it that returns 429 under normal Stripe redelivery: Stripe's retry schedule is
bursty by design, and a limiter that trips on it manufactures the lost-event failure this design spends
its whole budget preventing.

## 4. Secret wiring — test and live

Both credentials come from the **Secrets seam** under the P7 reserved logical names. There is no
environment variable read directly, and no second mechanism:

| Logical name | What it is | Used by |
|---|---|---|
| `billing_provider` | the Stripe **API key** | every outbound Stripe call |
| `billing_webhook` | the Stripe **webhook signing secret** | this endpoint, on every delivery |

They resolve **fail-closed**: no key means no call and no verification, never a fallback to an
unauthenticated path.

### Test and live are separated on the resolution path, not by convention

`billing.Rollout`'s zero value is **test**, so a deployment that configures nothing charges nothing
real. The provider asserts the key's mode on **every** call — not once at startup — because the seam
resolves at the moment of use and a key rotated to live under a test deployment must be refused at the
next call rather than at the next restart.

| Configured mode | Key prefix | Result |
|---|---|---|
| test | `sk_test_` / `rk_test_` | ✅ |
| test | `sk_live_` / `rk_live_` | ❌ refused — *a live key on a test surface is a real-money incident* |
| live | `sk_test_` / `rk_test_` | ❌ refused — *silent no-op billing is not a safe failure* |
| either | unrecognized prefix | ❌ refused — *assuming "probably test" is how a live key ends up on a test surface* |

A **separate signing secret per mode** is required. Stripe issues one per endpoint, and sharing one
across a test and a live endpoint means a test event verifies against a live deployment.

## 5. What `/readyz` tells you

```json
{
  "billing_rollout":  { "billing": "enabled", "provider_mode": "test", "gainshare": "disabled", ... },
  "billing_provider": { "provider": "stripe(test)", "secrets_source": "aws-secrets-manager" }
}
```

Two fields, two questions, deliberately not merged: **which gates are open** and **which processor is
behind them, resolving credentials from where**. The failure this makes checkable is a deployment whose
LLM credentials come from a manager while its billing credentials quietly come from somewhere else,
with a health endpoint confidently wrong about both.

Neither field ever contains a credential. If one did, the secret is compromised — rotate first, then
find out why.

## 6. Runbook

### The endpoint is returning 5xx

It is doing its job: it could not record the event and is asking Stripe to retry. Find out what could
not persist — the delivery store or the state store — and fix that. **Do not** make the endpoint return
200 to stop the noise. Every 5xx here is an event Stripe will resend; every 200 is one it will not.

Check Stripe's own dashboard for the pending retries; they are the backlog, and they drain on their own
once persistence recovers.

### The endpoint is returning 400 for deliveries that should be valid

In order of likelihood:

1. **Wrong signing secret** — a test-mode endpoint's secret against a live deployment, or vice versa.
2. **Clock skew** — the signed timestamp is outside the 5-minute window. Check NTP on the host; a
   drifted clock rejects every delivery and looks exactly like a forged one.
3. **A proxy rewriting the body** — the signature covers the bytes Stripe sent. A gateway that
   re-serializes JSON, strips whitespace, or transcodes will break every signature. Pass the body
   through byte-for-byte.
4. **Mid-rotation with only one secret configured** — configure both during the overlap; the verifier
   accepts either.

### An event was acked but its effect is missing

This is the case the design refuses to allow, so treat a real instance as a defect rather than an
operational hiccup. The one path that can produce it is a failed effect whose claim could *not* be
released — and the endpoint says so explicitly, using the word **RECONCILED**, because a retry will not
fix it: the claim is standing and the redelivery would be deduped into nothing.

Reconcile against the provider: the subscription's live state is Stripe's, and re-applying it is the
audited plan-change path, not a database edit.

### A customer says they were charged twice

They were not, or the ledger is lying — and the ledger is the thing to check first:

```
billing_event rows for {customer, period}   → the idempotency key is UNIQUE, so there is at most one
                                              row per operation
Stripe objects for the same key             → Stripe's own idempotency returns the original
```

Two charges for one operation would mean two different keys, which means a caller composed one instead
of deriving it. That is the failure the derived-key helpers exist to make unrepresentable.

If a charge genuinely is wrong, the fix is a **credit or refund** through the additive path. Never edit
or delete a row: recovery is a forward operation, and the mistake staying visible is what makes the
correction auditable.

### Flipping to live mode

Gated on **both** (PRD Q5):

1. the M16 exit checklist green, and
2. one reconciled test-mode billing period signed off by Finance.

Then, in order: provision the live API key and the live endpoint's signing secret in the secrets
source → flip `Rollout.Enable(ModeLive)` → confirm `/readyz` reports `provider_mode: live` and
`provider: stripe(live)` → send one Stripe test event to the live endpoint and confirm it is **rejected**
(a test event must move no real money).
