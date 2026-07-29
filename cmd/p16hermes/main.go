// Command p16hermes drives the P16 context-strategy optimization against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/p13hermes / p14hermes / p15hermes). It
// discovers the IR and then exercises every P16 code path this repository can actually reach.
//
// # What this run proves, and the boundary it deliberately does NOT hide
//
// P16's claim is narrow and load-bearing: a context override is either MATERIALIZED into the source or
// REFUSED by name, and never resolved-and-dropped. On this repository the outcome is the second one,
// everywhere — and §2 counts the reason, which is NOT "Python has no rewriter" (it has one). Every
// discovered call site here passes `**kwargs`: the request is assembled elsewhere in the program, so no
// rewriter in any language has turns to select among. The refusal says that, and names the unpacking.
//
// 🔴 That is the finding, not a gap in the demonstration. A run that quietly produced a diff here would
// mean the override had been dropped and the variant scored as its base configuration — the single
// worst thing this platform can emit. What §3 proves is that it cannot happen: a spec carrying BOTH a
// context override this engine cannot apply AND a model override it can is refused whole, so no partial
// diff exists to be scored.
//
// Concretely, all of the following are the shipped code paths running on the real IR:
//
//	§1  the Dimension enum is unchanged, and the drop-tolerance attribute is additive over the REAL
//	    discovered config: absent ⇒ byte-identical hash, present ⇒ a different configuration;
//	§2  the per-policy materializer coverage table — what Go writes into source and what it declines;
//	§3  🔴 the headline: every context policy is REFUSED at transform on this repository's language,
//	    by name, with no diff — and a spec carrying a refused override alongside another one is
//	    refused WHOLE rather than half-applied;
//	§4  the two P16 policies assemble host-side over a real conversation, with the drop MEASURED;
//	§5  🔴 the drop-tolerance gate rejects an over-dropping proposal on a real node, at emission,
//	    before any transform and before any eval spend;
//	§6  retrieval tuning proposes top-k / chunk size / embedding, is verified on a held-out split
//	    derived from the real config_hash, refuses an overlapping split, and pins its measurement.
//
// The boundaries, stated rather than worked around:
//
//	the MATERIALIZATION cannot run here, and §2 says why with a count rather than a claim. It is
//	exercised against real fixtures in the unit suite — TestGoContextMaterializes for Go and
//	TestPythonContextMaterializes for Python — and is reported here as not-applicable, never simulated.
//
//	the host services are a RECORDING DOUBLE. A summarizer call needs a provider and a credential;
//	what P16 asserts about it is that the call is issued host-side and its resolved request is
//	identical across runs, and both are observable against a double. The conversation is real text
//	read from the repository.
//
// It NEVER executes the target — it parses it (invariant I1). What is real: the repository, the
// discovered IR, the config hashes, the coverage table, the refusals, the assembled contexts, the gate
// verdicts, and the held-out split. What is illustrative: the diagnosis signals (in production those
// come from P4.5) and the eval series in §6 (in production those come from P4 through a provider).
//
//	go run ./cmd/p16hermes -repo /tmp/hermes-agent
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/contextassembly"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

	// The revision is RESOLVED against the checkout, never asserted: source_revision is half of the
	// reproducibility key, and a SHA that does not match the tree would put a false one on every hash
	// printed below.
	commitSHA, revNote, err := sourcerev.Resolve(*repo, *pin)
	if err != nil {
		log.Fatalf("%v", err)
	}

	res, err := discovery.Run(discovery.Options{
		Repo:      *repo,
		CommitSHA: commitSHA,
		RepoURL:   repoURL,
		Frontends: []discovery.LanguageFrontend{discovery.NewPythonFrontend()},
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR

	fmt.Printf("=== P16 context strategy optimization - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	base := baselineFrom(ir, commitSHA)
	if len(base.Order) < 4 {
		log.Fatalf("this demonstration needs at least 4 discovered nodes, got %d", len(base.Order))
	}
	fmt.Printf("baseline graph: %d ordered nodes\n", len(base.Order))
	for i, id := range base.Order[:3] {
		fmt.Printf("  [%d] %s  %s  context=%s\n", i, short(id), siteOf(ir, id), contextOf(ir, id))
	}
	fmt.Printf("  ... %d more\n\n", len(base.Order)-3)

	baseHash := additivity(ir, base)
	coverage(ir, base, *repo)
	refuseAtTransform(ir, base, *repo)
	newPolicies(*repo)
	dropGate(ir, base, baseHash)
	retrieval(ir, base, baseHash)

	fmt.Printf("\n=== P16 run complete on the real hermes-agent IR ===\n")
	fmt.Printf("The axis is MODELED, HASHED, GATED and MATERIALIZED — for Go and Python alike. On THIS\n")
	fmt.Printf("repository every context override is still REFUSED, and §2 counts why: its call sites unpack\n")
	fmt.Printf("their arguments rather than writing their turns out, so there is nothing for any rewriter to\n")
	fmt.Printf("select among. That refusal names the unpacking rather than promising a rewriter, because a\n")
	fmt.Printf("rewriter would decline the same call site for the same reason.\n")
}

// ── §1 — no new dimension, and an ADDITIVE attribute over the real config ────────────────────────

func additivity(ir *discovery.IR, base *variantspec.VariantSpec) string {
	fmt.Printf("-- §1 - no new Dimension; drop tolerance is additive over the REAL discovered config --\n")
	names := make([]string, 0, len(variantspec.Dimensions()))
	for _, d := range variantspec.Dimensions() {
		names = append(names, string(d))
	}
	fmt.Printf("  Dimension enum: [%s]\n", strings.Join(names, " "))
	fmt.Printf("  -> retrieval is a `rag-retrieval` POLICY with params under DimContext, not a DimRetrieval.\n")
	fmt.Printf("     A second member would split one axis's identity across two and double the hash surface.\n")

	baseHash, baseCanon := hashAndCanon(ir, base)
	fmt.Printf("  baseline config_hash: %s\n", baseHash)
	if strings.Contains(string(baseCanon), "context_drop_tolerance") {
		fmt.Printf("  !! a config declaring no tolerance emitted the key - every frozen hash would move\n")
	} else {
		fmt.Printf("  no node declares a tolerance -> the key is ABSENT from the canonical bytes, so this hash\n")
		fmt.Printf("     is byte-identical to what it was before the attribute existed\n")
	}

	// Now DECLARE one on a real node and watch the hash move — the other half of the contract.
	withTol := cloneSpec(base)
	target := base.Order[1]
	o := withTol.Nodes[target]
	tol := 0.25
	o.ContextDropTolerance = &tol
	withTol.Nodes[target] = o
	tolHash, tolCanon := hashAndCanon(ir, withTol)
	fmt.Printf("  node %s declares tolerance 0.25 -> config_hash %s %s\n",
		short(target), short(tolHash), sameOrDiff(tolHash, baseHash))
	if !strings.Contains(string(tolCanon), `"context_drop_tolerance":0.25`) {
		fmt.Printf("  !! the declared tolerance is missing from the canonical bytes\n")
	}
	// And ONLY that node acquires it: the tolerance is per-node, not per-config.
	others := strings.Count(string(tolCanon), "context_drop_tolerance")
	fmt.Printf("  occurrences of the key across %d nodes: %d (one, on the declaring node only)\n\n",
		len(base.Order), others)
	return baseHash
}

// ── §2 — the coverage table ──────────────────────────────────────────────────────────────────────

func coverage(ir *discovery.IR, base *variantspec.VariantSpec, repo string) {
	fmt.Printf("-- §2 - per-policy materializer coverage, the ONE source of truth (tasks 2.2, 3.3) --\n")
	// The policy table is language-INDEPENDENT — whether a summary exists in your source is a fact about
	// summarization, not about Python — so it is printed once, and the languages that can perform a
	// SELECTION are listed beside it.
	seen := map[string]bool{}
	for _, c := range transform.ContextMaterializerCoverage() {
		if seen[c.Policy] {
			continue
		}
		seen[c.Policy] = true
		switch c.Mode {
		case "identity":
			fmt.Printf("  %-22s %-16s the un-rewritten call site already assembles exactly this\n", c.Policy, c.Mode)
		case "select":
			fmt.Printf("  %-22s %-16s MATERIALIZED by deleting the turns the policy does not retain\n", c.Policy, c.Mode)
		default:
			fmt.Printf("  %-22s %-16s refused: %s\n", c.Policy, c.Mode, firstClause(c.Reason))
		}
	}
	fmt.Printf("  selection rewriter has landed for: %s\n",
		strings.Join(transform.ContextMaterializerLanguages(), ", "))
	fmt.Printf("  -> this table is what the rewriter refuses FROM, so a documented capability, a console\n")
	fmt.Printf("     badge and an emitted refusal cannot drift apart.\n\n")

	// 🔴 The finding, and the reason §3 declines everything. hermes-agent IS Python, and Python's
	// rewriter HAS landed — so "no rewriter yet" is not why nothing materializes here. The reason is in
	// the repository's own call sites, and it is worth counting rather than asserting.
	fmt.Printf("  call-site survey - what the %d discovered nodes actually write:\n", len(base.Order))
	written, unpacked, other := 0, 0, 0
	var exampleUnpack, exampleWritten string
	sites, err := discovery.IndexSpanCallSites(repo, ir.Workflow.Language, nil)
	if err != nil {
		log.Fatalf("index span call sites: %v", err)
	}
	for _, id := range base.Order {
		site, ok := sites[id]
		if !ok {
			other++
			continue
		}
		name := ""
		if site.ArgMap.Prompt != nil {
			name = site.ArgMap.Prompt.Name
		}
		switch {
		case name != "" && hasKeyword(site, name):
			written++
			if exampleWritten == "" {
				exampleWritten = siteOf(ir, id)
			}
		case site.KeywordUnpacking != nil:
			unpacked++
			if exampleUnpack == "" {
				exampleUnpack = fmt.Sprintf("%s  (%s)", siteOf(ir, id), site.KeywordUnpacking.Text)
			}
		default:
			other++
		}
	}
	fmt.Printf("    %2d write a message list at the call site\n", written)
	fmt.Printf("    %2d pass an UNPACKING instead (**kwargs) - the request is assembled elsewhere\n", unpacked)
	fmt.Printf("    %2d neither (the row names no message argument)\n", other)
	if exampleUnpack != "" {
		fmt.Printf("    e.g. %s\n", exampleUnpack)
	}
	fmt.Printf("  -> THIS is why §3 declines everything, and it is not a missing rewriter: a call site that\n")
	fmt.Printf("     does not write its turns has nothing for any rewriter, in any language, to select\n")
	fmt.Printf("     among. The refusal below says exactly that, and names the unpacking to fix.\n\n")
}

// hasKeyword reports whether the call site WRITES the named keyword argument.
func hasKeyword(site discovery.SpanCallSite, name string) bool {
	_, ok := site.Keywords[name]
	return ok
}

// ── §3 🔴 — every policy refuses on this repository, by name, with no diff ───────────────────────

func refuseAtTransform(ir *discovery.IR, base *variantspec.VariantSpec, repo string) {
	fmt.Printf("-- §3 - 🔴 every context override on this repository is REFUSED, by name (tasks 3.2-3.4) --\n")
	node := base.Order[0]

	for _, policy := range []string{"sliding-window", "semantic-compaction", "summarization", "rag-retrieval"} {
		entry := contextEntry(policy)
		r := resolvedFor(ir, base, map[string]variantspec.ResolvedOverride{
			node: {Context: entry},
		})
		patch, err := transform.Generate(r, repo)
		switch {
		case err != nil:
			var re *transform.RewriteError
			named := errors.As(err, &re) && re.Dim == "context" && re.NodeID == node
			fmt.Printf("  %-20s REFUSED  typed=%v names(node,dim)=%v\n",
				policy, errors.Is(err, transform.ErrUnsafeRewrite), named)
			if policy == "sliding-window" {
				fmt.Printf("      %s\n", wrap(re.Detail, 6))
			}
		case patch.IsEmpty():
			fmt.Printf("  %-20s !! NO diff and NO error - the override was resolved, hashed, and then\n", policy)
			fmt.Printf("      dropped. The run would use the call site's own assembly while reporting the\n")
			fmt.Printf("      variant's config_hash: a FALSE MEASUREMENT.\n")
		default:
			fmt.Printf("  %-20s applied (%d file(s))\n", policy, len(patch.Files))
		}
	}

	// 🔴 The companion assertion, and the one that makes the guarantee real: a spec carrying a refused
	// context override ALONGSIDE another dimension is refused WHOLE. A partial diff would carry the
	// variant's config_hash while assembling context the base configuration's way.
	//
	// Note which dimension the refusal is attributed to, and why it does not weaken the point.
	// Dimensions are attempted in a fixed order (model, prompt, skills, context, tools) and the FIRST
	// refusal aborts — so on a mixed spec the blame may land on either. What is being proved is not
	// "context refused first"; it is that ONE refused dimension takes the whole patch down, so a
	// half-applied transform cannot exist for any combination.
	fmt.Printf("\n  mixed spec (context override + a second dimension on the same node):\n")
	r := resolvedFor(ir, base, map[string]variantspec.ResolvedOverride{
		node: {Context: contextEntry("sliding-window"), Model: modelEntry()},
	})
	if patch, err := transform.Generate(r, repo); err == nil {
		fmt.Printf("    !! a patch was emitted (%d file(s), empty=%v) - one override would ship while the\n",
			len(patch.Files), patch.IsEmpty())
		fmt.Printf("       other was silently dropped, under the VARIANT's hash\n")
	} else {
		var re *transform.RewriteError
		errors.As(err, &re)
		fmt.Printf("    REFUSED WHOLE - no patch, no files, nothing partial (first refusing dim=%q)\n", re.Dim)
		fmt.Printf("    -> the transform is a pure function that returns bytes; a refusal returns NO bytes, so\n")
		fmt.Printf("       there is no diff for any scorer to reach even in principle.\n\n")
	}
}

// ── §4 — the two new policies, host-side, drop measured ─────────────────────────────────────────

func newPolicies(repo string) {
	fmt.Printf("-- §4 - hierarchical-summary and structured-extraction, behind the Policy interface --\n")
	conv := conversationFrom(repo)
	fmt.Printf("  conversation: %d messages, %d source characters (real text read from the repository)\n",
		len(conv.Messages), sourceChars(conv))

	host := &recordingHost{summary: "The user asked how the agent loads its configuration and how tools are registered."}
	tags := p0Tags("summarize-node")
	run := contextassembly.Runner{Host: host, Collector: nil}

	for _, tc := range []struct{ policy, params string }{
		{"hierarchical-summary", `{"summarizer_model_ref":"anthropic/claude-sonnet-5","recent_verbatim":2}`},
		{"structured-extraction", `{"fields":["question"]}`},
		{"semantic-compaction", `{"target_tokens":60}`},
	} {
		got, err := run.Assemble(context.Background(), contextassembly.Request{
			Tags: tags, Entry: contextEntryWithParams(tc.policy, tc.params), Conversation: conv, Seed: 7,
		})
		if err != nil {
			fmt.Printf("  %-22s failed closed: %v\n", tc.policy, firstClause(err.Error()))
			continue
		}
		req := "none (LLM-free)"
		if got.ResolvedRequest != nil {
			req = fmt.Sprintf("%s model=%s seed=%d", got.ResolvedRequest.Op,
				got.ResolvedRequest.ModelRef, got.ResolvedRequest.Seed)
		}
		fmt.Printf("  %-22s %d msg -> %d msg  lossy=%v  drop=%.2f  host request: %s\n",
			tc.policy, got.SourceMessageCount, len(got.Messages), got.Lossy, got.DropRatio, req)
	}
	fmt.Printf("  host-side summarizer calls: %d (a policy never holds a credential; the sandbox never\n", host.calls)
	fmt.Printf("     sees one, and the resolved request is the determinism handle)\n")
	fmt.Printf("  -> adding these two cost a Name/ParamsSchema/Assemble each. No registry schema, no\n")
	fmt.Printf("     ContextSpec field, no Dimension member moved.\n\n")
}

// ── §5 🔴 — the drop-tolerance gate, on a real node, before any spend ────────────────────────────

func dropGate(ir *discovery.IR, base *variantspec.VariantSpec, baseHash string) {
	fmt.Printf("-- §5 - 🔴 the drop-tolerance gate rejects before transform and before eval spend --\n")
	node := base.Order[1]

	spec := cloneSpec(base)
	o := spec.Nodes[node]
	tol := 0.25
	o.ContextDropTolerance = &tol
	spec.Nodes[node] = o
	fmt.Printf("  node %s (%s) declares a tolerance of %.0f%%\n", short(node), siteOf(ir, node), tol*100)

	menu := proposal.Menu{ContextPolicies: []proposal.ContextChoice{
		{Ref: refSummarize, Policy: "summarization", Lossy: true, ExpectedDrop: 0.85},
		{Ref: refWindow, Policy: "sliding_window", Lossy: true, ExpectedDrop: 0.15},
	}}
	eng := proposal.Engine{Base: spec, BaseVariantID: baseHash, Menu: menu, IR: ir}
	em := eng.Propose([]proposal.Target{{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, TaxonomyCode: diagnosis.CauseContextOverflow,
			EvidenceCaseIDs: []string{"case-hermes-1"}, Confidence: 0.7},
		Pattern: patternclassifier.Reflection,
	}})

	for _, c := range em.Candidates {
		fmt.Printf("  ADMITTED  %-14s %s\n", c.Operator, c.Rationale)
	}
	for _, r := range em.Refusals {
		if strings.Contains(r.Reason, "drop") {
			fmt.Printf("  REJECTED  %-14s %s\n", r.Operator, wrap(r.Reason, 12))
		}
	}
	fmt.Printf("  -> the 85%%-drop policy never becomes a diff and never consumes a multi-seed eval run.\n")
	fmt.Printf("     Scoring would reach the same verdict later, for the price of the run.\n")

	// And the gate does not refuse on ignorance: a policy with no measurement and no estimate is
	// admitted, because "we have no data" must not come to mean "no".
	unknown := proposal.Engine{Base: spec, BaseVariantID: baseHash, IR: ir,
		Menu: proposal.Menu{ContextPolicies: []proposal.ContextChoice{
			{Ref: refSummarize, Policy: "summarization"}, // no Lossy, no estimate
		}}}
	un := unknown.Propose([]proposal.Target{{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, TaxonomyCode: diagnosis.CauseContextOverflow,
			EvidenceCaseIDs: []string{"case-hermes-1"}, Confidence: 0.7},
		Pattern: patternclassifier.Reflection,
	}})
	fmt.Printf("  unmeasured, unestimated policy on the SAME node: %d candidate(s) admitted - the gate\n",
		len(un.Candidates))
	fmt.Printf("     refuses on evidence, never on ignorance; verification decides what it cannot.\n\n")
}

