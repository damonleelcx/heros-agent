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

// localpair.go claims a pairing code the person started in the console (P32 §4).
//
// # 🚫 What this transmits, exhaustively
//
// Three values: the code the person typed, the name this machine calls itself, and a commit id. That is
// the whole payload and the struct has no fourth field, which is the form of the guarantee that
// survives somebody being in a hurry — there is nowhere to put a path.
//
// It is a fifth transmit method rather than a flag on `link` for the reason every other one has its
// own: pairing a machine, sending a run's numbers, sending a repository and sending a measurement are
// four different things to agree to, and a reviewer must be able to point at the one entry point that
// does each.
//
// # What a pairing is not
//
// It is not a credential and it grants nothing. This request is already authenticated by the person's
// own token; the pairing only tells the console WHICH machine reads a workflow, so the surface can say
// something true instead of "structure arrived from somewhere".

// PairResult is the platform's answer to a claim.
type PairResult struct {
	PairingID   string `json:"pairing_id"`
	WorkflowID  string `json:"workflow_id"`
	State       string `json:"state"`
	MachineName string `json:"machine_name"`
	Revision    string `json:"revision"`
}

// pairClaim is the wire shape. Unexported: nothing outside this file constructs one, so there is one
// place where what crosses is decided.
type pairClaim struct {
	UserCode    string `json:"user_code"`
	MachineName string `json:"machine_name"`
	Revision    string `json:"revision,omitempty"`
}

// ClaimPairing claims a code. The destination pin is re-asserted per request, exactly as every other
// transmit path in this package does.
func (c *Client) ClaimPairing(ctx context.Context, userCode, machineName, revision string) (PairResult, error) {
	url := c.base + runlink.LocalPairingClaimPath
	if err := assertLinkTarget(url); err != nil {
		return PairResult{}, err
	}
	body, err := json.Marshal(pairClaim{UserCode: userCode, MachineName: machineName, Revision: revision})
	if err != nil {
		return PairResult{}, fmt.Errorf("pair: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return PairResult{}, fmt.Errorf("pair: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return PairResult{}, fmt.Errorf("pair: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go — this is the exact failure the ingress
	// fence exists to prevent, and the message has to tell the two apart or a customer spends an
	// afternoon re-typing a code that was never going to work.
	if err := c.edge404("pair", runlink.LocalPairingClaimPath, resp, raw); err != nil {
		return PairResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var out PairResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return PairResult{}, fmt.Errorf("pair: decode response: %w", err)
		}
		return out, nil
	case http.StatusGone:
		// 🔴 Distinct from 404 all the way to the person's screen. "Your code expired" and "no such
		// code" send them to two different places, and only one of them is where the problem is.
		return PairResult{}, fmt.Errorf(
			"pair: that code has expired — codes last ten minutes; start a new one in the console and try again")
	case http.StatusNotFound:
		return PairResult{}, fmt.Errorf(
			"pair: that code is not waiting to be claimed — check it against the console (O and 0 are not " +
				"both used, so a mistyped character is usually the cause), or start a new one")
	case http.StatusUnauthorized:
		return PairResult{}, fmt.Errorf("pair: authentication rejected — run `heros login`")
	case http.StatusServiceUnavailable:
		return PairResult{}, fmt.Errorf("pair: this deployment does not offer the local-repository bridge")
	default:
		return PairResult{}, fmt.Errorf("pair: platform returned %d: %s", resp.StatusCode, firstBytes(raw, 200))
	}
}

// firstBytes renders at most n bytes of a response for an error message.
//
// 🔴 Bounded, and the bound is not cosmetic. This is the DEFAULT arm — the status nobody anticipated —
// so `raw` may be anything the far side sent, including an HTML error page from a proxy. An unbounded
// interpolation would put a stranger's page into the customer's terminal and into whatever collects it.
func firstBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
