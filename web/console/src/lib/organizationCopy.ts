/**
 * organizationCopy.ts is every user-visible string P27's four surfaces render, in one place.
 *
 * # Why the copy is centralised rather than living beside its component
 *
 * Two reasons, and the second is the one that actually bites.
 *
 * A translation pass replaces a COLUMN here rather than hunting components. That is the stated reason
 * and it is the smaller one.
 *
 * The larger one is that these are **state → copy mappings**, and a state whose copy is written inline
 * is a state somebody adds without noticing it needs its own words. The member list has six states and
 * the runs list has three, and the characteristic failure of both is collapsing two into one: "you have
 * no runs" shown to somebody whose history predates ownership recording, or "waiting for them" shown
 * for an invitation that expired last week. A table makes the omission visible — a row with no words is
 * a row a reviewer sees.
 *
 * # 🔴 Enumerated copy is a fixed constant table, never assembled at runtime
 *
 * Every entry below is a literal. Nothing here interpolates a state name into a sentence, because the
 * sentence for a state is a product decision and a template is a way of not making it.
 *
 * The one exception is deliberate and narrow: a refusal that names two NUMBERS takes them as arguments,
 * because the numbers are data. The words around them are still fixed.
 *
 * # What may never appear here
 *
 * A seat count without a label. `seats` alone is two different numbers — the count that gates the next
 * invitation and the peak that prices the invoice — and a reader cannot tell which they were shown.
 * Every string below that mentions seats names which one.
 */

// ── the runs list · three states, not one ───────────────────────────────────────────────────────────

/**
 * RunsEmptyState is which of the three "no rows" facts we are looking at.
 *
 * 🔴 They are three facts with three next actions, and collapsing any pair is what makes a release read
 * as data loss to somebody who used the product last week.
 */
export type RunsEmptyState = "none-yet" | "pre-ownership" | "unreachable";

export const RUNS_COPY: Record<RunsEmptyState, { title: string; body: string; action?: string }> = {
  "none-yet": {
    title: "No runs yet",
    body: "A run appears here when you submit a variant, or when the CLI links one from your own pipeline.",
    action: "Configure a variant",
  },
  "pre-ownership": {
    title: "Your earlier runs are not listed here",
    body:
      "This platform did not record which organization owned a run until recently, and that information " +
      "cannot be recovered for the runs made before then. They are not deleted — each one is still " +
      "reachable by its id. Runs from now on appear in this list.",
  },
  unreachable: {
    title: "The platform did not answer",
    body:
      "This is a transport failure, not an empty list. Your runs are unaffected; we could not ask for " +
      "them. Retrying is safe.",
    action: "Try again",
  },
};

// ── the member list · six states ────────────────────────────────────────────────────────────────────

/**
 * MemberState is every row a member list can show.
 *
 * `last-owner` and `over-seat-limit` are STATES rather than errors: they are conditions a row is in
 * before anybody clicks, and a screen that only discovers them from a failed request has already let
 * somebody try.
 */
export type MemberState = "active" | "invited" | "expired" | "removed" | "last-owner" | "over-seat-limit";

export const MEMBER_COPY: Record<MemberState, { label: string; hint: string }> = {
  active: { label: "Member", hint: "" },
  invited: {
    label: "Invited",
    hint: "Waiting for them to sign in. The link fills in their address; signing in is what joins them.",
  },
  expired: {
    // 🔴 Its OWN state, not a variant of "invited". "Waiting for them" and "send it again" are
    // different next actions, and showing the first for the second is how an invitation sits dead in
    // somebody's inbox for a fortnight.
    label: "Invitation expired",
    hint: "Nobody used this within its window. Send a new one if they still need access.",
  },
  removed: {
    label: "Removed",
    hint: "Their access ended. The record stays so past actions still resolve to a name.",
  },
  "last-owner": {
    label: "Only owner",
    hint:
      "This organization would be left with nobody who can administer it. Make somebody else an owner " +
      "first — nobody can restore it afterwards.",
  },
  "over-seat-limit": {
    label: "Over the seat allowance",
    hint: "Your plan includes fewer seats than are in use. Upgrade, or remove a member.",
  },
};

