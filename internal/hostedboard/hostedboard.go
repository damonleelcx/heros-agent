// Package hostedboard assembles the P4 eval board from the runs a tenant has LINKED, rather than from
// runs the platform executed itself.
//
// # Why the board is assembled and not computed
//
// evalboard.Build works from a scoring.Cache, which is produced by bootstrapping raw per-observation
// series. That is the right thing where the eval ran — the CLI, or cmd/demo/evalboard's fan-out — and it
// is not available here: a linked run carries the RESULT of that bootstrap (a mean and an interval) and
// not the observations or the replicates behind it. The allowlist permits the interval and refuses the
// eval data, deliberately, and that is not going to change.
//
// So this package builds an evalboard.View directly, and the discipline it holds to is: fill a field
// only when a linked run actually says it, and say so explicitly where it cannot.
//
// 🔴 The specific thing this must not do is the thing that kept P4 mounted nil for so long — render a
// field the platform GUESSED. Three guesses were available and all three are refused here:
//
//   - `gate_pass` is now REPORTED (the customer's own CLI verdict crossed as of migration 0023). Before
//     that it could only have been invented, and inventing it would have accused a passing workflow of
//     failing, or worse, cleared a failing one.
//   - `TiedWith` / `AllTie` CANNOT be reported: the overlap test needs the bootstrap replicates. So
//     TieAnalysis is set to `unavailable` and the board says nothing about ties, rather than letting
//     `AllTie: false` assert that variants are distinguishable when nothing tested them.
//   - `Components` (the normalized per-metric contributions) are left ABSENT. Rendering a Normalized of
//     0 alongside a real Raw would read as "this metric contributed nothing", which is a measurement
//     claim, not a missing value.
//
// # What a "variant" is here
//
// One config_hash. That is what the word means everywhere else in this system — a variant IS a
// configuration, and its hash is its identity — and it is what makes two runs comparable at all.
//
// Within a config_hash, the NEWEST run wins. Not an average: averaging two runs' intervals is not a
// valid interval over their union, and the parts needed to do it properly (the observations) are exactly
// what does not cross. "The most recent measurement of this configuration" is a sentence that is true
// and that a user can act on; a mean of means is neither.
package hostedboard

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// RunSource is the read this package needs: a tenant's linked runs for one workflow.
type RunSource interface {
	ForWorkflow(tenantID, workflowID string) ([]linkingest.LinkedRun, error)
}

// Build assembles the board for one workflow from its linked runs.
//
// An empty run list is StateEmpty, not an error: a tenant who has linked nothing has an empty board, and
// the action is "link a run", which is what the console renders for that state.
func Build(workflowID string, runs []linkingest.LinkedRun) evalboard.View {
	v := evalboard.View{
		State:      evalboard.StateComplete,
		WorkflowID: workflowID,
		// Ties are not testable from linked runs. Stated as data — see this file's header.
		TieAnalysis: evalboard.TieUnavailable,
		// Ranking is a pure function of what was already linked; assembling a board enqueues nothing.
		RunsEnqueued: 0,
		Profiles:     nil,
	}
	if len(runs) == 0 {
		v.State = evalboard.StateEmpty
		return v
	}

	latest, superseded := newestPerConfig(runs)

	var rows []evalboard.Row
	var missingEvidence int
	for _, lr := range latest {
		row, ok := rowFor(lr)
		if !ok {
			// A run linked before the evidence crossed (migration 0023). It is NOT ranked, because
			// ranking it would require a quality score it never sent — and it is NOT dropped, because a
			// board silently showing two of a tenant's five configurations is the quietest kind of wrong.
			missingEvidence++
			v.Unmeasured = append(v.Unmeasured, evalboard.UnmeasuredView{
				VariantID: lr.ConfigHash,
				Label:     shortHash(lr.ConfigHash),
				Reason:    "linked by a CLI that did not report eval evidence — re-run `heros eval` and link it again",
			})
			continue
		}
		rows = append(rows, row)
	}

	// Rank by quality descending; config hash breaks the tie so the order is stable across reloads.
	// This is an ORDERING, not a statistical claim that the first row is better than the second — which
	// is exactly why TieAnalysis says the overlap test did not run.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].ConfigHash < rows[j].ConfigHash
	})

	// A gate failure DISQUALIFIES, exactly as it does on a locally computed board: a variant that failed
	// the customer's own threshold must not sit at rank 1 because its quality happened to be highest.
	for _, r := range rows {
		if r.GatePass {
			r.Rank = len(v.Ranked) + 1
			v.Ranked = append(v.Ranked, r)
		} else {
			v.Disqualified = append(v.Disqualified, r)
		}
	}

	v.Pareto = paretoOf(v.Ranked)
	v.Notes = notesFor(v, superseded, missingEvidence)
	if missingEvidence > 0 {
		v.State = evalboard.StatePartial
	}
	return v
}

