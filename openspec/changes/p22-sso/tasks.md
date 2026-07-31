# Tasks — P22: SSO & Identity

Ordered by workstream. P22 replaces the ADR-008 seam on two disjoint identity domains and adds the redirect/
callback routes each mechanism needs — and **nothing above the seam**. Each task is independently verifiable.
Every PR carries an **identity-form impact matrix** (customer OIDC / customer SAML / customer configured /
operator) with every "not affected" row explaining *why*.

## 1. System Designer + Backend — Decide the one-way doors first (blocks everything else)
- [x] 1.1 Ratify **D1 (the seam is the only thing that changes)**, **D2 (OIDC primary, SAML enterprise, one
      seam)**, and **D5 (operator IdP real+pluggable behind the existing seam; disjoint domain)** in `design.md`
      — these are the published federation contract (a one-way door), decided before any verifier is written.
      (`design.md` § *Ratification record*, with the fence each door is held by.)
- [x] 1.2 Define the **federation contract**: the trusted issuer set, the claims mapped (`sub`, `email`,
      domain / `NameID`), and the assertion validity/freshness bounds — one source the OIDC and SAML verifiers
      both derive from. (`design.md` § *The federation contract*; the single source is
      `web/console/src/lib/idp/federation.ts` — bounds, claim set, issuer rule, refusal string and tenant
      resolution all live there and neither verifier redefines one.)
- [x] 1.3 Confirm the **tenant-mapping config shape** (`domain` / `per-issuer` / `jit` with an explicit allow
      rule), injected like `CONSOLE_TENANT_ASSERTIONS` / via the `Secrets` seam — no compiled per-tenant branch.
      (`design.md` § *Tenant-mapping config shape*; `CONSOLE_IDP_TENANT_MAP`, parsed and validated at load by
      `parseTenantMap`, with `verified_domains` nested under the issuer registration — the whole of NFR9.)

## 2. Backend — The customer seam implementation (OIDC first, behind the unchanged contract)
- [x] 2.1 Implement the **OIDC (Authorization Code + PKCE)** provider behind `verify(assertion) → { tenantId }`:
      discovery/JWKS validation, `state`, `nonce`, ID-token verification; the seam contract is unchanged.
      (`lib/idp/oidc.ts` + `lib/idp/jwt.ts`: discovery with an `issuer` self-check, JWKS with an asymmetric-only
      algorithm allowlist — `alg:none` and every `HS*` have no landing site — S256 PKCE, `nonce` compared in
      constant time, `azp` checked when present. No branch can emit an implicit-flow request.)
- [x] 2.2 Implement the **tenant mapping** (domain / per-issuer / JIT-under-allow-rule) from injected config;
      refuse an unmapped identity as a **security event**; honor only a **proven** domain (no cross-tenant
      resolution). (`lib/idp/federation.ts` `resolveTenant` — the issuer must be registered *before* any domain
      is compared, and `verified_domains` is nested under the registration, so cross-tenant resolution is not a
      case the function can express. Unmapped ⇒ `logIdentity({event:"unmapped_identity"})`, no tenant created.)
- [x] 2.3 Ensure the **assertion is dropped** — verified, exchanged for a session, never stored/cookied/logged/
      traced/carried upstream; the BFF's own credential authorizes upstream calls. (`lib/identity.ts`: the token
      is an argument, read once, never returned or stored; `logIdentity` has **no parameter** that could carry
      it; `issueSession` records only `{id,tenantId,issuedAt,expiresAt}` as before. Fenced by 8.3.)
- [x] 2.4 Implement the **SAML 2.0** provider behind the same seam: signed assertion, audience restriction,
      allowlisted ACS; resolves to exactly one `tenantId`. (`lib/idp/saml.ts` + `lib/idp/xml.ts` — a
      prefix-preserving reader and a real exclusive-c14n writer, checked against the W3C exc-c14n spec example;
      signature wrapping closed structurally by returning *the element the digest covered* and reading claims
      only from inside it; SHA-1 absent from the algorithm allowlist; SP-initiated AuthnRequest signed with the
      `Secrets`-seam key.)

