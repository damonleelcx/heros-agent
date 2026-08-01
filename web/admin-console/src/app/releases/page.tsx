import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, PageFrame, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import type { PublishStep, ReleaseView, SmokeState, VerifyState } from "@/lib/types";

/**
 * The release and trust surface (P26 wave 26c) — READ-ONLY.
 *
 * # The incident it came from
 *
 * P20 shipped a signing pipeline, five install channels and a self-update path, and rotated the
 * signing key mid-flight after its private half turned up in a plaintext tool transcript. "Which key
 * is active, which are retired and when, and which published artefacts were signed with a retired
 * one" was an incident question with no surface behind it.
 *
 * # 🔴 Queued is not failed
 *
 * A retired runner label QUEUES until the workflow times out rather than failing. Rendering that as
 * *failed* sends an engineer to debug a build that never ran. It is a measured P20 lesson, and it is
 * why the smoke column has three values and a neutral tone for the third.
 *
 * # 🔴 A sequence is not a state
 *
 * Publish, verify and smoke happen in order, and a release that publishes green and smokes red is
 * precisely the state that reaches a stranger's laptop. The Sequence tab shows where the progression
 * stopped, not only its final outcome.
 *
 * # 🔴 No key material
 *
 * A key is an identifier and a fingerprint. This page offers no generation, no export, and no control
 * whose output is key material.
 */

const VERIFY_LABEL: Record<VerifyState, string> = {
  verified: "verified",
  failed: "failed verification",
  not_yet_verified: "not yet verified",
};

/* `not_yet_verified` is NEUTRAL, not a hazard: unchecked is not failed, and painting it with the
 * hazard palette would say something untrue about what happened (FR31). */
const VERIFY_TONE: Record<VerifyState, "ok" | "danger" | "neutral"> = {
  verified: "ok",
  failed: "danger",
  not_yet_verified: "neutral",
};

const SMOKE_LABEL: Record<Exclude<SmokeState, "">, string> = {
  passed: "passed",
  failed: "failed",
  queued_until_timeout: "queued until timeout",
};

/* 🔴 `queued_until_timeout` is NEUTRAL. The job never started; there is nothing to debug in the
 * build, and a hazard colour here would send an engineer to the wrong problem. */
const SMOKE_TONE: Record<Exclude<SmokeState, "">, "ok" | "danger" | "neutral"> = {
  passed: "ok",
  failed: "danger",
  queued_until_timeout: "neutral",
};

const STEP_LABEL: Record<PublishStep, string> = {
  publish: "stopped at publish",
  verify: "stopped at verify",
  smoke: "stopped at smoke",
  complete: "ran to completion",
};