// ── §6 — retrieval tuning, held out and pinned ──────────────────────────────────────────────────

func retrieval(ir *discovery.IR, base *variantspec.VariantSpec, baseHash string) {
	fmt.Printf("-- §6 - retrieval tuning: three knobs, a held-out verdict, a pinned measurement --\n")
	node := base.Order[2]

	spec := cloneSpec(base)
	o := spec.Nodes[node]
	o.ContextPolicy = refTopK
	spec.Nodes[node] = o

	menu := proposal.Menu{ContextPolicies: []proposal.ContextChoice{
		{Ref: refTopK, Policy: "topk", TopK: 5},
		{Ref: refTopKBig, Policy: "topk", TopK: 20},
		{Ref: refChunk, Policy: "topk", TopK: 5, ChunkSize: 256},
		{Ref: refEmbed, Policy: "topk", TopK: 5, EmbeddingModel: "text-embedding-3-large"},
	}}
	eng := proposal.Engine{Base: spec, BaseVariantID: baseHash, Menu: menu, IR: ir}
	em := eng.Propose([]proposal.Target{{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, TaxonomyCode: diagnosis.CauseRetrievalMiss,
			EvidenceCaseIDs: []string{"case-hermes-3"}, Confidence: 0.8},
		Pattern: patternclassifier.RetrievalRAG,
	}})
	proposal.SortCandidates(em.Candidates)
	fmt.Printf("  node %s, currently top-k 5:\n", short(node))
	for _, c := range em.Candidates {
		if c.Operator == proposal.OpRAGTune {
			fmt.Printf("    %s\n", c.Rationale)
		}
	}
	fmt.Printf("  -> each names the knob it moved and from what, so a verified win attributes to ONE change\n")
	fmt.Printf("     rather than to \"something about retrieval\".\n")

	// The held-out split, derived from the REAL config_hash over synthetic case ids (P4 owns the real
	// eval set; the derivation and its disjointness are what P16 owns).
	cases := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		cases = append(cases, fmt.Sprintf("case-%03d", i))
	}
	split := proposal.DeriveRetrievalSplit(baseHash, cases)
	fmt.Printf("\n  held-out split from config_hash %s: tuning=%d held-out=%d overlap=%d\n",
		short(baseHash), len(split.Tuning), len(split.HeldOut), len(split.Overlap()))

	weak := successSeries("base", cases, 0.42)
	strong := successSeries("cand", cases, 0.91)
	v, err := proposal.VerifyRetrievalChange(split, weak, strong, evalstats.DefaultConfig())
	if err != nil {
		fmt.Printf("  !! %v\n", err)
	} else {
		fmt.Printf("  verdict on the HELD-OUT half: verified=%v\n    %s\n", v.Verified, wrap(v.Reason, 4))
	}

	// 🔴 And the refusal. An authored split whose halves intersect has no honest verdict.
	overlapping := proposal.RetrievalSplit{Tuning: cases[:30], HeldOut: cases[20:]}
	if _, err := proposal.VerifyRetrievalChange(overlapping, weak, strong, evalstats.DefaultConfig()); err != nil {
		fmt.Printf("  overlapping split (10 shared cases): REFUSED (typed=%v)\n",
			errors.Is(err, proposal.ErrOverlappingSplit))
		fmt.Printf("    %s\n", wrap(firstClause(err.Error()), 4))
	} else {
		fmt.Printf("  !! an overlapping split produced a verdict - that number is overfit sold as a result\n")
	}

	// The measurement pin: same config_hash + source_revision + seed ⇒ identical resolved request.
	host := &recordingHost{chunks: []registry.Chunk{{ID: "c1", Text: "hermes tool registry"}, {ID: "c2", Text: "config loader"}}}
	run := contextassembly.Runner{Host: host}
	pin := contextassembly.MeasurementPin{ConfigHash: baseHash, SourceRevision: base.SourceRevision, Seed: 7}
	req := func() contextassembly.Request {
		return contextassembly.Request{
			Tags:         p0Tags(node),
			Entry:        contextEntryWithParams("rag-retrieval", `{"top_k":2,"retriever_ref":"hermes-kb","rerank":true}`),
			Conversation: registry.Conversation{Messages: []registry.Message{{Role: "user", Content: "how are tools registered?"}}},
		}
	}
	a, errA := run.Measure(context.Background(), req(), pin)
	b, errB := run.Measure(context.Background(), req(), pin)
	if errA != nil || errB != nil {
		fmt.Printf("  !! measurement failed: %v %v\n", errA, errB)
	} else {
		fmt.Printf("\n  pinned measurement (config_hash + source_revision + seed 7), run twice:\n")
		fmt.Printf("    identical resolved request: %v  (op=%s ref=%s top_k=%d seed=%d)\n",
			contextassembly.SameRequest(a, b), a.Request.Op, a.Request.Ref, a.Request.TopK, a.Request.Seed)
		fmt.Printf("    augmentation: drop=%.2f retrieved_chunks=%d - adding passages is RETRIEVAL, not loss\n",
			a.Assembled.DropRatio, a.Assembled.RetrievedChunks)
	}
	// An unpinned run is not a measurement at all.
	if _, err := run.Measure(context.Background(), req(), contextassembly.MeasurementPin{Seed: 7}); err != nil {
		fmt.Printf("    unpinned run: REFUSED as a measurement (typed=%v) - nothing could re-derive its number\n\n",
			errors.Is(err, contextassembly.ErrUnpinnedMeasurement))
	}
}

