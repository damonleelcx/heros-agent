/**
 * identity.ts is the GLOSSARY for every word the sign-in surface says (P22 tasks 9.1, 9.2, 9.3).
 *
 * # Why the copy is a module and not four strings in a page
 *
 * Sign-in messages are written once, under deadline, by whoever built the route — and then the same
 * distinctions get re-spelled slightly differently in the operator console, in a support macro, and in
 * a docs page. Within a quarter "your session ended" and "you were signed out" are two states as far
 * as a reader is concerned, and neither one is documented. Single-sourcing them means the four states
 * below are the only four the product has, and adding a fifth is a visible act.
 *
 * # The three rules every string here obeys
 *
 * 1. **Define every term.** "Session", "identity provider", "tenant" and "provisioned" all appear
 *    below, and each is explained where it is used rather than assumed. The reader of a sign-in error
 *    is, by definition, someone the product has just failed.
 * 2. **Leak no internal mechanism.** No secret's logical name, no provider-kind literal
 *    (`admin-idp-oidc`), no issuer, no allowlist entry, no environment variable. A user-facing message
 *    that names our internals is both a security leak and a support burden.
 * 3. **No price and no plan gate (task 9.3).** Identity proves *who*; entitlement is P7 and payment is
 *    P21. Nothing here may imply that signing in depends on a plan, and nothing on this path reads
 *    one.
 */

/**
 * SIGN_IN_STATES is the closed set of reasons the sign-in page is being shown.
 *
 * The four are distinct because the reader's NEXT ACTION is distinct in each, which is the only test
 * that matters for whether two messages should be one:
 *
 *   sign_in         → sign in. Nothing has gone wrong.
 *   session_ended   → sign in again. Nothing was lost; this is not an error.
 *   not_accepted    → the credential or assertion did not verify. Try again, then ask your IT team.
 *   idp_unreachable → nothing the reader can do. Wait, or tell their operator. NOT their fault.
 *   not_provisioned → their identity is fine and their account is not set up here. Ask their admin.
 *
 * `tone` drives presentation only. `error` is reserved for the two states where something actually
 * failed — a sign-in prompt styled as an error teaches people to ignore errors.
 */
export type SignInStateKey = "sign_in" | "session_ended" | "not_accepted" | "idp_unreachable" | "not_provisioned";

export type SignInState = {
  title: string;
  body: string;
  tone?: "error";
  /**
   * The one line in the alert region, for the states that have one.
   *
   * It is NOT the title again. Repeating the heading inside an alert box was the first thing a real
   * browser showed about this page — the same sentence rendered twice, three lines apart — and it
   * reads as a rendering fault rather than as emphasis. An alert earns its space by saying the thing
   * the heading could not: what to do next, in one clause.
   */
  alert?: string;
};

export const SIGN_IN_STATES: Record<SignInStateKey, SignInState> = {
  sign_in: {
    title: "Sign in to continue",
    body: "This surface renders your tenant's data — the workspace your organization's runs, prompts and transforms belong to — so it is not served without a session.",
  },
  session_ended: {
    title: "Your session ended",
    body: "A session is the server-side record that keeps you signed in. Sessions are time-bounded and can be revoked, and yours is no longer valid. Signing in again issues a new one; nothing you were looking at was lost.",
  },
  not_accepted: {
    title: "That sign-in was not accepted",
    body: "The credential your browser presented did not verify. Try signing in again. If it keeps happening, your organization's IT team can check whether your account is still active.",
    tone: "error",
    alert: "Nothing was signed in. Try again.",
  },
  idp_unreachable: {
    title: "Your identity provider could not be reached",
    body: "Your organization's identity provider — the system that proves who you are — did not answer. No session is issued when that happens, on purpose: we would rather sign nobody in than sign the wrong person in. Nothing is wrong with your account. Try again shortly.",
    tone: "error",
    alert: "This is on our side of the connection, not yours.",
  },
  not_provisioned: {
    title: "You are not set up for this workspace",
    body: "Your identity was verified, and it is not associated with a workspace here. Your organization's administrator can add you. We do not create an account automatically from a sign-in — an account you did not ask for is not a favour.",
  },
};

/**
 * REASON_ALIASES maps the query parameters routes actually emit onto the states above.
 *
 * Two vocabularies exist for a reason and neither is redundant. The routes emit a `RefusalClass`
 * (`credential` / `not_provisioned` / `idp_unreachable`) plus the two session states the pre-P22
 * console already emitted (`no_session` / `session_ended`) and its rejected-credential reason
 * (`rejected`). This table is where they meet, so a route never has to know a UI string and this
 * module never has to know a route.
 *
 * An unrecognised value falls to `sign_in`, deliberately: an unknown reason must not render an error
 * about itself, and the reader still needs the page to work.
 */
export const REASON_ALIASES: Record<string, SignInStateKey> = {
  no_session: "sign_in",
  session_ended: "session_ended",
  rejected: "not_accepted",
  credential: "not_accepted",
  idp_unreachable: "idp_unreachable",
  not_provisioned: "not_provisioned",
};

/** signInState resolves a `reason` query parameter to its state. */
export function signInState(reason: string | undefined): SignInState {
  return SIGN_IN_STATES[REASON_ALIASES[reason ?? ""] ?? "sign_in"];
}

/**
 * IDENTITY_ASSURANCES is what the sign-in surface says about how identity is handled here.
 *
 * # Why this is on the sign-in page rather than in a datasheet
 *
 * It is the one screen where a reader is thinking about exactly this question, and each line is a
 * property the code actually has, stated in the present tense because it is true now (task 9.2). The
 * honest boundary is enforced by what is ABSENT: no directory sync, no per-seat user model, no audit
 * attribution by person. Those are not built, so they are not claimed anywhere.
 *
 * The third line states the revocation semantics precisely — *next request*, not "instant" — because
 * "instant" is a word a security reviewer will ask us to defend and "at the next request, with no
 * grace period" is the thing we can actually defend.
 */
export const IDENTITY_ASSURANCES: Array<{ label: string; body: string }> = [
  {
    label: "No password database",
    body: "We run no password store and no identity provider of our own. Your organization's own provider proves who you are; we only ask it.",
  },
  {
    label: "Your browser never holds a key",
    body: "The proof of your identity is exchanged on the server and dropped. Your browser keeps one opaque session cookie it cannot read, and never a platform credential.",
  },
  {
    label: "Revocation lands on the next request",
    body: "Sessions are server-side records, read on every request. When one is revoked, the very next request is denied — there is no grace period in which it still works.",
  },
];