// ── refusals · one entry per reason code the platform can return ────────────────────────────────────

/**
 * REFUSAL_COPY maps the platform's machine-readable `reason_code` to what a person reads.
 *
 * 🔴 The console branches on the CODE and never on the prose. Branching on the sentence would put the
 * decision in two places and let a copy edit change behaviour — which is the same reason the platform
 * sends a code at all.
 */
export const REFUSAL_COPY: Record<string, { title: string; body: string }> = {
  self_serve_disabled: {
    title: "This deployment does not create organizations",
    body: "Ask whoever runs this install to add you to an existing organization.",
  },
  seat_limit_reached: {
    title: "No seats left",
    body: "Upgrade the plan, or remove a member first.",
  },
  last_owner: {
    title: "An organization needs an owner",
    body: "Make somebody else an owner before removing or demoting this one. Nobody can restore it afterwards.",
  },
  invitation_expired: {
    title: "That invitation is no longer valid",
    body: "It may have been used already, withdrawn, or left too long. Ask for a new one.",
  },
  invitation_identity_mismatch: {
    title: "That invitation is for a different account",
    body:
      "It was issued to somebody else's address. Signing in with the invited account is what joins you — " +
      "the link only fills the form in.",
  },
  not_a_member: {
    title: "You are not a member of this organization",
    body: "Ask an owner or an admin to invite you.",
  },
  insufficient_role: {
    title: "Your role does not permit this",
    body: "Owners and admins manage members; owners alone manage owners and the plan.",
  },
  organization_suspended: {
    title: "This organization is suspended",
    body: "Billing has stopped and access is paused. An owner can reach support to reopen it.",
  },
  account_system_not_mounted: {
    title: "This install does not carry the organization surface",
    body: "It needs a published plan catalog. Whoever runs the install can add one.",
  },
};

/** refusalCopy falls back to a HONEST generic rather than inventing a cause for an unknown code. */
export function refusalCopy(code: string | undefined, fallback: string): { title: string; body: string } {
  if (code && REFUSAL_COPY[code]) return REFUSAL_COPY[code];
  return {
    title: "That did not go through",
    // The platform's own sentence, carried through. A message invented here would replace the one
    // thing the server actually knew.
    body: fallback || "Nothing was changed.",
  };
}

// ── seats · both numbers, always named ──────────────────────────────────────────────────────────────

/**
 * SEATS_COPY names each quantity every time it is shown.
 *
 * 🔴 There is deliberately no string here that says just "seats". The current count and the period's
 * billed peak are different numbers, they move in opposite directions on the same day, and a reader
 * given one unlabelled cannot tell which they got.
 */
export const SEATS_COPY = {
  currentLabel: "Seats in use",
  currentHelp: "Active members right now. This is what the next invitation is checked against.",
  allowedLabel: "Seats included",
  allowedHelp: "What the plan allows. An operator can raise it for this organization alone.",
  unlimited: "Unlimited on this plan",
  /** Both numbers, and the remedy. The words are fixed; the numbers are data. */
  overLimit: (used: number, allowed: number) =>
    `${used} in use against ${allowed} included — upgrade, or remove a member first.`,
  /**
   * 🔴 The undecided part of the definition, said out loud on the surface that shows the number.
   *
   * Whether somebody who holds only a personal API key and never opens the console occupies a seat is
   * NOT settled (PRD Open Question 3). Until it is, this line is what keeps the screen honest: it shows
   * the count the platform enforces and declines to claim what a seat includes.
   */
  definitionPending:
    "Every active member counts as one seat. What a seat includes for billing is still being settled, " +
    "so this number is what is enforced, not a quote.",
} as const;

// ── credentials · the two kinds, and what removal does to each ──────────────────────────────────────