// ── fixtures and helpers ────────────────────────────────────────────────────────────────────────

const (
	refSummarize = "aaaa111111111111111111111111111111111111111111111111111111111111"
	refWindow    = "aaaa222222222222222222222222222222222222222222222222222222222222"
	refTopK      = "aaaa333333333333333333333333333333333333333333333333333333333333"
	refTopKBig   = "aaaa444444444444444444444444444444444444444444444444444444444444"
	refChunk     = "aaaa555555555555555555555555555555555555555555555555555555555555"
	refEmbed     = "aaaa666666666666666666666666666666666666666666666666666666666666"
)

// contextEntry builds a resolved context entry with the REAL policy implementation bound, the way
// registry.ResolveContextPolicy would. Binding the real one matters: the materializer asks the policy
// what it retains, so a fake would prove nothing about what a run assembles.
func contextEntry(policy string) *registry.ContextEntry {
	return contextEntryWithParams(policy, defaultParams(policy))
}

func contextEntryWithParams(policy, params string) *registry.ContextEntry {
	for _, p := range registry.BuiltinPolicies() {
		if p.Name() == policy {
			return &registry.ContextEntry{
				VersionID: strings.Repeat("e", 64), Name: "ctx-" + policy,
				Spec:   registry.ContextSpec{Policy: policy, Params: json.RawMessage(params)},
				Policy: p,
			}
		}
	}
	log.Fatalf("no builtin policy named %q", policy)
	return nil
}

