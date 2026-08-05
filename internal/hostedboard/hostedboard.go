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
		// Set here so no early return can leave it "" — an empty string is not one of the two states,
		// and a UI switching on it would fall through to whichever branch it wrote first. paretoOf
		// overwrites this with what it actually found.
		CostLatency: evalboard.CostLatencyUnavailable,
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
	// Counted here, where the customer's own verdict is still in hand, because the board-level note has
	// to tell "failed a threshold you set" apart from "was never held to one" — and evalboard.Row keeps
	// only the derived GatePass, in which both are false. See notesFor.
	var ungated int
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
		if lr.Eval.GateOutcome == runlink.GateNotConfigured {
			ungated++
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

	v.Pareto, v.CostLatency = paretoOf(v.Ranked, costLatencyByVariant(latest))
	v.Notes = notesFor(v, superseded, missingEvidence, ungated)
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
func notesFor(v evalboard.View, superseded, missingEvidence, ungated int) []string {
	var notes []string
	notes = append(notes,
		"This board is assembled from LINKED runs — evaluations that ran on your machines with your own "+
			"keys. Rows are ordered by reported quality; statistical tie detection did not run, because "+
			"the bootstrap replicates it needs stay on the machine that computed them.")
	// 🔴 The note the old code CLAIMED to emit. paretoOf's comment ended "...and the note says the board
	// is quality-ordered", and no such note was ever appended — the only conditional note in this file
	// was the weight-profile one. So the degenerate frontier shipped with no caveat at all, under an
	// axis labelled in dollars. A mitigation that exists only in the comment describing it is worth
	// less than no mitigation, because it stops the next reader looking.
	if v.CostLatency == evalboard.CostLatencyUnavailable && len(v.Pareto) > 0 {
		notes = append(notes,
			"Cost and latency were not reported for every variant here, so no cost/quality frontier was "+
				"computed. The highlighted points are simply the highest reported quality — not "+
				"\"nothing beats them on both\". Link a run from a CLI that reports cost_usd and latency_ms "+
				"to get the real frontier.")
	}
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
	// 🔴 "Disqualified" covers two different facts and this note used to assert the harsher one for both.
	//
	// A run is disqualified when GatePass is false, and that is false for `fail` AND for
	// `not-configured` — deliberately, because a run nobody held to a threshold must not be ranked as
	// though it cleared one. But the note said "Every measured configuration failed its configured
	// gate", which on a workflow with no gates is simply untrue: it reads as "your quality gates are
	// failing" to a reader who never set one, while the row three lines above says `no gate configured`.
	// One page, two contradictory statements, and the false one is the summary.
	if len(v.Ranked) == 0 && len(v.Disqualified) > 0 {
		switch {
		case ungated == len(v.Disqualified):
			notes = append(notes, "No configuration here has a gate. Nothing failed — a run that was never "+
				"held to a threshold is not ranked as though it cleared one, so there is no ranked winner "+
				"until you configure gates and link a run that reports them.")
		case ungated > 0:
			notes = append(notes, fmt.Sprintf(
				"There is no ranked winner: of %d measured configuration(s), %d failed a gate you set and "+
					"%d were never held to one. Both are excluded from the order, for different reasons.",
				len(v.Disqualified), len(v.Disqualified)-ungated, ungated))
		default:
			notes = append(notes, "Every measured configuration failed its configured gate — there is no ranked winner.")
		}
	}
	return notes
}

// paretoOf marks the non-dominated configurations on quality/cost/latency.
//
// Dominance is computed on the reported MEANS, which is honest: it is a statement about the numbers the
// customer reported, and it needs no replicates. A variant is dominated when another is at least as good
// on all three and strictly better on one.
// costLatency is one variant's reported spend and wall time, and whether both were reported at all.
type costLatency struct {
	cost, latency float64
	present       bool
}

// costLatencyByVariant reads cost_usd and latency_ms off the linked runs, keyed by the variant id
// rowFor uses (the config hash).
//
// 🔴 These scores were on the wire the whole time. `metrics.cost` and `metrics.latency` are allowlisted
// (internal/runlink/allowlist.go), `heros eval` reports `cost_usd` and `latency_ms` beside `quality` in
// the same Scores slice, and spendOf twenty lines below has always read `cost_usd` off this very record
// to total the board's spend. The frontier was not missing data; it was not asked for it.
func costLatencyByVariant(runs []linkingest.LinkedRun) map[string]costLatency {
	out := make(map[string]costLatency, len(runs))
	for _, lr := range runs {
		c, okC := scoreOf(lr.Scores, "cost_usd")
		l, okL := scoreOf(lr.Scores, "latency_ms")
		// BOTH or neither. A point with a cost and no latency cannot be compared on latency, and
		// filling the gap with 0 is the defect this whole change exists to remove.
		out[lr.ConfigHash] = costLatency{cost: c.Value, latency: l.Value, present: okC && okL}
	}
	return out
}

// paretoOf marks the non-dominated configurations, and reports whether cost and latency were measured.
//
// When every plotted variant reported both, dominance is the real multi-objective test on the reported
// MEANS: a variant is dominated when another is at least as good on quality, cost and latency and
// strictly better on one. When any variant reported neither, the comparison is undefined for that point,
// so the frontier reduces to the maximum-quality set — and says so through the returned state rather
// than through zeros the view would render as measurements.
func paretoOf(rows []evalboard.Row, cl map[string]costLatency) ([]evalboard.ParetoPoint, evalboard.CostLatencyAnalysis) {
	if len(rows) == 0 {
		// No points to plot. There is nothing whose cost could have been measured, and claiming
		// "measured" over an empty set would make the UI draw axes for no data.
		return nil, evalboard.CostLatencyUnavailable
	}

	measured := true
	for _, r := range rows {
		if !cl[r.VariantID].present {
			measured = false
			break
		}
	}

	pts := make([]evalboard.ParetoPoint, 0, len(rows))
	for _, r := range rows {
		p := evalboard.ParetoPoint{VariantID: r.VariantID, Label: r.Label, Quality: r.Score, NonDominated: true}
		if measured {
			p.CostUSD = cl[r.VariantID].cost
			p.LatencyMS = cl[r.VariantID].latency
		}
		pts = append(pts, p)
	}

	if !measured {
		// Quality-only, exactly as before — but now the caller carries a state that stops the console
		// drawing a cost axis over the zeros this leaves behind.
		best := pts[0].Quality
		for _, p := range pts {
			if p.Quality > best {
				best = p.Quality
			}
		}
		for i := range pts {
			pts[i].NonDominated = pts[i].Quality == best
		}
		return pts, evalboard.CostLatencyUnavailable
	}

	// Real dominance. Higher quality is better; lower cost and lower latency are better.
	for i := range pts {
		for j := range pts {
			if i == j {
				continue
			}
			a, b := pts[j], pts[i] // does a dominate b?
			atLeastAsGood := a.Quality >= b.Quality && a.CostUSD <= b.CostUSD && a.LatencyMS <= b.LatencyMS
			strictlyBetter := a.Quality > b.Quality || a.CostUSD < b.CostUSD || a.LatencyMS < b.LatencyMS
			if atLeastAsGood && strictlyBetter {
				pts[i].NonDominated = false
				break
			}
		}
	}
	return pts, evalboard.CostLatencyMeasured
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
