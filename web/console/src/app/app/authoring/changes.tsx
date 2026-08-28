import "server-only";
import { Section } from "@/components/primitives";
import { AuthoredChangeSummary } from "@/components/authoring";
import { ReadOn } from "@/components/editorKit";
import { load } from "@/lib/view";
import { AXIS_DOC } from "@/lib/axisSubject";

/**
 * AuthoredChanges renders the changes THIS READER has authored (P37 FR4).
 *
 * # 🔴 What this replaced
 *
 * Two fixture rows — `ac_4f19c2ab7d3e5610bb42` and `ac_77b0e4c1aa9f3d2265be` — sitting in exactly the
 * position the reader's own changes occupy. They were the honest maximum before P32: the component was
 * real, and there was no reader's change to put in it.
 *
 * # 🔴 Three states, unflattened
 *
 * `not-mounted`, `read-failed` and "you have authored none" are three different sentences with three
 * different next actions, and `view.ts` exists so a caller cannot render one as another. An empty list
 * rendered for a read failure would tell a reader their changes were lost.
 */
type Entry = {
  change_id: string;
  config_hash: string;
  verification_state: string;
  origin?: string;
  axis?: string;
  actor_id?: string;
  forked_from_proposal?: string;
};

export async function AuthoredChanges() {
  const { outcome } = await load<{ changes?: Entry[]; entries?: Entry[] }>((paths) =>
    paths.authoringHistory(),
  );

  if (!outcome.ok) {
    return (
      <Section title="Changes you have authored">
        <p className="hint">
          {outcome.kind === "not-mounted" ? (
            <>
              This deployment does not serve the authoring history, so there is nothing to list. Nothing
              failed — the capability is not served here.
            </>
          ) : (
            <>
              Your authored changes could not be read. This is <strong>not</strong> the same as having
              authored none: nothing has been lost, and retrying is safe.
            </>
          )}
        </p>
      </Section>
    );
  }

  const changes = outcome.data.changes ?? outcome.data.entries ?? [];
  if (changes.length === 0) {
    return (
      <Section title="Changes you have authored">
        <p className="hint">
          None yet. A change you compose on any axis surface appears here, stamped{" "}
          <span className="mono">unverified</span>, with the configuration it produced.
        </p>
        <ReadOn href={AXIS_DOC.prompt}>What an unapplied change is worth, and how to back one out</ReadOn>
      </Section>
    );
  }

  return (
    <Section title="Changes you have authored">
      {changes.map((c) => (
        <AuthoredChangeSummary
          key={c.change_id}
          changeId={c.change_id}
          configHash={c.config_hash}
          axis={c.axis ?? "unknown"}
          verificationState={c.verification_state}
          actorId={c.actor_id ?? "you"}
          forkedFrom={c.forked_from_proposal}
        />
      ))}
      <ReadOn href={AXIS_DOC.prompt}>What an unapplied change is worth, and how to back one out</ReadOn>
    </Section>
  );
}