func defaultParams(policy string) string {
	switch policy {
	case "sliding-window":
		return `{"window_size":2}`
	case "semantic-compaction":
		return `{"target_tokens":60}`
	case "summarization":
		return `{"summarizer_model_ref":"anthropic/claude-sonnet-5"}`
	case "rag-retrieval":
		return `{"top_k":3,"retriever_ref":"hermes-kb"}`
	default:
		return `{}`
	}
}

func modelEntry() *registry.ModelEntry {
	return &registry.ModelEntry{VersionID: strings.Repeat("m", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: "anthropic", ModelID: "claude-sonnet-5"}}
}

// recordingHost is the trusted host, doubled. It records what a policy asked it for so the run can show
// that the model/retrieval call went through HostServices and nowhere else.
type recordingHost struct {
	summary string
	chunks  []registry.Chunk
	calls   int
}

func (h *recordingHost) Summarize(_ context.Context, _ registry.ResolvedRequest) (string, error) {
	h.calls++
	return h.summary, nil
}

func (h *recordingHost) Retrieve(_ context.Context, _ registry.ResolvedRequest) ([]registry.Chunk, error) {
	h.calls++
	return h.chunks, nil
}

// conversationFrom builds a conversation out of REAL text from the repository, so the assembled
// contexts and the measured drops below are over something that exists rather than over lorem ipsum.
func conversationFrom(repo string) registry.Conversation {
	msgs := []registry.Message{{Role: "user", Content: "question: how does this agent load its configuration?"}}
	var files []string
	_ = filepath.Walk(repo, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files) // deterministic: the same checkout must produce the same conversation
	for i, f := range files {
		if i >= 3 {
			break
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(b))
		if len(text) > 600 {
			text = text[:600]
		}
		msgs = append(msgs, registry.Message{Role: "assistant", Content: text})
	}
	msgs = append(msgs, registry.Message{Role: "assistant", Content: "It reads a config file at startup."})
	return registry.Conversation{Messages: msgs}
}

