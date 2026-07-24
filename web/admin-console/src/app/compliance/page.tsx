import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, PageFrame, Section } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { timestamp } from "@/lib/format";
import { executeGDPR } from "@/lib/actions";
import type { GDPRRequest } from "@/lib/types";

/**
 * The compliance surface: GDPR data-deletion.
 *
 * Execution is Superadmin-only and irreversible, so the form requires the operator to TYPE the
 * subject identifier as a second confirmation (FR24). The completion record is verifiable and the
 * append-only audit chain stays intact via a non-PII tombstone reference — both shown here after the
 * erasure runs.
 *
 * This is the console's one true one-way door, and it is the reason the receipt names the absence of
 * an undo in words rather than leaving a missing button to imply it (FR36).
 */
export default async function CompliancePage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "gdpr.execute")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Compliance" title="Data deletion">
          <DeniedState
            capability="gdpr.execute"
            description="Execute a data-deletion request"
            heldBy={holdersOf(identity, "gdpr.execute")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let requests: GDPRRequest[] = [];
  let degraded: string | null = null;
  try {
    const res = await adminFetch<{ requests: GDPRRequest[] }>("/admin/api/gdpr", { sessionToken });
    requests = res.requests ?? [];
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Compliance"
        title="Data deletion"
        lede="Actioning a data-subject erasure removes or tombstones the subject’s content and produces a verifiable completion record. The audit chain keeps a non-PII tombstone reference, so no entry is ever removed and the chain stays verifiable."
      >
        <Section id="erasure" title="Execute an erasure">
          <ActionForm
            title="Data-subject erasure"
            hint="Irreversible. The subject's content is removed or tombstoned; a verifiable completion record is produced; the action is audited. Type the subject reference to confirm."
            submitLabel="Execute erasure"
            danger
            typedTarget
            targetIdentifier="subject:<the subject you enter>"
            actionName="gdpr.execute"
            action={executeGDPR}
          >
            <label htmlFor="subject_ref">Data subject reference</label>
            <p className="hint">
              The identifier of the subject whose data is to be erased. You will type{" "}
              <code>subject:&lt;this value&gt;</code> below to confirm.
            </p>
            <input id="subject_ref" name="subject_ref" type="text" autoComplete="off" required />
          </ActionForm>
        </Section>

        <Section title="Requests" aside={`${requests.length} filed`} flush>
          {degraded ? (
            <div className="section__body">
              <DegradedState what="the compliance store" detail={degraded} />
            </div>
          ) : requests.length === 0 ? (
            <div className="section__body">
              <EmptyState what="deletion requests" />
            </div>
          ) : (
            <DataTable
              caption="Data-deletion requests and their completion status."
              columns={[
                { label: "Request" },
                { label: "Subject" },
                { label: "Status" },
                { label: "Removed", numeric: true, unit: "items" },
                { label: "Verification ref" },
                { label: "Completed" },
              ]}
            >
              {requests.map((r) => (
                <tr key={r.request_id}>
                  <th scope="row" className="mono">
                    {r.request_id}
                  </th>
                  <td className="mono">{r.subject_ref}</td>
                  <td>
                    {r.status === "completed" ? (
                      <Pill tone="ok">completed</Pill>
                    ) : (
                      <Pill tone="warn">{r.status}</Pill>
                    )}
                  </td>
                  <td className="num">{r.removed_count}</td>
                  <td className="mono">{r.verification_ref ?? "—"}</td>
                  <td>{timestamp(r.completed_at)}</td>
                </tr>
              ))}
            </DataTable>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
