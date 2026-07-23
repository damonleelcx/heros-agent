package diagnosis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// analyst.go is the LLM-as-analyst half of §6. The analyst runs ONLY on the residue the rules did not
// explain, is constrained to the fixed taxonomy + a confidence, has any off-taxonomy label rejected
// (not recorded), and carries its calibration agreement on every diagnosis it emits. No single
// unverified analyst opinion drives a change — there is no apply path here; the constraint binds P5.5.

// DefaultLowConfidence is the confidence below which an analyst diagnosis is marked low-confidence on
// the scorecard. A low-confidence opinion is shown, but visibly, so it cannot masquerade as a fact.
const DefaultLowConfidence = 0.5

// Rubric is the structured instruction handed to the analyst for one node. Codes is the taxonomy
// SUBSET the node's pattern admits — the analyst is bounded to the relevant slice (Decision 5),
// which both cuts cost and stops the analyst inventing a mode the node's pattern cannot exhibit.
type Rubric struct {
	NodeID  string                    `json:"node_id"`
	Pattern patternclassifier.Pattern `json:"pattern"`
	Codes   []TaxonomyCode            `json:"codes"`
}

// BuildRubric constrains the analyst to the codes the node's pattern admits.
func BuildRubric(nodeID string, pattern patternclassifier.Pattern) Rubric {
	return Rubric{NodeID: nodeID, Pattern: pattern, Codes: AdmissibleCodes(pattern)}
}

// admits reports whether a code is offered by this rubric.
func (r Rubric) admits(c TaxonomyCode) bool {
	for _, code := range r.Codes {
		if code == c {
			return true
		}
	}
	return false
}

// AnalystResponse is what the LLM analyst returns for one case: a raw code string (which MAY be
// off-taxonomy — validation is the engine's job, not the model's) and a confidence.
type AnalystResponse struct {
	Code       string  `json:"code"`
	Confidence float64 `json:"confidence"`
}

// Analyst is the LLM seam. It is an interface so this package depends on no provider — the real
// implementation renders the rubric into a prompt and calls the gateway (metered, spend-capped, §9);
// tests stub it. It CANNOT return a mutation: its only output is a proposed taxonomy code.
type Analyst interface {
	Analyze(ctx context.Context, fc attribution.FailingCase, rubric Rubric) (AnalystResponse, error)
}

// AnalystReject records a residue case whose analyst response was rejected as off-taxonomy, with the
// offending label — logged for calibration, never surfaced as a diagnosis (task 6.2).
type AnalystReject struct {
	CaseID   string `json:"case_id"`
	NodeID   string `json:"node_id"`
	RawLabel string `json:"raw_label"`
	Reason   string `json:"reason"`
}

// Analyze invokes the analyst on EXACTLY the residue cases and returns the accepted analyst causes
// plus the rejects (task 6.1, 6.2). Each accepted cause is constrained to the fixed taxonomy and the
// node's admissible subset; an off-taxonomy or inadmissible label is rejected. The analyst is targeted
// at the case's first-divergence node where one exists, else the terminal node that produced the
// wrong output — never guessed at random.
//
// It returns raw analyst confidence on each cause; the low-confidence THRESHOLD is applied later, in
// Diagnose, when the frozen Diagnosis record is built — so Analyze does not take a threshold.
func Analyze(ctx context.Context, analyst Analyst, ir *discovery.IR, residue []attribution.FailingCase) ([]TypedCause, []AnalystReject, error) {
	var causes []TypedCause
	var rejects []AnalystReject
	for _, fc := range residue {
		node := analystTarget(ir, fc)
		pattern, _ := evalharness.NodePattern(ir, node)
		rubric := BuildRubric(node, pattern)

		resp, err := analyst.Analyze(ctx, fc, rubric)
		if err != nil {
			return nil, nil, err
		}
		code := TaxonomyCode(resp.Code)
		switch {
		case !ValidCode(code):
			rejects = append(rejects, AnalystReject{fc.Case.CaseID, node, resp.Code, "off-taxonomy label"})
			continue
		case !rubric.admits(code):
			// A valid code but inadmissible on this node's pattern — the same refusal §7 applies to
			// rules. Recording it would diagnose a node with a mode its pattern cannot exhibit.
			rejects = append(rejects, AnalystReject{fc.Case.CaseID, node, resp.Code, "code inadmissible on node pattern"})
			continue
		}
		conf := resp.Confidence
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}
		causes = append(causes, TypedCause{
			Code:       code,
			NodeID:     node,
			CaseID:     fc.Case.CaseID,
			Pattern:    pattern,
			Source:     SourceAnalyst,
			Confidence: conf,
			Evidence:   []string{fc.Case.CaseID},
		})
	}
	return causes, rejects, nil
}

