# P22 SSO & Identity — what we sell, what we say, and where the boundary is

- **Status:** Accepted (2026-07-31)
- **Audience:** anyone who writes a sentence about identity that a customer, a prospect, or a security
  reviewer reads — the console, a datasheet, a security questionnaire, an RFP answer, a support reply.
- **Rule:** identity is the one subject where an over-claim is discovered by the person best equipped to
  discover it. A security reviewer reads the claim, then asks for the mechanism. Everything below is
  written so that the second question is a good day.

## 1. The four rules, before any copy

1. **Sell what is built. Name the rest as not built.** The shipped list in §2 is exactly the capability
   set P22 delivers, and every item on it has a test. The deferred list in §3 is not a roadmap tease —
   it is the answer to give when a buyer asks, because the alternative is discovering it during
   implementation, after the signature.
2. **No price and no plan gate on the identity path.** Identity proves *who*; entitlement is P7 and
   payment is P21. Nothing in the sign-in surface may imply that signing in depends on a plan, and no
   code on that path reads one. This is a build gate, not a review note (§5).
3. **No internal mechanism name reaches a user.** No secret's logical name (`console_idp_client_secret`),
   no provider-kind literal (`admin-idp-oidc`), no issuer, no allowlist entry, no environment variable.
   A user reads *"your identity provider could not be reached"*, never *"discovery returned 503 from
   https://acme.okta.com"*. The cause is a server-side security event, where an operator can read it and
   an attacker cannot.
4. **State revocation as a next-request effect, not as "instant".** The honest sentence is *the very next
   request is denied; there is no grace period in which the session still works*. "Instant" is a word a
   security reviewer will ask us to defend, and the defensible version is the longer one.

## 2. What we sell — built, and testable

| Claim | What it means | Where it is proven |
|---|---|---|
| **SSO federation against your own identity provider** | OIDC Authorization Code + PKCE (primary) and SAML 2.0 (enterprise alternative), both behind one seam. Your deployment's config chooses; neither is a different product. | `web/console/src/lib/idp/`, `tests/sso-identity.test.mjs` |
| **We run no password database and no identity provider of our own** | There is no password store, no credential recovery flow, and nothing to breach. Your IdP proves who someone is; we ask it. This is a *differentiator*, and it is also simply true — the alternative would be a liability we chose not to acquire. | absence, plus `content/identity.ts` |
| **The proof of identity is dropped** | The ID token / SAML assertion is verified, exchanged for a session, and never stored, cookied, logged, traced, or carried upstream. | NFR2 fence in `tests/sso-identity.test.mjs` |
| **Sessions are server-side and revocation lands on the next request** | The store is read on every request. No self-vouching token, no grace window. | `tests/sso-identity.test.mjs` §4.2 |
| **A tenant is resolved server-side and a client cannot widen it** | The tenant comes only from a verified assertion via configured mapping. A tenant identifier in a path, query, body, header or `state` changes nothing. | NFR3/NFR9 fences |
| **Verified operator MFA** | Every operator authentication requires SSO **and a second factor the platform itself verifies** — WebAuthn preferred, TOTP fallback. A misconfigured IdP MFA policy still results in denial. | `internal/adminidentity/p22_test.go` |
| **Fail closed** | An unreachable IdP issues **no** session. No cached-credential login, no fallback to a weaker mechanism. `/readyz` names `identity_provider` when it is unreachable. | NFR5 fences, `internal/api/p22_readyz_test.go` |
| **Onboarding a federated tenant is configuration, not a release** | Issuer, client id, redirect allowlist and the tenant mapping arrive as runtime injection. One image federates against different IdPs. | `lib/idp/config.ts`, task 3.4 |

### The one sentence to lead with

> *Your people sign in with your identity provider. We never see a password, we do not keep the proof it
> gives us, and we do not run an identity system of our own for anyone to breach.*

## 3. What we do NOT commit to — say this out loud, early

Each of these is a real question a buyer will ask. Answering "not yet, here is what happens instead" in
the first conversation costs a follow-up; answering it after the contract costs the account.

