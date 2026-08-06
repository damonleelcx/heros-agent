# P28 Email & Password — what we sell, and the P22 claim it retires

- **Status:** Accepted (2026-08-05)
- **Audience:** anyone who writes a sentence about identity that a customer, a prospect or a security
  reviewer reads.
- **Reads with:** [P22 identity copy](P22-identity-copy.md), whose four rules apply here unchanged, and
  whose §2 table contains one claim this phase makes conditional.

## 1. 🔴 The correction, first, because it is the whole reason this file exists

P22's §2 table sells this as a differentiator:

> **We run no password database and no identity provider of our own.** There is no password store, no
> credential recovery flow, and nothing to breach.

**On a deployment running the `password` sign-in method, every clause of that is false.** There is a
password store (`user_password`), there is a credential recovery flow (`/forgot-password`), and there is
therefore something to breach. Saying it anyway would be the exact failure the P22 rules open with — an
over-claim discovered by the person best equipped to discover it, in a security questionnaire, after the
sentence has been in three RFP answers.

The claim is not deleted, because it is still **true and still a differentiator on the federated
deployments most enterprise buyers run**. It becomes conditional, and the condition is stated:

> *If your people sign in with your identity provider, we run no password store — your IdP proves who
> someone is, and we only ask it. If you use our own email-and-password sign-in instead, we store a
> per-account argon2id hash and nothing else; we never see or keep the password itself.*

🚫 **Never say "we run no password database" without knowing which sign-in method the customer will run.**
If you do not know, ask. It is one question, and it is the difference between a differentiator and a
retraction.

## 2. What P28 sells

| Claim | What it means | Where it is proven |
|---|---|---|
| **A person can create their own account and sign in — no operator, no cluster access** | An email address and a password they choose. Sign-up creates the organization, the owner, the free account and the password in one transaction, or none of them. | `internal/api/passwordauth_test.go`, `web/console/tests/password-identity.test.mjs` |
| **The same credentials work in the terminal** | `heros login` takes the address and the password (never as a flag), stores a **personal** credential, and reports which kind it stored. | `internal/clilink/passwordlogin_test.go` |
| **Passwords are stored as argon2id with a per-account salt** | 64 MiB, 3 passes, 4 lanes; the parameters travel in the stored value, so raising the cost is a deploy rather than a migration. Three independent layers refuse anything else: an AST fence, a store check, and a database CHECK. | `internal/password/fence_test.go`, migration `0041` |
| **A forgotten password is recoverable without a human** | A single-use link, one hour. Completing it **ends every session and every personal credential that person holds**, and the screen names the machine credentials it did not touch. | `TestResetEndsEverythingThatPersonHeldAndSaysWhatItDidNot` |
| **We do not tell anyone whether an address is registered** | Sign-in, sign-up and forgot-password answer identically for a known and an unknown address — in the body **and on the clock**, because a real argon2id verification runs on the "no such address" branch too. | `TestSignInDoesNotDiscloseWhetherAnAddressIsRegistered` |
| **Removing a member ends their access in the terminal too** | The credential `heros login` stores names the person. This was **not true** on the previous production sign-in, where the only credential was a shared string that member removal could not revoke. | P27's removal path, now reachable from the CLI |
| **SSO is unchanged and still selectable** | `configured`, `oidc`, `saml` and `platform` behave exactly as before. A customer who federates keeps federating. | `TestTheCredentialSeamStillRendersItsOwnForm` |

### The one sentence to lead with

> *Your people can sign themselves up and sign themselves in — or your identity provider can do it for
> them. Either way nobody has to ask an administrator for a secret, and removing somebody ends their
> access everywhere at their next request.*

## 3. What we do NOT commit to — say this out loud, early

- **No second factor on customer accounts.** Operator MFA exists (P22) and is untouched. TOTP or WebAuthn
  for a customer account has nowhere to attach until an account exists, which is what P28 builds; it is a
  later phase and not a roadmap tease. If a buyer requires customer MFA today, the answer is SSO.
- **No social sign-in.** No Google or GitHub buttons. Each is a separate published contract with its own
  consent surface.
- **No password policy engine.** A 12-character floor, a bundled common-password list, and a refusal of
  passwords containing the person's own address. 🔴 **Do not describe the blocklist as "checked against
  breach databases"** — it is a few hundred entries shipped in the binary, not a live corpus, and we
  deliberately make no network call that would disclose a customer's password prefix to a third party.
- **Mail is a dependency, and it is the customer's.** Confirmation and reset links need SMTP. A deployment
  that configures none still works — the links go to an operator-readable surface and `/readyz` reports
  `mail_configured: false` — but "self-serve password reset" is a claim that requires their mail server.
  Say so before an air-gapped buyer discovers it.
- **No SSO *and* passwords on one deployment.** A deployment picks one sign-in method. A customer who
  wants both is a real request and is not built; the honest answer is "not today", not "configurable".

## 4. Questions a security reviewer will ask, and the answers we can defend

| Question | Answer |
|---|---|
| *What hash?* | argon2id, 64 MiB / t=3 / p=4, 16-byte salt per account, parameters stored with the hash so the cost can be raised without a migration. |
| *What stops credential stuffing?* | Ten consecutive failures in fifteen minutes lock that account for fifteen minutes, counted in the database rather than in a process — a restart does not clear it and a second replica does not double the budget. ⚠️ We do not rate-limit by source address; say so rather than implying we do. |
| *Can I enumerate your customers?* | Not through sign-in, sign-up or forgot-password: the three answer identically for known and unknown addresses, and the timing is equalised by running a real verification against a decoy. The one exception is the account-lock message, which is only reachable after ten failed attempts against that address. |
| *What happens on a reset?* | Every session and every personal credential that person holds is revoked, across every organization they belong to, at the next request. Machine credentials are untouched and the completion screen lists them by name. |
| *Do you email me my password?* | No. Nothing in the system can produce a password: a message carries one single-use, purpose-bound link and nothing else. |
| *Where does the password go inside your system?* | Browser → the console's own origin → the platform, once, over one connection. It is not written to a session record, a cookie, a log, a trace attribute or a URL, and a test asserts its absence from every response body on every route. |

## 5. The commercial framing

P28 is not a feature to sell on its own — it is what makes the **self-serve** motion true. Before it, the
documented way for a new user to obtain access was two `aws ssm` commands run by whoever operates the
cluster. Every self-serve claim in [P27's account copy](P27-account-copy.md) rested on a front door that
did not exist.

So the honest positioning is: *nothing new to buy, and the thing you were already sold now works without
us.* Any pricing conversation that attaches money to sign-up is a conversation about the plan, not about
this.
