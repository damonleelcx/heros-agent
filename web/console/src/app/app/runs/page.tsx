import Link from "next/link";
import { redirect } from "next/navigation";
import { load } from "@/lib/view";
import { visitedSubjects, routes } from "@/lib/subjects";
import type { Enumeration } from "@/lib/enumeration";
import { runsToSubjects, orderByRecentlyVisited, discardedVisits } from "@/lib/enumeration";
import { PageFrame, Section, Empty, Failure, DataTable, Status, Value, Banner } from "@/components/primitives";
import { SubjectPicker } from "@/components/subjectPicker";
import { RUNS_COPY } from "@/lib/organizationCopy";
import { instant } from "@/lib/format";

/**
 * The run LIST — the question this console could not ask before P27.
 *
 * # What this page replaces, and why the old one was not a list
 *
 * It was a text field, and it had to be: the platform had no collection route, because `run` carried no
 * owning organization. "Which runs are mine" was not a question the API could be asked, so somebody who
 * ran three variants last week and closed the tab had lost them.
 *
 * The field is still here, below the list, because it is still the way in for a run somebody else
 * produced — but it is no longer the only one, and the page's subject is no longer "open a run".
 *
 * # 🔴 Three empty states, and never one
 *
 * Zero rows has three causes and three next actions:
 *
 *   * `none-yet` — you have not run anything. Go configure a variant.
 *   * `pre-ownership` — runs EXIST that predate ownership recording. They are not lost, they are not
 *     listed, and each is still reachable by id. Showing "no runs yet" here is the failure this page
 *     exists to avoid: it tells a returning customer their history is gone when it is not.
 *   * `unreachable` — the platform did not answer. Nothing is known; retrying is safe.
 *
 * The copy lives in `organizationCopy.ts`, because a state whose words are written inline is a state
 * somebody adds without noticing it needs its own.
 */
export const dynamic = "force-dynamic";

/**
 * RunSummary is ONE ROW of the merged list (P29 §4.2) — executed runs and linked runs together.
 *
 * 🔴 A LINKED run's fields are under `linked`, not at the top level, and that is deliberate on the
 * platform side: `status` is the EXECUTOR's terminal state and a linked run has none. Flattening the two
 * would mean inventing a value, and both candidates are wrong — `succeeded` claims we observed something
 * we did not, `""` renders as a broken row.
 *
 * So every field below except `run_id`, `origin` and `at` is OPTIONAL, and the renderer branches on
 * `origin`. This type was previously written as if every field were always present, which is how the
 * first deployment of the merged list produced `Cannot read properties of undefined (reading 'slice')`
 * and a 500 on this page — caught in the browser, not by the type-check, because the type was a
 * hand-written claim about a payload that had changed underneath it.
 */
type RunSummary = {
  run_id: string;
  origin?: "executed" | "linked";
  at?: string;
  // Present on an EXECUTED run.
  config_hash?: string;
  source_revision?: string;
  seed?: number;
  status?: string;
  started_at?: string;
  finished_at?: string;
  // Present on a LINKED run. A linked run has no executor status, no seed and no attempt groups — the
  // CLI transmits an allowlisted summary, so there is nothing to put in them.
  linked?: {
    workflow_id?: string;
    config_hash?: string;
    config_hash_display?: string;
    source_revision?: string;
    tool_version?: string;
    linked_at?: string;
    gate_outcome?: string;
  };
};

type RunsView = {
  runs: RunSummary[];
  // P29 §4.2 — the list now merges EXECUTED and LINKED runs. `origin` says which a row is, carried as
  // data so the detail view routes on it rather than guessing from which fields happen to be empty.
  origin?: string;
  pre_ownership_runs?: number;
  pre_ownership_note?: string;
  next_before?: string;
};

export default async function RunsPage({
  searchParams,
}: {
  searchParams: Promise<{ run_id?: string; before?: string }>;
}) {
  const params = await searchParams;
  const typed = params.run_id?.trim();
  if (typed) redirect(routes.run(typed));

  const before = params.before?.trim();
  const { outcome, session } = await load<RunsView>((paths) => paths.runs({ limit: 50, before }));

  // The session's list is an ORDERING HINT, and a remembered run the platform no longer lists is
  // DISCARDED rather than offered — see lib/enumeration.ts.
  const visited = visitedSubjects(session, "run");
  const available: Enumeration = !outcome.ok
    ? outcome.kind === "not-mounted"
      ? { state: "not-mounted", subjects: [], detail: outcome.error }
      : { state: "read-failed", subjects: [], detail: outcome.error }
    : (() => {
        const subjects = orderByRecentlyVisited(runsToSubjects(outcome.data), visited);
        return subjects.length === 0
          ? { state: "empty" as const, subjects: [] }
          : { state: "ok" as const, subjects };
      })();
  const discarded = discardedVisits(available.subjects, visited);

  return (
    <PageFrame eyebrow="Runs" title="Runs" lede="Every run this organization produced, newest first." wide>
      {!outcome.ok ? (
        // 🔴 A transport failure is NOT an empty list. Rendering one as the other tells the reader there
        // is genuinely nothing, and sends them to look in the wrong place.
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="runs" />
      ) : (
        <RunList view={outcome.data} />
      )}

      <Section title="Open a run by id">
        <SubjectPicker
          kind="run"
          // 🔴 Built from the SAME fetch the list above used (P29 §4.7). A second request for one list
          // is two chances for them to disagree, and the reader would see a run in the list that the
          // picker below did not offer.
          available={available}
          discarded={discarded}
          action="/app/runs"
          field={{ name: "run_id", label: "Run id", placeholder: "run-…" }}
          help="For a run somebody else produced, or one from before this platform recorded ownership."
        />
      </Section>
    </PageFrame>
  );
}

