import { redirect } from "next/navigation";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes } from "@/lib/subjects";
import { PageFrame } from "@/components/primitives";
import { SubjectPicker } from "@/components/subjectPicker";

/** Variant selection. A variant's scorecard explains a score; it never proposes a change. */
export const dynamic = "force-dynamic";

export default async function VariantsPage({
  searchParams,
}: {
  searchParams: Promise<{ variant_id?: string }>;
}) {
  const session = await requireSession();
  const typed = (await searchParams).variant_id?.trim();
  if (typed) redirect(routes.scorecard(typed));

  return (
    <PageFrame
      eyebrow="Variants"
      title="Open a variant scorecard"
      lede="Why a variant scored what it scored — per node, per cluster, with the ablation that was actually run."
    >
      <SubjectPicker
        kind="variant"
        visited={visitedSubjects(session, "variant")}
        action="/app/variants"
        field={{ name: "variant_id", label: "Variant id", placeholder: "variant-…" }}
        help="Every row on an eval board links to its variant's scorecard, so this field is for a variant you already have an identifier for."
      />
    </PageFrame>
  );
}
