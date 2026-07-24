import type { TransformView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { requireSession } from "@/lib/session";
import { recordVisit, routes } from "@/lib/subjects";
import { PageFrame, Section, Status, Chip, Empty, Failure, Banner, Row } from "@/components/primitives";
import { Disclosure } from "@/components/figure";
import { Diff } from "@/components/diff";

export const dynamic = "force-dynamic";

export default async function TransformPage({
  params,
}: {
  params: Promise<{ configHash: string; sourceRevision: string }>;
}) {
  const session = await requireSession();
  const { configHash, sourceRevision } = await params;
  const hash = decodeURIComponent(configHash);
  const revision = decodeURIComponent(sourceRevision);
  recordVisit(session, {
    kind: "transform",
    id: `${hash}@${revision}`,
    label: hash.slice(0, 12),
    href: routes.transform(hash, revision),
    hint: `revision ${revision}`,
  });
  const { outcome } = await load<TransformView>((paths) => paths.transform(hash, revision), [
    "config_hash",
    "source_revision",
  ]);
  return (
    <PageFrame
      eyebrow="Transform"
      title={hash.slice(0, 12)}
      lede={`The change this configuration produced against revision ${revision}, and what the build gate could prove about it.`}
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="transform" />
      ) : (
        <TransformBody transform={outcome.data} />
      )}
    </PageFrame>
  );
}

function TransformBody({ transform }: { transform: TransformView }) {
  const needsReview = transform.requires_human_review;
  const rejected = transform.status === "build-rejected";
  return (
    <>
      <Section title="This transform">
        <Row>
          <Status value={transform.status} />
          {/* Verification strength is a STATUS, not a chip: the difference between type-checked and
              syntax-checked is the difference between "a compiler agreed" and "a parser did", and it
              decides whether a human must read this before it runs. */}
          <Status value={transform.verification_strength} title="what the build gate was able to prove" />
          <Chip variant="hash" title={transform.config_hash}>
            config {transform.config_hash_display}
          </Chip>
          <Chip>rev {transform.source_revision}</Chip>
          {transform.variant_branch ? <Chip>branch {transform.variant_branch}</Chip> : null}
          {transform.variant_commit ? (
            <Chip variant="hash" title="the exact revision this diff was produced from">
              commit {transform.variant_commit.slice(0, 12)}
            </Chip>
          ) : null}
        </Row>

        {needsReview ? (
          <Banner tone="warn" title="This change was parsed, not type-checked">
            <p>
              A syntax-checked change is reviewed by a human at <strong>every</strong> automation level
              and is never applied automatically. The gate confirmed the change is well-formed; it did
              not confirm that it compiles against the rest of the code.
            </p>
          </Banner>
        ) : null}

        {rejected ? (
          <Banner tone="warn" title="This transform does not build, so it was never run">
            <Row>
              {transform.rejected_node_id ? <Chip>{transform.rejected_node_id}</Chip> : null}
              {transform.rejected_dimension ? <Chip>{transform.rejected_dimension}</Chip> : null}
            </Row>
            {transform.build_log ? (
              <Disclosure summary="Build log" open>
                <pre className="buildlog">{transform.build_log}</pre>
              </Disclosure>
            ) : (
              <p className="hint">The platform recorded no build log for this rejection.</p>
            )}
          </Banner>
        ) : null}
      </Section>

      <Section
        title="The diff"
        aside={transform.diff_hash ? <span className="mono">diff sha256: {transform.diff_hash}</span> : null}
      >
        {transform.diff.trim() === "" ? (
          <Empty title="No changes — this is the baseline, applied unchanged.">
            <p>
              An empty diff is a real and useful answer here: the configuration resolved to exactly what
              the code already does, so any difference in a run against it is not attributable to a
              change.
            </p>
          </Empty>
        ) : (
          <Diff patch={transform.diff} />
        )}
      </Section>
    </>
  );
}
