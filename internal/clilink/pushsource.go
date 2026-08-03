package clilink

import (
	"context"

	"github.com/heros-foreal/agentd/internal/cli"
)

// pushsource.go implements `heros push-source`.
//
// # The one command that sends source
//
// Everything else this CLI transmits is an allowlisted projection built field by field — `link` sends
// numbers, `link --with-ir` sends a workflow's shape. This sends the repository at a revision, so that
// the platform can run discovery and the pattern classifier ITSELF and produce a graph with real labels.
// The classifier reads prompt text and tool names; neither will ever cross as a payload field, so the
// only way to get labels is for the analysis to happen where the source is.
//
// It is a distinct command rather than a flag on `link` for the reason SendWorkflowIR is a distinct
// method: three different things to agree to, three places to point at.
//
// # What the user is told BEFORE anything leaves
//
// `--dry-run` reports exactly what the snapshot contains — the resolved revision, the file count, the
// uncompressed and compressed size — and transmits nothing. That is the honest default for a command
// whose payload is too large to print: a user cannot read 4,000 files, but they can read "4,000 files,
// 12 MiB, from commit abc1234" and know whether that is their repository or their repository plus a
// vendored dependency tree they forgot about.

// largeBundleBytes is the uncompressed size above which the command points at `export-ignore`.
//
// 64 MiB: comfortably above any real source tree (the Linux kernel's is around 1.5 GiB, but no LLM
// workflow is), and far below the platform's own caps — this is ADVICE, not a limit, and it must fire
// well before anything is refused so the user hears it while it is still cheap to act on.
const largeBundleBytes = 64 << 20

// PushSourceData is the machine payload for `push-source`.
type PushSourceData struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	Files          int    `json:"files"`
	Bytes          int    `json:"bytes"`
	Compressed     int    `json:"compressed_bytes"`
	// Transmitted is false for a dry run. Named rather than inferred from the presence of a graph: a
	// reader scripting against this must not have to deduce whether their source left the machine.
	Transmitted bool `json:"transmitted"`
	// Dirty reports that the working tree had uncommitted changes when the snapshot was taken. The
	// snapshot is the COMMITTED tree, so this is the difference between what was sent and what the
	// developer was looking at.
	Dirty bool `json:"working_tree_dirty"`

	// Graph is the platform's discovery summary, absent on a dry run.
	Graph *GraphSummary `json:"graph,omitempty"`
}

// GraphSummary is what the platform found.
type GraphSummary struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
	// Labelled and Unclassified stay separate all the way to the user's terminal, for the reason
	// api.DiscoverySummary keeps them separate: one number cannot distinguish "nothing was classified"
	// from "there was nothing to classify".
	Labelled     int `json:"labelled_regions"`
	Unclassified int `json:"unclassified_regions"`
	LLMCalls     int `json:"llm_calls"`
}

