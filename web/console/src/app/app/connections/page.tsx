import { load } from "@/lib/view";
import { platformFetch } from "@/lib/platformApi";
import { requireSession } from "@/lib/session";
import { scoped } from "@/lib/scope";
import { instant, shortHash } from "@/lib/format";
import { Tabs, type TabItem } from "@/components/tabs";
import {
  PageFrame,
  Section,
  Chip,
  Empty,
  Failure,
  DataTable,
  Banner,
  Stat,
  Stats,
} from "@/components/primitives";
import {
  CauseChip,
  CloneFailure,
  ConnectRepository,
  ModeChip,
  PairLocalRepository,
  ReadLedger,
  RevokeConnection,
} from "@/components/connections";
import type { ConnectionsView, ConnectionView, LocalPairingsView } from "@/lib/types.generated";

/**
 * Where a workflow's source comes from (P32 §6).
 *
 * # Why this is its own surface
 *
 * The question it answers is about the GRANT — what may the platform read, when did it, and how do I
 * stop it — rather than about a workflow. A person revoking access after an incident must be able to
 * find that without first knowing which workflow it belongs to, and a tab under Workflows would make
 * the revocation control reachable only through a subject they may not remember.
 *
 * # 🔴 The default mode is not a lesser tier, and this page must not imply it is
 *
 * FR12: no feature is gated on a connection. A tenant who only pushes bundles reaches every surface.
 * So the three modes are presented as three answers to one question, in the order of how much standing
 * capability each hands over — bundle (none), local (none), connected (a standing read grant) — and
 * the page never nags. §6.5: a workflow with no snapshot renders `not reported`; it does not prompt
 * for a connection as a precondition.
 *
 * # What is computed here: nothing
 *
 * The mode, the last successful read, the last failure and its cause all arrive from the platform's
 * read model. The rule "the last failure is the newest record whose outcome is not `succeeded`" lives
 * in Go, where it is reviewed once, rather than in a browser where a second consumer would implement
 * it differently.
 */
export const dynamic = "force-dynamic";

export default async function ConnectionsPage() {
  const { outcome } = await load<ConnectionsView>((paths) => paths.repoConnections(), ["connections", "forges"]);
  const pairings = await fetchPairings();

  const tabs: TabItem[] = [
    {
      id: "connections",
      label: "Connected repositories",
      content: !outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="repository connections" />
      ) : (
        <ConnectionsTab view={outcome.data} />
      ),
    },
    {
      id: "local",
      label: "Read on your machine",
      content: <LocalTab view={pairings} />,
    },
    { id: "bundle", label: "Push a bundle", content: <BundleTab /> },
  ];

  return (
    <PageFrame
      eyebrow="Source"
      title="Where your source comes from"
      lede="Three ways to give this platform something to analyse, and they differ in exactly one thing: what we can do when you are not here. A pushed bundle and a local read hand over no standing capability. A connected repository does, which is why every read of one is recorded and why you can revoke it from this page."
      wide
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}

/** fetchPairings reads the local-mode surface, tolerating a deployment that does not serve it. */
async function fetchPairings(): Promise<LocalPairingsView | null> {
  const session = await requireSession();
  const paths = scoped(session);
  const res = await platformFetch<LocalPairingsView>(paths.localPairings(), {
    tenantId: session.tenantId,
    userId: session.userId,
  });
  // 🔴 `null` on ANY failure, and the tab renders a stated absence rather than an error. A deployment
  // that does not offer the local bridge is not broken — it answers 503, which is a policy answer —
  // and the local tab is not a precondition for anything on this page.
  return res.ok ? res.data : null;
}

function ConnectionsTab({ view }: { view: ConnectionsView }) {
  const connections = view.connections ?? [];
  const forges = view.forges ?? [];
  const failing = connections.filter((c) => c.last_failure_cause).length;
  const neverRead = connections.filter((c) => c.last_success_at_ms === 0).length;

  return (
    <div className="conn">
      <Section title="Connect a repository">
        <p className="hint">
          One repository, read-only, revocable — and every read recorded. You can also{" "}
          <strong>push a bundle and connect nothing at all</strong>; nothing on this platform requires a
          connection.
        </p>
        <ConnectRepository forges={forges} retentionHours={view.retention_hours} />
      </Section>

      {connections.length === 0 ? (
        <Empty title="No repository is connected">
          {/* 🔴 §6.5 — a stated absence, NOT a prompt. This is the screen a Mode 1 customer sees
              forever, and it must not read as a setup step they have skipped. */}
          Nothing is missing. Every surface in this console works from a pushed bundle, and a connection
          is an upgrade you may never want — it is the only mode that lets us read your repository while
          you are not here.
        </Empty>
      ) : (
        <>
          <Stats>
            <Stat label="Connected" value={String(connections.length)} />
            {/* Reported separately rather than as a percentage: "none have been read" and "none exist"
                would print the same number, and those are the two states most worth telling apart. */}
            <Stat label="Never read" value={String(neverRead)} />
            <Stat label="Failing" value={String(failing)} />
          </Stats>

          {connections.map((c) => (
            <ConnectionCard key={c.connection_id} connection={c} />
          ))}
        </>
      )}
    </div>
  );
}

