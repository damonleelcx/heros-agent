import Link from "next/link";
import { redirect } from "next/navigation";
import { load } from "@/lib/view";
import { visitedSubjects, routes } from "@/lib/subjects";
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

type RunSummary = {
  run_id: string;
  config_hash: string;
  source_revision: string;
  seed: number;
  status: string;
  started_at: string;
  finished_at?: string;
};

type RunsView = {
  runs: RunSummary[];
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
          visited={visitedSubjects(session, "run")}
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
          { key: "status", label: "Status" },
          { key: "revision", label: "Source revision" },
          { key: "seed", label: "Seed", numeric: true },
          { key: "started", label: "Started" },
        ]}
      >
        <tbody>
          {runs.map((run) => (
            <tr key={run.run_id}>
              <td>
                <Link href={routes.run(run.run_id)} className="link font-mono text-xs">
                  {run.run_id}
                </Link>
              </td>
              <td>
                <Status value={run.status} />
              </td>
              <td>
                <span className="font-mono text-xs">{run.source_revision.slice(0, 12)}</span>
              </td>
              <td className="num">{run.seed}</td>
              <td>
                <Value>{instant(run.started_at)}</Value>
              </td>
            </tr>
          ))}
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
