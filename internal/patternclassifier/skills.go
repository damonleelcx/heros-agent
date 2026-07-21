package patternclassifier

import (
	"context"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// SkillRole is the retrieval-pipeline role a registered skill plays. It exists because "retriever",
// "embed" and "rerank" are not things the IR states: the IR states that a node is BOUND to a named
// skill, and the Skill Registry states what that skill is. Reading the role off the skill NAME
// ("looks like it contains 'search'") would be name-guessing dressed up as structure, so the mapping
// is supplied explicitly by the caller from registry facts instead.
type SkillRole string

const (
	SkillRoleRetrieval SkillRole = "retrieval"
	SkillRoleEmbedding SkillRole = "embedding"
	SkillRoleRerank    SkillRole = "rerank"
)

// SkillResolver answers the one registry question the classifier asks: does this tools_skills entry
// name a skill that actually exists? It is deliberately pure and synchronous — a detector must be a
// pure function of its inputs to be deterministic, so all I/O happens once, up front, in
// LoadSkillResolver.
type SkillResolver interface {
	ResolvesSkill(name string) bool
}

// staticSkillResolver is a resolved snapshot: the set of skill names that existed in the registry at
// the moment classification started.
type staticSkillResolver struct{ known map[string]bool }

func (r staticSkillResolver) ResolvesSkill(name string) bool { return r.known[name] }

// NewStaticSkillResolver builds a resolver over an explicit set of known skill names. Used by
// fixtures and by callers that already hold the registry snapshot.
func NewStaticSkillResolver(names ...string) SkillResolver {
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	return staticSkillResolver{known: known}
}

// LoadSkillResolver asks the REAL Skill Registry which of the IR's tool bindings resolve, and
// snapshots the answer. It queries only the names the IR actually mentions, so cost is proportional
// to the workflow, not to the registry.
//
// This is the only I/O the classifier performs, and it is done here rather than inside the Tool Use
// detector so that the detector stays a pure function (determinism NFR) and so that no code path can
// silently fall back to "assume it resolves" when the registry is unreachable — a registry error is
// returned, not swallowed.
func LoadSkillResolver(ctx context.Context, store *registry.Store, ir *discovery.IR) (SkillResolver, error) {
	if store == nil {
		return nil, fmt.Errorf("patternclassifier: nil skill registry store")
	}
	names := map[string]bool{}
	for _, n := range ir.Nodes {
		for _, t := range n.ToolsSkills {
			names[t] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	known := map[string]bool{}
	for _, n := range ordered {
		versions, err := store.SkillVersions(ctx, n)
		if err != nil {
			// A registry we cannot read is NOT "no tools resolve". Reporting that would turn an
			// outage into a workflow that silently contains no Tool Use — a false negative dressed
			// as a clean result. Fail loudly instead.
			return nil, fmt.Errorf("patternclassifier: resolving skill %q against the registry: %w", n, err)
		}
		if len(versions) > 0 {
			known[n] = true
		}
	}
	return staticSkillResolver{known: known}, nil
}
