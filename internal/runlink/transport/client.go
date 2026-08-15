// Package transport is the ONLY net/http importer on the linking path. Splitting it from runlink keeps
// runlink (allowlist, payload construction, run record) net-free, so the offline command surface can
// depend on those without linking the network in.
//
// 🔴 Every request targets https://heros-agent.space and nowhere else. The URL is built from
// runlink.PlatformBaseURL, and assertLinkTarget re-checks it before the request goes out — a
// belt-and-suspenders guard so no future edit can point the client somewhere else without tripping it.
// The RoundTripper is injectable for tests (which serve heros-agent.space locally), but the URL a test
// sees is still https://heros-agent.space, so the pin is exercised, not bypassed.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// Client transmits linked runs to the platform. It is created with an authenticated token; a client
// with no token cannot transmit (the caller checks authentication before constructing one).
type Client struct {
	token string
	http  *http.Client
	base  string
}

// Option configures a Client.
type Option func(*Client)

// WithRoundTripper injects a transport (tests serve https://heros-agent.space locally through this).
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(c *Client) { c.http = &http.Client{Transport: rt, Timeout: c.http.Timeout} }
}

// WithTimeout bounds every request. A slow platform must never stall a pipeline (PRD NFR7); the CI
// integration passes a tight bound here.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// DefaultTimeout bounds an ordinary platform request.
//
// 🔴 It suits a request the platform ANSWERS FROM STORAGE — a link, an ingest, a fetch. It does not
// suit one where the platform DOES WORK proportional to the caller's repository, and exporting it is
// how a caller that needs a longer bound can say so against a number rather than a guess. See
// `clilink.discoveryTimeout`, which must exceed this and has a fence saying why.
const DefaultTimeout = 30 * time.Second

// NewClient builds a client for the pinned endpoint. token authenticates the identity server-side.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token: token,
		http:  &http.Client{Timeout: DefaultTimeout},
		base:  runlink.PlatformBaseURL,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// LinkResult is the platform's response to a link.
type LinkResult struct {
	Accepted        bool   `json:"accepted"`
	RunURL          string `json:"run_url"`
	ContractVersion string `json:"contract_version"`
	// AlreadyLinked is true when the run was linked before — idempotency, surfaced so a retried CI step
	// can report "already counted" rather than pretending it counted again.
	AlreadyLinked bool   `json:"already_linked"`
	Message       string `json:"message,omitempty"`
}

