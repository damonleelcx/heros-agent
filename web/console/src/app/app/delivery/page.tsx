import { loadDeliveryProjection, loadProjection } from "@/lib/projection";
import { AxisFrame } from "@/components/axisFrame";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { CurrentValue, ReadOn } from "@/components/editorKit";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";
import { AXIS_DOC } from "@/lib/axisSubject";
import type { DeliveryProjectionOutcome } from "@/lib/projection";
import { AxisProjectionPanel } from "@/components/axisProjection";
import Link from "next/link";
import type { ChangeDeliveryView, DeliveriesView, DeliveryView } from "@/lib/types.generated";
import { load } from "@/lib/view";
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

export default async function DeliveryPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  // 🔴 P37 — bound to the reader's own node like every other axis surface, so a reader arriving from
  // `/app/context` is still looking at the same call site. The delivery LIST is workflow-wide and stays
  // that way; the subject is what the `undeliverable` count and the axis panel are about.
  const subjectOutcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const values = await loadAxisValues(
    subjectOutcome.state === "resolved" ? subjectOutcome.subject.workflow_id : undefined,
  );
  const { outcome } = await load<DeliveriesView>((paths) => paths.deliveries(), ["route"]);
  const routes = await fetchDeliveryRoutes();
  // 🔴 P29 §5.10 — the delivery table crossed with THIS organization's nodes. The table above is a
  // total build fact and it is correct; it has never said anything about the reader's own call sites,
  // and `undeliverable` is the number it exists to produce.
  const projection = await loadProjection();
  const delivery = await loadDeliveryProjection();

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
    {
      id: "your-nodes",
      label: "Your nodes",
      // Both projections in ONE tab: "which of my nodes can receive a change" and "what does each axis
      // do at each of my call sites" are the same question asked twice, and splitting them across two
      // tabs would make the reader compare across a click.
      content: (
        <div className="flex flex-col gap-8">
          <YourNodesTab outcome={delivery} />
          <AxisProjectionPanel axis="model" outcome={projection} />
        </div>
      ),
    },
  ];

  return (
    <PageFrame
      eyebrow="Delivery"
      title="How a change reaches your agent"
      lede="Every verified optimization that reached a pull request on your repository, and — for the ones that cannot — which route refused and why. A human merges; the platform never does below Autonomous."
      wide
    >
      {/*
        🔴 Bound to the reader's own node like every other axis surface (FR1), so a reader arriving from
        `/app/context` is still looking at the same call site. The delivery LIST is workflow-wide and
        stays that way — a pull request is not a per-node fact — but the `undeliverable` count and the
        axis panel are about the subject, and an unconnected reader must reach neither.
      */}
      {/*
        🔴 The AXIS half is inside the frame; the DELIVERY LIST is not, and the split is deliberate.
        FR4 requires that no fixture occupy the position the reader's own data would occupy — it does not
        require the rest of the page to disappear. A reader with pull requests and no reported IR
        structure still has pull requests, and gating them behind a subject would take a capability away
        from exactly the reader who has least. Found by P12's own acceptance run, which went red when the
        whole page was wrapped.
      */}
      <AxisFrame axis="delivery" outcome={subjectOutcome} returnTo="/app/delivery">
        {(subject) => (
          <div className="flex flex-col gap-4">
            <Section title="This node's delivery routes">
              <CurrentValue
                {...(valueFor(values, subject.node_id, "model")
                  ? {
                      state: "observed" as const,
                      current: `${subject.node_id} is reported`,
                      detail: subject.language,
                    }
                  : {
                      state: "not_measured" as const,
                      missingInput: "unresolved_in_ir",
                      because:
                        "this node was not present in the structure this workflow reported, so it cannot be counted as deliverable or undeliverable either way",
                    })}
              />
              <ReadOn href={AXIS_DOC.delivery}>The two routes, the six states, and whose move a refusal is</ReadOn>
            </Section>
          </div>
        )}
      </AxisFrame>
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}

/**
 * YourNodesTab is `the delivery table × your nodes` — and the one number it exists to produce is
 * `undeliverable`: how many of THIS reader's reported nodes cannot receive a change by EITHER route.
 *
 * 🚫 A node the platform was not told about is NOT counted as undeliverable. That would be a claim
 * about the customer's code drawn entirely from our own ignorance, and it is exactly the number a
 * reader would act on — so the denominator is `reported_cells`, printed beside it.
 */