// analystTarget picks the node the analyst diagnoses for a residue case: the first-divergence node if
// one is localizable, otherwise the terminal (last-executed) node that produced the wrong end-to-end
// output. Deterministic, so the analyst is always pointed at the same node for the same trace.
func analystTarget(ir *discovery.IR, fc attribution.FailingCase) string {
	if node, kind := attribution.FirstDivergence(ir, fc); node != "" && kind != attribution.DivergenceNone {
		return node
	}
	order := executionOrderOf(fc)
	if len(order) > 0 {
		return order[len(order)-1]
	}
	return ""
}

// executionOrderOf returns the node ids of a case in execution (start-time) order. It mirrors the
// attribution engine's ordering so the analyst and the localizer agree on "the last node".
func executionOrderOf(fc attribution.FailingCase) []string {
	type ne struct {
		id    string
		start int64
	}
	first := map[string]int64{}
	for _, sp := range fc.Trace.NodeSpans() {
		id, _ := sp.Attributes[telemetry.AttrNodeID].(string)
		if id == "" {
			id = sp.Name
		}
		s := sp.StartTime.UnixNano()
		if prev, ok := first[id]; !ok || s < prev {
			first[id] = s
		}
	}
	nes := make([]ne, 0, len(first))
	for id, s := range first {
		nes = append(nes, ne{id, s})
	}
	sort.Slice(nes, func(i, j int) bool {
		if nes[i].start != nes[j].start {
			return nes[i].start < nes[j].start
		}
		return nes[i].id < nes[j].id
	})
	out := make([]string, 0, len(nes))
	for _, n := range nes {
		out = append(out, n.id)
	}
	return out
}

// Conflict records a rule/analyst disagreement on the same {node, case}: the rule's code and the
// analyst's, kept for calibration. The rule wins (task 6.6); this logs what the analyst said so the
// disagreement can be reviewed, never applied.
type Conflict struct {
	NodeID      string       `json:"node_id"`
	CaseID      string       `json:"case_id"`
	RuleCode    TaxonomyCode `json:"rule_code"`
	AnalystCode TaxonomyCode `json:"analyst_code"`
}

// Resolve merges rule and analyst causes with the deterministic rule winning any {node, case}
// conflict (task 6.6). Because the analyst runs only on the residue, a conflict is rare — but the rule
// is enforced here regardless, and the analyst's disagreement is returned for calibration rather than
// silently dropped.
func Resolve(ruleCauses, analystCauses []TypedCause) (kept []TypedCause, conflicts []Conflict) {
	ruleByKey := map[string]TypedCause{}
	for _, c := range ruleCauses {
		ruleByKey[c.NodeID+"\x00"+c.CaseID] = c
		kept = append(kept, c)
	}
	for _, a := range analystCauses {
		key := a.NodeID + "\x00" + a.CaseID
		if r, clash := ruleByKey[key]; clash {
			if r.Code != a.Code {
				conflicts = append(conflicts, Conflict{a.NodeID, a.CaseID, r.Code, a.Code})
			}
			continue // rule wins; analyst cause dropped
		}
		kept = append(kept, a)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].CaseID != kept[j].CaseID {
			return kept[i].CaseID < kept[j].CaseID
		}
		if kept[i].NodeID != kept[j].NodeID {
			return kept[i].NodeID < kept[j].NodeID
		}
		return kept[i].Code < kept[j].Code
	})
	return kept, conflicts
}

