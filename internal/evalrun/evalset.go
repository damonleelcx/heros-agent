// Package evalrun is P4's run fan-out and data path: it turns "measure this variant on this eval
// set for N seeds" into queued, idempotent, metered, fully-tagged work.
//
// Four properties are structural here rather than operational habits:
//
//   - Every unit of work has a CONTENT-DERIVED run id, so a redelivered queue item re-executes the
//     same run instead of billing a second one (it inherits P2's idempotency rather than inventing a
//     second scheme).
//   - Every persisted result carries the full P0 seven-tag set, so every leaderboard slice — per
//     pattern, per failure cluster, per node, per seed — is a QUERY, not a re-run.
//   - The eval set is content-hashed, so a leaderboard is attributable to an exact
//     {eval_set_hash, config_hash} pair years later.
//   - Judge and generation spend is metered and CAPPED per eval run, so measuring a workflow cannot
//     silently blow a budget.
package evalrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// EvalSetVersion is the version of the hashing scheme. It is domain-separated and versioned for the
// same reason executor.RunIDFor is: a future change to what identifies an eval set must be a visible,
// greppable break, not a silent re-mapping of existing ids.
const EvalSetVersion = "heros.evalset.v1"

// EvalSet is a versioned, content-addressed collection of cases.
type EvalSet struct {
	// Hash is the content address. Every leaderboard cites it.
	Hash string `json:"eval_set_hash"`
	// Version is the schema version of the set itself, bumped when the case shape changes.
	Version    string             `json:"version"`
	WorkflowID string             `json:"workflow_id"`
	IRRef      string             `json:"ir_ref"`
	Thresholds json.RawMessage    `json:"thresholds,omitempty"`
	Difficulty float64            `json:"difficulty"`
	Diversity  float64            `json:"diversity"`
	Cases      []evalharness.Case `json:"cases"`
}

// NewEvalSet builds a content-addressed eval set from its cases.
//
// The hash covers the CASES — their ids, inputs, references, labels and taxonomy slots — and not
// the incidental metadata around them. That is the property that matters: two boards citing the same
// eval_set_hash were measured on demonstrably the same questions, and a set whose reference for one
// case was quietly edited gets a different hash rather than silently invalidating every comparison
// made against the old one.
func NewEvalSet(workflowID, irRef string, cases []evalharness.Case) (EvalSet, error) {
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			return EvalSet{}, err
		}
	}
	ordered := append([]evalharness.Case(nil), cases...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CaseID < ordered[j].CaseID })

	h, err := hashCases(workflowID, ordered)
	if err != nil {
		return EvalSet{}, err
	}
	return EvalSet{
		Hash:       h,
		Version:    EvalSetVersion,
		WorkflowID: workflowID,
		IRRef:      irRef,
		Cases:      ordered,
	}, nil
}

// hashCases computes the content address over a canonicalized projection of each case.
func hashCases(workflowID string, cases []evalharness.Case) (string, error) {
	type projection struct {
		CaseID       string                     `json:"case_id"`
		Input        json.RawMessage            `json:"input"`
		Reference    json.RawMessage            `json:"reference,omitempty"`
		OutputSchema json.RawMessage            `json:"output_schema,omitempty"`
		Pattern      string                     `json:"pattern,omitempty"`
		Rubric       string                     `json:"rubric,omitempty"`
		Label        evalharness.ReferenceLabel `json:"label"`
		EdgeCase     evalharness.EdgeCaseKind   `json:"edge_case,omitempty"`
	}
	proj := make([]projection, 0, len(cases))
	for _, c := range cases {
		proj = append(proj, projection{
			CaseID: c.CaseID, Input: c.Input, Reference: c.Reference,
			OutputSchema: c.OutputSchema, Pattern: c.Pattern, Rubric: c.Rubric,
			Label: c.Label, EdgeCase: c.EdgeCase,
		})
	}
	body, err := json.Marshal(struct {
		Version    string       `json:"version"`
		WorkflowID string       `json:"workflow_id"`
		Cases      []projection `json:"cases"`
	}{EvalSetVersion, workflowID, proj})
	if err != nil {
		return "", err
	}
	return confighash.SumBytes(body)
}

