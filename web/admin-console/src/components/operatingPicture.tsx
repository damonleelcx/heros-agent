"use client";

import { useEffect, useRef, useState } from "react";
import type { OperatingPicture, OverviewPanel } from "@/lib/overview";
import {
  DegradedState,
  DeniedState,
  EmptyState,
  GatedState,
  NotMountedState,
  Pill,
  UnusableState,
} from "./states";
import {
  AlarmBanner,
  DataTable,
  Freshness,
  Num,
  Section,
  Stat,
  StatRow,
  Timeline,
  TimelineItem,
} from "./primitives";
import { timestamp } from "@/lib/format";
import type { Role } from "@/lib/roles";

/**
 * operatingPicture.tsx renders — and keeps current — the answer to the two questions an operator opens
 * this console with: **is anything halted, and is anything wrong** (FR34).
 *
 * # The three rules this component exists to obey
 *
 * 1. **Readable without interaction.** Halted/not and wrong/not are legible on arrival, by word and by
 *    position as well as by colour, so the state is not a colour an operator has to be able to see.
 *
 * 2. **Updates land in place.** The panel order is fixed and the row order is server-given; a refresh
 *    replaces values inside a layout that does not move. Nothing reflows, nothing re-sorts under the
 *    pointer, and an update never blanks data that is already correct — a poll in flight is a marker
 *    on the freshness line, never a spinner where the numbers were.
 *
 * 3. **Stale announces itself.** Every figure carries the time it is current as of, and a poll that
 *    fails switches the picture to a STALE rendering while keeping the last-good values. This is the
 *    one that matters most: a stale figure read as current is how an operator concludes the fleet is
 *    halted when it is not.
 *
 * # Why the previous picture is retained on failure
 *
 * Because "we could not refresh" and "there is nothing here" are different facts, and the second one
 * would be a lie. The last-good values stay on screen, explicitly labelled with when they were last
 * confirmed.
 */

const POLL_MS = 15_000;
/** A picture older than this is stale even if no poll has actually failed yet. */
const STALE_AFTER_MS = 45_000;

