package variantspec

import (
	"errors"
	"strings"
	"testing"
)

// P16 §1 — the ONE-WAY DOOR this phase had to get right before any rewriter shipped.
//
// `context_drop_tolerance` joins a struct whose bytes P0 froze and P10/P14 already extended twice.
// The contract has two halves and both are load-bearing:
//
//	ABSENT  ⇒ byte-identical to a pre-P16 node. Not "similar", not "equivalent" — the SAME BYTES, so
//	          every config_hash already stored keeps resolving to the configuration it named.
//	PRESENT ⇒ moves the hash. Two variants that differ only in the drop a node tolerates are different
//	          configurations, because the second admits proposals the first rejects.
//
// A test that asserted only the first half would pass on a field nobody could ever set; one that
// asserted only the second would pass on a field that re-keyed every stored row.

// ── task 1.5 / 7.7 — additivity ──────────────────────────────────────────────────────────────────

func TestDropToleranceIsAdditiveToHash(t *testing.T) {
	regs := newFakeRegistries()

	// (1) A node that declares NO tolerance emits no key at all.
	noTolerance := baseSpec()
	baseHash, baseCanon := hashOf(t, noTolerance, regs)
	if strings.Contains(baseCanon, "context_drop_tolerance") {
		t.Fatalf("a node declaring no drop tolerance emitted the key into its canonical bytes; a pre-P16 "+
			"node must serialise byte-identically or every frozen config_hash moves:\n%s", baseCanon)
	}

	// (2) …and hashes identically to the same spec resolved through the pre-P16 code path. The strongest
	// available statement of that is the FROZEN golden vector, whose bytes predate this field entirely.
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)
	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Fatalf("a P16-era ResolvedConfig no longer reproduces the frozen canonical bytes.\n got: %s\nwant: %s",
			canon, g.Base.CanonicalJSON)
	}
	if got, err := rc.Hash(); err != nil || got != g.Base.ConfigHash {
		t.Fatalf("config_hash = %s (err %v), want the frozen %s; every stored result is keyed by this",
			got, err, g.Base.ConfigHash)
	}

	// (3) Setting a tolerance MOVES the hash — the field is identity-bearing when present.
	withTolerance := baseSpec()
	withTolerance.Nodes["n_a"] = NodeOverride{ContextDropTolerance: ptrF(0.2)}
	tolHash, tolCanon := hashOf(t, withTolerance, regs)
	if tolHash == baseHash {
		t.Error("declaring a drop tolerance did not change config_hash; two variants that differ only in " +
			"the drop a node tolerates are different configurations (the second admits proposals the first " +
			"rejects) and an eval comparison cannot tell them apart if they share a hash")
	}
	if !strings.Contains(tolCanon, `"context_drop_tolerance":0.2`) {
		t.Errorf("the declared tolerance is missing from the canonical bytes:\n%s", tolCanon)
	}

	// (4) A DIFFERENT tolerance is a different configuration again.
	other := baseSpec()
	other.Nodes["n_a"] = NodeOverride{ContextDropTolerance: ptrF(0.5)}
	if otherHash, _ := hashOf(t, other, regs); otherHash == tolHash {
		t.Error("tolerances 0.2 and 0.5 produced the same config_hash")
	}

	// (5) Only the DECLARING node acquires the key: the tolerance is per-node, so n_b must still
	// serialise as a pre-P16 node inside the very same resolved config.
	nodeB := tolCanon[strings.Index(tolCanon, `"node_id":"n_b"`):]
	if strings.Contains(nodeB, "context_drop_tolerance") {
		t.Errorf("node n_b declares no tolerance but acquired the key from its sibling:\n%s", nodeB)
	}
}

// A tolerance is a RATIO. Out of range is rejected at Validate — before the IR, before the registries,
// before any hash exists — rather than clamped, because "tolerate 1.5 of the context" is a
// misunderstanding and silently reading it as 1.0 hides it behind a gate that then never bites.
func TestDropToleranceRangeIsValidated(t *testing.T) {
	for _, bad := range []float64{-0.1, 1.5} {
		s := baseSpec()
		s.Nodes["n_a"] = NodeOverride{ContextDropTolerance: ptrF(bad)}
		err := s.Validate()
		if err == nil {
			t.Fatalf("Validate accepted context_drop_tolerance %v; a ratio outside [0,1] is a "+
				"misunderstanding, not a value to clamp", bad)
		}
		if !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("tolerance %v rejected with %v, want ErrInvalidSpec", bad, err)
		}
		var se *SpecError
		if errors.As(err, &se) && se.NodeID != "n_a" {
			t.Errorf("the rejection names node %q, want n_a: an unhappy path has to say where to look", se.NodeID)
		}
	}
	for _, ok := range []float64{0, 0.5, 1} {
		s := baseSpec()
		s.Nodes["n_a"] = NodeOverride{ContextDropTolerance: ptrF(ok)}
		if err := s.Validate(); err != nil {
			t.Errorf("Validate rejected the in-range tolerance %v: %v", ok, err)
		}
	}
}

// 🚫 A tolerance is a NUMBER, not a registry address. If it reached Refs() the loader would fail
// closed looking up "0.2" as a version_id — a dangling-ref rejection for a value that was never meant
// to be registered.
func TestDropToleranceIsNotARegistryRef(t *testing.T) {
	s := baseSpec()
	s.Nodes["n_a"] = NodeOverride{ContextDropTolerance: ptrF(0.25)}
	if refs := s.Refs(); len(refs) != 0 {
		t.Errorf("Refs() = %v, want none: a drop tolerance is a number, not a version_id", refs)
	}
}

// A node whose ONLY delta is its tolerance is not an empty override: it changes what the platform will
// admit for that node, and config_hash records it. isEmpty() deciding otherwise would drop the
// tolerance out of the resolved override the admissibility gate reads.
func TestToleranceOnlyOverrideIsNotEmpty(t *testing.T) {
	if (NodeOverride{ContextDropTolerance: ptrF(0.3)}).isEmpty() {
		t.Error("an override carrying only a drop tolerance reported itself empty; the gate reads it from " +
			"the resolved override, which is only populated for a non-empty one")
	}
	if !(NodeOverride{}).isEmpty() {
		t.Error("an override setting nothing must still report itself empty")
	}
}