export const CREDENTIAL_COPY = {
  personalLabel: "Personal",
  personalHelp: "Belongs to one person. Removing them revokes it at their next request.",
  machineLabel: "Machine",
  machineHelp:
    "Belongs to the organization. Removing a person does NOT revoke it — a build that depends on it " +
    "keeps running until somebody revokes it here.",
  secretOnce:
    "This is the only time this value is shown. Copy it now — nothing can retrieve it afterwards, " +
    "including us.",
} as const;

// ── removal · both halves, always ───────────────────────────────────────────────────────────────────

export const REMOVAL_COPY = {
  title: "Remove from this organization",
  willEnd: "Ends at their next request",
  willEndHelp: "No restart, no grace period. Their browser is signed out and their personal keys stop working.",
  willRemain: "NOT revoked by this",
  /**
   * 🔴 The sentence this whole screen exists for.
   *
   * An offboarding screen that lists what it revokes and hides what it leaves running is worse than no
   * screen: the person confirming it signs an attestation that is wrong, and a CI key created by the
   * departing engineer keeps deploying.
   */
  willRemainHelp:
    "These belong to the organization, not to this person. They keep working. Revoke any you no longer " +
    "want before you finish — a build that depends on one will break the moment you do.",
  confirm: "Remove them",
  nothingToRemain: "No organization keys are affected.",
} as const;

// ── sign-up ─────────────────────────────────────────────────────────────────────────────────────────

export const SIGNUP_COPY = {
  title: "Name your organization",
  lede:
    "This is what your colleagues will see, and what appears on your billing. You can be the only " +
    "person in it.",
  nameLabel: "Organization name",
  namePlaceholder: "Acme Inc",
  /** Asked for rather than derived: an email domain gives "Gmail" for every independent developer. */
  nameHelp: "Use the name people would recognise, not a domain.",
  submit: "Create it",
  freePlanNote:
    "You will start on the free plan with no payment method. Nothing is charged, and nothing asks for " +
    "a card until you choose a paid plan.",
  /**
   * Shown when the visitor has no session yet.
   *
   * 🔴 This page already refuses to render a form it cannot honour when self-serve is OFF, with the
   * reason written into its own header: *"a form that collects a name and then says 'no' teaches
   * somebody the product is broken, where a page that never offered one says [a next action]"*. That
   * argument was only applied to ONE of the two preconditions. Creating an organization also needs a
   * person, because the platform takes the identity from the session and never from the body — so a
   * signed-out visitor filled the form in, pressed Create it, and got `no session`: two technical words,
   * no next action, on the first screen of the product.
   *
   * Found by walking the flow in a browser. Nothing failed: the page rendered, the button worked, and
   * the refusal was correct.
   */
  signInFirstTitle: "Sign in first",
  signInFirstLede:
    "We run no password store. Your own identity provider proves who you are — then you name your " +
    "organization, on the next screen.",
  signInFirstCta: "Continue with your identity provider",
} as const;

// ── invitation acceptance ───────────────────────────────────────────────────────────────────────────

export const JOIN_COPY = {
  title: "Join an organization",
  /** 🔴 The link is a convenience. Saying so on the page is what stops it reading as a credential. */
  lede:
    "This link fills in who invited you and which address it was sent to. Signing in with that account " +
    "is what joins you — the link on its own grants nothing.",
  signIn: "Sign in to join",
  /**
   * ⚠️ Shown where an invitation cannot be accepted because the sign-in seam knows no address.
   *
   * `oidc` and `saml` supply a verified address; `configured` and `dev` do not. Rather than let somebody
   * press a button that will always refuse, the page says which door they are actually at.
   */
  noAddressSeam:
    "This install signs people in without an email address, so an emailed invitation cannot be matched. " +
    "Ask whoever runs it to add you directly.",
  invitedAs: "Invited as",
  organization: "Organization",
  role: "Role",
} as const;

// ── the control-visibility matrix ───────────────────────────────────────────────────────────────────

