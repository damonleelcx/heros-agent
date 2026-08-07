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

// transformreceipt.go transmits what a locally-generated transform DID (P29 §2.9).
//
// A fourth method rather than a flag on any of the other three, for the reason the file header on
// sourcepush.go gives: these are separate things to agree to, and a flag is how "opt-in" becomes "opt-in
// unless you forget".

// TransformReceiptResult is the platform's response to a receipt.
type TransformReceiptResult struct {
	Accepted     bool   `json:"accepted"`
	ConfigHash   string `json:"config_hash"`
	TransformURL string `json:"transform_url"`
	Replaced     bool   `json:"replaced"`
}

// SendTransformReceipt transmits one receipt. The destination pin is re-asserted per request, exactly as
// every other transmit path in this package does.
func (c *Client) SendTransformReceipt(ctx context.Context, r runlink.TransformReceipt) (TransformReceiptResult, error) {
	url := c.base + runlink.TransformReceiptPath
	if err := assertLinkTarget(url); err != nil {
		return TransformReceiptResult{}, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return TransformReceiptResult{}, fmt.Errorf("receipt: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return TransformReceiptResult{}, fmt.Errorf("receipt: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Heros-Contract", runlink.TransformReceiptContractVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return TransformReceiptResult{}, fmt.Errorf("receipt: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go.
	if err := c.edge404("receipt", runlink.TransformReceiptPath, resp, raw); err != nil {
		return TransformReceiptResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out TransformReceiptResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return TransformReceiptResult{}, fmt.Errorf("receipt: decode response: %w", err)
		}
		return out, nil
	case http.StatusUnauthorized:
		return TransformReceiptResult{}, fmt.Errorf("receipt: authentication rejected — run `heros login`")
	case http.StatusUpgradeRequired:
		return TransformReceiptResult{}, fmt.Errorf(
			"receipt: contract mismatch — this CLI sends %s and the platform does not accept it; "+
				"run `heros upgrade`", runlink.TransformReceiptContractVersion)
	case http.StatusServiceUnavailable:
		return TransformReceiptResult{}, fmt.Errorf("receipt: this deployment does not accept transform receipts")
	default:
		return TransformReceiptResult{}, fmt.Errorf("receipt: platform returned %d: %s",
			resp.StatusCode, bytes.TrimSpace(raw))
	}
}