// Link transmits a payload under the authenticated identity. It returns the platform's result, or an
// error. A transport error is returned as-is so the caller can keep the local result valid (FR19) and
// report the link — not the run — as the failure.
func (c *Client) Link(ctx context.Context, p runlink.Payload) (LinkResult, error) {
	url := c.base + runlink.LinkPath
	if err := assertLinkTarget(url); err != nil {
		return LinkResult{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return LinkResult{}, fmt.Errorf("link: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return LinkResult{}, fmt.Errorf("link: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The token authenticates the identity. It is a request header, never a payload field, and never
	// logged (the caller's narration prints the identity, not the token).
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Heros-Contract", runlink.ContractVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return LinkResult{}, fmt.Errorf("link: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go: reporting "no such thing"
	// for a path the reverse proxy never routed sends the reader to check an id that was
	// never wrong, which is exactly what happened to these routes in production.
	if err := c.edge404("link", runlink.LinkPath, resp, raw); err != nil {
		return LinkResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		var r LinkResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return LinkResult{}, fmt.Errorf("link: decode response: %w", err)
		}
		if resp.StatusCode == http.StatusConflict {
			r.AlreadyLinked = true
			r.Accepted = true
		}
		return r, nil
	case http.StatusUnauthorized:
		return LinkResult{}, fmt.Errorf("link: authentication rejected — run `heros login`")
	case http.StatusUpgradeRequired, http.StatusBadRequest:
		return LinkResult{}, fmt.Errorf("link: platform rejected the request: %s", string(raw))
	default:
		return LinkResult{}, fmt.Errorf("link: platform returned %d", resp.StatusCode)
	}
}

// Validate checks a token against the platform (used by `login`). It returns the identity the platform
// attributes to the token, or an error.
func (c *Client) Validate(ctx context.Context) (identity string, err error) {
	url := c.base + "/api/v1/whoami"
	if err := assertLinkTarget(c.base); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("login: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("login: the platform rejected this token")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login: platform returned %d", resp.StatusCode)
	}
	var body struct {
		Identity string `json:"identity"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("login: decode response: %w", err)
	}
	if body.Identity == "" {
		return "", fmt.Errorf("login: platform accepted the token but returned no identity")
	}
	return body.Identity, nil
}

// Endpoint returns the pinned base URL, for narration.
func (c *Client) Endpoint() string { return c.base }

// assertLinkTarget is the last guard before a request leaves: the destination must be the one pinned
// origin. This can only fail if runlink.PlatformBaseURL is ever changed to something invalid — which is
// exactly when a guard should fire.
func assertLinkTarget(url string) error {
	if !runlink.IsLinkTarget(url) {
		return fmt.Errorf("refusing to transmit: %q is not the pinned link endpoint %s", url, runlink.PlatformBaseURL)
	}
	return nil
}

// FetchAgentDefinition reads the active agent definition, for a customer-placed analysis (task 7.1).
//
// The ONE request on this seam that travels platform → customer. It is a GET carrying nothing but the
// bearer token: the tenant comes from the principal, exactly as it does on every ingest here, so there
// is no field a caller could use to ask about somebody else's placement.
func (c *Client) FetchAgentDefinition(ctx context.Context) (runlink.AgentDefinition, error) {
	url := c.base + runlink.AgentDefinitionPath
	if err := assertLinkTarget(url); err != nil {
		return runlink.AgentDefinition{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return runlink.AgentDefinition{}, fmt.Errorf("analyse: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Heros-Contract", runlink.AgentDefinitionContractVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return runlink.AgentDefinition{}, fmt.Errorf("analyse: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if err := c.edge404("analyse", runlink.AgentDefinitionPath, resp, raw); err != nil {
		return runlink.AgentDefinition{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var d runlink.AgentDefinition
		if err := json.Unmarshal(raw, &d); err != nil {
			return runlink.AgentDefinition{}, fmt.Errorf("analyse: decode response: %w", err)
		}
		return d, nil
	case http.StatusUnauthorized:
		return runlink.AgentDefinition{}, fmt.Errorf("analyse: authentication rejected — run `heros login`")
	case http.StatusServiceUnavailable:
		return runlink.AgentDefinition{}, fmt.Errorf("analyse: this deployment runs no analysis agent: %s", string(raw))
	default:
		return runlink.AgentDefinition{}, fmt.Errorf("analyse: platform returned %d: %s", resp.StatusCode, string(raw))
	}
}

// WorkflowIRResult is the platform's response to an opt-in structure upload.
type WorkflowIRResult struct {
	Accepted   bool   `json:"accepted"`
	WorkflowID string `json:"workflow_id"`
	Nodes      int    `json:"nodes"`
	Edges      int    `json:"edges"`
	GraphURL   string `json:"graph_url"`
}

// SendWorkflowIR transmits the OPT-IN workflow structure.
//
// A separate method from Link, on purpose. They carry different contracts, are versioned separately, and
// one of them happens only when a developer asks for it by name — collapsing them into a single call
// with a flag is how "opt-in" becomes "opt-in unless you forget", and how a reviewer loses the ability
// to point at the one function that sends structure.
//
// The destination is re-asserted here exactly as it is for a link: the pin is per-request, so a new
// transmit path cannot inherit an unchecked base.
func (c *Client) SendWorkflowIR(ctx context.Context, p runlink.WorkflowIRPayload) (WorkflowIRResult, error) {
	// P29 · flat. The workflow id is in the payload and nowhere else.
	url := c.base + runlink.WorkflowIRPath
	if err := assertLinkTarget(url); err != nil {
		return WorkflowIRResult{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return WorkflowIRResult{}, fmt.Errorf("link: marshal workflow structure: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return WorkflowIRResult{}, fmt.Errorf("link: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Heros-Contract", runlink.WorkflowIRContractVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return WorkflowIRResult{}, fmt.Errorf("link: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 🔴 An edge 404 is not a platform refusal. See edge404.go: reporting "no such thing"
	// for a path the reverse proxy never routed sends the reader to check an id that was
	// never wrong, which is exactly what happened to these routes in production.
	if err := c.edge404("link", runlink.WorkflowIRPath, resp, raw); err != nil {
		return WorkflowIRResult{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var r WorkflowIRResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return WorkflowIRResult{}, fmt.Errorf("link: decode response: %w", err)
		}
		return r, nil
	case http.StatusUnauthorized:
		return WorkflowIRResult{}, fmt.Errorf("link: authentication rejected — run `heros login`")
	case http.StatusServiceUnavailable:
		// A deployment that does not accept structure is a legible answer, not a failure of the upload.
		return WorkflowIRResult{}, fmt.Errorf("link: this deployment does not accept workflow structure: %s", string(raw))
	default:
		return WorkflowIRResult{}, fmt.Errorf("link: platform returned %d: %s", resp.StatusCode, string(raw))
	}
}
