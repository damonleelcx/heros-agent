import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, Drawer, Num, PageFrame, Section, Stat, StatRow } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { timestamp } from "@/lib/format";
import { retryJob, cancelJob } from "@/lib/actions";
import type { FleetHealth, Job } from "@/lib/types";

/**
 * The jobs & fleet surface, over the EXISTING P4/P6 queue.
 *
 * Fleet health is derived from the same queue the jobs come from — the console stands up no second
 * pipeline. An unreachable queue renders DEGRADED, distinct from an idle fleet (FR26). Cancelling a
 * job is destructive: reason-required, confirmed, audited.
 *
 * The state filter is a URL parameter (FR33) so "show me the dead letters" is a link, and the
 * unfiltered count stays on screen so a narrowed queue can never be read as a short one.
 */
export default async function FleetPage({
  searchParams,
}: {
  searchParams: Promise<{ state?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { state: stateFilter } = await searchParams;

  if (!hasCapability(identity, "job.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Fleet" title="Jobs & fleet">
          <DeniedState
            capability="job.read"
            description="Read jobs and fleet health"
            heldBy={holdersOf(identity, "job.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  const canOperate = hasCapability(identity, "job.cancel");

  let jobs: Job[] = [];
  let health: FleetHealth | null = null;
  let degraded: string | null = null;
  try {
    const [jobsRes, healthRes] = await Promise.all([
      adminFetch<{ jobs: Job[] }>("/admin/api/jobs?limit=100", { sessionToken }),
      adminFetch<FleetHealth>("/admin/api/fleet", { sessionToken }),
    ]);
    jobs = jobsRes.jobs ?? [];
    health = healthRes;
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  const shown = stateFilter ? jobs.filter((j) => j.state === stateFilter) : jobs;

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Fleet"
        title="Jobs & fleet"
        lede="The discovery, eval and optimization jobs on the existing P4/P6 queue, and the health of the worker fleet running them."
      >
        <Section title="Worker fleet" aside={health ? health.source : undefined}>
          {degraded ? (
            <DegradedState what="the queue" detail={degraded} />
          ) : !health ? (
            <DegradedState what="fleet health" />
          ) : health.degraded ? (
            <DegradedState what="the queue" detail={health.detail} />
          ) : (
            <StatRow>
              <Stat label="Ready" value={<Num value={health.ready} />} unit="jobs" />
              <Stat label="Leased" value={<Num value={health.leased} />} unit="jobs" />
              <Stat label="Done" value={<Num value={health.done} />} unit="jobs" />
              <Stat
                label="Dead"
                value={<Num value={health.dead} />}
                unit="jobs"
                tone={health.dead > 0 ? "warn" : undefined}
                meta={health.dead > 0 ? "Dead letters need a decision" : undefined}
              />
              <Stat label="Workers" value={<Num value={Object.keys(health.workers ?? {}).length} />} />
              <Stat
                label="Expired leases"
                value={<Num value={health.expired_leases} />}
                tone={health.expired_leases > 0 ? "warn" : undefined}
                meta={
                  health.oldest_lease_age_seconds > 0
                    ? `Oldest lease ${health.oldest_lease_age_seconds}s`
                    : undefined
                }
              />
            </StatRow>
          )}
        </Section>

        <Section
          id="jobs"
          title="Jobs"
          aside={
            <>
              {stateFilter ? <Pill tone="accent">state = {stateFilter}</Pill> : null}
              <span>
                {shown.length} shown · {jobs.length} on the queue
              </span>
            </>
          }
          flush
        >
          <div className="section__body">
            <form method="get" className="form-row">
              <span>
                <label htmlFor="state">Filter by state</label>
                <input
                  id="state"
                  name="state"
                  type="search"
                  defaultValue={stateFilter ?? ""}
                  placeholder="ready, leased, done, dead"
                  autoComplete="off"
                />
              </span>
              <button type="submit" className="primary">
                Apply
              </button>
              <a className="palette-trigger" href="/fleet">
                Clear
              </a>
            </form>
          </div>
          {degraded ? (
            <div className="section__body">
              <DegradedState what="the queue" detail={degraded} />
            </div>
          ) : shown.length === 0 ? (
            <div className="section__body">
              <EmptyState
                what="jobs"
                hint={stateFilter ? `No job is in state “${stateFilter}”.` : undefined}
              />
            </div>
          ) : (
            <DataTable
              caption="Queue items, newest first."
              columns={[
                { label: "Run" },
                { label: "State" },
                { label: "Attempts", numeric: true },
                { label: "Worker" },
                { label: "Enqueued" },
                ...(canOperate ? [{ label: "Administer" }] : []),
              ]}
            >
              {shown.map((j) => (
                <tr key={j.run_id}>
                  <th scope="row" className="mono">
                    {j.run_id}
                  </th>
                  <td>
                    {j.state === "dead" ? (
                      <Pill tone="danger">{j.state}</Pill>
                    ) : j.state === "leased" ? (
                      <Pill tone="neutral">running</Pill>
                    ) : (
                      <Pill tone="neutral">{j.state}</Pill>
                    )}
                    {j.dead_letter_reason ? <div className="hint">{j.dead_letter_reason}</div> : null}
                  </td>
                  <td className="num">{j.attempts}</td>
                  <td className="mono">{j.leased_by ?? "—"}</td>
                  <td>{timestamp(j.enqueued_at)}</td>
                  {canOperate ? (
                    <td>
                      {j.state === "dead" ? (
                        <Drawer summary="Retry">
                          <ActionForm
                            title={`Retry ${j.run_id}`}
                            submitLabel="Retry"
                            targetLabel={j.run_id}
                            actionName="job.retry"
                            action={retryJob.bind(null, j.run_id)}
                          />
                        </Drawer>
                      ) : (
                        <Drawer summary="Cancel">
                          <ActionForm
                            title={`Cancel ${j.run_id}`}
                            hint="Parks the running or queued job with your reason."
                            submitLabel="Cancel job"
                            danger
                            targetLabel={j.run_id}
                            actionName="job.cancel"
                            action={cancelJob.bind(null, j.run_id)}
                          />
                        </Drawer>
                      )}
                    </td>
                  ) : null}
                </tr>
              ))}
            </DataTable>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
