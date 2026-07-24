import type { RunMonitor } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { PageFrame, Failure } from "@/components/primitives";
import { LiveMonitor } from "./liveMonitor";

export const dynamic = "force-dynamic";

export default async function LiveRunPage({ params }: { params: Promise<{ runId: string }> }) {
  const { runId } = await params;
  const id = decodeURIComponent(runId);
  const { outcome } = await load<RunMonitor>((paths) => paths.monitor(id), ["run_id", "status"]);
  return (
    <PageFrame
      eyebrow="Live run"
      title={id}
      lede="Node metrics as they arrive. If streaming is unavailable this view keeps working by polling."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="run" />
      ) : (
        <LiveMonitor runId={id} initial={outcome.data} />
      )}
    </PageFrame>
  );
}
