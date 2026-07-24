import { requireIdentity, holdersOf } from "@/lib/session";
import { loadOperatingPicture } from "@/lib/overview";
import { OperatorShell } from "@/components/shell";
import { PageFrame } from "@/components/primitives";
import { LiveOperatingPicture } from "@/components/operatingPicture";
import type { Role } from "@/lib/roles";

/**
 * The console's home is the OPERATING PICTURE (FR34), not a redirect to a list.
 *
 * # Why this page exists at all
 *
 * The console used to open on the tenant directory: a table of names, which answers a question nobody
 * arrives with. An operator opening this surface has one of two questions — *is anything halted* and
 * *is anything wrong* — and both used to take three or four navigations to answer. That is the
 * difference between a console an operator reaches for and a `psql` prompt they reach for instead,
 * which on this phase is a security outcome rather than a convenience one (design.md Decision 13).
 *
 * # Why it is server-rendered first and polled second
 *
 * The first paint is the real picture, assembled server-side with the operator's own session — not a
 * skeleton that fills in. The client then keeps it current in place. An operator who glances at this
 * page one second after opening it sees state, not a loading shell.
 */
export default async function OverviewPage() {
  const { identity, sessionToken } = await requireIdentity();
  const picture = await loadOperatingPicture(sessionToken, identity.capabilities);

  // The escalation paths are resolved here, from the same permission map the backend enforces, so a
  // panel the role cannot see names who holds it rather than rendering a bare refusal (FR22).
  const holders: Record<string, Role[]> = {};
  for (const capability of [
    "killswitch.operate",
    "job.read",
    "tenant.read",
    "impersonate.read",
    "crosstenant.read",
    "audit.read",
  ]) {
    holders[capability] = holdersOf(identity, capability);
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Operations"
        title="Overview"
        lede="The live state of the platform: what is halted, what is wrong, who is acting as whom, and what was done last. Every figure carries the time it is current as of."
      >
        <LiveOperatingPicture initial={picture} holders={holders} />
      </PageFrame>
    </OperatorShell>
  );
}
