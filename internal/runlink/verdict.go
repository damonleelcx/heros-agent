package runlink

// verdict.go is the THIRD payload: what the customer's CI reports back after it verifies a proposal.
//
// # Why a verdict has to cross at all
//
// P5.5's guarantee is that nothing unverified surfaces, and the gate that decides it runs the eval
// harness over a held-out split — which needs the eval cases, the traces, and a provider. All three
// live in the customer's environment and none of them crosses this boundary. So a hosted platform can
// generate a proposal and can never measure one. The verdict is the measurement coming back, and it is
// the only way `gate_result = 'pass'` can ever be true of a row in `verdict` without this platform
// inventing it.
//
// The direction matters for reading the rest of this file: the run link transmits what a run DID, and
// this transmits what MEASURING A PROPOSAL FOUND. Both are measurements about the customer's system; the
// same rule applies to both, which is that a quantity crosses and the thing it was measured over
// does not.
//
// # Two fields that were deliberately left out
//
// 🔴 THE CASE IDS. verification.Verdict carries `CasesFixed` and `CasesBroken` as lists of case ids, and
// the console's proposal card renders them. They are NOT on this allowlist; the COUNTS are. A case id is
// customer-authored text — `case_ceo_comp_2025` is a perfectly ordinary thing to name a case — so the
// list is a channel for exactly the content `eval.case_count` was written to keep out ("a count says how
// much evidence there was; it does not carry any of it"). The counterexample worth answering is
// `nodes.symbol` on the opt-in structure payload, which IS customer-authored text and IS permitted:
// there the label is the whole reason the payload exists, because a graph without it is unlabelled dots.
// Here it is not — "this change fixed 4 cases and broke 0" is the entire claim the surface makes, and a
// count says it completely.
//
// 🔴 THE REASON. verification.Verdict.Reason is free text, described in its own doc comment as the
// narration the UI shows. Free text is a hole with a label on it: the field is not wrong today and there
// is nothing in a type that stops the next author from putting a failing case's output in it, because
// "explain why the gate failed" invites exactly that. The platform narrates from the structured fields
// instead (internal/verification/view.go builds its sentence out of gate_result, held_out, delta and the
// counts) — so the narration is DERIVED from what crossed rather than transmitted alongside it, and the
// two can never disagree.
//
// # What this payload cannot do
//
// It cannot create a proposal. `proposal_id` is issued by the platform, which generated the proposal;
// the ingest refuses an id the authenticated tenant does not own (internal/proposalstore.PutVerdict does
// that with a WHERE clause). A verdict for a proposal that does not exist is not a way to write one.

// VerdictPath is the authenticated ingest path a verdict is POSTed to, under PlatformBaseURL. Pinned
// here for the same reason LinkPath and WorkflowIRPath are: the destination of anything leaving a
// customer's machine is a constant a reviewer can find, never a value a flag can move.
//
// 🔴 P29 · FLAT, and it was `"/api/v1/proposals/"` with the proposal id appended. See WorkflowIRPath for
// why a path with a caller-supplied segment is a path that never reaches production. `VerdictPayload`
// already carried `proposal_id`, so the segment was a duplicate of the body.
const VerdictPath = "/api/v1/proposal-verdicts"

// VerdictContractVersion versions this payload independently of the other two. A deployment can accept
// run links and refuse verdicts, and the three move for different reasons.
const VerdictContractVersion = "p55.verdict.v1"

// VerdictAllowlist is the ratified field set for a reported verdict — the same security-review artifact
// Allowlist is, kept separate so a reviewer sees at a glance what verifying adds over linking.
//
// 🔴 Never permitted here, and deliberately not expressible: the eval cases, their inputs, expected
// answers or outputs; CASE IDS (the counts cross instead — see the file header); the gate THRESHOLDS
// that produced the result; the free-text reason; the source diff or any part of it; the transformed
// working copy; provider credentials.
var VerdictAllowlist = []AllowlistField{
	{"proposal_id", "identity", "Which proposal was measured. Platform-issued: it goes back the way it came, and the ingest refuses one the authenticated tenant does not own."},
	{"config_hash", "provenance", "The candidate configuration that was built and run — a determinism anchor, not the config's contents. Already crosses on the run link."},
	{"diff_hash", "provenance", "The content address of the diff that was applied. A hash, and emphatically NOT the diff: `generated diffs` are on the never-permitted list and this is what lets the platform check it measured the change it proposed."},
	{"source_revision", "provenance", "The commit the measurement ran at — a revision id, never the code at it."},
	{"metric", "scores", "Which metric the delta is in (e.g. quality). A metric NAME, already permitted under scores.metric."},
	{"delta", "scores", "The measured change in that metric — a number, not the eval set behind it."},
	{"ci_low", "scores", "Lower bound of the delta's confidence interval. Travels WITH the delta: a number whose interval was left behind reads as a stronger claim than it is."},
	{"ci_high", "scores", "Upper bound of the delta's confidence interval."},
	{"significant", "scores", "Whether the delta cleared the significance bar. A boolean about the number, not the policy behind it."},
	{"held_out", "scores", "Whether the delta was measured on cases the proposal was NOT generated from. Without it the platform cannot tell a generalising result from an overfit one, and would render them identically."},
	{"cost_delta", "metrics", "Change in provider spend — the same quantity metrics.cost already carries, as a difference."},
	{"latency_delta", "metrics", "Change in wall time."},
	{"regression_pass", "scores", "Whether the regression check passed. A verdict, not the cost/latency budget that decided it."},
	{"gate_result", "scores", "The terminal gate verdict: pass | fail_significance | fail_regression | fail_constraint | unrun. The same class of field as eval.gate_outcome — a verdict, never the policy behind it."},
	{"cases_fixed_count", "eval", "HOW MANY cases the change fixed. A count, never the cases and never their ids — see the file header for why the ids are refused where nodes.symbol is permitted."},
	{"cases_broken_count", "eval", "How many cases the change broke. The count that has to cross even when it is zero: an absent one would render as `broke nothing`, which is a claim rather than a silence."},
}