func sourceChars(c registry.Conversation) int {
	n := 0
	for _, m := range c.Messages {
		n += len(m.Content)
	}
	return n
}

// p0Tags is the seven-tag attribution every assembly measurement carries. The collector is nil in this
// run (nothing is published to a TSDB from a demonstration), but the tags are built the way a real run
// builds them, because a measurement that cannot be attributed to a node of a run is not a measurement.
func p0Tags(nodeID string) telemetry.P0Tags {
	return telemetry.P0Tags{
		VariantID: "p16-hermes", RunID: "run-p16", NodeID: nodeID, CaseID: "case-hermes-1", Seed: 7,
	}
}

func successSeries(variant string, cases []string, v float64) evalstats.Series {
	s := evalstats.Series{VariantID: variant, Metric: "task_success"}
	for _, c := range cases {
		for _, seed := range []int64{1, 2, 3, 4, 5} {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: c, Seed: seed, Value: v})
		}
	}
	return s
}

func baselineFrom(ir *discovery.IR, commitSHA string) *variantspec.VariantSpec {
	spec := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: commitSHA,
		Nodes: map[string]variantspec.NodeOverride{},
	}
	for _, n := range ir.Nodes {
		spec.Order = append(spec.Order, n.NodeID)
	}
	for _, e := range ir.Edges {
		spec.Edges = append(spec.Edges, variantspec.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	return spec
}

func cloneSpec(s *variantspec.VariantSpec) *variantspec.VariantSpec {
	out := &variantspec.VariantSpec{
		WorkflowID: s.WorkflowID, ParentVariantID: s.ParentVariantID, SourceRevision: s.SourceRevision,
		Order: append([]string(nil), s.Order...),
		Nodes: make(map[string]variantspec.NodeOverride, len(s.Nodes)),
		Edges: append([]variantspec.Edge(nil), s.Edges...),
	}
	for k, v := range s.Nodes {
		out.Nodes[k] = v
	}
	return out
}

func hashAndCanon(ir *discovery.IR, spec *variantspec.VariantSpec) (string, []byte) {
	r, err := variantspec.Resolve(context.Background(), spec, ir, emptyRegistries{})
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	canon, err := r.Config.Canonical()
	if err != nil {
		log.Fatalf("canonical: %v", err)
	}
	return r.ConfigHash, canon
}

// resolvedFor builds the Resolved projection the transform engine consumes, with the given overrides
// laid on. It goes through the real resolver for the hash and language, then attaches the resolved
// overrides — the same shape variantspec.Resolve produces for a spec that references registry entries.
func resolvedFor(ir *discovery.IR, base *variantspec.VariantSpec, overrides map[string]variantspec.ResolvedOverride) *variantspec.Resolved {
	r, err := variantspec.Resolve(context.Background(), base, ir, emptyRegistries{})
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	r.Overrides = overrides
	return r
}

type emptyRegistries struct{}

func (emptyRegistries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}

// ResolveMemory completes variantspec.Registries (P17). It fails closed like its siblings: this
// harness pins no memory strategy, so a memory_ref here names nothing and must not resolve to something.
func (emptyRegistries) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}

