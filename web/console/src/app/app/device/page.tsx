import { requireSession } from "@/lib/session";
import { PageFrame } from "@/components/primitives";
import { ApproveDevice } from "@/components/deviceApprove";

/**
 * Where a person approves a terminal login (P27 task 13.1).
 *
 * # Why this page is session-gated and the terminal is not
 *
 * The whole point of device authorization is that the CLI never handles an assertion, a password or an ID
 * token. It cannot, therefore, prove who is running it — so the proving happens HERE, on a page the
 * middleware already refuses without a session, behind the customer's own identity provider.
 *
 * The terminal shows a code and waits. This page turns "somebody who can sign in to this organization
 * says yes" into a credential. Nothing else in the flow can.
 *
 * # ⚠️ The code is not a secret, and this page is why that is safe
 *
 * The short code carries about 39 bits — guessable, given enough attempts. It is allowed to be, because
 * holding one grants nothing: approving requires a signed-in person, the platform refuses an approval
 * into an organization that person holds no ACTIVE membership in, and the code alone cannot collect the
 * credential (that needs the terminal's own 32-byte device code, which never leaves the machine).
 */
export const dynamic = "force-dynamic";

export default async function DevicePage() {
  await requireSession();
  return (
    <PageFrame
      eyebrow="Command line"
      title="Sign in a terminal"
      lede="Enter the code your terminal is showing. Approving creates a personal API credential for this organization — it is listed with your other credentials, and removing you from the organization revokes it."
    >
      <ApproveDevice />
    </PageFrame>
  );
}
