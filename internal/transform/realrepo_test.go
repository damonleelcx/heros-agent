//go:build realrepo

// Real-repo proof for the F1 unpacking guard (ADR-003 / ADR-001 requirement 2).
//
// Behind a tag because it needs a real checkout of nousresearch/hermes-agent — the 3,055-file Python
// repository whose 39 call sites are what ADR-003 was written about, and where this bug was found.
// Fixtures are where the boundaries get asserted; this is where the claim "33 of 39 nodes were being
// mis-rewritten" is either true or not.
//
//	HERMES_REPO=/path/to/hermes-agent GOWORK=off go test -tags realrepo ./internal/transform/ -v
//
// It never executes the target (I1) — it parses it and asks the engine what it would emit.

package transform

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// TestRealRepo_HermesAgent_SplatCallSitesAreRefusedNotMisRewritten runs every node the real repo
// discovers through the real engine and reports the honest split.
//
// The load-bearing assertion is on node n_3ac4435908c48539 — the one that was recorded `built` and
// shown to a reviewer behind a green badge while emitting:
//
//	refreshed_client.chat.completions.create(**kwargs, model="gpt-4o-2024-11-20")
//
// which raises TypeError the moment it runs, because `kwargs["model"]` is assigned two lines above it.
func TestRealRepo_HermesAgent_SplatCallSitesAreRefusedNotMisRewritten(t *testing.T) {
	root := os.Getenv("HERMES_REPO")
	if root == "" {
		t.Fatal("HERMES_REPO is unset. This proof runs against a real checkout of " +
			"github.com/nousresearch/hermes-agent; a skip here would be a safety proof quietly not running.")
	}
	sites, err := discovery.IndexSpanCallSites(root, "python", nil)
	if err != nil {
		t.Fatalf("IndexSpanCallSites: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("no call sites discovered; the proof would pass for the wrong reason")
	}

	ids := make([]string, 0, len(sites))
	for id := range sites {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var rewritable, refusedSplat, refusedOther []string
	reasons := map[string]string{}
	for _, id := range ids {
		// One node at a time: Generate fails the whole patch on the first refusal, and the question here
		// is per-node.
		_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
			id: {Model: modelEntry("openai", "gpt-4o-2024-11-20")},
		}), root)
		switch {
		case err == nil:
			rewritable = append(rewritable, id)
		case strings.Contains(err.Error(), "may ALREADY be among them"):
			refusedSplat = append(refusedSplat, id)
			reasons[id] = err.Error()
		default:
			refusedOther = append(refusedOther, id)
			reasons[id] = err.Error()
		}
	}

	t.Logf("hermes-agent: %d discovered node(s) — %d honestly rewritable, %d refused (unpacking), "+
		"%d refused (other)", len(ids), len(rewritable), len(refusedSplat), len(refusedOther))
	for _, id := range rewritable {
		t.Logf("  rewritable: %s (%s:%d)", id, sites[id].FileRel, sites[id].LineStart)
	}
	for _, id := range refusedOther {
		t.Logf("  refused (other): %s (%s:%d)\n    %s", id, sites[id].FileRel, sites[id].LineStart, reasons[id])
	}

	// 🔴 The node from the report.
	const reported = "n_3ac4435908c48539"
	site, ok := sites[reported]
	if !ok {
		t.Fatalf("%s is not in this checkout; the proof is anchored on the wrong revision", reported)
	}
	if site.KeywordUnpacking == nil {
		t.Fatalf("%s (%s:%d) carries no unpacking, but the reported source is "+
			"`create(**kwargs)` — discovery is not seeing the splat, so the guard cannot fire",
			reported, site.FileRel, site.LineStart)
	}
	if site.KeywordInsert != nil {
		t.Errorf("%s still offers an insertion point despite passing %s; the guard is not fail-closed",
			reported, site.KeywordUnpacking.Text)
	}
	msg, refused := reasons[reported], false
	for _, id := range refusedSplat {
		if id == reported {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("%s (%s:%d) was NOT refused for its unpacking. It is the node that shipped\n"+
			"    create(**kwargs, model=\"gpt-4o-2024-11-20\")\n"+
			"to a reviewer as `built`, and it raises TypeError at runtime.\ngot: %s",
			reported, site.FileRel, site.LineStart, msg)
	}
	t.Logf("%s (%s:%d) is now refused:\n%s", reported, site.FileRel, site.LineStart, msg)

	// The guard must not have swallowed the repo whole: this engine still has real work to do here, and
	// a "fix" that refuses all 39 would be a regression wearing a safety badge.
	if len(rewritable) == 0 {
		t.Error("every node in the real repo is now refused; the guard is over-reaching")
	}
}
