import { PageFrame, Section, Banner } from "@/components/primitives";
import { requireSession } from "@/lib/session";
import { platformFetch } from "@/lib/platformApi";
import { passwordSignInEnabled } from "@/lib/identity";
import { ChangePasswordForm, ResendConfirmation } from "@/components/account";
import { PASSWORD_COPY } from "@/content/passwordAccount";

/**
 * Your own account: your password, and whether your address is confirmed.
 *
 * # 🔴 Why this is NOT on `/app/settings/members`
 *
 * Members is *other people*. The thing being changed here is yours, and putting a self-service action on
 * the administration screen is how an admin ends up changing their own password from a row that looks like
 * it belongs to somebody else. The IA rule the PRD states is that a page has one subject; these are two.
 *
 * # What the page does not do
 *
 * It does not show sessions or credentials. Those are on Members, because revoking somebody's key is an
 * administration act — and duplicating the list here would be two places that can disagree about what is
 * live. What this page owns is exactly what only YOU can do: prove your address, and change your password
 * with the current one.
 */
export const dynamic = "force-dynamic";

type WhoAmI = {
  user_id?: string;
  email?: string;
  email_verified?: boolean;
};

export default async function AccountPage() {
  const session = await requireSession();

  if (!passwordSignInEnabled()) {
    // A federated deployment has no password here to change: the identity provider owns it, and offering
    // a form that could only refuse would be worse than saying where the control actually lives.
    return (
      <PageFrame eyebrow="Settings" title="Account" lede="Your sign-in on this install.">
        <Section title="Password">
          <Banner tone="info" title="Managed by your identity provider">
            This install signs you in through your organization&apos;s identity provider, so your password
            is managed there rather than here. Your IT team can change or reset it.
          </Banner>
        </Section>
      </PageFrame>
    );
  }

  const who = await platformFetch<WhoAmI>("/api/v1/whoami", {
    tenantId: session.tenantId,
    userId: session.userId,
  });
  const email = who.ok ? (who.data.email ?? "") : "";
  // 🔴 UNKNOWN is not the same as unconfirmed. A failed read must not render "your address is not
  // confirmed" — that sentence would send somebody hunting for an email that was never re-sent, over an
  // outage of ours. The banner is shown only when the platform actually said so.
  //
  // 🔴 AN ABSENT FIELD IS ALSO UNKNOWN, and reading it as `false` was a shipped defect. `whoami` did
  // not return `email_verified` at all, so `Boolean(undefined)` said "unconfirmed" to a person whose
  // address was confirmed — permanently, with a "Send it again" button that could not change the
  // answer. The rule above was already written; it was applied to a failed READ and not to a missing
  // FIELD, which is the same unknown arriving by a different route.
  const verified = who.ok ? who.data.email_verified !== false : true;

  return (
    <PageFrame eyebrow="Settings" title="Account" lede="Your sign-in on this install.">
      {who.ok && !verified ? (
        <Section title="Email">
          <Banner tone="warn" title={PASSWORD_COPY.unverified.banner(email || "your address")} />
          <ResendConfirmation email={email} />
        </Section>
      ) : null}

      <Section title="Password">
        <ChangePasswordForm />
      </Section>
    </PageFrame>
  );
}
