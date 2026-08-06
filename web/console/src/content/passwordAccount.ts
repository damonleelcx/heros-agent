/**
 * passwordAccount.ts is every string the account-lifecycle screens render: creating an account, resetting a
 * password, and confirming an address.
 *
 * # 🔴 Why this is NOT in `content/identity.ts`
 *
 * `tests/sso-identity.test.mjs` fences the identity path — `lib/identity.ts`, `content/identity.ts`,
 * `lib/idp/*` and `app/auth/*` — against the words *plan*, *billing*, *entitlement* and any price literal.
 * Its reasoning is the load-bearing part: making sign-in depend on a billing record turns the first billing
 * outage into an authentication outage.
 *
 * Two strings below name a paid plan, because P28's confirmation gate genuinely limits one — an
 * unconfirmed address may not invite people and may not move to a plan that charges (ADR-012 Decision 7).
 * Those strings first went into `content/identity.ts` and the fence failed the build, correctly.
 *
 * The fix is this file, not an exemption. Deleting the words to satisfy a word-scan would have hidden a
 * real product rule from the person it applies to — they would meet it as an unexplained refusal at
 * checkout — and exempting a file from the fence would have made the fence advisory for everything after.
 * The two surfaces really are different: sign-in states, and what an account can do once it exists.
 *
 * 🚫 Nothing here is imported by `lib/identity.ts`, `lib/idp/*` or `app/auth/*`, which is what keeps the
 * separation real rather than filed.
 */

/**
 * PASSWORD_COPY is every string the five password screens render.
 *
 * Single-sourced for the reason the states above are: otherwise "confirm your email" becomes "activate
 * your account" on one screen and "verify your address" on another, and a reader concludes they are three
 * different things. The noun dictionary in the PRD is the authority; this is where it is spelled.
 */
export const PASSWORD_COPY = {
  // The reveal toggle's accessible name, single-sourced with everything else for the same reason: it is
  // the only name a screen-reader user has for this control, and six copies of it become six wordings.
  // It states the ACTION the control performs, not the state it is in — "Show password" on a masked
  // field — which is the convention `aria-pressed` completes.
  field: {
    show: "Show password",
    hide: "Hide password",
  },
  signIn: {
    emailLabel: "Email",
    passwordLabel: "Password",
    submit: "Sign in",
    forgot: "Forgot your password?",
    noAccount: "Create an account",
    hint: "We check your password on the server. Your browser receives a session cookie it cannot read, and never a platform key.",
  },
  signUp: {
    title: "Create your account",
    body: "One organization, one owner — you. You can invite the rest of your team once your email is confirmed.",
    nameLabel: "Organization name",
    nameHelp: "What your team calls itself. You can change it later.",
    emailLabel: "Work email",
    passwordLabel: "Password",
    // 🔴 The rule is stated BEFORE the field is submitted. A password refused on submit for a rule the
    // reader was never shown is the archetypal error with no next action.
    passwordHelp: "At least 12 characters. A short sentence works well, and beats a short password with symbols in it.",
    submit: "Create account",
    haveAccount: "Sign in instead",
  },
  forgot: {
    title: "Reset your password",
    body: "Type the address you signed up with and we will send you a link.",
    emailLabel: "Email",
    submit: "Send the link",
    // 🔴 The neutral acknowledgement. It is the same sentence whether or not the address is registered,
    // because a page that said "no such account" would answer a question we decline to answer.
    sent: "If that address has an account, we have sent it a link. It works once and expires in an hour.",
    backToSignIn: "Back to sign in",
  },
  reset: {
    title: "Choose a new password",
    passwordLabel: "New password",
    submit: "Set password and sign in",
    // Stated on the form, not discovered afterwards: this is the consequence people most need to know
    // before they act, because for many of them it is the reason they are here.
    warning: "Setting a new password ends every session and every personal access credential you hold, everywhere. Machine credentials your organization uses for automation are not affected, and we will list them.",
    expired: "This link is no longer usable. Request a new one.",
  },
  verify: {
    okTitle: "Email confirmed",
    okBody: "Thank you. Inviting people and changing to a paid plan are now available.",
    failTitle: "This link is no longer usable",
    failBody: "Confirmation links work once and expire. Sign in and ask for another from your account settings.",
  },
  unverified: {
    // Names the ADDRESS, so a typo is visible. A banner that says "your email is unconfirmed" without
    // saying which one leaves somebody who mistyped it with nothing to notice.
    banner: (email: string) => `Confirm ${email} to unlock inviting people and paid plans.`,
    resend: "Send it again",
    resent: "Sent. Check that inbox.",
  },
} as const;
