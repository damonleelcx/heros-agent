import { Banner } from "@/components/primitives";

/**
 * NoCollection is what an install with no payment provider says in place of a billing account.
 *
 * # Why this is not a `Failure`
 *
 * Both pages used to render the platform's 404 through `<Failure kind="not-found">`, whose copy is
 * *"This is a routing fact, not a measurement: the identifier does not resolve."* That sentence is
 * exactly right for a typo'd tenant id and exactly wrong here. On a deployment with no payment
 * provider, an account is not missing — it is unreachable by construction, because the only thing that
 * creates one is checkout, and checkout is P21. There is no record to find, no button to press, and
 * nothing for an operator to fix. Rendering that as a failed lookup sends them looking anyway.
 *
 * # What it deliberately does NOT say
 *
 * It does not say billing is broken, unavailable, or coming soon. P7 is mounted and working on this
 * deployment — the ledger, the accounts table, the meters and the entitlement gate are all real and
 * all durable. What is absent is COLLECTION. Entitlements still resolve, usage is still metered, and
 * every other surface is unaffected; saying so is the difference between a configured product and a
 * half-broken one.
 *
 * The trigger is the platform's `reason_code`, never the prose — see `ReasonCollectionNotConfigured`
 * in internal/api/billing.go. The server is the only party that knows whether this deployment mounts a
 * payment provider, so it is the party that decides.
 */
export function NoCollection({ tenantId }: { tenantId?: string }) {
  return (
    <Banner tone="info" title="This deployment collects no payments">
      <p>
        No billing account exists{tenantId ? <> for {tenantId}</> : null}, and none ever will: an account
        is created by checkout, and this install has no payment provider configured. That is its
        configured state, not a missing record.
      </p>
      <p>
        Everything billing is FOR still works. Your plan&apos;s entitlements resolve, usage is metered
        against them, and the ledger records what was used. What is absent is the part that charges for
        it.
      </p>
      <p className="hint">
        An operator adds collection by configuring a payment provider; until then there is nothing on
        this page to fix, and nothing missing from your data.
      </p>
    </Banner>
  );
}
