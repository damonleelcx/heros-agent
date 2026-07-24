import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, Drawer, PageFrame, Section } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { timestamp } from "@/lib/format";
import { addModel, deprecateModel, repointPriceRef } from "@/lib/actions";
import type { ModelRecord } from "@/lib/types";

/**
 * The model-registry surface (Platform-SRE).
 *
 * Models carry a PRICE REFERENCE — an opaque handle into the provider's price catalogue — never an
 * amount (FR28). Repointing a reference affects open and future periods only; closed periods keep what
 * they closed with, which the backend enforces. Every change is audited.
 */
export default async function RegistryPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { q } = await searchParams;

  if (!hasCapability(identity, "registry.admin")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Configuration" title="Model registry">
          <DeniedState
            capability="registry.admin"
            description="Administer models and price references"
            heldBy={holdersOf(identity, "registry.admin")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let models: ModelRecord[] = [];
  let degraded: string | null = null;
  try {
    const res = await adminFetch<{ models: ModelRecord[] }>("/admin/api/registry", { sessionToken });
    models = res.models ?? [];
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  const needle = (q ?? "").trim().toLowerCase();
  const shown = needle
    ? models.filter(
        (m) =>
          m.model_id.toLowerCase().includes(needle) ||
          m.provider.toLowerCase().includes(needle) ||
          m.price_ref.toLowerCase().includes(needle),
      )
    : models;

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Configuration"
        title="Model registry"
        lede="Models and the per-model price references used to derive SUM. Price references are opaque handles into the provider’s price catalogue — no amount is entered or shown here. A repoint is non-retroactive: closed metering periods keep the reference they closed with."
      >
        <Section id="add-model" title="Add a model">
          <ActionForm
            title="Add model"
            hint="Register a new model with its provider and price reference."
            submitLabel="Add model"
            actionName="registry.add_model"
            action={addModel}
          >
            <label htmlFor="model_id">Model id</label>
            <input id="model_id" name="model_id" type="text" autoComplete="off" required />
            <label htmlFor="provider">Provider</label>
            <input id="provider" name="provider" type="text" autoComplete="off" required />
            <label htmlFor="price_ref">Price reference (an opaque handle — never an amount)</label>
            <input
              id="price_ref"
              name="price_ref"
              type="text"
              autoComplete="off"
              placeholder="price_ref_model_v1"
              required
            />
          </ActionForm>
        </Section>

        <Section
          title="Models"
          aside={
            <>
              {needle ? <Pill tone="accent">Filtered</Pill> : null}
              <span>
                {shown.length} shown · {models.length} registered
              </span>
            </>
          }
          flush
        >
          <div className="section__body">
            <form method="get" role="search" className="form-row">
              <span>
                <label htmlFor="q">Find a model, provider or price reference</label>
                <input id="q" name="q" type="search" defaultValue={q ?? ""} autoComplete="off" />
              </span>
              <button type="submit" className="primary">
                Search
              </button>
              <a className="palette-trigger" href="/registry">
                Clear
              </a>
            </form>
          </div>
          {degraded ? (
            <div className="section__body">
              <DegradedState what="the model registry" detail={degraded} />
            </div>
          ) : shown.length === 0 ? (
            <div className="section__body">
              <EmptyState
                what="registered models"
                hint={needle ? "No model matches that search." : undefined}
              />
            </div>
          ) : (
            <DataTable
              caption="Administered models and their current price references."
              columns={[
                { label: "Model" },
                { label: "Provider" },
                { label: "Price reference" },
                { label: "State" },
                { label: "Revision", numeric: true },
                { label: "Updated" },
                { label: "Administer" },
              ]}
            >
              {shown.map((m) => (
                <tr key={m.model_id}>
                  <th scope="row" className="mono">
                    {m.model_id}
                  </th>
                  <td>{m.provider}</td>
                  <td className="mono">{m.price_ref}</td>
                  <td>{m.deprecated ? <Pill tone="warn">deprecated</Pill> : <Pill tone="ok">active</Pill>}</td>
                  <td className="num">{m.revision}</td>
                  <td>{timestamp(m.updated_at)}</td>
                  <td>
                    <Drawer summary="Repoint price">
                      <ActionForm
                        title={`Repoint ${m.model_id}`}
                        hint="Applies to open and future periods only; closed periods are unchanged."
                        submitLabel="Repoint price reference"
                        actionName="registry.repoint_price_ref"
                        targetLabel={m.model_id}
                        action={repointPriceRef.bind(null, m.model_id)}
                      >
                        <label htmlFor={`ref-${m.model_id}`}>New price reference</label>
                        <input id={`ref-${m.model_id}`} name="price_ref" type="text" autoComplete="off" required />
                      </ActionForm>
                    </Drawer>
                    {!m.deprecated ? (
                      <Drawer summary="Deprecate">
                        <ActionForm
                          title={`Deprecate ${m.model_id}`}
                          hint="No new runs will select it. Closed-period SUM is unaffected."
                          submitLabel="Deprecate model"
                          danger
                          actionName="registry.deprecate_model"
                          targetLabel={m.model_id}
                          action={deprecateModel.bind(null, m.model_id)}
                        />
                      </Drawer>
                    ) : null}
                  </td>
                </tr>
              ))}
            </DataTable>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