/** ConnectionCard is §6.1: mode, last successful read, last failure and its cause. */
function ConnectionCard({ connection: c }: { connection: ConnectionView }) {
  return (
    <div className="conn__card">
      <div className="conn__head">
        <span className="conn__repo">{c.repository}</span>
        <span className="flex flex-wrap items-center gap-2">
          <ModeChip mode={c.mode} />
          <Chip tone="neutral">{c.forge}</Chip>
          <Chip tone="neutral">{c.grant_kind.replace(/_/g, " ")}</Chip>
          {c.last_failure_cause ? <CauseChip cause={c.last_failure_cause} /> : null}
        </span>
      </div>

      <div className="conn__facts">
        <div className="conn__fact">
          <span className="stat__label">Workflow</span>
          <span className="mono text-sm text-foreground">{c.workflow_id}</span>
        </div>
        <div className="conn__fact">
          <span className="stat__label">Last successful read</span>
          {/* 🔴 Zero renders as `never read`, not as an epoch date. A connection that has never
              succeeded and one that succeeded in 1970 are different facts and only one is possible. */}
          <span className="text-sm text-foreground">
            {c.last_success_at_ms ? (
              <>
                {formatWhen(c.last_success_at_ms)}
                {c.last_success_revision ? (
                  <span className="mono text-muted-foreground"> · {shortRev(c.last_success_revision)}</span>
                ) : null}
              </>
            ) : (
              <span className="hint">never read</span>
            )}
          </span>
        </div>
        <div className="conn__fact">
          <span className="stat__label">Last read by</span>
          <span className="text-sm text-foreground">
            {c.last_actor === "person" ? (
              "a person, who was present"
            ) : c.last_actor === "scheduled" ? (
              // Named plainly. This is the property the whole disclosure exists for, and the ledger is
              // where a customer checks it after the fact.
              "a scheduled process, with nobody present"
            ) : (
              <span className="hint">not reported</span>
            )}
          </span>
        </div>
        {c.sub_path ? (
          <div className="conn__fact">
            <span className="stat__label">Rooted at</span>
            <span className="mono text-sm text-foreground">{c.sub_path}</span>
          </div>
        ) : null}
      </div>

      {/* §6.4 — the failure, as its own message with its own next action. */}
      {c.last_failure_cause ? (
        <CloneFailure cause={c.last_failure_cause} at={formatWhen(c.last_failure_at_ms)} />
      ) : null}

      <ReadLedger connectionId={c.connection_id} />
      <RevokeConnection connection={c} />
    </div>
  );
}

function LocalTab({ view }: { view: LocalPairingsView | null }) {
  if (!view) {
    return (
      <Banner tone="info" title="This deployment does not serve the local-repository bridge">
        Nothing failed — the capability is not served here. Push a source bundle, or connect a
        repository.
      </Banner>
    );
  }
  const pairings = view.pairings ?? [];
  return (
    <div className="conn">
      <Section title="Read a repository in place">
        <PairLocalRepository availability={view.availability} command={view.command} />
      </Section>
      {pairings.length === 0 ? (
        <Empty title="No machine is paired">
          A paired machine reads its own disk and sends us the workflow&apos;s structure — never the
          files, never a prompt, never a diff.
        </Empty>
      ) : (
        <Section title="Paired machines">
          <DataTable
            caption="Machines paired to read a repository in place, with the revision each reported."
            columns={[
              { key: "workflow", label: "Workflow" },
              { key: "machine", label: "Machine" },
              { key: "state", label: "State" },
              { key: "revision", label: "Revision" },
            ]}
          >
            <tbody>
              {pairings.map((p) => (
                <tr key={p.pairing_id}>
                  <td className="mono">{p.workflow_id}</td>
                  <td>{p.machine_name || <span className="hint">not claimed yet</span>}</td>
                  <td>
                    <Chip tone={p.state === "paired" ? "ok" : p.state === "expired" ? "warn" : "info"}>
                      {p.state}
                    </Chip>
                  </td>
                  {/* 🔴 `not reported` — §6.5's vocabulary, used here for the same reason it is used
                      everywhere else: an em-dash would read as a value the platform holds and could
                      not render, and this is a value nobody has sent. */}
                  <td className="mono">
                    {p.revision ? shortRev(p.revision) : <span className="hint">not reported</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        </Section>
      )}
    </div>
  );
}

/**
 * BundleTab is the DEFAULT mode's tab, and it is deliberately the least loud of the three.
 *
 * It exists so the page's own framing is honest: a customer reading "three ways" must be able to see
 * the one they are already using, and a page that offered only the two upgrades would be a prompt in
 * disguise.
 */
function BundleTab() {
  return (
    <div className="conn">
      <Banner tone="info" title="Pushing a bundle hands over no standing capability">
        <p>
          You run a command, for one revision, and can stop. We hold what you pushed — not a credential
          that can read your repository again tomorrow. It is the default and it loses you nothing: no
          feature on this platform is gated on a connection.
        </p>
      </Banner>
      <Section title="The command">
        <p className="mono text-sm text-foreground">heros push-source --repo . --workflow-id &lt;your workflow&gt;</p>
        <p className="hint">
          Add <code className="mono">--dry-run</code> to see the revision, the file count and the size
          without sending anything, and <code className="mono">--forget</code> to delete a snapshot we
          are holding.
        </p>
      </Section>
    </div>
  );
}

/**
 * formatWhen renders an epoch-millisecond value through the console's ONE swap point.
 *
 * 🔴 It calls `format.instant` rather than constructing an `Intl.DateTimeFormat` here, and
 * `scan-strings.mjs` is what caught the first version doing the latter. The rule is not stylistic:
 * every formatter in this console resolves through `src/lib/format.ts` so that the locale is pinned to
 * `en-US` in one place — a page that builds its own formatter is a page whose dates change when
 * somebody's browser language does, and it is invisible until a customer reports it.
 *
 * The one thing kept local is the ZERO case. `format.instant` renders an absent value as an em-dash,
 * which is the right answer for a value the platform holds and could not render; here 0 means "this
 * has never happened", and those are different facts.
 */
function formatWhen(ms: number): string {
  if (!ms) return "never";
  return instant(ms);
}

/** shortRev truncates a revision through the same helper every other surface uses. */
function shortRev(rev: string): string {
  return shortHash(rev);
}