/**
 * ViewerRole is the acting person's role in the organization they are looking at.
 *
 * `"none"` is a MACHINE credential or somebody with no active membership. It is a real value, not a
 * missing one: a CI key looking at this surface has no role, and defaulting it to `member` would render
 * a member's controls for a caller that is not a person.
 */
export type ViewerRole = "owner" | "admin" | "member" | "none";

/**
 * CONTROL_MATRIX decides which controls each role SEES, per surface.
 *
 * # 🔴 Why this is a table and not a set of `&&`s in the JSX
 *
 * The alternative is deciding visibility at each control, which is how a surface ends up rendering a
 * button that is always refused. That is a *silent dead write*: the person presses it, the platform
 * says no, and what they learn is that the product is broken rather than that the action is not theirs.
 * The reviewer's question — *which controls does an admin see?* — has an answer here rather than
 * requiring a read of four components.
 *
 * # It does not replace the platform's check, and cannot
 *
 * Every entry below has a matching refusal on the platform, and the platform is the one that decides.
 * This table decides what to ASK. Hiding a control the platform would allow is a product bug; showing
 * one it would refuse is a product bug too, and only the second one teaches somebody the wrong thing
 * about the product.
 *
 * # The rules, stated once
 *
 *   * An OWNER does everything. Ownership and the plan are financial authority.
 *   * An ADMIN manages people, but not owners and not the plan: they may not promote to owner, demote an
 *     owner, remove an owner, or close the account.
 *   * A MEMBER sees the organization and its people, and changes nothing.
 *   * NONE — a machine credential — sees nothing here at all. It is not a person, and the surface says
 *     so rather than showing it a read-only view it has no business in.
 */
export const CONTROL_MATRIX: Record<
  ViewerRole,
  {
    viewMembers: boolean;
    invite: boolean;
    changeRole: boolean;
    changeOwnerRole: boolean;
    promoteToOwner: boolean;
    removeMember: boolean;
    removeOwner: boolean;
    manageKeys: boolean;
    closeAccount: boolean;
  }
> = {
  owner: {
    viewMembers: true,
    invite: true,
    changeRole: true,
    changeOwnerRole: true,
    promoteToOwner: true,
    removeMember: true,
    removeOwner: true,
    manageKeys: true,
    closeAccount: true,
  },
  admin: {
    viewMembers: true,
    invite: true,
    changeRole: true,
    // An admin may not touch an owner in either direction. Ownership moves only by an owner's hand.
    changeOwnerRole: false,
    promoteToOwner: false,
    removeMember: true,
    removeOwner: false,
    manageKeys: true,
    closeAccount: false,
  },
  member: {
    viewMembers: true,
    invite: false,
    changeRole: false,
    changeOwnerRole: false,
    promoteToOwner: false,
    removeMember: false,
    removeOwner: false,
    manageKeys: false,
    closeAccount: false,
  },
  none: {
    viewMembers: false,
    invite: false,
    changeRole: false,
    changeOwnerRole: false,
    promoteToOwner: false,
    removeMember: false,
    removeOwner: false,
    manageKeys: false,
    closeAccount: false,
  },
};

/** controlsFor narrows an unknown role string to the matrix. An unrecognised role gets `none`. */
export function controlsFor(role: string | undefined): (typeof CONTROL_MATRIX)[ViewerRole] {
  const key = (role ?? "none") as ViewerRole;
  return CONTROL_MATRIX[key] ?? CONTROL_MATRIX.none;
}

/**
 * ROLE_COPY is what each role's absence of a control says, when the surface explains itself.
 *
 * A hidden control with no explanation reads as a missing feature. One sentence turns it into a
 * boundary somebody can act on — by asking an owner.
 */
export const ROLE_COPY: Record<ViewerRole, string> = {
  owner: "",
  admin: "Owners manage owners and the plan. Everything else here is yours.",
  member: "Owners and admins manage members, keys and the plan. You can see who is here.",
  none: "You are signed in with a key that names no person, so this surface has nobody to act as.",
};
