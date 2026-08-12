package herosagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// rehearsal.go is D7: a published definition is INACTIVE until it runs against the pinned fixtures and
// meets the floor ON EVERY FIXTURE INDIVIDUALLY.
//
// # 🔴 Why the gate reads the MINIMUM and not the mean (task 5.4)
//
// Precision/recall over a fixture set is exactly the aggregate that hides a per-repository catastrophe.
// An agent that is excellent on four languages and connects everything it sees on the fifth passes a
// mean and ships a disaster to one language's customers. The mean is REPORTED, because it is the number
// people quote; the gate reads the minimum, because that is the number that protects somebody.
//
// # Why the near-misses are not optional
//
// A fixture whose true graph is a rich one measures whether the agent can find edges. It cannot measure
// whether the agent DISCRIMINATES, and an agent that emits a complete graph over everything scores well
// on it. The near-misses — a linear chain that is not a router, a fan-out with no merge, two calls with
// no data dependency — are the evidence of discrimination, and an agent that connects whatever is
// nearby scores ZERO PRECISION on a case that exists for exactly that purpose.

// GroundTruth says where a fixture's true edge set came from. It is recorded because the two kinds
// carry different authority, and a report that mixed them without saying so would let a hand-declared
// number be read as a measurement.
type GroundTruth string

const (
	// TruthMeasured: the Go frontend's REAL edge output (task 5.2). The only ground truth the platform
	// already owns, which is why Go fixtures matter even though Go is the language that needs HEROS
	// least.
	TruthMeasured GroundTruth = "go_frontend"
	// TruthDeclared: a human wrote down the true graph. Every non-Go fixture, necessarily — the
	// frontends that read those languages cannot produce edges, which is the whole problem.
	TruthDeclared GroundTruth = "hand_declared"
)

// Fixture is one calibration repository with a known true graph.
type Fixture struct {
	Name     string      `json:"name"`
	Language string      `json:"language"`
	Repo     string      `json:"repo"`
	Truth    GroundTruth `json:"ground_truth"`
	// TrueEdges is the correct edge set, by node id. EMPTY IS A REAL ANSWER and is the point of the
	// near-misses: the correct number of edges over two independent calls is zero.
	TrueEdges []Pair `json:"true_edges"`
	// Note records what this fixture is EVIDENCE FOR, in words. It is printed in a failing report,
	// because "py_fanout_no_merge scored 0.4 precision" tells a reader nothing about what went wrong.
	Note string `json:"note"`
}

// Score is one fixture's result.
type Score struct {
	Fixture   string      `json:"fixture"`
	Language  string      `json:"language"`
	Truth     GroundTruth `json:"ground_truth"`
	Precision float64     `json:"precision"`
	Recall    float64     `json:"recall"`
	// TruePositives / FalsePositives / FalseNegatives are carried so a reader can see the SHAPE of a
	// failure. "Precision 0.5" is ambiguous between one wrong edge out of two and fifty out of a
	// hundred, and those are different problems.
	TruePositives  int `json:"true_positives"`
	FalsePositives int `json:"false_positives"`
	FalseNegatives int `json:"false_negatives"`
	// Vacuous marks a fixture whose true graph is EMPTY. Precision over an empty truth is defined as 1
	// when the agent emitted nothing and 0 when it emitted anything — see scoreEdges — and the flag is
	// carried so a report never presents "precision 1.0" from a near-miss as evidence of skill.
	Vacuous bool `json:"vacuous"`
	// HeldOutEdges is how many edges were REMOVED from the IR this fixture was shown, so that its own
	// answer was proposable at all — see withholdAnswer. Non-zero only for a fixture whose truth the
	// frontend itself produced. Carried because a report that did not say so would present a held-out
	// measurement as a finding over an untouched graph.
	HeldOutEdges int    `json:"held_out_edges"`
	Note         string `json:"note"`
}