// PushSource builds a snapshot of the repository at a revision and sends it to the platform.
func (c Commands) PushSource(cfg cli.Config, s cli.Streams) error {
	ctx := context.Background()

	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	revision, err := cli.ResolveRevision(ctx, repo, cfg.Get("commit"))
	if err != nil {
		return &cli.ExitError{Code: cli.ExitInvalidCfg, Msg: err.Error(), Err: err}
	}
	workflowID := cfg.Get("workflow-id")
	if workflowID == "" {
		return &cli.ExitError{Code: cli.ExitInvalidCfg,
			Msg: "push-source: --workflow-id is required — it is the id the platform files this snapshot " +
				"and its graph under, and guessing it from the module path would silently attach source to " +
				"the wrong workflow"}
	}

	// --forget is the retraction, handled before anything is built: it sends nothing and needs no bundle.
	if cfg.Get("forget") == "true" {
		cred, ok := cli.LoadCredential()
		if !ok {
			return &cli.ExitError{Code: cli.ExitOperational,
				Msg: "push-source: authentication required — run `heros login` first (nothing was transmitted)"}
		}
		if err := c.client(cred.Token).DeleteSource(ctx, workflowID, revision); err != nil {
			return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
		}
		s.Narratef("push-source: the platform no longer holds source for %s@%s", workflowID, revision[:min(12, len(revision))])
		return s.EmitJSON("push-source", cli.ExitOK, PushSourceData{
			WorkflowID: workflowID, SourceRevision: revision,
		}, nil, nil)
	}

	dirty, _ := cli.WorkingTreeIsDirty(ctx, repo)

	bundle, stats, err := cli.BuildSourceBundle(ctx, repo, revision)
	if err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}

	data := PushSourceData{
		WorkflowID: workflowID, SourceRevision: revision,
		Files: stats.Files, Bytes: stats.Bytes, Compressed: stats.Compressed, Dirty: dirty,
	}

	if dirty {
		// A warning, not a refusal — see cli.WorkingTreeIsDirty. Stated plainly because the developer is
		// about to attribute a graph to code they are not looking at.
		s.Narratef("push-source: WARNING — the working tree has uncommitted changes. The snapshot is the " +
			"COMMITTED tree at this revision, not what is on disk.")
	}
	s.Narratef("push-source: %d files, %s uncompressed (%s compressed), from %s",
		stats.Files, humanBytes(stats.Bytes), humanBytes(stats.Compressed), revision[:min(12, len(revision))])

	// A snapshot much larger than source has to be is almost always committed build artifacts, and the
	// remedy is git's own rather than a denylist of ours: `export-ignore` in .gitattributes is exactly
	// the "exclude this from git archive" mechanism, it lives in the customer's repository where they
	// can review it, and it needs no feature here.
	//
	// This fired on the first real repository it was pointed at — 693 MiB across 2,103 files, which is
	// 330 KB per "source file" and obviously wrong. The cause was dozens of committed 17 MB release
	// binaries under .smoke/. Nothing was broken; the number was simply visible, which is the entire
	// argument for printing it before transmitting rather than after.
	if stats.Bytes > largeBundleBytes {
		s.Narratef("push-source: NOTE — that is large for a source tree. Discovery reads source, so "+
			"committed binaries and vendored trees cost upload time and buy nothing. To exclude paths "+
			"from the snapshot, add `<path> export-ignore` to .gitattributes — the same mechanism "+
			"`git archive` already honours. (Largest contributors are whatever `git ls-tree -r -l %s "+
			"| sort -k4 -nr | head` reports.)", revision[:min(12, len(revision))])
	}

	if cfg.Get("dry-run") == "true" {
		s.Narratef("push-source: --dry-run — nothing was transmitted.")
		return s.EmitJSON("push-source", cli.ExitOK, data, nil, nil)
	}

	cred, ok := cli.LoadCredential()
	if !ok {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "push-source: authentication required — run `heros login` first (nothing was transmitted)"}
	}
	cl := c.client(cred.Token)

	s.Narratef("push-source: transmitting the snapshot to %s…", cred.Endpoint)
	if _, err := cl.PushSource(ctx, workflowID, revision, bundle); err != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}
	data.Transmitted = true

	s.Narratef("push-source: running discovery on the platform…")
	res, err := cl.RunDiscovery(ctx, workflowID, revision)
	if err != nil {
		// The snapshot IS stored — the push succeeded and only the analysis failed. Say so, or the user
		// will re-push source that is already there while believing nothing was sent.
		s.Narratef("push-source: the snapshot was stored, but discovery failed. " +
			"Re-run `heros push-source` to retry the analysis, or `--forget` to remove the snapshot.")
		return &cli.ExitError{Code: cli.ExitOperational, Msg: err.Error(), Err: err}
	}
	data.Graph = &GraphSummary{
		Nodes: res.Nodes, Edges: res.Edges,
		Labelled: res.Labelled, Unclassified: res.Unclassified, LLMCalls: res.LLMCalls,
	}
	s.Narratef("push-source: discovered %d nodes, %d edges — %d labelled region(s), %d unclassified (%d model call(s))",
		res.Nodes, res.Edges, res.Labelled, res.Unclassified, res.LLMCalls)
	return s.EmitJSON("push-source", cli.ExitOK, data, nil, nil)
}

// humanBytes renders a byte count for the line a user reads before deciding to transmit.
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return formatDecimal(float64(n)/(1<<20)) + " MiB"
	case n >= 1<<10:
		return formatDecimal(float64(n)/(1<<10)) + " KiB"
	default:
		return formatDecimal(float64(n)) + " B"
	}
}

func formatDecimal(f float64) string {
	// One decimal place, which is the precision that matters when the question is "is that my repo or
	// my repo plus node_modules".
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