// newestPerConfig collapses runs to one per config_hash, keeping the newest, and reports how many were
// superseded so the board can say a number rather than silently discarding rows.
//
// runs arrive newest-first from the store; this does not re-sort, it takes the first sighting of each
// hash. Depending on the store's documented order rather than re-deriving it keeps ONE definition of
// "newest" in the system.
func newestPerConfig(runs []linkingest.LinkedRun) (latest []linkingest.LinkedRun, superseded int) {
	seen := map[string]bool{}
	for _, lr := range runs {
		if seen[lr.ConfigHash] {
			superseded++
			continue
		}
		seen[lr.ConfigHash] = true
		latest = append(latest, lr)
	}
	return latest, superseded
}

// rowFor projects one linked run onto a board row. ok=false when the run carries no eval evidence.
func rowFor(lr linkingest.LinkedRun) (evalboard.Row, bool) {
	if !lr.EvalEvidencePresent() {
		return evalboard.Row{}, false
	}
	quality, ok := scoreOf(lr.Scores, "quality")
	if !ok {
		// Evidence present but no quality score: nothing to rank on. Treated the same as missing
		// evidence rather than ranked at zero, which would read as "this variant answered nothing
		// correctly" — a measurement claim nobody made.
		return evalboard.Row{}, false
	}
	return evalboard.Row{
		// The variant IS the configuration. Using the config hash as the id keeps that true rather than
		// minting a synthetic variant id the customer has never seen anywhere else.
		VariantID:       lr.ConfigHash,
		ConfigHash:      lr.ConfigHash,
		ConfigHashShort: shortHash(lr.ConfigHash),
		Label:           shortHash(lr.ConfigHash),

		Score:  quality.Value,
		CILow:  quality.CILow,
		CIHigh: quality.CIHigh,
		NSeeds: lr.Eval.SeedCount,
		NCases: lr.Eval.CaseCount,
		// Method names where the interval came from, so a stored number stays interpretable. It is NOT
		// the bootstrap's own method string — that did not cross — and claiming one would be describing
		// a computation this side never performed.
		Method: "reported-by-cli",

		// The customer's own verdict, transmitted. `not-configured` is NOT a pass: a run with no gate
		// must not be ranked as though it cleared one.
		GatePass:    lr.Eval.GateOutcome == runlink.GatePass,
		FailedGates: append([]string(nil), lr.Eval.GateFailures...),

		Provisional: lr.Eval.SingleSeed,
		Flags:       flagsFor(lr),
		// TiedWith deliberately absent — see TieAnalysis.
		// Components deliberately absent — a Normalized of 0 beside a real Raw reads as a measurement.
	}, true
}

// flagsFor renders row states as TEXT chips, because identity is never carried by colour alone.
func flagsFor(lr linkingest.LinkedRun) []string {
	var out []string
	if lr.Eval.SingleSeed {
		out = append(out, "provisional (single seed)")
	}
	if lr.Eval.GateOutcome == runlink.GateNotConfigured {
		// Said out loud on the row. A reader comparing two rows must not have to work out that one
		// cleared a threshold and the other was never held to one.
		out = append(out, "no gate configured")
	}
	return out
}