// RehearsalReport is the whole verdict.
type RehearsalReport struct {
	// Floors are the thresholds this run was gated on, recorded so a stored report stays interpretable
	// after they move.
	MinPrecision float64 `json:"min_precision_floor"`
	MinRecall    float64 `json:"min_recall_floor"`
	// Scores are per fixture, in a stable order.
	Scores []Score `json:"scores"`
	// MeanPrecision and MeanRecall are REPORTED. 🚫 Nothing is gated on them.
	MeanPrecision float64 `json:"mean_precision"`
	MeanRecall    float64 `json:"mean_recall"`
	// WorstPrecision and WorstRecall are what the GATE reads.
	WorstPrecision float64 `json:"worst_precision"`
	WorstRecall    float64 `json:"worst_recall"`
	Passed         bool    `json:"passed"`
	// Failures name the failing fixture, its language and its numbers (task 5.5).
	Failures []string `json:"failures"`
}

// JSON renders the report for storage on the version row (task 5.6).
func (r RehearsalReport) JSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("herosagent: encoding the rehearsal report: %w", err)
	}
	return string(b), nil
}

// FixtureLoader supplies the pinned calibration set.
//
// It returns an ERROR rather than an empty slice on failure, and that distinction is task 5.7: a
// fixture set that fails to load must FAIL the rehearsal. An empty set passes every threshold
// vacuously, and a gate that passes when it has nothing to measure is a gate that stops guarding at
// the exact moment its inputs break.
type FixtureLoader interface {
	Fixtures() ([]Fixture, error)
}

// Analyser runs one inference over a fixture's residue. It is the Runner in production and a fake in
// tests; the rehearsal does not care which, only that it produces edges.
type Analyser interface {
	Infer(ctx context.Context, in Input, agentConfigHash string, placement Placement) (Result, error)
}

// Discoverer produces a fixture's rule IR. Injected so the rehearsal can run against real trees
// without this package importing a discovery driver.
type Discoverer func(repo string) (*discovery.IR, discovery.DiscoveryReport, error)

// Rehearsal runs the calibration set and returns a verdict.
type Rehearsal struct {
	loader   FixtureLoader
	discover Discoverer
	analyse  Analyser
	// MinPrecision and MinRecall are the per-fixture floors.
	//
	// 🔴 ASYMMETRIC, and PRD Q4 says why: a MISSING edge degrades to today's behaviour, while a WRONG
	// edge actively misleads eval scope, proposals and cost attribution. The two failures are not
	// equally bad and the thresholds must not pretend they are.
	MinPrecision float64
	MinRecall    float64
}

// Q4's starting floors (design.md, "Open, not blocking").
//
// 🔴 They are a STARTING POINT that measurement must replace, and they are constants here rather than
// defaults buried in a constructor so that changing them is a visible edit. Task 5.9 records the first
// real measurement against them.
const (
	DefaultMinPrecision = 0.90
	DefaultMinRecall    = 0.70
)

// NewRehearsal wires the gate.
func NewRehearsal(l FixtureLoader, d Discoverer, a Analyser, minPrecision, minRecall float64) (*Rehearsal, error) {
	switch {
	case l == nil:
		return nil, fmt.Errorf("%w: a fixture loader is required", ErrInvalidDefinition)
	case d == nil:
		return nil, fmt.Errorf("%w: a discoverer is required", ErrInvalidDefinition)
	case a == nil:
		return nil, fmt.Errorf("%w: an analyser is required", ErrInvalidDefinition)
	case minPrecision <= 0 || minRecall <= 0:
		return nil, fmt.Errorf("%w: both floors must be positive; a zero floor passes everything and is "+
			"a gate that reports success for an agent nothing measured", ErrInvalidDefinition)
	}
	return &Rehearsal{loader: l, discover: d, analyse: a,
		MinPrecision: minPrecision, MinRecall: minRecall}, nil
}

