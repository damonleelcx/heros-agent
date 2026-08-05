import { requireSession } from "@/lib/session";
import { PageFrame } from "@/components/primitives";
import { JOIN_COPY } from "@/lib/organizationCopy";
import { AcceptInvitation } from "@/components/joinAccept";

/**
 * The second half of the invitation flow, and the reason `/join/<id>` hands off here.
 *
 * # Why acceptance happens on a SESSION-GATED page rather than in the callback
 *
 * An invitation is accepted by a person, and there is no person until somebody signs in. Putting the
 * acceptance in the sign-in callback would mean threading an invitation id through the OIDC `state`, the
 * SAML `RelayState` and the configured seam's form — three places, one of which is a security-critical
 * parameter with its own single-use rules.
 *
 * Landing on a gated page instead uses the mechanism that already exists: `/signin?next=…` carries the
 * destination, the middleware refuses the page without a session, and by the time this renders the
 * platform knows exactly who is asking. The invitation id is in the URL, where it is a subject rather
 * than an authority.
 *
 * # ⚠️ An invitation needs a seam that knows an ADDRESS
 *
 * Acceptance matches the invitation's address against the one recorded on the signed-in person, and that
 * address came from a verified assertion. The `oidc` and `saml` seams supply one. The `configured` and
 * `dev` seams do NOT — they map an assertion to an organization and know no address behind it — so on
 * those deployments an emailed invitation cannot be accepted and the refusal will be
 * `invitation_identity_mismatch`.
 *
 * That is stated here rather than discovered: those deployments add people by configuration, which is
 * how they added everybody already. It is not a defect to fix by loosening the match — matching on
 * anything the person supplies is exactly what turns the link back into a credential.
 *
 * # What this page does NOT do
 *
 * It does not accept on render. A GET that changes state is a link a mail scanner follows, and an
 * invitation spent by a corporate link-preview bot is an invitation nobody can use. The acceptance is a
 * POST behind a control the person presses.
 */
export const dynamic = "force-dynamic";

export default async function AcceptInvitationPage({
  params,
}: {
  params: Promise<{ invitationId: string }>;
}) {
  await requireSession();
  const { invitationId } = await params;

  return (
    <PageFrame eyebrow="Invitation" title={JOIN_COPY.title} lede={JOIN_COPY.lede}>
      <AcceptInvitation invitationId={invitationId} />
    </PageFrame>
  );
}
