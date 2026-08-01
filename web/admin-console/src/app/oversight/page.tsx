import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, PageFrame, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { timestamp } from "@/lib/format";
import type { IntegrationState, OversightView } from "@/lib/types";

/**
 * The oversight surface (P26 wave 26e) — READ-ONLY.
 *
 * Three questions an operator could not answer before, and one it deliberately still cannot:
 *
 *   · which factor authenticated each operator session, and when
 *   · which legal document versions each tenant has accepted, and which it OWES
 *   · whether observability reporting is actually working
 *   · payments — NOT YET READABLE, stated as such rather than rendered as an empty page
 *
 * # 🔴 Unknown is an answer, and a guess is not
 *
 * A per-tenant deployed version that no signal carries renders as *unknown*, naming the collection
 * that would make it readable. An inferred version rendered as a version is a wrong number that gets
 * acted on during an incident — which is exactly when it will be read.
 *
 * # 🔴 Reporting health comes from the platform's own readiness surface
 *
 * Never from a third party's dashboard, which is the least available part of the system during an
 * incident — and never as a boolean, because *not configured* is a decision and *degraded* is a fault.
 */

const INTEGRATION_LABEL: Record<IntegrationState, string> = {
  absent: "not configured",
  configured: "configured",
  degraded: "degraded",
};

/* `absent` is NEUTRAL: nothing is wrong, nothing is configured. Only `degraded` is a fault, and it is
 * the only state on this page that earns the hazard palette. */
const INTEGRATION_TONE: Record<IntegrationState, "ok" | "warn" | "neutral"> = {
  absent: "neutral",
  configured: "ok",
  degraded: "warn",
};