// Run executes the calibration set.
func (r *Rehearsal) Run(ctx context.Context, agentConfigHash string) (RehearsalReport, error) {
	rep := RehearsalReport{
		MinPrecision: r.MinPrecision, MinRecall: r.MinRecall,
		Scores: []Score{}, Failures: []string{},
	}

	fixtures, err := r.loader.Fixtures()
	if err != nil {
		// 🔴 TASK 5.7 — ANTI-VACUITY. A load failure FAILS the rehearsal; it never passes over what it
		// could not read.
		return rep, fmt.Errorf("herosagent: the calibration set could not be loaded, so this rehearsal "+
			"measured nothing and CANNOT pass: %w", err)
	}
	if len(fixtures) == 0 {
		return rep, fmt.Errorf("herosagent: the calibration set is EMPTY. An empty set passes every " +
			"threshold vacuously, which is a gate that reports success for an agent nothing measured")
	}

	for _, f := range fixtures {
		ir, report, derr := r.discover(f.Repo)
		if derr != nil {
			return rep, fmt.Errorf("herosagent: fixture %q (%s) could not be discovered, so it measured "+
				"nothing: %w", f.Name, f.Language, derr)
		}
		// 🔴 THE ANSWER IS HELD OUT. See withholdAnswer — without this the measured fixture asks for two
		// edges the residue is not allowed to offer.
		shown, held := withholdAnswer(ir, f.TrueEdges)
		residue := SelectResidue(shown, report, nil)
		if missing := unproposable(shown, residue, f.TrueEdges); len(missing) > 0 {
			return rep, fmt.Errorf("herosagent: fixture %q (%s) is UNMEASURABLE: %d of its %d true edges "+
				"are not in the residue it is scored against (%v). The agent would be asked for an edge it "+
				"is structurally forbidden to propose, so its recall on this fixture is zero whatever it "+
				"answers. That is a defect in the CALIBRATION SET, and a rehearsal that reported the "+
				"number anyway would be blaming the model for the harness",
				f.Name, f.Language, len(missing), len(f.TrueEdges), missing)
		}
		res, aerr := r.analyse.Infer(ctx, Input{
			WorkflowID: f.Name, SourceRevision: "fixture", RuleIR: shown,
			Residue: residue,
			Budget:  Budget{MaxTokens: 100_000, MaxWall: rehearsalWall},
			// A rehearsal is the PLATFORM rehearsing, whatever any tenant's placement says — it runs
			// against pinned fixture repositories, not against a customer, so there is no tenant here
			// whose setting could apply.
		}, agentConfigHash, PlacementPlatform)
		if aerr != nil {
			return rep, fmt.Errorf("herosagent: fixture %q (%s) could not be analysed: %w",
				f.Name, f.Language, aerr)
		}
		sc := scoreEdges(f, res.Edges)
		sc.HeldOutEdges = held
		rep.Scores = append(rep.Scores, sc)
	}

	return r.verdict(rep), nil
}

// withholdAnswer returns the IR the agent is SHOWN — the fixture's own answer removed from it — and how
// many edges that removed.
//
// # 🔴 Why a rehearsal has to ablate, and what the first live run proved
//
// D3's fence 1 is a SELECTION: an edge a frontend established is never offered to the agent, so it
// cannot propose one over it. `go_chain`'s ground truth IS the Go frontend's edges (task 5.2, and the
// only ground truth this platform owns). Those two statements together made that fixture UNANSWERABLE:
// the residue excluded exactly the two edges the answer key demanded, and the first live gate run scored
// it 0.00 precision and 0.00 recall — a number that read as a model failure and was a harness one.
//
// Holding the answer out is what makes the question askable, and it is not a trick played to get a pass:
// it manufactures, on a Go tree, the condition every other language is in permanently — a graph whose
// dependencies the frontend did not resolve. The ground truth stays MEASURED, because what was removed
// was measured. What the fixture then tests is the product's actual claim: recover a dependency that is
// really there from a graph that does not carry it.
//
// 🚫 REHEARSAL ONLY. On a customer's repository the gap is real and nothing is withheld; there is no
// answer to hold out. `Score.HeldOutEdges` records the count so a stored report can never be read as the
// agent having found edges in an untouched graph.
//
// For every hand-declared fixture this is a no-op — those frontends emit no edges, which is the problem
// HEROS exists for — and the no-op is asserted rather than assumed (TestTheHeldOutAnswerIsProposable).
func withholdAnswer(ir *discovery.IR, answer []Pair) (*discovery.IR, int) {
	if ir == nil || len(answer) == 0 {
		return ir, 0
	}
	held := map[Pair]bool{}
	for _, p := range answer {
		held[p] = true
	}
	shown := *ir
	shown.Edges = make([]discovery.IREdge, 0, len(ir.Edges))
	var n int
	for _, e := range ir.Edges {
		if held[Pair{From: e.FromNodeID, To: e.ToNodeID}] {
			n++
			continue
		}
		shown.Edges = append(shown.Edges, e)
	}
	return &shown, n
}