// CaseByID returns a case by id.
func (s EvalSet) CaseByID(id string) (evalharness.Case, bool) {
	for _, c := range s.Cases {
		if c.CaseID == id {
			return c, true
		}
	}
	return evalharness.Case{}, false
}

// WeakCaseIDs returns the ids of the weak-labeled cases in the set.
func (s EvalSet) WeakCaseIDs() []string {
	var out []string
	for _, c := range s.Cases {
		if c.Label == evalharness.LabelWeak {
			out = append(out, c.CaseID)
		}
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Run identity
// ─────────────────────────────────────────────────────────────────────────────

// RunIDVersion is the domain separator for an eval run's id.
const RunIDVersion = "heros.evalrun.v1"

// RunIDFor derives the run id for one unit of eval work.
//
// It mirrors executor.RunIDFor exactly, with the CASE added to the key, and for the same reasons:
//
//   - Content-derived, so a redelivered queue item collapses onto the run that already exists rather
//     than minting a second one. A minted id would make every redelivery a new run, and every one of
//     them would be billed.
//   - seed is IN the key, because a different seed is a genuinely different experiment — it is how
//     variance gets measured — and must get its own run.
//   - case_id is IN the key for the same reason: the same configuration on a different question is
//     different work.
func RunIDFor(configHash, sourceRevision, caseID string, seed int64) string {
	h := sha256.Sum256([]byte(RunIDVersion + "\n" + configHash + "\n" + sourceRevision + "\n" +
		caseID + "\n" + strconv.FormatInt(seed, 10)))
	return "evr_" + hex.EncodeToString(h[:])[:24]
}

// Unit is one queued piece of eval work: one variant, one case, one seed.
type Unit struct {
	RunID          string `json:"run_id"`
	VariantID      string `json:"variant_id"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
	CaseID         string `json:"case_id"`
	Seed           int64  `json:"seed"`
	// Sandboxed is true when this case must execute in the P3 sandbox with no ambient credentials.
	Sandboxed bool `json:"sandboxed"`
}

// PlanUnits enumerates the fan-out for one variant over one eval set and a seed list.
//
// The plan is a pure function of {config_hash, source_revision, eval set, seeds}: planning twice
// yields byte-identical run ids, which is what makes re-planning after a crash safe rather than a
// way to double the bill.
func PlanUnits(variantID, configHash, sourceRevision string, set EvalSet, seeds []int64) []Unit {
	out := make([]Unit, 0, len(set.Cases)*len(seeds))
	for _, c := range set.Cases {
		for _, seed := range seeds {
			out = append(out, Unit{
				RunID:          RunIDFor(configHash, sourceRevision, c.CaseID, seed),
				VariantID:      variantID,
				ConfigHash:     configHash,
				SourceRevision: sourceRevision,
				CaseID:         c.CaseID,
				Seed:           seed,
				Sandboxed:      RequiresSandbox(c),
			})
		}
	}
	return out
}

// RequiresSandbox reports whether a case must run in the P3 sandbox with no ambient credentials.
//
// Adversarial and injection cases MUST: running a prompt-injection probe on a host that still holds
// provider credentials is not a robustness test, it is a live attack against production secrets with
// the safety catch off. Malformed and empty inputs ride along because they are the cases most likely
// to drive a workflow into an unplanned code path.
func RequiresSandbox(c evalharness.Case) bool {
	switch c.EdgeCase {
	case evalharness.EdgeCaseAdversarial, evalharness.EdgeCaseMalformedInput, evalharness.EdgeCaseEmptyInput:
		return true
	default:
		return c.Origin == evalharness.OriginAdversarial
	}
}

// Validate rejects an eval set whose hash does not match its contents — the same re-verification
// discipline the content-addressed registries apply on every read. A stored set whose bytes were
// edited underneath its hash must fail loudly rather than serve a comparison nobody can reproduce.
func (s EvalSet) Validate() error {
	want, err := hashCases(s.WorkflowID, s.Cases)
	if err != nil {
		return err
	}
	if want != s.Hash {
		return fmt.Errorf("evalrun: eval set %s does not match its contents (recomputed %s): "+
			"the stored cases were edited underneath the hash", s.Hash, want)
	}
	return nil
}