export function LiveOperatingPicture({
  initial,
  holders,
}: {
  initial: OperatingPicture;
  /** Which roles hold each capability, so a denied panel can name an escalation path (FR22). */
  holders: Record<string, Role[]>;
}) {
  const [picture, setPicture] = useState(initial);
  const [stale, setStale] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      setRefreshing(true);
      try {
        const res = await fetch("/api/overview", { cache: "no-store" });
        if (!res.ok) throw new Error(`overview responded ${res.status}`);
        const next = (await res.json()) as OperatingPicture;
        if (cancelled) return;
        // The new values replace the old ones inside the SAME layout. No list is re-sorted here and
        // no panel is added or removed by a refresh, so nothing moves under the operator's pointer.
        setPicture(next);
        setStale(false);
      } catch {
        // Keep the last-good picture and say it is stale. Blanking it would replace a known-old truth
        // with no truth at all, which is worse in every incident this console exists for.
        if (!cancelled) setStale(true);
      } finally {
        if (!cancelled) setRefreshing(false);
      }
    }

    timer.current = setInterval(poll, POLL_MS);
    return () => {
      cancelled = true;
      if (timer.current) clearInterval(timer.current);
    };
  }, []);

  // Age-based staleness catches the case where the tab was suspended: no poll failed, but the numbers
  // on screen are old, and the operator has no way to know that from the numbers themselves.
  useEffect(() => {
    const check = setInterval(() => {
      const age = Date.now() - new Date(picture.as_of).getTime();
      if (age > STALE_AFTER_MS) setStale(true);
    }, 5_000);
    return () => clearInterval(check);
  }, [picture.as_of]);

  const asOf = timestamp(picture.as_of);
  const ks = picture.killswitch;
  const halted = ks.data?.global_armed || (ks.data?.tenant_armed.length ?? 0) > 0;

  return (
    <>
      {/* The fleet-wide halt is stated before anything else on the page, because it is the one fact
       * that changes what every figure below it means. A per-tenant halt is NOT banner-worthy: it is
       * ordinary operations, and a banner that appears for ordinary operations is a banner operators
       * learn to look past — which would cost exactly the salience the fleet-wide case needs. */}
      {ks.data?.global_armed ? (
        <AlarmBanner word="Global kill switch armed">
          every tenant&rsquo;s autonomous merges are halted
        </AlarmBanner>
      ) : null}

      <p className="freshness-line">
        <Freshness asOf={asOf} stale={stale} />
        {refreshing && !stale ? <span className="caption"> · refreshing</span> : null}
      </p>

      {/* ── Is anything halted? ── */}
      <Section
        title="Autonomous merges"
        aside={
          ks.state !== "ok" ? null : halted ? (
            <Pill tone="danger">Halted</Pill>
          ) : (
            <Pill tone="ok">Running</Pill>
          )
        }
      >
        <PanelBody panel={ks} capability="killswitch.operate" what="the kill-switch state store" holders={holders}>
          {ks.data ? (
            <StatRow>
              <Stat
                label="Fleet-wide"
                small
                tone={ks.data.global_armed ? "alarm" : undefined}
                value={ks.data.global_armed ? "ARMED — every tenant halted" : "Disarmed"}
                meta={ks.data.global_armed ? "No tenant is merging" : "Merges proceed under each tenant's own gate"}
              />
              <Stat
                label="Per-tenant halts"
                small
                tone={ks.data.tenant_armed.length > 0 ? "warn" : undefined}
                value={<Num value={ks.data.tenant_armed.length} />}
                unit="tenants"
                meta={
                  ks.data.tenant_armed.length > 0
                    ? ks.data.tenant_armed.map((t) => t.scope).join(", ")
                    : "None"
                }
              />
            </StatRow>
          ) : null}
        </PanelBody>
      </Section>

      {/* ── Is anything wrong? ── */}
      <div className="grid-2">
        <Section title="Worker fleet" aside={picture.fleet.data ? picture.fleet.data.source : undefined}>
          <PanelBody panel={picture.fleet} capability="job.read" what="the queue" holders={holders}>
            {picture.fleet.data ? (
              <StatRow>
                <Stat label="Ready" value={<Num value={picture.fleet.data.ready} />} unit="jobs" />
                <Stat label="Running" value={<Num value={picture.fleet.data.leased} />} unit="jobs" />
                <Stat
                  label="Dead"
                  value={<Num value={picture.fleet.data.dead} />}
                  unit="jobs"
                  tone={picture.fleet.data.dead > 0 ? "warn" : undefined}
                />
                <Stat
                  label="Expired leases"
                  value={<Num value={picture.fleet.data.expired_leases} />}
                  tone={picture.fleet.data.expired_leases > 0 ? "warn" : undefined}
                />
              </StatRow>
            ) : null}
          </PanelBody>
        </Section>

        <Section title="Tenants">
          <PanelBody panel={picture.tenants} capability="tenant.read" what="the tenant directory" holders={holders}>
            {picture.tenants.data ? (
              <StatRow>
                <Stat label="On the platform" value={<Num value={picture.tenants.data.total} />} />
                <Stat
                  label="Suspended"
                  value={<Num value={picture.tenants.data.suspended} />}
                  tone={picture.tenants.data.suspended > 0 ? "warn" : undefined}
                />
                <Stat
                  label="Merges halted"
                  value={<Num value={picture.tenants.data.halted} />}
                  tone={picture.tenants.data.halted > 0 ? "warn" : undefined}
                />
              </StatRow>
            ) : null}
          </PanelBody>
        </Section>
      </div>

      <Section
        title="Active impersonations"
        aside={
          picture.impersonations.state === "ok" && (picture.impersonations.data?.length ?? 0) > 0 ? (
            <Pill tone="danger">{picture.impersonations.data?.length} live</Pill>
          ) : undefined
        }
        flush={(picture.impersonations.data?.length ?? 0) > 0}
      >
        <PanelBody
          panel={picture.impersonations}
          capability="impersonate.read"
          what="the impersonation register"
          holders={holders}
          padded
        >
          {picture.impersonations.data && picture.impersonations.data.length > 0 ? (
            <DataTable
              caption="Impersonation sessions currently open, and when each expires."
              columns={[
                { label: "Tenant" },
                { label: "Scope" },
                { label: "Reason" },
                { label: "Expires in", numeric: true, unit: "seconds" },
              ]}
            >
              {picture.impersonations.data.map((s) => (
                <tr key={s.id}>
                  <th scope="row" className="mono">
                    {s.tenant_id}
                  </th>
                  <td>
                    {s.scope === "elevated_write" ? (
                      <Pill tone="danger">write</Pill>
                    ) : (
                      <Pill tone="warn">read-only</Pill>
                    )}
                  </td>
                  <td>{s.reason}</td>
                  <td className="num">
                    <Num value={s.remaining_seconds} />
                  </td>
                </tr>
              ))}
            </DataTable>
          ) : (
            <EmptyState what="open impersonation sessions" hint="Nobody is acting as a tenant right now." />
          )}
        </PanelBody>
      </Section>

      <div className="grid-2">
        <Section title="Anomalies">
          <PanelBody
            panel={picture.anomalies}
            capability="crosstenant.read"
            what="the P2.5 anomaly aggregate"
            holders={holders}
          >
            {picture.anomalies.data?.suppressed ? (
              <p className="hint">{picture.anomalies.data.suppression_reason}</p>
            ) : picture.anomalies.data?.rows && picture.anomalies.data.rows.length > 0 ? (
              <DataTable
                caption="Unresolved anomalies across the fleet."
                columns={[{ label: "Signal" }, { label: "Value", numeric: true }, { label: "Detail" }]}
              >
                {picture.anomalies.data.rows.map((r) => (
                  <tr key={`${r.label}-${r.detail ?? ""}`}>
                    <th scope="row">{r.label}</th>
                    <td className="num">
                      <Num value={r.value} kind="quantity" />
                    </td>
                    <td>{r.detail ?? "—"}</td>
                  </tr>
                ))}
              </DataTable>
            ) : (
              <EmptyState what="unresolved anomalies" />
            )}
          </PanelBody>
        </Section>

        <Section title="Recent privileged actions">
          <PanelBody panel={picture.recent} capability="audit.read" what="the audit store" holders={holders}>
            {picture.recent.data && picture.recent.data.length > 0 ? (
              <Timeline>
                {picture.recent.data.map((e) => (
                  <TimelineItem key={e.seq} when={timestamp(e.created_at)}>
                    <a href={`/audit?seq=${e.seq}`} className="mono">
                      {e.action}
                    </a>{" "}
                    on <span className="mono">{e.target}</span> by{" "}
                    <span className="mono">{e.actor_admin_id}</span>
                    {e.result !== "applied" ? <> · {e.result}</> : null}
                  </TimelineItem>
                ))}
              </Timeline>
            ) : (
              <EmptyState what="recent actions" />
            )}
          </PanelBody>
        </Section>
      </div>
    </>
  );
}

/**
 * PanelBody renders the panel's permission and transport verdicts before its content, so a denial can
 * never be mistaken for emptiness and a failed subsystem can never be mistaken for a healthy one
 * (FR22, FR26).
 */
function PanelBody<T>({
  panel,
  capability,
  what,
  holders,
  padded,
  children,
}: {
  panel: OverviewPanel<T>;
  capability: string;
  what: string;
  holders: Record<string, Role[]>;
  padded?: boolean;
  children: React.ReactNode;
}) {
  const verdict = ((): React.ReactNode => {
    switch (panel.state) {
      case "denied":
        return (
          <DeniedState capability={capability} description={panel.detail} heldBy={holders[capability] ?? []} />
        );
      case "not_mounted":
        return <NotMountedState what={what} detail={panel.detail} />;
      case "gated":
        return <GatedState what={what} />;
      case "unusable":
        return <UnusableState what={what} detail={panel.detail} />;
      case "degraded":
        return <DegradedState what={what} detail={panel.detail} />;
      default:
        return null;
    }
  })();

  if (verdict) return padded ? <div className="section__body">{verdict}</div> : verdict;
  return <>{children}</>;
}
