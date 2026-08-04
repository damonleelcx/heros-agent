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
        <>
          <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="transform" />
          {/*
            🔴 "No such transform" is TRUE and, on its own, useless — which is the whole complaint.
            The reader arrived by following a link from a run, so the identifier plainly resolves
            somewhere; being told it does not resolve here reads as a broken product.

            The reason it does not is structural rather than accidental. A transform is a change the
            PLATFORM produced and holds — a spec submitted here, applied here, with a diff and a build
            gate behind it. A LINKED run is the opposite direction: it measured a configuration on the
            customer's own machine and sent back the result. Its config hash names a configuration that
            was never submitted to this deployment, so there is nothing here to show and never was.

            This says that, only on the not-found path, and does not soften the Failure above it: the
            identifier really did not resolve, and the panel that says so stays exactly as it is.
          */}
          {outcome.kind === "not-found" ? (
            <Banner tone="info" title="A run you linked will not have one">
              <p>
                A transform is a change this platform produced and holds: a Variant Spec submitted here,
                applied to a pushed source revision, with the diff and the build gate&apos;s verdict
                attached. It is written when you submit a spec — not when you link a run.
              </p>
              <p>
                A linked run travels the other way. It measured a configuration on your machine and sent
                back the scores, so its config hash names a configuration this deployment has never been
                asked to build. Nothing is missing or lost; there is no transform to hold.
              </p>
              <p className="hint">
                What a linked run does carry is on its own page — the scores with their intervals, and
                per-node cost and latency on the variant&apos;s scorecard.
              </p>
            </Banner>
          ) : null}
        </>
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