// unproposable returns the true edges a fixture's own residue cannot express.
//
// 🔴 This is the anti-vacuity rule at the grain the set itself needed. Task 5.7 refuses a set that
// measured nothing; this refuses a FIXTURE that can measure nothing — one whose answer the agent is not
// allowed to give. Both fences check the residue AND the write boundary, because those are two different
// gates and a fixture is only measurable if it clears both.
func unproposable(ir *discovery.IR, res Residue, answer []Pair) []Pair {
	if len(answer) == 0 {
		return nil
	}
	offered := map[Pair]bool{}
	for _, p := range res.Pairs {
		offered[p] = true
	}
	var out []Pair
	for _, p := range answer {
		if !offered[p] || !EdgeIsAvailable(ir, p.From, p.To) {
			out = append(out, p)
		}
	}
	return out
}

// verdict aggregates the per-fixture scores into the gate's answer.
//
// 🔴 Separate from Run so the MINIMUM-not-mean rule is testable WITHOUT a model, a discoverer or a
// fixture tree. The rule is the load-bearing part of D7 and it deserves a test that can state it
// directly: eight perfect fixtures and one catastrophe.
func (r *Rehearsal) verdict(rep RehearsalReport) RehearsalReport {
	sort.SliceStable(rep.Scores, func(i, j int) bool { return rep.Scores[i].Fixture < rep.Scores[j].Fixture })
	if rep.Failures == nil {
		rep.Failures = []string{}
	}
	rep.MinPrecision, rep.MinRecall = r.MinPrecision, r.MinRecall

	rep.WorstPrecision, rep.WorstRecall = 1, 1
	var sumP, sumR float64
	for _, s := range rep.Scores {
		sumP += s.Precision
		sumR += s.Recall
		if s.Precision < rep.WorstPrecision {
			rep.WorstPrecision = s.Precision
		}
		if s.Recall < rep.WorstRecall {
			rep.WorstRecall = s.Recall
		}
		if s.Precision < r.MinPrecision || s.Recall < r.MinRecall {
			// Task 5.5 — the failure NAMES the fixture, its language and its numbers. "The rehearsal
			// failed" sends somebody to read a report; this sends them to a file.
			rep.Failures = append(rep.Failures, fmt.Sprintf(
				"%s (%s): precision %.2f (floor %.2f), recall %.2f (floor %.2f) — %d correct, %d wrong, "+
					"%d missed. This fixture is evidence for: %s",
				s.Fixture, s.Language, s.Precision, r.MinPrecision, s.Recall, r.MinRecall,
				s.TruePositives, s.FalsePositives, s.FalseNegatives, s.Note))
		}
	}
	if n := float64(len(rep.Scores)); n > 0 {
		// REPORTED, and nothing branches on it.
		rep.MeanPrecision, rep.MeanRecall = sumP/n, sumR/n
	}
	// 🔴 THE GATE READS THE MINIMUM, expressed as "no fixture failed" — which is the same statement and
	// is the one that can name WHICH.
	rep.Passed = len(rep.Failures) == 0
	return rep
}

