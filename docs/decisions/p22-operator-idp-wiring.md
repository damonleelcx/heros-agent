# Wiring the operator console to a real identity provider

- **Status:** Accepted (2026-07-31)
- **Audience:** whoever points a deployment's operator console at a real IdP.

P22 built the operator identity *seam*; this document is the wiring that makes it reachable from a
browser, and the exact steps to configure it.

## The split, and why it is this way

| Half | Who owns it | Why |
|---|---|---|
| `state`, `nonce`, PKCE verifier | the operator console BFF | they are per-BROWSER, and the BFF is what set the cookie |
| the OIDC **client secret**, the code exchange, ID-token verification | the platform (`internal/adminidentity`) | every other operator credential already resolves through `providergateway.Secrets`; putting this one in the Node process would give the operator BFF a *second* credential from a *second* source, and `/readyz` would report one while the thing that mints sessions came from elsewhere |

The consequence: **the operator BFF holds exactly one credential** — `ADMIN_PLATFORM_CREDENTIAL`,
which proves a request came through it. It never sees a client secret and never sees an ID token.

## Finding your Okta issuer

The issuer is **not** the admin-console URL. `https://<org>-admin.okta.com` is where you administer
Okta; the issuer drops the `-admin`:

| What you have | What the issuer is |
|---|---|
| `https://example-admin.okta.com` (admin console) | org: `https://example.okta.com` |
| default custom authorization server | `https://example.okta.com/oauth2/default` ← **prefer this** |
| org authorization server | `https://example.okta.com` |

Confirm before configuring anything — the platform refuses a discovery document whose `issuer` does
not match what you configured, exactly:

```bash
curl -s https://<org>.okta.com/oauth2/default/.well-known/openid-configuration | jq .issuer
```

Copy that value verbatim, trailing slash and all. In the Okta Admin Console the same value is under
**Security → API → Authorization Servers → Issuer URI**.

## Configuration

Platform (`p8hermes`, or your own launch path):

```
ADMIN_IDENTITY_MODE=oidc                     # required, no default — see below
ADMIN_IDP_ISSUER=https://<org>.okta.com/oauth2/default
ADMIN_IDP_CLIENT_ID=0oa…                     # public
ADMIN_IDP_REDIRECTS=["https://admin.example.com/auth/callback"]
HEROS_ENV=production                         # refuses a test-mode issuer
ADMIN_WEBAUTHN_RP_ID=admin.example.com
ADMIN_CONSOLE_ORIGIN=https://admin.example.com
```

Operator console BFF:

```
ADMIN_IDENTITY_MODE=oidc
ADMIN_IDP_CALLBACK_URL=https://admin.example.com/auth/callback
ADMIN_API_BASE=…            ADMIN_PLATFORM_CREDENTIAL=…
```

Secrets, under their reserved logical names — `env` spells a logical name as
`HEROS_<UPPERCASE>`; a managed deployment resolves the *same* logical name from AWS Secrets Manager,
and an air-gapped one from a mounted file:

| Logical name | env spelling |
|---|---|
| `admin_oidc_client_secret` | `HEROS_ADMIN_OIDC_CLIENT_SECRET` |
| `admin_sso_signing` / `admin_mfa_signing` / `admin_session_signing` | `HEROS_ADMIN_*_SIGNING` |
| `admin_totp_seed/<admin_id>` | `HEROS_ADMIN_TOTP_SEED_<ADMIN_ID>` |

At Okta, the **Sign-in redirect URI** must equal `ADMIN_IDP_REDIRECTS[0]` and
`ADMIN_IDP_CALLBACK_URL` exactly. Three places have to agree; they are stated once each rather than
derived, because a derived origin is the defect `web/console/src/lib/redirect.ts` records at length.

## Why `ADMIN_IDENTITY_MODE` has no default

A missing value is an error at startup, not a fall back to the fixture. Booting with a fixture issuer
while an operator believes the real IdP is live is the failure this refusal exists to prevent — and it
is the one that would never surface, because everything would appear to work.

Setting it to `oidc` also **closes the fixture door**: `TestModeIdP` is nil, `/admin/api/testmode/assert`
404s, and there is no second way in.

## Registering the redirect URI at Okta — the step that is easy to miss

The redirect URI must be listed in the Okta app itself, and Okta refuses the whole authorization
request until it is. The refusal is early and specific, which is the good case: the browser never
reaches a login form and Okta names the app to fix.

> `400 Bad Request` — *The 'redirect_uri' parameter must be a Login redirect URI in the client app
> settings*, with a link straight to `.../admin/app/oidc_client/instance/<app-id>#tab-general`.

**Applications → your app → General → Sign-in redirect URIs → Add**, and the value must equal
`ADMIN_IDP_REDIRECTS[0]` and `ADMIN_IDP_CALLBACK_URL` character for character — scheme, host, port and
path. `http://localhost:4318/auth/callback` and `http://127.0.0.1:4318/auth/callback` are different
URIs to Okta, and so are trailing-slash variants.

## What still requires a human

Completing a sign-in means authenticating at Okta with a real password, at a real keyboard. Nothing
here automates that, deliberately. And a principal with no **platform-enrolled** factor will be
refused on return with `ErrMFARequired` — that is NFR8 working, not a fault. Enrol first:

```
POST /admin/api/mfa/enroll     { "admin_id": "...", "kind": "webauthn"|"totp", … }
```

It is gated on `role.grant` (Superadmin). Self-service enrolment is deliberately absent: an attacker
holding one authenticated session would otherwise enrol their own factor and convert a temporary hold
into permanent access.