// notesFor builds the board-level caveats. These change what the WHOLE board means, which is why they
// are notes rather than per-row badges.
func notesFor(v evalboard.View, superseded, missingEvidence int) []string {
	var notes []string
	notes = append(notes,
		"This board is assembled from LINKED runs — evaluations that ran on your machines with your own "+
			"keys. Rows are ordered by reported quality; statistical tie detection did not run, because "+
			"the bootstrap replicates it needs stay on the machine that computed them.")
	if superseded > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d older run(s) were superseded: where a configuration was linked more than once, the newest "+
				"run is shown. Runs are not averaged — combining reported intervals is not a valid interval.",
			superseded))
	}
	if missingEvidence > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d configuration(s) could not be ranked because they were linked before eval evidence was "+
				"recorded. They are listed below rather than dropped.", missingEvidence))
	}
	if len(v.Ranked) == 0 && len(v.Disqualified) > 0 {
		notes = append(notes, "Every measured configuration failed its configured gate — there is no ranked winner.")
	}
	return notes
}

// paretoOf marks the non-dominated configurations on quality/cost/latency.
//
// Dominance is computed on the reported MEANS, which is honest: it is a statement about the numbers the
// customer reported, and it needs no replicates. A variant is dominated when another is at least as good
// on all three and strictly better on one.
func paretoOf(rows []evalboard.Row) []evalboard.ParetoPoint {
	if len(rows) == 0 {
		return nil
	}
	pts := make([]evalboard.ParetoPoint, 0, len(rows))
	for _, r := range rows {
		pts = append(pts, evalboard.ParetoPoint{
			VariantID: r.VariantID, Label: r.Label, Quality: r.Score, NonDominated: true,
		})
	}
	// Cost and latency are not on the row (the board's Row carries them via Components, which this
	// assembler deliberately omits), so dominance here reduces to quality alone — which makes every
	// point except the maximum dominated. Rather than emit a degenerate frontier that looks like an
	// analysis, the frontier is only the maximum-quality set, and the note says the board is
	// quality-ordered.
	best := pts[0].Quality
	for _, p := range pts {
		if p.Quality > best {
			best = p.Quality
		}
	}
	for i := range pts {
		pts[i].NonDominated = pts[i].Quality == best
	}
	return pts
}

func scoreOf(scores []runlink.Score, metric string) (runlink.Score, bool) {
	for _, s := range scores {
		if s.Metric == metric {
			return s, true
		}
	}
	return runlink.Score{}, false
}

// shortHash is the 12-char display prefix. Rendered short, carried full: a board row must stay
// attributable to an exact configuration.
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// Source serves the board from a linked-run store.
type Source struct{ runs RunSource }

// NewSource returns a board source over a linked-run store.
func NewSource(runs RunSource) *Source { return &Source{runs: runs} }

// Board returns the eval board for one tenant's workflow.
//
// ok=false means this tenant has no runs for this workflow. A read failure ALSO returns false, which is a
// real limitation inherited from the api.EvalBoardSource signature rather than introduced here — it has
// nowhere to put an error, so a database outage renders as "no such workflow". Stated rather than
// hidden; widening that interface is a contract change of its own.
// profile is accepted and NOT applied, and the board says so rather than ignoring it silently. A weight
// profile re-ranks by recombining each variant's normalized metric components; this assembler has no
// components (see the header), so there is nothing for a profile to reweight. Rendering a requested
// profile's name over an unchanged ranking would be the most convincing possible lie about this board.
func (s *Source) Board(tenantID, workflowID, profile string) (evalboard.View, bool) {
	runs, err := s.runs.ForWorkflow(tenantID, workflowID)
	if err != nil || len(runs) == 0 {
		return evalboard.View{}, false
	}
	// Spend is the sum of what the linked runs reported. It is the customer's OWN provider spend, with
	// no markup — the platform neither resells nor reprices these tokens.
	view := Build(workflowID, runs)
	view.Spend = spendOf(runs)
	if profile != "" {
		view.Notes = append(view.Notes, fmt.Sprintf(
			"Profile %q was requested but not applied: re-ranking by weight profile needs each variant's "+
				"normalized metric components, which are computed where the eval runs and do not cross. "+
				"The ordering below is by reported quality.", profile))
	}
	return view, true
}

// spendOf totals reported cost across the linked runs.
func spendOf(runs []linkingest.LinkedRun) evalrun.SpendReport {
	var total float64
	for _, lr := range runs {
		if c, ok := scoreOf(lr.Scores, "cost_usd"); ok {
			total += c.Value
		}
	}
	return evalrun.SpendReport{TotalUSD: total}
}
