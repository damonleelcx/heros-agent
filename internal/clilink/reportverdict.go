package clilink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/verdictrecord"
)

// reportverdict.go implements `heros report-verdict` — the command a customer's CI runs after it has
// verified a proposal, to report the measurement back.
//
// # Why this is a command and not a flag on `link`
//
// A run link says what a run DID. This says what MEASURING A PROPOSED CHANGE FOUND, and the two happen
// at different times in different jobs: linking follows an eval, reporting follows a verification of a
// specific proposal the platform issued. They also carry different contracts, versioned separately. The
// deciding reason is the same one that kept `push-source` separate: a reviewer must be able to point at
// the one entry point that sends a given thing.
//
// # What it drops, in one visible place
//
// The input is a `verdictrecord.Verdict` — the local, complete record, with case ids and a free-text
// reason. Neither crosses. The drop happens in runlink.BuildVerdict, and `--dry-run` renders the exact
// bytes so a customer can see for themselves that it did, before anything is transmitted.

// VerdictData is the machine-output payload for `report-verdict`.
type VerdictData struct {
	ProposalID string                 `json:"proposal_id"`
	DryRun     bool                   `json:"dry_run"`
	Endpoint   string                 `json:"endpoint"`
	Payload    runlink.VerdictPayload `json:"payload"`
	// Withheld names what was read out of the local verdict and deliberately NOT sent. It is reported
	// rather than merely omitted: a customer auditing this command should see the fields it decided to
	// keep, not have to infer them from what is absent.
	Withheld VerdictWithheld `json:"withheld"`
	Accepted bool            `json:"accepted,omitempty"`
}

// VerdictWithheld is the account of what stayed on this machine.
type VerdictWithheld struct {
	CaseIDsFixed  int  `json:"case_ids_fixed"`
	CaseIDsBroken int  `json:"case_ids_broken"`
	Reason        bool `json:"reason"`
}

// ReportVerdict reads a measured verdict and transmits the allowlisted view of it.
func (c Commands) ReportVerdict(cfg cli.Config, s cli.Streams) error {
	proposalID, err := cfg.Require("proposal")
	if err != nil {
		return err
	}
	from, err := cfg.Require("from")
	if err != nil {
		return err
	}

	local, err := readVerdict(from)
	if err != nil {
		return err
	}

	// 🔴 The proposal id comes from the FLAG, not from the file. A verdict file is produced by the
	// customer's own verification step and can name any proposal; the flag is what the CI job was told
	// to report on. Where they disagree the command REFUSES rather than picking one — attaching a
	// measurement to the wrong change is how an unrelated proposal acquires a passing gate.
	if local.ProposalID != "" && local.ProposalID != proposalID {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: fmt.Sprintf(
			"report-verdict: --proposal is %s but %s measures %s — refusing rather than choosing one",
			proposalID, from, local.ProposalID)}
	}

	payload := runlink.BuildVerdict(runlink.VerdictRecord{
		ProposalID:     proposalID,
		ConfigHash:     local.ConfigHash,
		DiffHash:       local.DiffHash,
		SourceRevision: cfg.Get("revision"),
		Metric:         local.Metric,
		Delta:          local.Delta.Mean,
		CILow:          local.Delta.Low,
		CIHigh:         local.Delta.High,
		Significant:    local.Significant,
		HeldOut:        local.HeldOut,
		CostDelta:      local.CostDelta,
		LatencyDelta:   local.LatencyDelta,
		RegressionPass: local.RegressionPass,
		GateResult:     string(local.GateResult),
		CasesFixed:     local.CasesFixed,
		CasesBroken:    local.CasesBroken,
		Reason:         local.Reason,
	})

	// Defense in depth, exactly as `link` does it: whatever is about to be rendered or sent must contain
	// only allowlisted keys, asserted at the boundary itself rather than only in CI.
	if offenders, aerr := verdictAllowlistCheck(payload); aerr != nil {
		return &cli.ExitError{Code: cli.ExitOperational, Msg: "report-verdict: payload self-check failed: " + aerr.Error()}
	} else if len(offenders) > 0 {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "report-verdict: REFUSING to transmit — payload carries non-allowlisted keys: " + strings.Join(offenders, ", ")}
	}

	withheld := VerdictWithheld{
		CaseIDsFixed:  len(local.CasesFixed),
		CaseIDsBroken: len(local.CasesBroken),
		Reason:        local.Reason != "",
	}
	if n := withheld.CaseIDsFixed + withheld.CaseIDsBroken; n > 0 || withheld.Reason {
		s.Narratef("report-verdict: %d case id(s) and the free-text reason stay on this machine; the "+
			"counts (%d fixed, %d broken) and the measurements are what cross.",
			n, payload.CasesFixedCount, payload.CasesBrokenCount)
	}

	data := VerdictData{
		ProposalID: proposalID, Endpoint: runlink.PlatformBaseURL,
		Payload: payload, Withheld: withheld,
	}

	if cfg.Get("dry-run") == "true" {
		s.Narratef("report-verdict: dry-run — rendering the exact payload for %s; nothing is transmitted", proposalID)
		data.DryRun = true
		return s.EmitJSON("report-verdict", cli.ExitOK, data, nil, nil)
	}

	cred, ok := cli.LoadCredential()
	if !ok {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "report-verdict: authentication required — run `heros login` first (nothing was transmitted)"}
	}

	s.Narratef("report-verdict: transmitting the verdict for %s to %s as %s…",
		proposalID, runlink.PlatformBaseURL, cred.Identity)
	res, err := c.client(cred.Token).ReportVerdict(context.Background(), payload)
	if err != nil {
		// A failed report never invalidates the local verdict, exactly as a failed link never invalidates
		// the local run: the measurement happened, and the file is still the record of it.
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: err.Error() + " (the local verdict in " + from + " is unaffected)"}
	}
	data.Accepted = res.Accepted
	s.Narratef("report-verdict: recorded — gate result %s", res.GateResult)
	return s.EmitJSON("report-verdict", cli.ExitOK, data, nil, nil)
}

// readVerdict loads a verdictrecord.Verdict from a file, or from stdin when the path is "-".
func readVerdict(path string) (verdictrecord.Verdict, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = readAllStdin()
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return verdictrecord.Verdict{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "report-verdict: cannot read the verdict: " + err.Error()}
	}
	var v verdictrecord.Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return verdictrecord.Verdict{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "report-verdict: " + path + " is not a verification verdict: " + err.Error()}
	}
	if v.GateResult == "" {
		return verdictrecord.Verdict{}, &cli.ExitError{Code: cli.ExitOperational,
			Msg: "report-verdict: " + path + " carries no gate_result — a verdict with no gate result is " +
				"not an unverified proposal, it is an unreadable file, and the platform stores the two differently"}
	}
	// 🔴 The count, not len(). A verdict file written by an older tool has ids and no counts; taking
	// len() would be right there and wrong for a file that carries counts and no ids, and only one of
	// those two shapes can be checked here. Verdict.SetCases keeps them in step at the source; this
	// repairs the older shape explicitly rather than silently preferring one field.
	if v.CasesFixedCount == 0 && len(v.CasesFixed) > 0 {
		v.SetCases(v.CasesFixed, v.CasesBroken)
	}
	return v, nil
}

func readAllStdin() ([]byte, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return nil, fmt.Errorf("no verdict on stdin")
	}
	return io.ReadAll(os.Stdin)
}

// verdictAllowlistCheck marshals the payload and asserts every key is permitted.
func verdictAllowlistCheck(p runlink.VerdictPayload) ([]string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return runlink.AssertVerdictAllowlisted(b)
}