| Not built | What a buyer should hear |
|---|---|
| **SCIM / directory provisioning** | We do not sync your directory. Access is granted by mapping your IdP to a workspace, and a person who is not mapped is refused — we never auto-create an account from a sign-in. Deprovisioning is *your IdP stops asserting them*, which takes effect at their next sign-in — and a session they already hold ends when it expires, within the console session timeout your deployment sets. We do not receive a signal from your directory, so we do not claim to act on one. |
| **A per-seat user model, per-user audit attribution** | The platform authenticates a **tenant**, not a named person. So we cannot show "who did this" per user, and we do not bill per seat. A session can be revoked; it cannot yet say whose it was. Inventing a value for that field would be worse than the gap. |
| **Per-seat revocation** | Follows from the above. What we do have is *revocation of a session, effective at the next request*. |
| **"Verified with Okta"** (or any provider, by name) | Our OIDC and SAML implementations are standards-based and adversarially tested — against an identity provider we run, which is the only kind that can be told to misbehave on demand. What we have **not** done yet is complete a sign-in against a real customer org, so we do not put a provider's name on a slide. The first federation is a scheduled joint exercise with your IdP administrator, from a written recipe: they create an application and hand back an issuer and a client id; we register it; somebody signs in. Say this plainly — a buyer who is told "fully certified with Okta" and then spends an afternoon on an issuer mismatch remembers the first sentence. |
| **Identity for the transformed program** | The program we optimise calls **its own** providers with **its own** credentials (ADR-002). Our identity system does not reach into it, and a claim that it does would be a claim about the customer's own runtime. |
| **We choose your MFA policy for your staff** | Your IdP does. What we assert is the operator side: **our** operators cannot reach the fleet-halting surface without a factor **we** verify. |

## 4. Answering the three questions a security reviewer always asks

**"Where do the secrets live?"** In your secrets manager, resolved at the moment of use through one
mechanism the whole platform shares, reached by an ambient workload identity so wiring identity creates
no bootstrap secret. `/readyz` names the live source, so the claim is checkable from the running system
rather than from a document.

**"What happens when our IdP is down?"** Nobody signs in. That is the only safe direction to fail on an
authentication boundary, and it is a design decision rather than an accident — we will not issue a
session against a credential your IdP can no longer vouch for. Existing sessions continue until they
expire or are revoked.

**"We disable someone in Okta at 09:00. When do they lose access to your console?"** Two things happen at
two different times, and the honest answer separates them. They can start **no new session** immediately —
the next sign-in fails at your IdP, not at us. A session they already hold ends **when it expires**, within
the console session timeout your deployment configures. We do not poll your directory and we do not ask you
for a credential that would let us — a standing, read-access token into your whole user directory is a bigger
risk than the window it would close, and your security team would be right to say so. If your policy requires
push-based revocation, that is directory provisioning (SCIM / event hooks), and it is on the roadmap rather
than in the product. Shortening the session timeout is the lever available today.

**"How do you stop one customer's IdP claiming another customer's domain?"** A verified domain belongs to
an *issuer registration*, not to a global table. An assertion from IdP A carrying an address in tenant
B's domain resolves to nothing unless A is B's registered issuer. It is not a check we remember to
perform — it is a shape the mapping cannot express.

## 5. The honesty gates (build-enforced, not review-enforced)

| Gate | What it forbids | Where |
|---|---|---|
| Bundle scan | An identity secret, a PEM private key, or the tenant map in the client bundle | `web/console/scripts/scan-bundle.mjs` |
| Apply-time lint | A committed identity secret, a PEM key in the deploy tree, or a **bootstrap secret** for the secret store | `scripts/deploy/check-no-plaintext-secrets.sh` |
| gitleaks | An identity secret assigned a value, or a populated tenant map, anywhere in git | `.gitleaks.toml` |
| Claims scan | The over-claim phrases this repository has already banned | `web/console/scripts/scan-claims.mjs` |
| **No price, no plan gate on the identity path** | Any price literal or entitlement read in the seam, the `/auth/*` routes, or the sign-in copy | `tests/sso-identity.test.mjs` §9.3 |

The last one deserves its reason stated. It would be easy, and commercially tempting, to make SSO a
paid tier by adding one entitlement check to the seam. That check would be a **security-critical code
path whose behaviour depends on a billing record**, and the first billing outage would become an
authentication outage. Identity proves who you are; what you are entitled to is a separate question
asked later, by code that is allowed to be wrong without locking anybody out.