function YourNodesTab({ outcome }: { outcome: DeliveryProjectionOutcome }) {
  if (outcome.state === "not-mounted") {
    return (
      <p className="hint">
        This deployment does not accept workflow structure, so there is nothing to cross the delivery
        table with. Nothing failed — the capability is not served here.
      </p>
    );
  }
  if (outcome.state === "read-failed") {
    return (
      <p className="hint">
        Your reported structure could not be read. This is <strong>not</strong> the same as having sent
        none, and nothing has been lost.
        {outcome.detail ? <span className="mono block">{outcome.detail}</span> : null}
      </p>
    );
  }
  if (outcome.state === "not-reported") {
    return (
      <div className="flex flex-col gap-3 rounded-xl border border-dashed border-border bg-card/50 p-5">
        <p className="text-sm text-foreground">
          No node of yours can be counted as undeliverable, because the platform has not been told about
          any.
        </p>
        <p className="hint">
          {outcome.detail} Send it with <code className="mono">{outcome.fill_with}</code>.
        </p>
      </div>
    );
  }

  const p = outcome.projection;
  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-xl border border-border bg-card p-5">
        <p className="text-sm text-muted-foreground">Undeliverable by BOTH routes</p>
        {/* 🔴 The denominator, beside the count. `undeliverable / reported_cells` is the honest ratio;
            over `cells` it would silently treat every unreported cell as deliverable. */}
        <p className="mono text-2xl text-foreground">
          {p.undeliverable}
          <span className="text-base text-muted-foreground"> / {p.reported_cells} reported</span>
        </p>
        <p className="hint">
          across {p.node_count} node{p.node_count === 1 ? "" : "s"} and {p.cells} cell
          {p.cells === 1 ? "" : "s"}; {p.cells - p.reported_cells} not reported and therefore not counted
          either way.
        </p>
      </div>
      <ul className="divide-y divide-border/50 overflow-hidden rounded-xl border border-border">
        {p.rows
          .filter((r) => r.deliverable === "neither")
          .map((r) => (
            <li className="flex items-start gap-3 px-4 py-3" key={`${r.node_id}:${r.axis}`}>
              <span className="min-w-0 flex-1">
                <span className="mono block truncate text-sm text-foreground">{r.symbol || r.node_id}</span>
                <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                  {[r.axis, r.language].filter(Boolean).join(" · ")}
                </span>
              </span>
              {r.owner ? (
                <span className="shrink-0 text-xs text-muted-foreground">closed by {r.owner}</span>
              ) : null}
            </li>
          ))}
      </ul>
    </div>
  );
}

/**
 * fetchDeliveryRoutes reads the route ledger.
 *
 * 🚫 There is no local fallback table. A console carrying its own copy of what each route can deliver
 * would be the second delivery table the contract exists to prevent, and it would drift in the usual
 * direction — the copy is always the optimistic one. When the platform cannot be read, the tab says so
 * rather than rendering a plausible table nobody verified.
 *
 * # 🔴 It goes through `load()`, and it did not
 *
 * It called `platformFetch` directly and declared no `requires`, so a 200 whose body was missing fields
 * came back as a NON-NULL view and rendered as a complete answer: an empty ledger, an empty legend, and
 * `read against coverage table undefined`.
 *
 * The irony was in this file. `RoutesTab`'s own banner says *"Showing a partial answer would be worse
 * than showing none, because a missing row reads as 'not applicable' — a claim about your code that
 * nobody made"* — and the code implemented that for a FAILED fetch and not for an unusable 200, which is
 * the case the sentence actually describes.
 *
 * `requires` names the fields the ledger and the three legends walk. An unusable body becomes an
 * `upstream` outcome, which lands on the same `null` the tab already renders its banner for — so the
 * copy that was already right about the intent is now also right about the behaviour.
 *
 * 🚫 It does NOT crash today, and the fix is not for a crash: every dereference in `deliveryRoutes.tsx`
 * carries a `??`. Saying that plainly matters, because the two projection reads beside it DID crash and
 * were fixed for a different reason (`lib/projection.ts`). Conflating them would file one defect twice
 * and leave the reader unsure which fence protects which.
 */
async function fetchDeliveryRoutes(): Promise<ChangeDeliveryView | null> {
  const { outcome } = await load<ChangeDeliveryView>(
    () => "/api/v1/change-delivery",
    // Every top-level array the ledger and the three legends walk, plus the version the heading states.
    // A body missing any of them renders a table that reads as "nothing applies to you".
    ["version", "routes", "cells", "source_cells", "languages", "states", "causes"],
  );
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
      {/*
        🔴 Kept, shortened. "A rollout is not a delivery" is a claim about what the platform will and will
        not do to the reader's repository, so §8.1 reviews it as a customer-facing commitment. The
        mechanism moved to the reading surface; the promise did not.
      */}
      <Banner tone="info" title="A rollout is evidence, not delivery">
        A gradual rollout runs inside your own process and never merges anything. Permanence still costs a
        codemod, a pull request, and a human.
        <ReadOn href={AXIS_DOC.delivery}>The two routes, the six states, and whose move a refusal is</ReadOn>
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