function RunList({ view }: { view: RunsView }) {
  const runs = view.runs ?? [];
  const preOwnership = view.pre_ownership_runs ?? 0;

  if (runs.length === 0) {
    // Which of the two zero-row facts is this? The platform tells us — the count of pre-ownership runs
    // is a separate field precisely so the console does not have to guess.
    const state = preOwnership > 0 ? "pre-ownership" : "none-yet";
    const copy = RUNS_COPY[state];
    return (
      <Section title="Runs">
        <Empty title={copy.title}>
          <p>{copy.body}</p>
          {state === "pre-ownership" ? (
            <p className="mt-2 text-xs text-muted-foreground">
              {preOwnership} run{preOwnership === 1 ? "" : "s"} on this platform predate ownership recording.
            </p>
          ) : null}
          {state === "none-yet" && copy.action ? (
            <p className="mt-3">
              <Link href={routes.configure()} className="link">
                {copy.action}
              </Link>
            </p>
          ) : null}
        </Empty>
      </Section>
    );
  }

  return (
    <Section title="Runs" aside={`${runs.length} shown`}>
      {preOwnership > 0 ? (
        // Shown ALONGSIDE the list, not instead of it. Somebody with both new and old runs needs to know
        // the list is partial, and why, without losing the rows they came for.
        <Banner tone="info" title={RUNS_COPY["pre-ownership"].title}>
          {RUNS_COPY["pre-ownership"].body}
        </Banner>
      ) : null}

      <DataTable
        caption="Runs this organization produced, newest first"
        columns={[
          { key: "run", label: "Run" },
          // 🔴 ORIGIN is a column, not a badge tucked into another one. It decides what the rest of the
          // row can say and what the detail view can show, so a reader must be able to scan it.
          { key: "origin", label: "Origin" },
          { key: "status", label: "Status" },
          { key: "revision", label: "Source revision" },
          { key: "seed", label: "Seed", numeric: true },
          { key: "started", label: "Started" },
        ]}
      >
        <tbody>
          {runs.map((run) => {
            const linked = run.origin === "linked";
            const revision = run.source_revision ?? run.linked?.source_revision;
            const at = run.at ?? run.started_at ?? run.linked?.linked_at;
            return (
              <tr key={run.run_id}>
                <td>
                  <Link href={routes.run(run.run_id)} className="link font-mono text-xs">
                    {run.run_id}
                  </Link>
                </td>
                <td>
                  <span className="text-xs text-muted-foreground">{run.origin ?? "executed"}</span>
                </td>
                <td>
                  {/*
                    🔴 A LINKED run has NO executor status, and this cell says so rather than showing a
                    blank or inventing one. "Not observed" is the truth: the platform learned of the run,
                    it did not perform it — which is P25's standing refusal, visible in a table cell.
                  */}
                  {linked ? (
                    <span className="text-xs text-muted-foreground" title="The platform learned of this run; it did not perform it, so there is no execution status to report.">
                      not observed
                    </span>
                  ) : (
                    <Status value={run.status ?? ""} />
                  )}
                </td>
                <td>
                  <span className="font-mono text-xs">{revision ? revision.slice(0, 12) : "—"}</span>
                </td>
                <td className="num">{run.seed ?? "—"}</td>
                <td>
                  <Value>{at ? instant(at) : "—"}</Value>
                </td>
              </tr>
            );
          })}
        </tbody>
      </DataTable>

      {view.next_before ? (
        <p className="mt-3 text-sm">
          {/*
            The cursor comes from the PLATFORM and is handed straight back. The client does not know the
            ordering column and must not have to guess it — and an offset page shifts under a concurrent
            write, silently skipping or repeating a row.
          */}
          <Link href={`/app/runs?before=${encodeURIComponent(view.next_before)}`} className="link">
            Older runs →
          </Link>
        </p>
      ) : null}
    </Section>
  );
}
