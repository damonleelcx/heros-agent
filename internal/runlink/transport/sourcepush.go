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

// sourcepush.go transmits a SOURCE SNAPSHOT — the largest thing this client ever sends, and the only one
// that is not an allowlisted projection.
//
// A third method rather than a flag on either of the others, for the reason SendWorkflowIR is separate
// from Link: these are three different things to agree to, and a reviewer must be able to point at the
// one function that sends source. A flag is how "opt-in" becomes "opt-in unless you forget".
//
// The destination pin is re-asserted per request, exactly as the other two do, so this new transmit path
// cannot inherit an unchecked base.

// SourcePushResult is the platform's response to a snapshot upload.
type SourcePushResult struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	Bytes          int    `json:"bytes"`
}

// DiscoveryResult is the platform's summary of a completed discovery.
type DiscoveryResult struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	Nodes          int    `json:"nodes"`
	Edges          int    `json:"edges"`
	Labelled       int    `json:"labelled_regions"`
	Unclassified   int    `json:"unclassified_regions"`
	LLMCalls       int    `json:"llm_calls"`
}

// sourceURL returns the pinned, FLAT snapshot URL.
//
// P29 · it takes no arguments now. The workflow revision used to be two path segments here, which made
// the route unpublishable as an Exact ingress rule — see runlink.WorkflowSourcePath. It travels in the
// two headers `setSourceRef` writes instead.
func (c *Client) sourceURL() (string, error) {
	url := c.base + runlink.WorkflowSourcePath
	if err := assertLinkTarget(url); err != nil {
		return "", err
	}
	return url, nil
}

// setSourceRef names the workflow revision a flat source request applies to.
//
// One helper rather than four call sites, so the push, the retraction and the discovery request cannot
// disagree about which header carries what — the failure that would produce is a snapshot stored under a
// revision nobody asked about, discovered later as an empty graph.
func setSourceRef(req *http.Request, workflowID, revision string) {
	req.Header.Set("X-Heros-Workflow-Id", workflowID)
	req.Header.Set("X-Heros-Source-Revision", revision)
}

// PushSource transmits a gzipped tar of the workflow's source at one revision.
func (c *Client) PushSource(ctx context.Context, workflowID, revision string, bundle []byte) (SourcePushResult, error) {
	url, err := c.sourceURL()
	if err != nil {
		return SourcePushResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bundle))
	if err != nil {
		return SourcePushResult{}, fmt.Errorf("push-source: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Authorization", "Bearer "+c.token)
	setSourceRef(req, workflowID, revision)

	resp, err := c.http.Do(req)
	if err != nil {
		return SourcePushResult{}, fmt.Errorf("push-source: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go: reporting "no such thing"
	// for a path the reverse proxy never routed sends the reader to check an id that was
	// never wrong, which is exactly what happened to these routes in production.
	if err := c.edge404("push-source", runlink.WorkflowSourcePath, resp, raw); err != nil {
		return SourcePushResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK, http.StatusCreated:
		var r SourcePushResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return SourcePushResult{}, fmt.Errorf("push-source: decode response: %w", err)
		}
		return r, nil
	case http.StatusUnauthorized:
		return SourcePushResult{}, fmt.Errorf("push-source: authentication rejected — run `heros login`")
	case http.StatusRequestEntityTooLarge:
		return SourcePushResult{}, fmt.Errorf("push-source: the snapshot is too large for this platform: %s", string(raw))
	case http.StatusServiceUnavailable:
		return SourcePushResult{}, fmt.Errorf("push-source: this deployment does not accept source snapshots: %s", string(raw))
	default:
		return SourcePushResult{}, fmt.Errorf("push-source: platform returned %d: %s", resp.StatusCode, string(raw))
	}
}

// DeleteSource removes a previously pushed snapshot. The retraction, reachable from the CLI so that
// "stop holding my source" is a command rather than a support request.
func (c *Client) DeleteSource(ctx context.Context, workflowID, revision string) error {
	url, err := c.sourceURL()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("forget-source: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	setSourceRef(req, workflowID, revision)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forget-source: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go: reporting "no such thing"
	// for a path the reverse proxy never routed sends the reader to check an id that was
	// never wrong, which is exactly what happened to these routes in production.
	if err := c.edge404("forget-source", runlink.WorkflowSourcePath, resp, raw); err != nil {
		return err
	}

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("forget-source: authentication rejected — run `heros login`")
	default:
		return fmt.Errorf("forget-source: platform returned %d: %s", resp.StatusCode, string(raw))
	}
}

// RunDiscovery asks the platform to discover an already-pushed snapshot.
func (c *Client) RunDiscovery(ctx context.Context, workflowID, revision string) (DiscoveryResult, error) {
	// 🔴 `c.base + <constant>`, not `<local variable> + "/discover"`. The ingress fence reads this
	// package's SOURCE for exactly that construction, so the old form — which built on the value
	// `sourceURL` returned — was a machine-addressed path no fence could see and no manifest published.
	url := c.base + runlink.SourceDiscoveryPath
	if err := assertLinkTarget(url); err != nil {
		return DiscoveryResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("discover: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	setSourceRef(req, workflowID, revision)

	resp, err := c.http.Do(req)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("discover: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go: reporting "no such thing"
	// for a path the reverse proxy never routed sends the reader to check an id that was
	// never wrong, which is exactly what happened to these routes in production.
	if err := c.edge404("discover", runlink.SourceDiscoveryPath, resp, raw); err != nil {
		return DiscoveryResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var r DiscoveryResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return DiscoveryResult{}, fmt.Errorf("discover: decode response: %w", err)
		}
		return r, nil
	case http.StatusConflict:
		// The platform is telling us the snapshot is missing, not that the workflow is. Say which.
		return DiscoveryResult{}, fmt.Errorf("discover: no source has been pushed for %s@%s", workflowID, revision)
	case http.StatusUnauthorized:
		return DiscoveryResult{}, fmt.Errorf("discover: authentication rejected — run `heros login`")
	case http.StatusServiceUnavailable:
		return DiscoveryResult{}, fmt.Errorf("discover: this deployment does not run discovery: %s", string(raw))
	default:
		return DiscoveryResult{}, fmt.Errorf("discover: platform returned %d: %s", resp.StatusCode, string(raw))
	}
}