## 3. Frontend — The redirect/callback routes and the no-key/shell rules
- [x] 3.1 Add `/auth/login`, `/auth/callback`, and the SAML **ACS** route in the BFF; set the single-use
      browser-bound `state` + PKCE verifier as `HttpOnly`, consume the assertion server-side, call the existing
      `issueSession()`. (`app/auth/login|callback|saml/acs/route.ts`. The browser holds an opaque **flow id** in
      an `HttpOnly` cookie; the `state`, PKCE verifier and `nonce` never leave the process. The flow is consumed
      — deleted — *before* verification, so single-use means single USE, not single success. Both flows verified
      end to end in a real Chrome via `npm run dev:sso` / `npm run dev:sso -- saml`.)
- [x] 3.2 Honor the **shell rule**: an unauthenticated request or a failed callback **redirects** to sign-in with
      a `reason` (session-ended vs sign-in vs IdP-unreachable vs not-provisioned) — never a broken shell.
      (Five states, single-sourced in `content/identity.ts`; every route failure path returns a redirect and
      clears the spent flow cookie. `/auth/login` fails closed at the FIRST hop when the IdP is unreachable
      rather than handing the user a broken page on somebody else's domain.)
- [x] 3.3 Honor the **no-key rule**: the client secret and platform credential stay server-side; the browser
      holds only the opaque `HttpOnly` session token; the ID token / SAML assertion never reaches client JS or a
      URL fragment. Extend the **bundle scan** to the client id/secret surface. (`scan-bundle.mjs` gains the
      identity env names plus shape patterns for a PEM private key and the two `Secrets` logical names;
      `CONSOLE_IDP_CLIENT_ID` is deliberately excluded — it is public by protocol design and flagging it would
      teach people to disable the fence. Confirmed in Chrome: page script sees no session cookie, no JWT in the
      DOM, and nothing in `localStorage`/`sessionStorage`.)
- [x] 3.4 Keep **build artifact and runtime config separate**: issuer, client id, redirect allowlist and mapping
      strategy arrive as **runtime** injection so one image federates against different IdPs without a rebuild.
      (`lib/idp/config.ts` reads the running process's environment and validates fail-static at load; nothing
      identity-shaped is a compile-time constant. The same built image ran against an OIDC IdP and then a SAML
      IdP in this session with no rebuild between them.)

## 4. Backend — Session, revocation, fail-closed (assert unchanged above the seam)
- [x] 4.1 Assert by **regression test** that the session store, cookie flags, revocation, scope derivation and
      fail-closed middleware are **unchanged** by P22 (ADR-008 Rule 3, NFR1) — the only changed files are the
      seam and the `/auth/*` routes. (`tests/sso-identity.test.mjs`: pinned sha256 of `session.ts`,
      `middleware.ts`, `scope.ts`, `entitlements.ts`; the *session cookie's own* declarations asserted by value
      in the grown `cookies.ts`; plus two semantic fences — no mechanism word above the seam, and an allowlist
      of the only files permitted to name one.)
- [x] 4.2 Confirm **revocation is immediate, no grace** (store read every request) and that a **refresh
      re-verifies** rather than silently extends; no self-vouching token is used as the session. (Four live
      tests: the NEXT request after revoke is denied; a revoked token is not resurrected by a fresh sign-in;
      the browser token is 32 CSPRNG bytes with no JWT structure; and a session's lifetime is computed in
      exactly one place — "no refresh extends" is an ABSENCE, so it is fenced by a grep, which is the honest
      form for a path that must not exist.)
- [x] 4.3 Implement **fail-closed** sign-in: an unreachable IdP (OIDC discovery/JWKS or SAML metadata) issues
      **no session**; no cached-credential login; no silent fallback to a weaker mechanism. (**A real defect
      this test found:** discovery is cached for five minutes, so an IdP that died four minutes ago still
      produced a well-formed authorization URL and `/auth/login` redirected the user onto a dead host. No
      session was issued — the letter of fail-closed held — but the person was dropped on somebody else's error
      page and our own surface never learned. Fixed with `ensureReachable` / `ensureMetadata`: the login hop
      confirms the IdP answers NOW, against the live provider, not against a warm cache.)

## 5. Backend — Identity security posture (replay / CSRF / open-redirect / secrets)
- [x] 5.1 Enforce **CSRF/replay defenses** at the callback: single-use browser-bound `state`, `nonce`, PKCE, an
      assertion **freshness window** + a **one-time** guard; a single generic refusal reason. (`lib/idp/flow.ts`
      holds the two halves — an opaque flow id in an `HttpOnly` cookie and the `state` in the URL — and neither
      alone completes a sign-in. All five defenses have a named red-able test in `sso-identity.test.mjs`; every
      refusal goes through one `refuse()` constructor, so a future call site cannot invent a more helpful
      message and rebuild the oracle.)
- [x] 5.2 Enforce the **redirect / SAML ACS allowlist**; refuse an off-allowlist or reflected/wildcard target.
      (`allowedRedirect` compares origin+path for EXACT equality — not `startsWith`, not a hostname check;
      `urlAllowlist` refuses a `*` at load, so a wildcard is not a mistake a deployment can make. Checked twice
      on the SAML path, and the two checks ask different questions: is our published ACS on the list, and does
      the assertion's own `Destination`/`Recipient` name an allowlisted one.)
- [x] 5.3 Resolve **every identity secret** (OIDC client secret, SAML SP private key, session/signing keys)
      through the `Secrets` seam with an ambient identity and **no bootstrap secret**; add the apply-time lint +
      extend gitleaks so a committed identity secret fails CI. (`lib/idp/secrets.ts` presents the platform's
      `HEROS_SECRETS_SOURCE` contract in the console's own process with `env` and `file` sources, read at the
      MOMENT OF USE so a rotation lands without a restart, and with no AWS client inside a browser-facing
      process — the managed path is a CSI/agent-projected file the pod is authorised for ambiently, which is
      the same "no bootstrap secret" property `secrets-baseline.md` §1.1 calls the L1 tiebreak.
      `check-no-plaintext-secrets.sh` gains three legs — identity names, any committed PEM private key, and a
      **bootstrap secret for the secret store itself** — and each was proven able to go red before being
      claimed. `.gitleaks.toml` gains two rules; the allowlists needed `regexTarget = "line"`, which was found
      by running the scanner against a probe file and watching `change-me-…` get flagged anyway.)

## 6. Backend — Operator SSO + MFA made real (P8 surface)
- [x] 6.1 Implement a **real, pluggable OIDC/SAML admin IdP** behind the existing
      `adminidentity.IdentityProvider` seam (new provider kinds alongside `admin-idp-hmac`), keeping
      `Verify`/`Describe`; the fixture `TestMode` issuer refuses production. (`oidcprovider.go` +
      `samlprovider.go`, with `jws.go` and `xmlc14n.go` behind them — both stdlib-only, because a JWT or XML-DSig
      dependency on the operator authentication path is unread code inside the highest-blast-radius boundary in
      the platform. `Assertion` grew `Token`/`Nonce`/`Factor` additively rather than sprouting a second
      interface, which is the leak ADR-008 forbids. `NewAuthenticatorFor` refuses to construct a production
      authenticator around a `TestMode` provider — the same guard `identity.ts` applies to
      `CONSOLE_TENANT_IDENTITY=dev`, on the surface that can halt the fleet.)
- [x] 6.2 Add a **platform-verified second factor** (WebAuthn preferred, TOTP fallback): valid SSO + no verified
      factor ⇒ **no session** (`ErrMFARequired`); the platform's verification, not the IdP's claim, is the
      invariant. (`mfa.go`. Both real providers return an **empty `MFAFactor`** — they do not read `amr`, `acr`
      or `<AuthnContextClassRef>` — and the test proves it by minting an ID token that claims `amr:["mfa"]` and
      asserting no session is issued. WebAuthn checks origin, RP-ID, user *verification* (not merely presence),
      the server-minted challenge and the signature counter; TOTP is RFC 6238 with a one-time guard per
      (principal, step). Wiring a real provider with no verifier **refuses to construct**, because that failure
      looks like success — every login works and the surface has quietly become single-factor.)
- [x] 6.3 Keep the operator domain **disjoint** (separate origin, disjoint cookie jar, principal type with no
      tenant_id, no promotion path from `auth.Principal`); **disable ⇒ revoke all sessions** for the principal.
      (Disjointness was already structural and is re-asserted by the pre-existing
      `TestCustomerPrincipalCannotReachAdminCapability`. What P22 adds is the **reconcile read**: `Authorize`
      now asks whether the principal is still active, so offboarding lands even when somebody disables and
      forgets to revoke — 🔴 `event-write-reconcile-read`, because "A must be accompanied by B" held by
      convention is an invariant one hurried call site breaks. `Offboard` is the explicit door that does both,
      disable first so no new session can be obtained between the two writes.)
- [x] 6.4 Source operator identity secrets from the manager under the reserved logical names, **fail closed**,
      and report the live `admin_idp` on `/readyz`. (`Secrets` gains `Named` for the per-principal secrets P22
      introduces; `TOTPSeedName` derives the reserved name so an enrollment cannot invent one nobody
      provisioned, and **the seed never enters the enrollment directory** — that record is an index, and a
      directory row carrying a seed is a credential store with an ordinary backup policy. `/readyz` is task 7.1.)

## 7. DevOps + Backend — Readiness and secret wiring
- [x] 7.1 Extend `/readyz` (`internal/api/server.go`) to aggregate **`identity_provider: {kind, issuer,
      reachable}`** (customer) alongside the existing `admin_idp` and `secrets_source`, naming it when degraded;
      the signal measures **reachability**, not traffic, and does not depend on the traffic it gates.
      (`IdentityProbe` + `HTTPIdentityProbe` read the console's `/api/health` identity block — the platform asks
      the component that actually depends on the customer IdP rather than probing it independently, so there is
      one answer instead of two that can disagree. `admin_idp` was previously only on the credential-gated
      `/admin/api/readyz`; it is now on the public readiness endpoint too, because "is this box still pointed at
      the test-mode fixture" is asked from outside the operator console. Two components, not one:
      `customer_console` says the surface is down, `identity_provider` says the surface is up and nobody can
      sign in. Fenced by `internal/api/p22_readyz_test.go`, including "an unreported block is not a green one".)
- [x] 7.2 Wire identity secrets to the store paths — env (dev), AWS Secrets Manager (managed, per
      `secrets-baseline.md`), an on-prem equivalent for air-gapped — with **no bootstrap secret**; the
      provider→secret mapping is **configuration, not code**. (k8s: four new `ExternalSecret` references
      materialised at apply time by the External Secrets Operator from the store, reached by an **ambient**
      workload identity — the operator IdP's client secret is a *different remote key* again, because the two
      identity domains are disjoint down to their credentials. Compose + env-example: `${VAR:-}` interpolation
      and names only. Air-gapped: `HEROS_SECRETS_SOURCE=file` + `HEROS_SECRETS_DIR`, read at the moment of use.
      Adding a federated customer is a change to a `remoteRef` and a tenant map, never a release.)

## 8. QA — The acceptance gate (adversarial before happy-path green)
- [x] 8.1 **Seam-unchanged** regression: assert the whole layer above the seam is byte-for-byte unchanged (NFR1).
      (Pinned digests + two semantic fences. See 4.1.)
- [x] 8.2 **Cross-tenant resolution refused** (NFR9, first): a self-asserted domain from a foreign IdP is
      refused; JIT never crosses a tenant boundary; an unmapped identity is a security event, not a signup.
      (The test deployment registers **two** issuers on purpose — a single-tenant map would make this
      vacuous — and the foreign IdP presenting `attacker@other.test` is refused with `not_provisioned` while
      the security event lands in the console's log. An unverified `email` maps nothing at all.)
- [x] 8.3 **Assertion-never-persisted** grep (NFR2) and **forged-tenant-never-widens** (NFR3), tested
      adversarially in every client-controlled position. (Structural rather than by grep alone: `logIdentity`
      has no field that could carry an assertion, and `Session` has no field that could store one. The live
      half decodes the SAML response the IdP actually sent and asserts neither it nor the `NameID` appears in
      any log line or `Set-Cookie`.)
- [x] 8.4 **Security mechanisms tested by removal** (NFR7): replayed `state`, reused code, stale/replayed
      assertion, off-allowlist redirect each **refused**; removing a defense turns a test **red**. (Every case
      MUTATES a genuinely valid message — a real ID token whose header becomes `alg:none`, a real signed SAML
      assertion with one character changed after signing — because a hand-written invalid message only proves
      the verifier rejects garbage, and an attacker does not send garbage.)
- [x] 8.5 **Fail-closed** (NFR5): IdP down ⇒ **no session**, no cached-credential login; `/readyz` names
      `identity_provider`. (This is the test that found the warm-cache defect in 4.3. It now brings its own
      console and IdP so the suite is order-independent — sharing one left every later test federating against
      a dead port, which is a test that passes for the wrong reason.)
- [x] 8.6 **Operator MFA by denial** (NFR8): valid SSO + no verified factor ⇒ no session; a **disabled operator**
      obtains no session and their live sessions are revoked on disable; the operator surface is **unreachable
      from a customer origin** (cross-origin test). (`internal/adminidentity/p22_test.go` +
      `internal/api/p22_crossorigin_test.go`. The cross-origin test asserts the BROWSER's boundary rather than
      routing: no `Access-Control-Allow-*` on any admin path including the open ones, no preflight answer, and
      an unknown admin path answers 401 rather than 404 so an unauthenticated caller learns nothing.)
- [x] 8.7 **Identity-form matrix**: customer OIDC, customer SAML, customer configured (open-core), and operator
      are each exercised; a capability that works in only one form is a bug. (The post-sign-in assertion is
      written ONCE and run against each form, so it cannot drift for one of them. The **operator** form is
      deliberately not run through the console harness — it is a different process, language and origin, and
      pretending otherwise is the domain blurring the two-domain split exists to prevent; the matrix names its
      three evidence files and the test reads them, so the coverage claim stays checkable rather than implied.)

## 9. Product Designer + Sales Operations — Messages and the honest boundary
- [x] 9.1 Write the sign-in / MFA messages: define every term; distinguish **"session ended" / "sign in" / "IdP
      unreachable" / "not provisioned for this tenant"**; leak **no internal mechanism** (secret logical name,
      provider-kind literal, issuer allowlist); single-source the glossary. (`src/content/identity.ts` — five
      states, distinct because the reader's NEXT ACTION differs in each, which is the only test that matters
      for whether two messages should be one. Two fences: user-facing strings are extracted and checked against
      a forbidden-term list, and every `reason=` a route can emit must map to a state — a reason with no entry
      would silently render the generic prompt. Both proven able to go red.)
- [x] 9.2 Encode the **honest commitment boundary**: sell **SSO federation (OIDC + SAML) + verified operator
      MFA** (built); do **not** commit SCIM, a per-seat user/audit model, or transformed-program identity
      (ADR-002); state that we run **no password database / home-grown IdP** as a differentiator; per-seat
      revocation is a **next-request** effect, stated as such. (`docs/sales/P22-identity-copy.md`, following
      the P21 billing-copy pattern: a shipped table where every row names its test, a **not-built** table
      written as the answer to give in the first conversation rather than after the signature, and the three
      questions a security reviewer always asks. Revocation is stated as *the very next request is denied,
      with no grace period* — never "instant", which is the word a reviewer asks us to defend.)
- [x] 9.3 Honesty gates: **no price value and no plan gate** on the identity path (identity proves *who*;
      entitlement is P7, payments P21); no operator-facing message implies SSO is paywalled by wiring a plan
      check into the seam. (A build gate, not a review note. Making SSO a paid tier is one entitlement check
      away and commercially tempting — and that check would be a security-critical code path whose behaviour
      depends on a billing record, so the first billing outage becomes an authentication outage. The test
      walks the whole identity path and refuses `entitlement`, `plan`, `billing` and any price literal;
      verified red by adding one such symbol to `lib/idp/oidc.ts`.)

## 10. Documentation & fold-in
- [x] 10.1 Cross-link the PRD, this change, and the ADRs it inherits (002/004/006/008) and `secrets-baseline.md`;
      add the P22 row to `docs/prd/README.md`. (The row already existed; what was missing was the **implemented**
      paragraph, which now names both folded capabilities, the sales doc, and the two things worth reading even
      if you never touch the code — the seam did not move, and operator MFA is now an invariant rather than a
      claim. Every link in the folded specs and the README was resolved against the filesystem, not eyeballed.)
- [x] 10.2 On deploy, fold the two delta specs (`sso-identity`, `operator-sso-mfa`) into `openspec/specs/` (drop
      the `## ADDED` headers). (Folded, following the P21 precedent: the change header and the delta framing are
      dropped — a folded spec describes what IS, not what a change adds — the inherited-ADR cross-links move to
      the top, and the relative paths are rewritten one level. 16 requirements in `sso-identity`, 5 in
      `operator-sso-mfa`.)

## 11. Backend + DevOps + Sales Ops — The real identity provider (Okta first), and the offboarding claim corrected

Sections 1–10 are complete and their fences are proven able to go red. What they prove is that the mechanisms
are correct and the refusals are real — against an IdP **this repository runs**, which is the only kind of IdP
that can be *instructed* to send a stale assertion. It is the right shape for a refusal and it proves nothing
about acceptance. This section is the other half: one **real** provider, on both domains, plus the four
behaviours a real provider has that a fixture does not, and one claim that has to get smaller.

- [ ] 11.1 **Exact-match issuer registration, with a named diagnosis for the mismatch.** Validate registrations
      at load (absolute `https`, no trailing slash); compare by equality only. A well-formed but unregistered
      issuer SHALL produce a **named configuration diagnosis** for the operator, distinct from "an identity we
      do not recognise", while the user still receives one generic refusal. Okta asserts three legitimate
      issuer forms (org authorization server / custom authorization server / custom domain) — the failure this
      catches is a correct refusal with an opaque cause. *(FR22, D11)*
- [ ] 11.2 **Rotation as normal operation.** Select the key by `kid` across a set carrying several; on an
      unknown `kid`, refresh **at most once per bounded interval**, then refuse — asserted by **counting
      fetches**, not by reading the code. A rotation SHALL NOT invalidate sessions already issued. *(FR23, D12)*
- [ ] 11.3 **A bounded call budget into somebody else's system.** Cache discovery, key-set, metadata and
      readiness fetches with a floor. An org's per-endpoint limits are shared with every other application the
      customer runs on it, so an unbounded probe degrades systems that are not ours. *(FR24, NFR13)*
- [ ] 11.4 **SAML as a real IdP emits it.** Accept an **assertion-level** signature (response signing
      optional), an audience equal to our entity id, a `Recipient` on the ACS allowlist, RSA with SHA-256 or
      stronger. The registration SHALL hold **every currently-valid certificate** (or resolve the set from IdP
      metadata) so a rollover is not an outage; a certificate outside its validity window is refused. *(FR25)*
- [ ] 11.5 **Verified email as defence in depth on domain mapping.** An identity from a registered issuer whose
      email is not asserted as verified SHALL NOT resolve a tenant by domain. The registration remains the
      trust anchor. *(FR26, FR8)*
- [ ] 11.6 **Operator MFA proven against a real org.** An ID token **that org issued**, carrying an `amr` value
      claiming multi-factor authentication, SHALL still yield **no** session without a platform-verified
      factor. The invariant is only convincingly demonstrated when the system making the claim is not ours.
      *(FR28, NFR8)*
- [ ] 11.7 **The offboarding claim, corrected everywhere it appears.** No directory back-channel: a
      deactivated user starts **no new session** and keeps an existing one until it **expires**, bounded by the
      published console session TTL. PRD, sales copy, docs and UI each state **which** revocation they mean.
      Polling the customer's directory is rejected and the reason is recorded — it would require them to issue
      us a standing, high-privilege directory credential. *(G11, FR27, NFR14, D13)*
- [ ] 11.8 **The registration recipe**, per mechanism and per domain: what the customer's admin creates, what
      they hand back, what we hand them. **No secret in it**; every value has one owner and one destination;
      it names **which** issuer form to paste. Verified by somebody who did not write it following it front to
      back. *(FR29)*
- [ ] 11.9 **No provider branch.** The diff for this section is configuration, documentation and tests. A
      provider brand appearing in verifier **logic** — not a comment, a fixture, or a document — is a review
      failure. *(NFR12, D10)*
- [ ] 11.10 **Point the existing probe at the real org, then have a human finish the job.**
      `PROBE_ISSUER` / `PROBE_CLIENT_ID` → [`liveidp_test.go`](../../../internal/adminidentity/liveidp_test.go)
      answers the credential-free questions (discovery parses, key set loads, the authorization URL is code
      flow + S256 with no implicit response type). Completing a sign-in comes after and is a person at their
      own keyboard — the same boundary P21 draws around typing a card number.
- [ ] 11.11 **Fold in.** On deploy, fold this section's added requirements into the two live capability specs
      (`openspec/specs/sso-identity`, `openspec/specs/operator-sso-mfa`), dropping the `## ADDED` headers, the
      same way §10.2 folded the originals.

## Verification record

### The identity-form impact matrix (V2 — the template every P22 PR carries)

Four forms. A capability that works in only one of them is a bug, and a "not affected" row has to say
*why* rather than being left blank.

| Change area | customer OIDC | customer SAML | customer configured (open-core) | operator |
|---|---|---|---|---|
| Seam implementation (`lib/identity.ts`, `lib/idp/*`) | **Affected** — the mechanism | **Affected** — the mechanism | **Affected** — kept working unchanged; the credential form is still the only sign-in path and is asserted present | Not affected — a different process, language and origin (`internal/adminidentity`); the two identity domains share no code |
| `/auth/*` routes | **Affected** — login + callback | **Affected** — login + ACS | Not affected — no redirect flow exists, and `/auth/login` redirects to the credential form rather than pretending to federate | Not affected — the operator console has its own BFF on its own origin |
| Above the seam (session, cookie, revocation, scope, middleware) | Not affected — **byte-for-byte**, fenced by pinned digests | Not affected — same fence | Not affected — same fence | Not affected — a separate session store with its own TTL and signing key |
| Sign-in surface (`signin/page.tsx`, `content/identity.ts`) | **Affected** — SSO action | **Affected** — SSO action | **Affected** — credential form retained; a redesign may not drop a capability | Not affected — the operator console renders its own sign-in |
| Readiness (`/readyz`) | **Affected** — `identity_provider` | **Affected** — `identity_provider` | **Affected** — reports `reachable: true`, because a static map is always serviceable and reporting `false` would page for a dependency the deployment does not have | **Affected** — `admin_idp` moved onto the public readiness endpoint |
| Secrets (`Secrets` seam, lint, gitleaks) | **Affected** — client secret | **Affected** — SP private key | Not affected — federates with nobody, so there is no identity secret to source | **Affected** — admin IdP client secret + per-principal TOTP seeds |
| Second factor | Not affected — the customer's IdP owns their MFA policy; claiming otherwise would be a claim about somebody else's system | Not affected — same reason | Not affected — same reason | **Affected** — platform-verified WebAuthn/TOTP is the whole of NFR8 |

### V1 — M16 exit checklist

- [x] V1 M16 exit checklist (PRD §13) fully green across all four identity forms (customer OIDC / customer SAML /
      customer configured / operator). Every box in [PRD §13](../../../docs/prd/P22-sso-identity.md) names where
      it is proven. **One PRD recommendation was not followed and is recorded rather than quietly skipped:**
      §14 Q1 suggested a well-audited OIDC/SAML library; the implementation uses the language standard libraries
      with hand-written envelope/canonicalization layers instead, for the reason and with the compensating
      evidence written into §13. It drops in behind the same seam later with no caller change.

### V2 — the fences, proven able to go red

- [x] V2 Identity-form impact matrix attached to every P22 PR, with the seam-unchanged (NFR1), assertion-never-
      persisted (NFR2), tenant-never-widened (NFR3) and cross-tenant-refused (NFR9) fences proven able to go red.

      A fence that has never failed is a fence nobody has checked. Each of the four was **deliberately broken,
      observed red, and restored** — not reasoned about:

      | Fence | The defence removed | Result |
      |---|---|---|
      | **NFR1** seam unchanged | one line appended to `lib/session.ts` | `8.1/4.1 the layer above the seam is byte-for-byte unchanged` — RED |
      | **NFR2** assertion never persisted | a `token?: string` field added to `logIdentity` | `8.3 nothing on the identity path can log or store an assertion` — RED |
      | **NFR3** client never widens the tenant | `forward()` made to send a tenant that is not the session's | `8.3 NFR3 · a forged tenant in EVERY client-controlled position` — RED |
      | **NFR9** no cross-tenant resolution | `resolveTenant` made to search every registration for a matching domain (the flat-table mistake) | `8.2 NFR9 · a self-asserted domain cannot claim another tenant's registration` — RED |

      Two further gates were red-checked the same way while being written: the identity legs of
      `check-no-plaintext-secrets.sh` (an identity secret in an env-example, a committed PEM key, a bootstrap
      secret for the store), and the two honesty gates in §9 (a mechanism word in user-facing copy, an
      entitlement symbol on the identity path).

### V3 — the real identity provider (Okta): **NOT RUN**

- [ ] V3 The real-IdP exit checklist ([PRD §13.1](../../../docs/prd/P22-sso-identity.md)) green against a
      **named** real org, on all four identity forms. **NOT STARTED**, and it is a separate record from V1 for
      a reason worth stating rather than assuming: **V1 is a claim about the platform; V3 is a claim about an
      org.** V1's IdP is one this repository runs, which is what makes its refusals provable — a fixture can be
      told to send a stale assertion, a wrapped signature, or a token signed with the wrong key, and Okta
      cannot. Those tests stay where they are. What moves to the real org is the half a fixture cannot
      establish: that a real discovery document parses, a real key set loads and rotates, a real assertion
      shape verifies, and that an ID token *that org issued* claiming `amr:["mfa"]` still yields no session.

      [`liveidp_test.go`](../../../internal/adminidentity/liveidp_test.go) has been sitting ready for this
      since the module was written and **skips**, because no org has been pointed at it. That is the honest
      state: not a gap discovered late, a step nobody has taken yet.

      One box on that checklist is not automatable by design — a human completing a sign-in with their own
      credentials at their own keyboard — and the probe stops deliberately short of it.

### Suite state at completion

| Suite | Result |
|---|---|
| `web/console` (`npm test`) | **420/420 pass** — 48 of them P22's, added by this change |
| Go (`go test ./...`) | **all packages pass**, including 21 new P22 cases in `internal/adminidentity` and `internal/api` |
| `npm run build` (token/string/markup/claims/bundle scans) | **pass** |
| `make deploy-lint` (digest pins, image parity, no plaintext secrets) | **pass** |
| Rendered-browser acceptance (R11) | OIDC and SAML sign-in driven end to end in Chrome; page script sees no session cookie, no token in the DOM, nothing in storage |

### The hermes-agent run

`cmd/proof/identity` runs P22 against a real `github.com/nousresearch/hermes-agent` checkout
(HEAD `dbe14424ed192b83993e5655629b0dd5714f3355`, tenant `cus_nousresearch`).

It asks a different question from every previous hermes runner, and the difference is the point. The
others ask *what does the platform do TO this repository*. P22 supplies identity for the people who
**operate** the platform and for the **tenant** that owns the work — never for the transformed program
(ADR-002) — so the most useful thing the runner produces is a measurement of that boundary:

| Identity surface found in hermes-agent | Files | In P22 scope |
|---|---|---|
| sessions / login | 1960 | **No** — hermes's own runtime |
| api keys / bearer tokens | 778 | **No** |
| provider credentials | 530 | **No** — it calls its own providers with its own keys |
| OAuth / OIDC | 506 | **No** |
| SAML | 0 | — |

2551 of 5580 source files handle identity or credentials in some form, and **every one is out of
scope**. That is the finding rather than a caveat: it turns ADR-002 from a sentence into a number a
future change would have to move, and moving it is the review conversation.

What the runner then exercises for real, in-process, against the operator seam:

| Case | Result |
|---|---|
| valid SSO, the ID token **claims** `amr:["mfa"]`, no platform-verified factor | **denied** (`ErrMFARequired`) — NFR8 |
| valid SSO + platform-verified TOTP | session issued, factor recorded as `totp` |
| WebAuthn signed on an attacker's origin | **denied** — origin binding |
| a **disabled** operator's live session, next request | **denied** — the authorization path reconciles against the principal |
| `/readyz` with the IdP answering / unreachable | `200` / `503` naming `identity_provider` with `{kind, issuer, reachable}` |

The **customer** seam is not exercisable from a Go binary — it is the console's, in TypeScript
(ADR-008) — and the runner says so rather than printing a green tick for something it did not run. It
was run separately, for the same tenant: `TENANT=cus_nousresearch npm run dev:sso` completed a full
OIDC Authorization Code + PKCE sign-in in a real Chrome, and the console rendered as
`cus_nousresearch`.
