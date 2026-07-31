import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "@/components/authoring";

/**
 * selection.tsx renders SKILL and TOOL authoring on the configure surface (P14 14c, tasks 9.9–9.11).
 *
 * # The one rule this file exists to hold: an empty picker is a lie
 *
 * A node whose language has no skill materializer cannot carry a binding. Rendering that as an empty
 * list says "this node has no skills available" — a statement about the CATALOGUE — when the truth is
 * "this language cannot carry one yet", a statement about the PLATFORM. Those send a reader to two
 * different places: one to look for skills to install, the other to wait for a materializer. So the
 * boundary is stated, with the language named and the covered languages listed.
 *
 * # Why there is no text input anywhere here
 *
 * A skill is bound from a sealed registry contract; a tool is selected from what discovery found at the
 * call site. Neither is free text. A field a user could type into would let them name a tool the codemod
 * cannot delete — the emitted diff removes nothing, or removes the wrong span, and both are silent. So
 * every control here is a choice among what the platform already knows about, and a test asserts no
 * input element exists on this path.
 *
 * # Why a reorder is presented as a change
 *
 * Skill order is identity-bearing: the call site binds them in the order given, so two orders are two
 * configurations with two hashes. Presenting a reorder as tidying would let a reader make a real,
 * scoreable change while believing they had rearranged a list.
 */

/** A node's selectable surface, exactly as the platform reports it. Nothing here is derived. */
type NodeSelection = {
  node_id: string;
  language: string;
  /** Sealed, pinned skills. An unpinned entry is not offered at all — it is not a lesser offer. */
  offered_skills: { ref: string; name: string; version_id: string }[];
  /** The tools discovery located at this call site. */
  discovered_tools: string[];
  /** True when the tool set is assembled at run time and cannot be pruned. */
  tool_set_dynamic: boolean;
  /** Set when the language cannot carry a skill binding at all. */
  skills_refused?: PreflightResult;
};

const GO_NODE: NodeSelection = {
  node_id: "classify",
  language: "go",
  offered_skills: [
    { ref: "skill-rerank@v3", name: "rerank", version_id: "v3" },
    { ref: "skill-summarize@v1", name: "summarize", version_id: "v1" },
  ],
  discovered_tools: ["search", "calculator", "fetch"],
  tool_set_dynamic: false,
};

const PYTHON_NODE: NodeSelection = {
  node_id: "answer",
  language: "python",
  offered_skills: [],
  discovered_tools: [],
  tool_set_dynamic: true,
  skills_refused: {
    verdict: "refused",
    node_id: "answer",
    field: "skills",
    shape: "skill binding",
    cause:
      'node "answer", dim skills: binding a skill means constructing SDK tool values at the call site, and no materializer for python has landed yet (covered today: go)',
  },
};

const DYNAMIC_TOOLS: PreflightResult = {
  verdict: "refused",
  node_id: "answer",
  field: "tools",
  shape: "tool selection",
  cause:
    'node "answer", dim tools: this node\'s tool set is assembled at run time, so there is no static declaration to prune — the deletion site is not inferred',
};

export function SkillToolSelection() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Bind a skill">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          A skill is bound from a sealed registry contract at a pinned version. Only sealed, pinned
          entries are offered — an unpinned binding would construct whatever shape the registry holds at
          apply time, which is not a configuration anyone reviewed.
        </p>
        <Card>
          <Row>
            <Chip title="the node">{GO_NODE.node_id}</Chip>
            <Chip title="this node&rsquo;s discovered language">{GO_NODE.language}</Chip>
            <UnverifiedLabel state="unverified" />
          </Row>
          <Row>
            {GO_NODE.offered_skills.map((s) => (
              <Chip key={s.ref} title={`sealed at version ${s.version_id}`}>
                {s.name} · {s.version_id}
              </Chip>
            ))}
          </Row>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Binding, unbinding and <strong>reordering</strong> are each a real change: the call site binds
            skills in the order given, so two orders are two configurations with two hashes. A reorder is
            scored like any other change, not treated as tidying.
          </p>
        </Card>
      </Section>

      <Section title="When a node cannot carry a binding">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          The boundary is stated before you choose, because whether this node&rsquo;s language has a
          materializer is knowable without you doing anything first.
        </p>
        {PYTHON_NODE.skills_refused ? <PreflightPanel result={PYTHON_NODE.skills_refused} /> : null}
        <Banner tone="info" title="This is a gap in the platform, not in your catalogue">
          <p>
            The skills exist and this node could use them — what has not landed is the code that writes an
            SDK tool value for {PYTHON_NODE.language} call sites. Discovery still finds the node, and the
            binding will apply when that materializer ships.
          </p>
        </Banner>
      </Section>

      <Section title="Prune a tool">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          You can drop tools this node offers but never calls. The list is exactly what discovery found at
          the call site — a tool that is not there could not be deleted from your source, so it is not
          offered.
        </p>
        <Card>
          <Row>
            {GO_NODE.discovered_tools.map((t) => (
              <Chip key={t} title="a tool discovery found at this call site">
                {t}
              </Chip>
            ))}
          </Row>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Restoring every tool you pruned returns the node to the byte-identical configuration it had.
          </p>
        </Card>
      </Section>

      <Section title="When a tool set cannot be read">
        <PreflightPanel result={DYNAMIC_TOOLS} />
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          &ldquo;This node offers no tools&rdquo; and &ldquo;we could not see what this node offers&rdquo;
          are different facts. The second is shown as itself rather than as an empty list, because an
          empty list would invite you to conclude there is nothing to prune.
        </p>
      </Section>

      <Section title="What a bind or a prune claims">
        <Banner tone="info" title="Nothing, until the harness runs">
          <p>
            A prune&rsquo;s token reduction is visible immediately — the declared-tool tokens drop — and
            that is exactly why it is not reported as a saving. Until a multi-seed evaluation runs, task
            success is unmeasured, and a prune that removed a tool the model needed under rare inputs is a
            failure a token count cannot see.
          </p>
        </Banner>
      </Section>
    </div>
  );
}
