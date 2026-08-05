import { JOIN_COPY } from "@/lib/organizationCopy";

/**
 * The invitation acceptance page.
 *
 * # 🔴 The link is a convenience. This page says so, out loud.
 *
 * It pre-fills which organization invited you and which address the invitation was sent to. It grants
 * nothing. Membership is created only when a completed sign-in yields a VERIFIED address matching the
 * invitation — so forwarding this link to a colleague creates nothing, and the page tells the reader
 * that before they wonder.
 *
 * # Why there is no session check here
 *
 * Somebody arriving at an invitation has, by definition, not signed in yet. The page renders with no
 * session, shows what it can (the id, which is opaque), and hands off to the sign-in flow carrying the
 * invitation so the callback can accept it once an identity is proved.
 *
 * # What is deliberately NOT shown
 *
 * The organization's name and the invited address are NOT fetched and rendered here. Doing so would make
 * an invitation id an oracle: anybody holding a leaked link could learn which company invited whom. The
 * details appear after sign-in, when the platform knows who is asking.
 */
export const dynamic = "force-dynamic";

export default async function JoinPage({ params }: { params: Promise<{ invitationId: string }> }) {
  const { invitationId } = await params;
  // 🔴 `next`, not a bespoke `invitation` parameter.
  //
  // The first version sent `/signin?invitation=<id>` and nothing read it — the link dead-ended at the
  // sign-in page. `next` is the mechanism that already exists: it survives the sign-in round trip, it is
  // validated as a same-origin path, and it lands on a SESSION-GATED page where the platform knows who
  // is asking. The invitation id is a subject in a URL, never an authority.
  const next = `/signin?next=${encodeURIComponent(`/app/join/${invitationId}`)}`;

  return (
    <main className="mx-auto flex min-h-screen max-w-lg flex-col justify-center gap-5 px-6">
      <div>
        <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-primary">Invitation</p>
        <h1 className="mt-1 font-display text-2xl">{JOIN_COPY.title}</h1>
        <p className="mt-2 text-sm text-muted-foreground">{JOIN_COPY.lede}</p>
      </div>

      <a
        href={next}
        className="rounded-md border border-border px-4 py-2 text-center text-sm hover:bg-muted/40"
      >
        {JOIN_COPY.signIn}
      </a>

      <p className="text-xs text-muted-foreground">
        If you sign in with a different account, nothing happens — the invitation stays unused and you
        will be told it is for a different account.
      </p>
    </main>
  );
}
