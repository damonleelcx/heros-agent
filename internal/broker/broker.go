// Package broker is the host-side credentialed channel for sandboxed tools (P3 tasks 4.1–4.3). A tool
// inside an isolate holds no provider credential and has no network egress. When it legitimately needs
// a model completion, a retrieval, or an allowlisted HTTP call, it asks the broker; the TRUSTED HOST
// performs the call through the provider gateway (which holds the real secret) and returns only the
// result. The credential never crosses into the isolate.
//
// The broker is the one sanctioned egress path, so it is also a potential egress hole — and it is
// closed the same way direct egress is (task 4.2): it exposes only a fixed call vocabulary
// (complete / retrieve / allowlisted HTTP), it applies the SAME default-deny allowlist, and it audits
// every call. A tool cannot use the broker to reach a host the egress policy does not permit.
//
// Every brokered call is audited with metadata only — op, ref/host, decision, and token counts — never
// a credential or a prompt body, so the audit record is secret-free by construction (task 4.3). A
// defensive redaction pass on the free-text reason is the belt to that suspenders.
package broker

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sandbox"
)

var (
	// ErrEgressDenied: a brokered call targeted a host not on the allowlist. The broker cannot be used
	// to bypass the isolate's default-deny egress (task 4.2).
	ErrEgressDenied = errors.New("broker: egress to a non-allowlisted host denied")
	// ErrUnavailable: the broker was asked for a capability it was not wired with (e.g. retrieval with
	// no retriever). Fails closed rather than silently returning empty.
	ErrUnavailable = errors.New("broker: requested capability is not available")
)

// Completer is the subset of the provider gateway the broker calls. providergateway.Gateway satisfies it.
type Completer interface {
	Complete(ctx context.Context, entry *registry.ModelEntry, req providergateway.Request, seed *int64) (*providergateway.Response, error)
}

// ModelResolver resolves a model ref to its entry. registry.Store satisfies it.
type ModelResolver interface {
	ResolveModel(ctx context.Context, versionID string) (*registry.ModelEntry, error)
}

// Retriever is the host retrieval backend behind a retriever_ref.
type Retriever interface {
	Retrieve(ctx context.Context, ref, query string, topK int, seed int64) ([]registry.Chunk, error)
}

// AuditRecord is one brokered call's audit line. Metadata only: it NEVER carries a credential, a prompt
// body, or a completion body (task 4.3).
type AuditRecord struct {
	NodeID       string
	RunID        string
	Op           string // "complete" | "retrieve" | "http"
	Ref          string // model/retriever ref, or target host for http
	Allowed      bool
	Reason       string // secret-free; redacted defensively before recording
	InputTokens  int
	OutputTokens int
	Chunks       int
}

// Auditor receives brokered-call audit records.
type Auditor interface {
	Record(AuditRecord)
}

// Broker performs credentialed calls on behalf of sandboxed tools. It implements registry.HostServices
// so host-side context policies (summarization, rag-retrieval) reach models/retrievers through the same
// audited, allowlisted seam.
type Broker struct {
	gw     Completer
	models ModelResolver
	retr   Retriever
	egress sandbox.EgressPolicy
	audit  Auditor
}

// Config wires a Broker.
type Config struct {
	Gateway   Completer
	Models    ModelResolver
	Retriever Retriever
	Egress    sandbox.EgressPolicy
	Audit     Auditor
}

func New(cfg Config) *Broker {
	return &Broker{gw: cfg.Gateway, models: cfg.Models, retr: cfg.Retriever, egress: cfg.Egress, audit: cfg.Audit}
}

// CompleteRequest is a sandboxed tool's request for a model completion. It names the model by registry
// ref — never by endpoint or key — so the tool cannot redirect the call or supply a credential.
type CompleteRequest struct {
	NodeID   string
	RunID    string
	ModelRef string
	System   string
	Messages []providergateway.Message
	Seed     *int64
}

// CompleteResult is what returns to the isolate: the content and usage, and nothing about how the call
// was authenticated.
type CompleteResult struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Provider     string
	ModelID      string
}

