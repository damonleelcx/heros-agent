package runlink

import (
	"encoding/json"
	"strings"
	"testing"
)

// verdict_egress_test.go is egress_test.go's argument applied to the third payload. The two fields this
// payload refuses are real fields of verification.Verdict, not synthetic canaries, so these tests fail
// the moment somebody "completes" the mapping by copying them across.

func sampleVerdictRecord() VerdictRecord {
	return VerdictRecord{
		ProposalID:     "prop-abc",
		ConfigHash:     strings.Repeat("a", 64),
		DiffHash:       strings.Repeat("b", 64),
		SourceRevision: "deadbeef",
		Metric:         "quality",
		Delta:          0.06, CILow: 0.02, CIHigh: 0.10,
		Significant: true, HeldOut: true,
		CostDelta: -0.004, LatencyDelta: -120,
		RegressionPass: true, GateResult: "pass",
		// Case ids that look like what a customer actually names cases, because that is the risk: an id
		// is a place to write a sentence, and people write sentences.
		CasesFixed:  []string{"case_ceo_comp_2025_q3", "case_patient_intake_ssn"},
		CasesBroken: []string{"case_internal_pricing_sheet"},
		Reason:      "held-out delta +0.06 on 'summarise the attached severance agreement for ACME/Jane Doe'",
	}
}

// The one that matters. Both refused fields carry content-shaped values here.
func TestVerdictCaseIdsAndReasonDoNotCross(t *testing.T) {
	b, err := json.Marshal(BuildVerdict(sampleVerdictRecord()))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(b)
	for _, leak := range []string{
		"case_ceo_comp_2025_q3", "case_patient_intake_ssn", "case_internal_pricing_sheet",
		"cases_fixed", "cases_broken", "reason", "severance", "Jane Doe",
	} {
		// `cases_fixed_count` legitimately contains "cases_fixed"; check the refused key exactly.
		if leak == "cases_fixed" || leak == "cases_broken" {
			if strings.Contains(wire, `"`+leak+`":[`) || strings.Contains(wire, `"`+leak+`":"`) {
				t.Errorf("the id LIST %q crossed the boundary:\n%s", leak, wire)
			}
			continue
		}
		if strings.Contains(wire, leak) {
			t.Errorf("verdict payload contains %q — the mapping leaked a local field:\n%s", leak, wire)
		}
	}
}

// The counts must cross, including a zero one. An absent `cases_broken_count` renders as "broke
// nothing", which is a claim the platform would be making rather than reporting.
func TestVerdictCountsCrossIncludingZero(t *testing.T) {
	rec := sampleVerdictRecord()
	p := BuildVerdict(rec)
	if p.CasesFixedCount != 2 || p.CasesBrokenCount != 1 {
		t.Errorf("counts did not cross: fixed=%d broken=%d", p.CasesFixedCount, p.CasesBrokenCount)
	}

	rec.CasesBroken = nil
	b, err := json.Marshal(BuildVerdict(rec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"cases_broken_count":0`) {
		t.Errorf("a zero broken-count must be transmitted, not omitted:\n%s", b)
	}
}

func TestOnlyAllowlistedVerdictKeysCross(t *testing.T) {
	b, _ := json.Marshal(BuildVerdict(sampleVerdictRecord()))
	offenders, err := AssertVerdictAllowlisted(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("verdict payload carries non-allowlisted keys: %v", offenders)
	}
}

// Every key the payload struct declares must be on the allowlist, and every allowlisted key must appear
// on the wire. The first direction catches a field added without ratification; the second catches a
// ratified field the builder forgot, which would be a contract that promises more than it sends.
func TestVerdictPayloadAndAllowlistAgree(t *testing.T) {
	b, _ := json.Marshal(BuildVerdict(sampleVerdictRecord()))
	keys, err := PayloadKeys(b)
	if err != nil {
		t.Fatal(err)
	}
	onWire := map[string]bool{}
	for _, k := range keys {
		onWire[k] = true
	}
	for _, f := range VerdictAllowlist {
		if !onWire[f.Name] {
			t.Errorf("allowlist ratifies %q but BuildVerdict never sends it", f.Name)
		}
	}
}

// The refused fields must stay refused. A widening that admitted one of these would pass every other
// test in this file.
func TestVerdictAllowlistRefusesContent(t *testing.T) {
	for _, forbidden := range []string{
		"cases_fixed", "cases_broken", "case_ids", "reason", "diff", "source_diff",
		"gate_thresholds", "eval.cases", "prompt", "trace",
	} {
		if VerdictPermitted(forbidden) {
			t.Errorf("the verdict allowlist admits %q — content or policy, not measurement", forbidden)
		}
	}
}

// The three contracts version independently. Sharing a constant would mean a change to one payload
// forced a version bump on the other two, and a deployment that accepts run links would start refusing
// them because verdicts moved.
func TestTheThreeContractVersionsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for name, v := range map[string]string{
		"run link":         ContractVersion,
		"workflow ir":      WorkflowIRContractVersion,
		"reported verdict": VerdictContractVersion,
	} {
		if other, dup := seen[v]; dup {
			t.Errorf("%s and %s share the contract version %q", name, other, v)
		}
		seen[v] = name
	}
}