func siteOf(ir *discovery.IR, id string) string {
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return fmt.Sprintf("%s:%d %s", n.CallSite.File, n.CallSite.LineStart, n.CallSite.Symbol)
		}
	}
	return "?"
}

func contextOf(ir *discovery.IR, id string) string {
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return n.ContextAssembly.Policy
		}
	}
	return "?"
}

func sameOrDiff(h, base string) string {
	if h == base {
		return "(SAME as baseline - the attribute did not reach the hash)"
	}
	return "(differs - a declared tolerance is part of the configuration's identity)"
}

func short(s string) string {
	if len(s) > 14 {
		return s[:14]
	}
	return s
}

// firstClause trims a long engine sentence to its first clause, for a survey line. The FULL sentence is
// printed wherever a reader has to act on it — a survey may abbreviate, a refusal may not.
func firstClause(s string) string {
	if i := strings.Index(s, ";"); i > 0 && i < 140 {
		return s[:i]
	}
	if len(s) > 140 {
		return s[:140] + "..."
	}
	return s
}

// wrap reflows a long refusal to the terminal at the given indent, so the engine's own words are
// readable rather than a single 400-column line.
func wrap(s string, indent int) string {
	const width = 96
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	col := 0
	for _, w := range strings.Fields(s) {
		if col > 0 && col+len(w)+1 > width {
			b.WriteString("\n" + pad)
			col = 0
		}
		if col > 0 {
			b.WriteString(" ")
			col++
		}
		b.WriteString(w)
		col += len(w)
	}
	return b.String()
}