export default async function ReleasesPage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "release.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Distribution" title="Releases & trust">
          <DeniedState
            capability="release.read"
            description="Read published releases, artefact verification and signing-key state"
            heldBy={holdersOf(identity, "release.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let view: ReleaseView | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<ReleaseView>("/admin/api/releases", { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Distribution"
        title="Releases & trust"
        lede={
          <>
            What each install channel serves, whether each artefact verified, and which signing key is
            active. A key is shown by <strong>identifier and fingerprint only</strong>. This surface
            halts nothing, unpublishes nothing and re-signs nothing.
          </>
        }
      >
        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="release oversight" detail={failure.message} />
          ) : (
            <DegradedState what="the release record" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the release record" />
        ) : (
          <Tabs
            tabs={[
              {
                id: "channels",
                label: "Channels",
                content: (
                  <Section title="Install channels" aside={view.source} flush>
                    <DataTable
                      caption="Each channel, whether a user can install from it today, and how it establishes that the bytes are ours."
                      columns={[
                        { label: "Channel" },
                        { label: "Installable today" },
                        { label: "Verification" },
                        { label: "Published versions" },
                      ]}
                    >
                      {view.channels.map((c) => (
                        <tr key={c.id}>
                          <th scope="row">{c.label}</th>
                          <td>
                            {c.delivered ? (
                              <Pill tone="ok">published</Pill>
                            ) : (
                              <>
                                <Pill tone="neutral">not yet</Pill>
                                <div className="hint">{c.blocker}</div>
                              </>
                            )}
                          </td>
                          <td>{c.verification}</td>
                          <td className="mono">
                            {c.versions && c.versions.length > 0 ? c.versions.join(", ") : "no records"}
                          </td>
                        </tr>
                      ))}
                    </DataTable>
                  </Section>
                ),
              },
              {
                id: "artefacts",
                label: "Artefacts",
                content: (
                  <Section
                    title="Published artefacts"
                    aside={view.degraded ? undefined : `${view.artefacts.length} artefacts`}
                    flush={view.artefacts.length > 0}
                  >
                    {view.degraded ? (
                      <DegradedState what="the release record" detail={view.detail} />
                    ) : view.artefacts.length === 0 ? (
                      <EmptyState what="published artefacts" />
                    ) : (
                      <DataTable
                        caption="Each artefact, its verification, its post-publish smoke, and the key that signed it."
                        columns={[
                          { label: "Version" },
                          { label: "Platform" },
                          { label: "Published" },
                          { label: "Verification" },
                          { label: "Smoke" },
                          { label: "Signing key" },
                        ]}
                      >
                        {view.artefacts.map((a) => (
                          <tr key={`${a.version}-${a.platform}-${a.name}`}>
                            <th scope="row" className="mono">
                              {a.version}
                            </th>
                            <td className="mono">{a.platform}</td>
                            <td>
                              {a.published ? (
                                <Pill tone="ok">published</Pill>
                              ) : (
                                /* A platform with no artefact is shown as ABSENT rather than omitted:
                                   an omitted row makes an incomplete release look complete. */
                                <Pill tone="neutral">absent</Pill>
                              )}
                            </td>
                            <td>
                              <Pill tone={VERIFY_TONE[a.verification]}>{VERIFY_LABEL[a.verification]}</Pill>
                            </td>
                            <td>
                              {a.smoke ? (
                                <>
                                  <Pill tone={SMOKE_TONE[a.smoke]}>{SMOKE_LABEL[a.smoke]}</Pill>
                                  {a.smoke_detail ? <div className="hint">{a.smoke_detail}</div> : null}
                                </>
                              ) : (
                                <span className="hint">not run</span>
                              )}
                            </td>
                            <td className="mono">
                              {a.signing_key_id}
                              {a.key_fingerprint ? ` · ${a.key_fingerprint}` : ""}
                              {a.signed_with_retired_key ? (
                                <div className="hint">
                                  signed with a RETIRED key, withdrawn {a.key_retired_at}
                                </div>
                              ) : null}
                            </td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "sequence",
                label: "Publish sequence",
                content: (
                  <Section title="Where each release's sequence stopped" flush={view.sequences.length > 0}>
                    {view.sequences.length === 0 ? (
                      <EmptyState what="release sequences" />
                    ) : (
                      <DataTable
                        caption="publish → verify → smoke, and the step that did not complete."
                        columns={[
                          { label: "Version" },
                          { label: "Channel" },
                          { label: "Completed" },
                          { label: "Outcome" },
                          { label: "Why" },
                        ]}
                      >
                        {view.sequences.map((s) => (
                          <tr key={`${s.version}-${s.channel}`}>
                            <th scope="row" className="mono">
                              {s.version}
                            </th>
                            <td>{s.channel}</td>
                            <td className="mono">{(s.completed ?? []).join(" → ") || "—"}</td>
                            <td>
                              <Pill tone={s.stopped_at === "complete" ? "ok" : "neutral"}>
                                {STEP_LABEL[s.stopped_at]}
                              </Pill>
                            </td>
                            <td>{s.reason}</td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "keys",
                label: "Signing keys",
                content: (
                  <Section title="Trust root and rotation record" aside="identifier and fingerprint only" flush>
                    <p className="hint">
                      No key material appears on this surface, and no control here generates or exports
                      any. A key is identified by its label and a fingerprint; the private half exists
                      only as a CI secret and has never been in this repository.
                    </p>
                    <DataTable
                      caption="Each signing key, its role, and — for a retired key — when it was withdrawn and why."
                      columns={[
                        { label: "Key" },
                        { label: "Fingerprint" },
                        { label: "Role" },
                        { label: "Retired" },
                        { label: "Signed releases" },
                        { label: "Recorded reason" },
                      ]}
                    >
                      {view.keys.map((k) => (
                        <tr key={k.id}>
                          <th scope="row" className="mono">
                            {k.id}
                          </th>
                          <td className="mono">{k.fingerprint}</td>
                          <td>
                            <Pill tone={k.role === "active" ? "ok" : "neutral"}>{k.role}</Pill>
                          </td>
                          <td>{k.retired_at ?? "—"}</td>
                          <td className="mono">
                            {k.signed_releases && k.signed_releases.length > 0
                              ? k.signed_releases.join(", ")
                              : "none"}
                          </td>
                          <td>{k.note}</td>
                        </tr>
                      ))}
                    </DataTable>
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
