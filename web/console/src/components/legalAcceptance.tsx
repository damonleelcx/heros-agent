import Link from "next/link";
import { DataTable, Empty, Section } from "@/components/primitives";

/**
 * legalAcceptance.tsx renders the acceptance history and the outstanding-document notice (tasks 10.1,
 * 10.3).
 *
 * # 🔴 Every entry links to the EXACT ARCHIVED TEXT that was accepted
 *
 * Not to the current version. A history that said "Terms of Service · 2026-07-31" and linked to whatever
 * is live today would be a list of dates: the reader could not see what they agreed to, which is the only
 * question this surface exists to answer.
 *
 * The link goes to `/legal/{kind}/v/{version}`, which is served forever and never redirects to the
 * current version — a superseded page says it is superseded and keeps showing its own text.
 *
 * # 🔴 The notice does not block anything
 *
 * When a document is outstanding, this renders a persistent, dismissible notice naming the document and
 * its effective date and offering to read it. The console keeps working. That is Decision 4: a consent
 * modal keyed to a deployment blocks every customer simultaneously, on release day, converting a legal
 * update into an outage while interrupting work somebody is in the middle of.
 *
 * The gate lives at the COMMITMENT — first sign-in, checkout, plan change — and nowhere else.
 */

export type AcceptanceRow = {
  document_kind: string;
  document_version: string;
  content_hash: string;
  accepted_at: string;
  method: string;
  principal_id: string;
  archived_route: string;
  superseded: boolean;
};

export type PendingRow = {
  document_kind: string;
  document_version: string;
  /** The hash of the exact text the reader is shown. The gate submits it and the server re-checks it. */
  content_hash: string;
  effective_date: string;
  route: string;
  material: boolean;
};

const KIND_LABEL: Record<string, string> = {
  terms: "Terms of Service",
  privacy: "Privacy Notice",
};

const METHOD_LABEL: Record<string, string> = {
  signin: "at sign-in",
  checkout: "at checkout",
  plan_change: "at a plan change",
  api: "through the API",
};

function label(kind: string): string {
  return KIND_LABEL[kind] ?? kind;
}

/** OutstandingNotice names what is outstanding and offers to read it. It blocks nothing. */
export function OutstandingNotice({ pending, unknown }: { pending: PendingRow[]; unknown?: boolean }) {
  if (unknown) {
    /*
     * 🔴 "We could not determine what is outstanding" is NOT the same as "nothing is outstanding", and
     * rendering the second when we mean the first would silently clear the gate. The platform reports
     * `pending_unknown` for exactly this, and it is surfaced rather than smoothed.
     */
    return (
      <div className="rounded-xl border border-border bg-card p-4">
        <p className="text-sm text-foreground">
          We could not check whether any document needs your acceptance.
        </p>
        <p className="hint mt-2 max-w-none">
          This is a fault on our side, not a statement that nothing is outstanding. Your history below is
          accurate and complete. Nothing about your access has changed.
        </p>
      </div>
    );
  }

  if (pending.length === 0) return null;

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <p className="stat__label">Needs your acceptance</p>
      <ul className="mt-2 flex list-none flex-col gap-3 p-0">
        {pending.map((row) => (
          <li key={`${row.document_kind}-${row.document_version}`}>
            <p className="text-sm text-foreground">
              A new <strong className="font-semibold">{label(row.document_kind)}</strong> (version{" "}
              {row.document_version}) takes effect on {row.effective_date}.
            </p>
            <p className="caption mt-1">
              {row.material
                ? "This is a material change, so we will ask you to accept it before your next checkout or plan change."
                : "This is a non-material change and asks nothing of you."}
            </p>
            <Link className="prose-link mt-2 inline-block text-sm" href={row.route}>
              Read it
            </Link>
          </li>
        ))}
      </ul>
      <p className="hint mt-3 max-w-none">
        The console keeps working. Nothing here interrupts a run in progress, and you can read any of
        these documents without accepting anything.
      </p>
    </div>
  );
}

/** AcceptanceHistory is the record of what this tenant has accepted, and when. */
export function AcceptanceHistory({
  accepted,
  pending,
  unknown,
}: {
  accepted: AcceptanceRow[];
  pending: PendingRow[];
  unknown?: boolean;
}) {
  return (
    <Section title="Agreements">
      <OutstandingNotice pending={pending} unknown={unknown} />

      {accepted.length === 0 ? (
        <Empty title="Nothing has been accepted yet">
          When a document is accepted at sign-in, at checkout or at a plan change, it is recorded here
          with the version and a hash of the exact text.
        </Empty>
      ) : (
        <DataTable
          caption="Legal documents this tenant has accepted, with the version and content hash of the exact text"
          columns={[
            { key: "document", label: "Document" },
            { key: "version", label: "Version" },
            { key: "accepted", label: "Accepted" },
            { key: "how", label: "How" },
            { key: "who", label: "Principal" },
            { key: "hash", label: "Content hash" },
          ]}
        >
          <tbody>
            {accepted.map((row) => (
              <tr key={`${row.document_kind}-${row.document_version}-${row.accepted_at}`}>
                <td>{label(row.document_kind)}</td>
                {/* The version is the LINK, because the version is what identifies the text. */}
                <td>
                  <Link className="prose-link" href={row.archived_route}>
                    {row.document_version}
                  </Link>
                </td>
                <td>
                  {row.accepted_at.slice(0, 10)}
                  {row.superseded ? <span className="caption"> · superseded</span> : null}
                </td>
                <td>{METHOD_LABEL[row.method] ?? row.method}</td>
                {/* The opaque principal. Never an email, never a name — there is no column for one. */}
                <td className="mono text-xs">{row.principal_id}</td>
                <td className="mono reading__hash text-xs">{row.content_hash.slice(0, 16)}…</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      )}

      <p className="hint max-w-none">
        A recorded acceptance points at a document <strong className="font-semibold">version and a
        content hash</strong>, never at a URL — so the text you agreed to can still be read years later,
        exactly as it was. See the{" "}
        <Link className="prose-link" href="/legal">
          version history
        </Link>
        .
      </p>
    </Section>
  );
}