// VerdictPayload is the exact bytes on the wire. Built field by field by BuildVerdict.
type VerdictPayload struct {
	ContractVersion string `json:"contract_version"`

	ProposalID     string `json:"proposal_id"`
	ConfigHash     string `json:"config_hash"`
	DiffHash       string `json:"diff_hash"`
	SourceRevision string `json:"source_revision"`

	Metric string `json:"metric"`
	// Delta and its interval are three flat fields rather than a nested object, matching how the run
	// link carries scores: the interval must be impossible to drop while keeping the number.
	Delta       float64 `json:"delta"`
	CILow       float64 `json:"ci_low"`
	CIHigh      float64 `json:"ci_high"`
	Significant bool    `json:"significant"`
	HeldOut     bool    `json:"held_out"`

	CostDelta    float64 `json:"cost_delta"`
	LatencyDelta float64 `json:"latency_delta"`

	RegressionPass bool   `json:"regression_pass"`
	GateResult     string `json:"gate_result"`

	// Counts, not ids. See the file header.
	CasesFixedCount  int `json:"cases_fixed_count"`
	CasesBrokenCount int `json:"cases_broken_count"`
}

// VerdictRecord is the LOCAL representation of a verdict, as the machine that measured it holds one.
//
// Unlike RunRecord it is deliberately RICHER than the wire: it carries the case ids and the free-text
// reason, because `verification.Verdict` does and the CLI maps one to the other. That asymmetry is the
// point — it is what gives TestVerdictCaseIdsAndReasonDoNotCross something real to catch, rather than a
// synthetic canary field nobody would ever have populated. The drop happens in exactly one place,
// BuildVerdict, and a reviewer can read the whole of it.
type VerdictRecord struct {
	ProposalID     string
	ConfigHash     string
	DiffHash       string
	SourceRevision string

	Metric      string
	Delta       float64
	CILow       float64
	CIHigh      float64
	Significant bool
	HeldOut     bool

	CostDelta    float64
	LatencyDelta float64

	RegressionPass bool
	GateResult     string

	// 🔴 NEITHER OF THESE CROSSES. They are here because the local verdict has them and the mapping has
	// to be explicit about dropping them; see the file header for why each is refused.
	CasesFixed  []string
	CasesBroken []string
	Reason      string
}

// BuildVerdict constructs the wire payload field by field from a local record. It never marshals
// VerdictRecord — that distinction is the whole of the FR11 guarantee, applied to this payload.
//
// The counts are computed HERE, from the id lists, rather than taken as numbers from the caller. One
// less field for a caller to get wrong, and it makes "we send how many, not which" a property of this
// function rather than an instruction in a doc comment.
func BuildVerdict(rec VerdictRecord) VerdictPayload {
	return VerdictPayload{
		ContractVersion: VerdictContractVersion,
		ProposalID:      rec.ProposalID,
		ConfigHash:      rec.ConfigHash,
		DiffHash:        rec.DiffHash,
		SourceRevision:  rec.SourceRevision,
		Metric:          rec.Metric,
		Delta:           rec.Delta,
		CILow:           rec.CILow,
		CIHigh:          rec.CIHigh,
		Significant:     rec.Significant,
		HeldOut:         rec.HeldOut,
		CostDelta:       rec.CostDelta,
		LatencyDelta:    rec.LatencyDelta,
		RegressionPass:  rec.RegressionPass,
		GateResult:      rec.GateResult,
		// len(), not the lists. The one line this file exists for.
		CasesFixedCount:  len(rec.CasesFixed),
		CasesBrokenCount: len(rec.CasesBroken),
	}
}

// AssertVerdictAllowlisted returns the keys in a marshaled verdict payload that are NOT permitted. The
// egress test walks the actual bytes with it, exactly as it does for the run link.
func AssertVerdictAllowlisted(payloadJSON []byte) ([]string, error) {
	keys, err := PayloadKeys(payloadJSON)
	if err != nil {
		return nil, err
	}
	var offenders []string
	for _, k := range keys {
		if k == "contract_version" || VerdictPermitted(k) {
			continue
		}
		offenders = append(offenders, k)
	}
	return offenders, nil
}

// VerdictAllowlistKeys returns the permitted wire keys for a reported verdict.
func VerdictAllowlistKeys() []string {
	out := make([]string, 0, len(VerdictAllowlist))
	for _, f := range VerdictAllowlist {
		out = append(out, f.Name)
	}
	return out
}

// VerdictPermitted reports whether a dotted wire key is on the verdict allowlist.
func VerdictPermitted(key string) bool {
	for _, f := range VerdictAllowlist {
		if f.Name == key {
			return true
		}
	}
	return false
}