export default async function OversightPage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "audit.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Oversight" title="Identity, consent & reporting">
          <DeniedState
            capability="audit.read"
            description="Read operator sessions, consent state and reporting health"
            heldBy={holdersOf(identity, "audit.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let view: OversightView | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<OversightView>("/admin/api/oversight", { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Oversight"
        title="Identity, consent & reporting"
        lede={
          <>
            Which factor authenticated each operator session, which legal versions each tenant owes, and
            whether reporting is working — read from the platform&rsquo;s own readiness surface. Where a
            read is not possible, this page says so and names what would make it possible.
          </>
        }
      >
        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="oversight" detail={failure.message} />
          ) : (
            <DegradedState what="the oversight read model" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the oversight read model" />
        ) : (
          <Tabs
            tabs={[
              {
                id: "sessions",
                label: "Operator sessions",
                content: (
                  <>
                    <Section
                      title="Identity provider"
                      aside={
                        view.identity_provider.test_mode ? (
                          <Pill tone="neutral">test mode</Pill>
                        ) : (
                          <Pill tone="ok">production</Pill>
                        )
                      }
                    >
                      <p className="state__body">{view.identity_provider.note}</p>
                      <p className="state__body mono">
                        {view.identity_provider.kind} · {view.identity_provider.issuer}
                      </p>
                    </Section>
                    <Section
                      title="Sessions and the factor that authenticated them"
                      flush={view.sessions.length > 0}
                    >
                      {view.sessions.length === 0 ? (
                        <EmptyState what="operator sessions" />
                      ) : (
                        <DataTable
                          caption="Each operator session, the factor the platform verified, and when."
                          columns={[
                            { label: "Admin" },
                            { label: "Factor" },
                            { label: "Strength" },
                            { label: "Verified at" },
                            { label: "Expires" },
                            { label: "Live" },
                          ]}
                        >
                          {view.sessions.map((s) => (
                            <tr key={s.session_id}>
                              <th scope="row" className="mono">
                                {s.admin_id}
                              </th>
                              <td className="mono">{s.factor || "none recorded"}</td>
                              <td>
                                {/* A reviewer READS the strength rather than inferring it. */}
                                <Pill tone={s.multi_factor ? "ok" : "neutral"}>
                                  {s.multi_factor ? "multi-factor" : "single factor"}
                                </Pill>
                              </td>
                              <td>{timestamp(s.verified_at)}</td>
                              <td>{timestamp(s.expires_at)}</td>
                              <td>{s.live ? <Pill tone="ok">live</Pill> : <Pill tone="neutral">ended</Pill>}</td>
                            </tr>
                          ))}
                        </DataTable>
                      )}
                    </Section>
                  </>
                ),
              },
              {
                id: "legal",
                label: "Legal acceptance",
                content: (
                  <Section title="Accepted and owed, per tenant" flush={view.legal.length > 0}>
                    {!view.legal_known ? (
                      <DegradedState
                        what="the legal acceptance record"
                        detail="No legal service is wired in this deployment. An empty acceptance table would read as 'nobody owes anything', which is a different claim."
                      />
                    ) : view.legal.length === 0 ? (
                      <EmptyState what="recorded acceptances" />
                    ) : (
                      <DataTable
                        caption="Each tenant's accepted document versions and the versions owed after a material publication."
                        columns={[
                          { label: "Tenant" },
                          { label: "Document" },
                          { label: "Accepted" },
                          { label: "Archived text" },
                          { label: "Owed" },
                        ]}
                      >
                        {view.legal.map((l, i) => (
                          <tr key={`${l.tenant_id}-${l.kind ?? "none"}-${l.owed_version ?? i}`}>
                            <th scope="row" className="mono">
                              {l.tenant_id}
                            </th>
                            <td>{l.kind || "—"}</td>
                            <td className="mono">{l.accepted_version || "—"}</td>
                            <td>
                              {/* The ARCHIVED text at the accepted content hash — not the current text,
                                  which is a different document and is what a dispute is about. */}
                              {l.archive_href ? (
                                <a href={l.archive_href} className="mono">
                                  {l.accepted_hash?.slice(0, 12)}
                                </a>
                              ) : (
                                "—"
                              )}
                            </td>
                            <td>
                              {l.owed_version ? (
                                <>
                                  <Pill tone="warn">re-acceptance owed</Pill>
                                  <div className="hint">
                                    <a href={l.owed_href}>{l.owed_version}</a> since {l.owed_since}
                                  </div>
                                </>
                              ) : (
                                <span className="hint">nothing owed</span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "reporting",
                label: "Reporting health",
                content: (
                  <Section title="Observability integrations" flush={view.integrations.length > 0}>
                    {!view.integrations_known ? (
                      <DegradedState
                        what="the platform's readiness surface"
                        detail="No readiness surface is wired, so the integrations' states were not read. They are NOT reported as 'not configured' — 'nothing is configured' and 'we did not ask' are different answers."
                      />
                    ) : view.integrations.length === 0 ? (
                      <EmptyState what="observability integrations" />
                    ) : (
                      <DataTable
                        caption="Each integration's state, read from the platform's own readiness surface."
                        columns={[
                          { label: "Integration" },
                          { label: "State" },
                          { label: "Failure class" },
                          { label: "Read from" },
                        ]}
                      >
                        {view.integrations.map((n) => (
                          <tr key={n.name}>
                            <th scope="row">{n.name}</th>
                            <td>
                              <Pill tone={INTEGRATION_TONE[n.state]}>{INTEGRATION_LABEL[n.state]}</Pill>
                            </td>
                            <td>{n.failure_class || "—"}</td>
                            <td className="hint">{n.source}</td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "deployments",
                label: "Deployments",
                content: (
                  <Section title="Shape and version, where derivable" flush={view.deployments.length > 0}>
                    {view.deployments.length === 0 ? (
                      <EmptyState what="tenant deployments" />
                    ) : (
                      <DataTable
                        caption="Each tenant's deployment shape and version, or an explicit unknown with the missing collection named."
                        columns={[
                          { label: "Tenant" },
                          { label: "Shape" },
                          { label: "Version" },
                          { label: "Why unknown" },
                        ]}
                      >
                        {view.deployments.map((d) => (
                          <tr key={d.tenant_id}>
                            <th scope="row" className="mono">
                              {d.tenant_id}
                            </th>
                            <td>{d.shape || <span className="hint">unknown</span>}</td>
                            <td className="mono">
                              {/* 🔴 Nothing here is inferred from an API contract version, a feature
                                  probe or any other proxy. */}
                              {d.unknown ? <span className="hint">unknown</span> : d.version}
                            </td>
                            <td className="hint">
                              {d.unknown ? `requires ${d.missing_collection}` : "—"}
                            </td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "not-yet",
                label: "Not yet readable",
                content: (
                  <Section title="Reads this platform cannot make yet" aside="stated, not blank">
                    {/* 🔴 No count, no zero, no empty table. A capability that has not shipped is stated
                        as such; rendering an empty page as though it were working is the failure this
                        whole phase exists to correct. */}
                    {view.not_yet_readable.map((n) => (
                      <div className="state state--empty" role="note" key={n.subject}>
                        <p className="state__title">{n.subject}</p>
                        <p className="state__body">{n.statement}</p>
                        <p className="state__body">
                          <strong>Requires:</strong> {n.requires}
                        </p>
                      </div>
                    ))}
                  </Section>
                ),
              },
            ]}
          />
        )}
      </PageFrame>
    </OperatorShell>
  );
}
