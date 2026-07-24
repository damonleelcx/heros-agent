import Link from "next/link";
import type { RunView } from "@/lib/types.generated";
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

  // The subject is named in the frame regardless of how the load went (R13). A failure that also
  // erased the subject would leave the reader unable to tell WHICH run failed to load.
  return (
    <PageFrame eyebrow="Run" title={id} lede="What this run did, node by node, exactly as the record states it.">
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="run" />
      ) : (
        <RunBody run={outcome.data} />
      )}
    </PageFrame>
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
