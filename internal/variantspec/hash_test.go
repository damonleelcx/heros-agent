package variantspec

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P14 — the additive-hash contract for skills and tools.
//
// Every capability P14 adds joins `config_hash` the same way: a change to it MUST move the hash, and
// a config that does not use it MUST hash to exactly the bytes it did before the capability existed.
// Those two halves are one requirement — the first alone would let a prune be invisible to eval
// comparison, and the second alone would re-key every row already stored.

func p14Skill(t *testing.T, versionID, name string) *registry.SkillEntry {
	t.Helper()
	return &registry.SkillEntry{VersionID: versionID, Name: name}
}

// hashOf resolves a spec against the two-node test IR and returns its config_hash and canonical bytes.
func hashOf(t *testing.T, spec *VariantSpec, regs *fakeRegistries) (string, string) {
	t.Helper()
	r, err := Resolve(context.Background(), spec, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	canon, err := r.Config.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	return r.ConfigHash, string(canon)
}

// ── task 2.6 / 7.3 — skill order is identity-bearing; a no-skill node is byte-identical ──────────

func TestSkillReorderChangesHash_NoSkillByteIdentical(t *testing.T) {
	regs := newFakeRegistries()
	a := strings.Repeat("1", 64)
	b := strings.Repeat("2", 64)
	regs.skills[a] = p14Skill(t, a, "search_kb")
	regs.skills[b] = p14Skill(t, b, "issue_lookup")

	bind := func(refs ...string) *VariantSpec {
		s := baseSpec()
		s.Nodes["n_a"] = NodeOverride{SkillRefs: refs}
		return s
	}

	forward, _ := hashOf(t, bind(a, b), regs)
	reversed, _ := hashOf(t, bind(b, a), regs)
	if forward == reversed {
		t.Fatal("binding the same skills in a different order produced the same config_hash; skill order " +
			"is identity-bearing (ResolvedNode.SkillRefs is never sorted), and a rerank that does not move " +
			"the hash is a change the eval cannot tell from the baseline")
	}

	// Adding a skill moves the hash too — otherwise add/remove would be invisible to comparison.
	single, _ := hashOf(t, bind(a), regs)
	if single == forward {
		t.Error("adding a second skill did not change config_hash")
	}

	// 🔴 The other half: a node that binds NO skill must serialize exactly as it did before this
	// capability existed. SkillRefs is a frozen, always-present field, so the baseline here is the
	// discovered list — and the bytes must not acquire anything new.
	noSkill := baseSpec()
	_, canon := hashOf(t, noSkill, regs)
	for _, forbidden := range []string{`"tool_selection"`, `"tools"`, `"skills":`} {
		if strings.Contains(canon, forbidden) {
			t.Errorf("a no-skill, no-prune node emitted %s into its canonical bytes; a pre-P14 node must "+
				"serialize byte-identically:\n%s", forbidden, canon)
		}
	}
	if !strings.Contains(canon, `"skill_refs":`) {
		t.Errorf("skill_refs is a frozen always-present field and must still be emitted:\n%s", canon)
	}
}

// A no-skill, no-prune configuration still reproduces the frozen P0 golden vectors. This is the
// strongest statement of the additive rule available: the golden bytes predate every P14 field, so
// reproducing them proves nothing new leaked into a config that uses none of it.
func TestNoSkillNoPruneReproducesGolden(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)

	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Fatalf("a P14-era ResolvedConfig no longer reproduces the frozen canonical bytes.\n got: %s\nwant: %s",
			canon, g.Base.CanonicalJSON)
	}
	got, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Fatalf("config_hash = %s, want the frozen %s; every stored result is keyed by this", got, g.Base.ConfigHash)
	}
}
