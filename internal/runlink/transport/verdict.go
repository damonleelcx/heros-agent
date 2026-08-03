package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// verdict.go transmits a verification verdict the customer's CI measured.
//
// A separate method from Link and SendWorkflowIR for the reason those two are separate from each other:
// three contracts, versioned independently, sent at different moments by different commands. Collapsing
// them behind one call with a mode argument is how a reviewer loses the ability to point at the one
// function that sends a given thing.

// VerdictResult is the platform's response to a reported verdict.
type VerdictResult struct {
	Accepted        bool   `json:"accepted"`
	ProposalID      string `json:"proposal_id"`
	GateResult      string `json:"gate_result"`
	ContractVersion string `json:"contract_version"`
}

// ReportVerdict transmits a measured verdict for one proposal.
//
// The destination is re-asserted here, exactly as it is for a link: the pin is per-request, so a new
// transmit path cannot inherit an unchecked base.
func (c *Client) ReportVerdict(ctx context.Context, p runlink.VerdictPayload) (VerdictResult, error) {
	url := c.base + runlink.VerdictPath + urlPathEscape(p.ProposalID) + "/verdict"
	if err := assertLinkTarget(url); err != nil {
		return VerdictResult{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return VerdictResult{}, fmt.Errorf("verdict: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return VerdictResult{}, fmt.Errorf("verdict: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Heros-Contract", runlink.VerdictContractVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return VerdictResult{}, fmt.Errorf("verdict: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var r VerdictResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return VerdictResult{}, fmt.Errorf("verdict: decode response: %w", err)
		}
		return r, nil
	case http.StatusUnauthorized:
		return VerdictResult{}, fmt.Errorf("verdict: authentication rejected — run `heros login`")
	case http.StatusNotFound:
		// The platform answers 404 for both "no such proposal" and "not yours", deliberately. Repeating
		// that ambiguity here rather than guessing which it was: a CLI that said "this proposal belongs
		// to another tenant" would be re-deriving the fact the server declined to disclose.
		return VerdictResult{}, fmt.Errorf(
			"verdict: the platform has no proposal %s for this identity — check the id, and that the "+
				"token belongs to the tenant the proposal was generated for", p.ProposalID)
	case http.StatusUpgradeRequired:
		return VerdictResult{}, fmt.Errorf(
			"verdict: contract mismatch — this CLI sends %s and the platform does not accept it; "+
				"run `heros upgrade`", runlink.VerdictContractVersion)
	case http.StatusServiceUnavailable:
		return VerdictResult{}, fmt.Errorf("verdict: this deployment does not accept reported verdicts")
	default:
		return VerdictResult{}, fmt.Errorf("verdict: platform returned %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
}