// rehearsalWall bounds one fixture's analysis. Generous — a rehearsal is not on a request path — and
// present because an unbounded run is an unbounded bill however rarely it happens.
const rehearsalWall = 5 * 60 * 1e9 // 5 minutes, as time.Duration nanoseconds

// scoreEdges computes precision and recall on EDGES for one fixture.
//
// # 🔴 The empty-truth case, stated rather than left to a division
//
// A near-miss fixture's true edge set is EMPTY, and precision is undefined there by the usual formula.
// The definition used is the one that makes the fixture do its job: emitting nothing is PERFECT
// (precision 1, recall 1 — the agent correctly found no dependency), and emitting anything at all is
// ZERO PRECISION. That is what "an agent that emits a complete graph over a repository with no
// dependencies scores zero precision on a case that exists for that purpose" means numerically.
//
// The alternative — treating an empty truth as unscoreable and skipping it — would make the
// near-misses invisible to the gate, which is the same as not having them.
func scoreEdges(f Fixture, got []ProvenancedEdge) Score {
	s := Score{Fixture: f.Name, Language: f.Language, Truth: f.Truth, Note: f.Note,
		Vacuous: len(f.TrueEdges) == 0}

	truth := map[Pair]bool{}
	for _, p := range f.TrueEdges {
		truth[p] = true
	}
	seen := map[Pair]bool{}
	for _, e := range got {
		p := Pair{From: e.From, To: e.To}
		if seen[p] {
			continue
		}
		seen[p] = true
		if truth[p] {
			s.TruePositives++
		} else {
			s.FalsePositives++
		}
	}
	for p := range truth {
		if !seen[p] {
			s.FalseNegatives++
		}
	}

	switch {
	case len(truth) == 0 && len(seen) == 0:
		// Correctly found nothing. This is what a near-miss rewards.
		s.Precision, s.Recall = 1, 1
	case len(truth) == 0:
		// Emitted edges over a graph with none. Zero precision; recall is vacuously 1 (there was
		// nothing to miss), and the gate's precision floor is what catches this.
		s.Precision, s.Recall = 0, 1
	default:
		if len(seen) > 0 {
			s.Precision = float64(s.TruePositives) / float64(len(seen))
		}
		s.Recall = float64(s.TruePositives) / float64(len(truth))
	}
	return s
}

// GateActivation runs the rehearsal and records its verdict on the version, refusing activation on
// failure (task 5.5, 5.6).
//
// 🔴 It writes the report whether it passed or failed. A failed rehearsal whose numbers were discarded
// leaves an operator with "it did not pass" and nothing to act on.
func (r *Rehearsal) GateActivation(ctx context.Context, store interface {
	SetRehearsal(ctx context.Context, configHash string, state RehearsalState, report string) error
}, agentConfigHash string) (RehearsalReport, error) {
	rep, err := r.Run(ctx, agentConfigHash)
	if err != nil {
		// The run itself failed — no numbers exist. Recorded as `failed` with the reason, because a
		// version left `pending` after a rehearsal that errored looks like one nobody has run yet.
		_ = store.SetRehearsal(ctx, agentConfigHash, RehearsalFailed, `{"error":`+quote(err.Error())+`}`)
		return rep, err
	}
	js, jerr := rep.JSON()
	if jerr != nil {
		return rep, jerr
	}
	state := RehearsalFailed
	if rep.Passed {
		state = RehearsalPassed
	}
	if serr := store.SetRehearsal(ctx, agentConfigHash, state, js); serr != nil {
		return rep, serr
	}
	if !rep.Passed {
		return rep, fmt.Errorf("%w: %s", ErrRehearsalNotPassed, strings.Join(rep.Failures, "; "))
	}
	return rep, nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
