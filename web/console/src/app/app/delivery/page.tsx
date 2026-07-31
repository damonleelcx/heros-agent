import Link from "next/link";
import type { ChangeDeliveryView, DeliveriesView, DeliveryView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { platformFetch } from "@/lib/platformApi";
import { requireSession } from "@/lib/session";
import { Tabs, type TabItem } from "@/components/tabs";
import {
  DeliveryRouteLedger,
  DeliveryRouteLegend,
  DeliverySourceReality,
  DeliveryStateLegend,
  undeliverableChanges,
} from "@/components/deliveryRoutes";
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
 * Two questions live here, and before P13 13e only the first had a surface:
 *
 *   "What reached my repository?"       — the pull requests, their outcome, the route condition (P12).
 *   "How does a change reach it AT ALL?" — the route ledger (P13 13e).
 *
 * The second matters because the honest answer is usually *it does not*. Read against the coverage
 * tables, most cells have no materializer: memory refuses in every language, harness refuses in every
 * language, skill binding materializes in Go for two providers. When the rewriter refuses there is no
 * diff, so there is no pull request, so nothing appears on the first tab — and an empty list is exactly
 * what "nobody has gotten to it yet" looks like. The ledger turns that silence into a stated reason
 * with an owner.
 *
 * 🔴 The two are tabs of one surface rather than two pages, because a reader who finds no delivery needs
 * the explanation one click away, not one navigation away.
 */
export const dynamic = "force-dynamic";

export default async function DeliveryPage() {
  const { outcome } = await load<DeliveriesView>((paths) => paths.deliveries(), ["route"]);
  const routes = await fetchDeliveryRoutes();

  const tabs: TabItem[] = [
    {
      id: "pull-requests",
      label: "Pull requests",
      content: !outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="delivery" />
      ) : (
        <Body view={outcome.data} />
      ),
    },
    { id: "routes", label: "Routes", content: <RoutesTab view={routes} /> },
  ];

  return (
    <PageFrame
      eyebrow="Delivery"
      title="How a change reaches your agent"
      lede="Every verified optimization that reached a pull request on your repository — and, for the ones that cannot, which route was expected and why it refused. A human merges; the platform never does below the Autonomous level."
      wide
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}

/**
 * fetchDeliveryRoutes reads the route ledger.
 *
 * 🚫 There is no local fallback table. A console carrying its own copy of what each route can deliver
 * would be the second delivery table the contract exists to prevent, and it would drift in the usual
 * direction — the copy is always the optimistic one. When the platform cannot be read, the tab says so
 * rather than rendering a plausible table nobody verified.
 */
async function fetchDeliveryRoutes(): Promise<ChangeDeliveryView | null> {
  const session = await requireSession();
  const outcome = await platformFetch<ChangeDeliveryView>("/api/p13/delivery", {
    tenantId: session.tenantId,
  });
  return outcome.ok ? outcome.data : null;
}

function RoutesTab({ view }: { view: ChangeDeliveryView | null }) {
  if (!view) {
    return (
      <Banner tone="warn" title="The route ledger is unavailable">
        This tab states what each delivery route can and cannot carry. Showing a partial answer would be
        worse than showing none, because a missing row reads as &ldquo;not applicable&rdquo; — a claim
        about your code that nobody made.
      </Banner>
    );
  }

  const cells = view.cells ?? [];
  const runtimeLive = cells.filter((c) => c.route === "runtime" && c.status === "delivers").length;
  // 🔴 The join, not the ledger alone: a change is undeliverable only when the runtime route refuses AND
  // every language refuses the source route. See undeliverableChanges().
  const undeliverable = undeliverableChanges(view).size;

  return (
    <>
      <Banner tone="info" title="A rollout is evidence, not delivery">
        A gradual rollout runs inside your own process, assigns each unit of work to an arm
        deterministically, expires to the parent arm, and reverts itself if a guard trips — without
        reaching the platform. It never merges anything. Permanence still costs a codemod, a pull
        request, and a human.
      </Banner>

      <Section title="What each route carries" aside={`read against coverage table ${view.version}`}>
        <Stats>
          <Stat
            label="Rollout-eligible"
            value={runtimeLive}
            note="changes a bound node can try under real load"
          />
          <Stat
            label="Undeliverable"
            value={undeliverable}
            note="every route refuses — each names why"
          />
        </Stats>
        <DeliveryRouteLedger view={view} />
      </Section>

      <Section
        title="Whose move is a refusal"
        aside="the identifier selects the treatment, never the sentence"
      >
        <DeliveryRouteLegend view={view} />
      </Section>

      <Section
        title="Will the pull-request route produce a diff"
        aside="this is the half that varies by language"
      >
        <DeliverySourceReality view={view} />
      </Section>

      <Section title="Delivery states" aside="a closed set, with no “pending”">
        <DeliveryStateLegend view={view} />
      </Section>
    </>
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
              <p>
                If you expected one and it never arrived, the Routes tab says which route was expected
                and why it refused. Most changes are refused by the rewriter rather than queued.
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
