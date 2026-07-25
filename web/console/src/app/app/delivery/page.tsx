import Link from "next/link";
import type { DeliveriesView, DeliveryView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import {
  PageFrame,
  Section,
  Chip,
  Status,
  Empty,
  Failure,
  DataTable,
  Banner,
  Stat,
  Stats,
} from "@/components/primitives";

/**
 * Delivery.
 *
 * The loop from a verified proposal to an outcome on the customer's repository, made visible: each
 * delivery's state (open / merged / closed / superseded) linked back to the proposal that produced it,
 * and — the part silence would hide — the delivery-route CONDITION rendered as a condition with a next
 * action rather than as an empty list (FR13). This surface inherits P9's rules unchanged: the token
 * system, English strings, render-as-received, the four data states, and browser-rendered acceptance.
 */
export const dynamic = "force-dynamic";

export default async function DeliveryPage() {
  const { outcome } = await load<DeliveriesView>((paths) => paths.deliveries(), ["route"]);
  return (
    <PageFrame
      eyebrow="Delivery"
      title="Forge delivery"
      lede="Every verified optimization that reached a pull request on your repository, and its outcome. A human merges; the platform never does below the Autonomous level."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="delivery" />
      ) : (
        <Body view={outcome.data} />
      )}
    </PageFrame>
  );
}

function Body({ view }: { view: DeliveriesView }) {
  const deliveries = view.deliveries ?? [];
  const counts = tally(deliveries);

  return (
    <>
      {/*
        FR13 / tasks 6.1, 6.2 — the route CONDITION, never an empty list. A missing, degraded, or
        revoked route is rendered as a condition WITH a next action, so silence is never mistaken for
        "the product found nothing". A configured route needs no banner.
      */}
      <RouteConditionBanner route={view.route} />

      <Section title="Outcomes" aside="a human merges; the platform never merges below Autonomous">
        <Stats>
          <Stat label="Open for review" value={counts.open} note="awaiting a human's merge" />
          <Stat label="Merged" value={counts.merged} note="shipped — the only billable outcome" />
          <Stat label="Superseded" value={counts.superseded} note="closed for a newer verified proposal" />
          <Stat label="Closed" value={counts.closed} note="closed without merging" />
        </Stats>
      </Section>

      <Section title="Deliveries" aside="each links back to the proposal that produced it">
        {deliveries.length === 0 ? (
          view.route.kind === "configured" ? (
            <Empty title="No deliveries yet.">
              <p>
                When a proposal passes the verification gate and a route is configured, it is delivered
                as a pull request and appears here. This is a real state, not a failure to load.
              </p>
            </Empty>
          ) : (
            // With no route, "no deliveries" is explained by the condition above — not a bare empty.
            <p className="hint">
              No deliveries yet. This is expected while the condition above is unresolved — it is not the
              product having found nothing.
            </p>
          )
        ) : (
          <DataTable
            caption="Every delivery for this tenant, its current state, the mode that opened it, and a link to its proposal"
            columns={[
              { key: "state", label: "State" },
              { key: "target", label: "Repository" },
              { key: "mode", label: "Mode" },
              { key: "pr", label: "Pull request" },
              { key: "proposal", label: "Proposal" },
            ]}
          >
            <tbody>
              {deliveries.map((d) => (
                <DeliveryRow key={d.delivery_id} d={d} />
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>
    </>
  );
}

function DeliveryRow({ d }: { d: DeliveryView }) {
  return (
    <tr>
      <td>
        <Status value={d.state} />
        {d.reason ? <p className="caption mt-1">{d.reason}</p> : null}
        {d.state === "merged" && d.merge_commit ? (
          <p className="caption mono mt-1">merge {d.merge_commit}</p>
        ) : null}
      </td>
      <td>
        <span className="mono text-sm">{d.target}</span>
      </td>
      <td>
        {/* The credential path that opened it — an audit fact, shown plainly. */}
        <Chip title={d.mode === "ci" ? "opened by your CI with its own token" : "opened by the hosted Git App"}>
          {d.mode === "ci" ? "CI-mediated" : "hosted app"}
        </Chip>
      </td>
      <td>
        <span className="mono text-sm">{d.forge_ref}</span>
      </td>
      <td>
        {/* task 6.3 — the loop from proposal to outcome is one click, not a search. */}
        <Link className="text-primary underline underline-offset-2" href={d.proposal_ref}>
          Open evidence
        </Link>
      </td>
    </tr>
  );
}

/** RouteConditionBanner renders a non-configured route as a condition with a next action (FR13). */
function RouteConditionBanner({ route }: { route: DeliveriesView["route"] }) {
  if (route.kind === "configured") return null;

  const title =
    route.kind === "no_route"
      ? "No delivery route configured for this repository"
      : route.kind === "revoked"
        ? "Delivery is revoked — the hosted Git App was removed"
        : "Delivery is degraded";
  const tone = route.kind === "no_route" ? "info" : route.kind === "revoked" ? "bad" : "warn";

  return (
    <Banner tone={tone} title={title}>
      {route.detail ? <p>{route.detail}</p> : null}
      {route.next_action ? (
        <p>
          <span className="font-medium text-foreground">Next: </span>
          {route.next_action}
        </p>
      ) : null}
      <p className="caption">
        This is a reported condition, not an error and not an empty result — verified proposals are not
        lost, and delivery resumes once it is resolved.
      </p>
    </Banner>
  );
}

/** tally counts deliveries by outcome for the summary stats. `updated` counts as open. */
function tally(deliveries: DeliveryView[]) {
  const c = { open: 0, merged: 0, superseded: 0, closed: 0, reverted: 0 };
  for (const d of deliveries) {
    switch (d.state) {
      case "opened":
      case "updated":
        c.open++;
        break;
      case "merged":
        c.merged++;
        break;
      case "superseded":
        c.superseded++;
        break;
      case "closed":
        c.closed++;
        break;
      case "reverted":
        c.reverted++;
        break;
    }
  }
  return c;
}