// Diagnosis is the FROZEN diagnosis record (task 8.3 / design Q7): the exact contract P5.5's operators
// consume. It is the taxonomy code + node + evidence case ids + confidence + agreement, plus the
// provenance a human needs to weigh it. Nothing in this record is a proposal or a mutation.
type Diagnosis struct {
	DiagID          string       `json:"diag_id"`
	VariantID       string       `json:"variant_id"`
	EvalSetHash     string       `json:"eval_set_hash"`
	ConfigHash      string       `json:"config_hash"`
	NodeID          string       `json:"node_id"`
	TaxonomyCode    TaxonomyCode `json:"taxonomy_code"`
	TaxonomyVersion string       `json:"taxonomy_version"`
	Source          Source       `json:"source"`
	Confidence      float64      `json:"confidence"`
	EvidenceCaseIDs []string     `json:"evidence_case_ids"`

	// Analyst-only trust fields, reported ALONGSIDE every analyst diagnosis (task 6.4). For a rule
	// diagnosis they are zero/false — a rule needs no calibration, it is reproducible.
	Agreement      float64 `json:"agreement"`
	NHuman         int     `json:"n_human"`
	Calibrated     bool    `json:"calibrated"`
	AnalystFlagged bool    `json:"analyst_flagged"` // uncalibrated or below-floor
	LowConfidence  bool    `json:"low_confidence"`
}

// Diagnose is the whole §6 orchestration: rules first on every case, analyst on the residue only,
// conflict resolution (rule wins), and a frozen Diagnosis record per surviving cause — analyst records
// carrying the calibration agreement and the flags that let a human judge them (tasks 6.1–6.6).
//
// It emits reports only. There is no return value, and no argument, that mutates a Variant Spec, a
// registry, or a config — the read-only guarantee is a property of the signature.
func Diagnose(ctx context.Context, analyst Analyst, ir *discovery.IR, v attribution.Variant, cases []attribution.FailingCase, cal AnalystCalibration, lowConfidence float64) ([]Diagnosis, []AnalystReject, []Conflict, error) {
	if lowConfidence <= 0 {
		lowConfidence = DefaultLowConfidence
	}
	ruleCauses, residue := DetectSet(ir, cases)

	var analystCauses []TypedCause
	var rejects []AnalystReject
	if analyst != nil && len(residue) > 0 {
		var err error
		analystCauses, rejects, err = Analyze(ctx, analyst, ir, residue)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	kept, conflicts := Resolve(ruleCauses, analystCauses)

	diags := make([]Diagnosis, 0, len(kept))
	for _, c := range kept {
		d := Diagnosis{
			DiagID:          diagID(v, c),
			VariantID:       v.VariantID,
			EvalSetHash:     v.EvalSetHash,
			ConfigHash:      v.ConfigHash,
			NodeID:          c.NodeID,
			TaxonomyCode:    c.Code,
			TaxonomyVersion: TaxonomyVersion,
			Source:          c.Source,
			Confidence:      c.Confidence,
			EvidenceCaseIDs: c.Evidence,
		}
		if c.Source == SourceAnalyst {
			d.Agreement = cal.Agreement
			d.NHuman = cal.NHuman
			d.Calibrated = cal.Calibrated
			d.AnalystFlagged = cal.BelowFloor()
			d.LowConfidence = c.Confidence < lowConfidence
		}
		diags = append(diags, d)
	}
	return diags, rejects, conflicts, nil
}

func diagID(v attribution.Variant, c TypedCause) string {
	h := sha256.New()
	h.Write([]byte("heros.p45.diag.v1"))
	for _, p := range []string{v.VariantID, v.EvalSetHash, c.NodeID, c.CaseID, string(c.Code), string(c.Source)} {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