// Complete performs a model completion on the trusted host and returns only the result. The isolate
// never receives the provider credential (task 4.1).
func (b *Broker) Complete(ctx context.Context, req CompleteRequest) (*CompleteResult, error) {
	if b.gw == nil || b.models == nil {
		b.record(AuditRecord{NodeID: req.NodeID, RunID: req.RunID, Op: "complete", Ref: req.ModelRef, Allowed: false, Reason: "completion not available"})
		return nil, fmt.Errorf("%w: completion", ErrUnavailable)
	}
	entry, err := b.models.ResolveModel(ctx, req.ModelRef)
	if err != nil {
		b.record(AuditRecord{NodeID: req.NodeID, RunID: req.RunID, Op: "complete", Ref: req.ModelRef, Allowed: false, Reason: "model ref did not resolve"})
		return nil, fmt.Errorf("broker: model ref %q: %w", req.ModelRef, err)
	}
	resp, err := b.gw.Complete(ctx, entry, providergateway.Request{System: req.System, Messages: req.Messages}, req.Seed)
	if err != nil {
		// The gateway already scrubs secrets from its errors; the broker records only that the call
		// failed, not the error text (which could echo a request detail).
		b.record(AuditRecord{NodeID: req.NodeID, RunID: req.RunID, Op: "complete", Ref: req.ModelRef, Allowed: true, Reason: "provider call failed"})
		return nil, fmt.Errorf("broker: completion failed: %w", err)
	}
	b.record(AuditRecord{
		NodeID: req.NodeID, RunID: req.RunID, Op: "complete", Ref: req.ModelRef, Allowed: true,
		Reason: "ok", InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
	})
	return &CompleteResult{
		Content: resp.Content, InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
		Provider: resp.Provider, ModelID: resp.ModelID,
	}, nil
}

// HTTP is the allowlisted-HTTP vocabulary. The broker permits it only for a host on the egress
// allowlist; anything else is denied and recorded (task 4.2). It returns the decision, not a live
// connection, so this method is where "the broker cannot bypass egress deny" is enforced.
func (b *Broker) HTTP(ctx context.Context, nodeID, runID, host string) error {
	_ = ctx
	if !b.egress.Permits(host) {
		b.record(AuditRecord{NodeID: nodeID, RunID: runID, Op: "http", Ref: host, Allowed: false, Reason: "host not on egress allowlist"})
		return fmt.Errorf("%w: %s", ErrEgressDenied, host)
	}
	b.record(AuditRecord{NodeID: nodeID, RunID: runID, Op: "http", Ref: host, Allowed: true, Reason: "allowlisted"})
	return nil
}

// ── registry.HostServices ──────────────────────────────────────────────────────
// Host-side context policies reach the model/retriever through the broker, so the credentialed path is
// identical whether the caller is a sandboxed tool or a trusted context policy.

// Summarize implements registry.HostServices: it runs the summarizer model via the gateway.
func (b *Broker) Summarize(ctx context.Context, req registry.ResolvedRequest) (string, error) {
	msgs := make([]providergateway.Message, 0, len(req.Messages)+1)
	for _, m := range req.Messages {
		msgs = append(msgs, providergateway.Message{Role: providergateway.Role(m.Role), Content: m.Content})
	}
	res, err := b.Complete(ctx, CompleteRequest{
		ModelRef: req.ModelRef,
		System:   "Summarize the following conversation faithfully and concisely.",
		Messages: msgs,
		Seed:     seedPtr(req.Seed),
	})
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// Retrieve implements registry.HostServices: it resolves the retriever_ref and retrieves on the host.
func (b *Broker) Retrieve(ctx context.Context, req registry.ResolvedRequest) ([]registry.Chunk, error) {
	if b.retr == nil {
		b.record(AuditRecord{Op: "retrieve", Ref: req.Ref, Allowed: false, Reason: "retrieval not available"})
		return nil, fmt.Errorf("%w: retrieval", ErrUnavailable)
	}
	chunks, err := b.retr.Retrieve(ctx, req.Ref, req.Query, req.TopK, req.Seed)
	if err != nil {
		b.record(AuditRecord{Op: "retrieve", Ref: req.Ref, Allowed: true, Reason: "retriever failed"})
		return nil, fmt.Errorf("broker: retrieval failed: %w", err)
	}
	b.record(AuditRecord{Op: "retrieve", Ref: req.Ref, Allowed: true, Reason: "ok", Chunks: len(chunks)})
	return chunks, nil
}

func (b *Broker) record(r AuditRecord) {
	r.Reason = redactSecrets(r.Reason)
	r.Ref = redactSecrets(r.Ref)
	if b.audit != nil {
		b.audit.Record(r)
	}
}

func seedPtr(s int64) *int64 { return &s }

// secretPattern matches the common credential shapes so a stray token in a free-text field is redacted
// before it is recorded (task 4.3). The audit fields are metadata-only by design; this is defense in
// depth, not the primary control.
var secretPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9]{8,}|AKIA[0-9A-Z]{12,}|Bearer\s+[A-Za-z0-9._-]{8,}|xox[baprs]-[A-Za-z0-9-]{8,})`)

func redactSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "[REDACTED]")
}
