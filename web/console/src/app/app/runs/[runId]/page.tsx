import Link from "next/link";
import type { RunView, LinkedRunView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { routes } from "@/lib/subjects";
import {
  PageFrame,
  Section,
  Status,
  Chip,
  Empty,
  Failure,
  DataTable,
  Banner,
  Row,
} from "@/components/primitives";
import { shortHash, integer, NULL_VALUE } from "@/lib/format";
import { WatchToggle } from "./watch";

export const dynamic = "force-dynamic";

export default async function RunPage({ params }: { params: Promise<{ runId: string }> }) {
  const { runId } = await params;
  const id = decodeURIComponent(runId);
  const { outcome } = await load<RunView>((paths) => paths.run(id), ["run_id", "status", "config_hash"]);

  // A run id can name either of two things, and until now this page knew only one of them.
  //
  // `paths.run` reads the EXECUTOR's record — a run this platform executed. A run the CLI transmitted
  // with `heros link` is stored somewhere else entirely (`run_link`), so asking the executor about it
  // answered "no such run" for a run the platform had accepted, stored, and would answer
  // `409 already_linked` about. The reader was told their run did not exist.
  //
  // So the linked record is consulted whenever the executor has nothing to show. Only then: a real
  // executed run is the richer subject and stays the page's primary answer, and this second read costs
  // nothing on the path that already succeeded.
  const linked =
    outcome.ok ? null : (await load<LinkedRunView>((paths) => paths.linkedRun(id), ["run_id"])).outcome;

  // The subject is named in the frame regardless of how the load went (R13). A failure that also
  // erased the subject would leave the reader unable to tell WHICH run failed to load.
  const lede = linked?.ok
    ? "A run measured on your machine and linked to this platform — the scores are the ones your harness computed."
    : "What this run did, node by node, exactly as the record states it.";

  return (
    <PageFrame eyebrow="Run" title={id} lede={lede}>
      {outcome.ok ? (
        <RunBody run={outcome.data} />
      ) : linked?.ok ? (
        <LinkedRunBody run={linked.data} />
      ) : (
        // Neither subject resolved. The EXECUTOR's failure is the one reported: it is the primary
        // read, and reporting the second one would answer a question the reader did not ask.
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="run" />
      )}
    </PageFrame>
  );
}

/**
 * LinkedRunBody renders a run that was measured elsewhere and transmitted here.
 *
 * # Why it is not RunBody with empty columns
 *
 * A linked run has no per-node input, no output, no attempt groups and no terminal status — the link
 * payload is an allowlisted summary and carries none of them. Rendering it through `RunBody` would mean
 * printing those headers with dashes under them, which reads as "this run did nothing" rather than as
 * "this platform was never told". What a linked run does carry is the measurement, so that is what this
 * shows, and the absence is stated in words rather than implied by empty cells.
 */
function LinkedRunBody({ run }: { run: LinkedRunView }) {
  const scores = run.scores ?? [];
  return (
    <>
      <Section title="This run" aside="linked from the CLI">
        <Row>
          <Chip variant="hash" title={run.config_hash}>
            config {run.config_hash_display}
          </Chip>
          <Chip title="the source revision this configuration was measured against">
            rev {run.source_revision}
          </Chip>
          <Chip title="the CLI build that produced these numbers">heros {run.tool_version}</Chip>
          <Chip>linked {run.linked_at}</Chip>
        </Row>
        <Banner tone="info" title="Measured on your machine, not on this platform">
          <p>
            This run was executed by the <code>heros</code> CLI with your own keys and linked here. The
            platform holds the scores below and the structure of the workflow — never your source, your
            prompts, or your provider keys. There is no per-node input or output to show because none
            was transmitted.
          </p>
        </Banner>
      </Section>

      <Section title="Scores" aside={`${scores.length} metric${scores.length === 1 ? "" : "s"}`}>
        {scores.length === 0 ? (
          <Empty title="This run was linked without scores.">
            The link recorded the run and its configuration, but the CLI reported no scored metrics for
            it — so there is nothing here to compare against another variant.
          </Empty>
        ) : (
          <DataTable
            caption="Each metric the CLI reported, with the interval that qualifies it"
            columns={[
              { key: "metric", label: "Metric" },
              { key: "value", label: "Value", numeric: true },
              { key: "interval", label: "95% interval", numeric: true },
            ]}
          >
            <tbody>
              {scores.map((s) => (
                <tr key={s.metric}>
                  <td className="mono">{s.metric}</td>
                  <td className="mono">{s.value.toFixed(4)}</td>
                  {/* The interval travels with the value everywhere it is shown. A point estimate on
                      its own is the one thing this platform does not publish — so it is NOT muted.
                      Rendered as secondary chrome it read as a footnote to the number beside it, which
                      is backwards: the interval is what makes that number a measurement rather than a
                      claim, and it has to be as readable as the value it qualifies. */}
                  <td className="mono">
                    [{s.ci_low.toFixed(4)}, {s.ci_high.toFixed(4)}]
                  </td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>

      <Section title="Where this run came from">
        <p className="text-muted">
          Workflow <span className="mono">{run.workflow_id}</span>. The configuration it measured is{" "}
          <Link
            className="text-primary underline underline-offset-2"
            href={routes.transform(run.config_hash, run.source_revision)}
          >
            the transform for {run.config_hash_display} at {run.source_revision}
          </Link>
          , if this deployment holds one.
        </p>
      </Section>
    </>
  );
}

function RunBody({ run }: { run: RunView }) {
  const nodes = run.nodes ?? [];
  return (
    <>
      <Section
        title="This run"
        aside={
          <Link
            className="text-primary underline underline-offset-2"
            href={routes.transform(run.config_hash, run.source_revision)}
          >
            the transform that produced it
          </Link>
        }
      >
        <Row>
          {/* Rendered from the RECORD's own status, verbatim (X4). The console never derives a run's
              status from its node list — a run whose nodes all succeeded but which was halted is
              exactly the case a derived status gets wrong, and it is the case that matters. */}
          <Status value={run.status} />
          <Chip variant="hash" title={run.config_hash}>
            config {run.config_hash_display}
          </Chip>
          <Chip>seed {integer(run.seed)}</Chip>
          <Chip title="the source revision this configuration was applied to">rev {run.source_revision}</Chip>
          <WatchToggle runId={run.run_id} initialStatus={run.status} />
        </Row>

        {run.status === "halted" ? (
          <Banner tone="warn" title="This run was halted">
            <p>
              This node&apos;s output violated its typed I/O contract, so it was never passed
              downstream. The run stopped rather than carrying a bad value forward.
            </p>
            <Row>
              {run.halted_node_id ? <Chip>{run.halted_node_id}</Chip> : null}
              {run.halted_reason ? <span className="mono caption">{run.halted_reason}</span> : null}
            </Row>
          </Banner>
        ) : null}
      </Section>

      <Section title="Nodes" aside={`${nodes.length} recorded`}>
        {nodes.length === 0 ? (
          // Status-dependent empty copy (X3, P2-26). A run with no nodes reads differently while it is
          // still starting than when it is terminal, because the next action is different: wait, or
          // look at why nothing executed.
          run.status === "running" || run.status === "queued" ? (
            <Empty title="No nodes have reported yet — the run is still starting." />
          ) : (
            <Empty title="This run recorded no node executions." />
          )
        ) : (
          <DataTable
            caption="Each node's attempt, status, and the blobs it read and wrote"
            columns={[
              { key: "node", label: "Node" },
              { key: "attempt", label: "Attempt", numeric: true },
              { key: "status", label: "Status" },
              { key: "input", label: "Input" },
              { key: "output", label: "Output" },
              { key: "key", label: "Idempotency key" },
            ]}
          >
            <tbody>
              {nodes.map((node, index) => (
                // A fragment per node so an error row can sit beneath its node as its own full-width
                // row (P2-22) rather than being crammed into a cell nobody can read.
                <NodeRows key={`${node.node_id}-${node.attempt_group}-${index}`} node={node} />
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>
    </>
  );
}

function NodeRows({ node }: { node: NonNullable<RunView["nodes"]>[number] }) {
  return (
    <>
      <tr>
        <td className="mono">{node.node_id}</td>
        <td className="num mono">{integer(node.attempt_group)}</td>
        <td>
          <Status value={node.status} />
        </td>
        <td className="mono caption" title={node.input_blob_hash}>
          {node.input_blob_hash ? shortHash(node.input_blob_hash) : NULL_VALUE}
        </td>
        <td className="mono caption" title={node.output_blob_hash}>
          {node.output_blob_hash ? shortHash(node.output_blob_hash) : NULL_VALUE}
        </td>
        <td className="mono caption">{node.idempotency_key || NULL_VALUE}</td>
      </tr>
      {node.error ? (
        <tr>
          <td colSpan={6} className="row-error">
            {node.error}
          </td>
        </tr>
      ) : null}
    </>
  );
}
