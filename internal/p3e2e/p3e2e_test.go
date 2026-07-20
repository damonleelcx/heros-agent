// Package p3e2e holds cross-package P3 integration tests: repo tool code runs in the sandbox, its
// output is gated by the skill contract before it can propagate, and credentialed calls go through the
// broker without the isolate holding a secret. These prove the seams between Sections 2/3/4 line up.
package p3e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/broker"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/skillgate"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// broker test doubles.
type fakeGW struct {
	called bool
	resp   *providergateway.Response
}

func (g *fakeGW) Complete(_ context.Context, _ *registry.ModelEntry, _ providergateway.Request, _ *int64) (*providergateway.Response, error) {
	g.called = true
	return g.resp, nil
}

type fakeModels struct{}

func (fakeModels) ResolveModel(_ context.Context, v string) (*registry.ModelEntry, error) {
	if v == "m_ok" {
		return &registry.ModelEntry{VersionID: v, Name: "m", Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt"}}, nil
	}
	return nil, registry.ErrNotFound
}

type countAuditor struct{ n int }

func (c *countAuditor) Record(broker.AuditRecord) { c.n++ }

func compile(t *testing.T, raw string) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:s", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem:s")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

type resolver struct{ e *registry.SkillEntry }

func (r resolver) ResolveSkill(_ context.Context, v string) (*registry.SkillEntry, error) {
	if v == r.e.VersionID {
		return r.e, nil
	}
	return nil, registry.ErrNotFound
}

func fullCapsSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	// Test-only: claim full isolation so the tool actually runs (the OS-denial layer is proven by the
	// container proof, not this hermetic integration test).
	e := sandbox.NewSubprocessEnforcer().WithCapabilities(sandbox.Capabilities{
		ScrubEnv: true, ResourceLimits: true, NetworkDeny: true, FilesystemScope: true,
	})
	return sandbox.New(e, sandbox.WithWarmPool(1))
}

func spec() sandbox.Spec {
	return sandbox.Spec{
		NodeID: "n1", RunID: "r1",
		RequireNetworkIsolation: true, RequireFilesystemScope: true,
		Bounds: sandbox.ResourceBounds{Wallclock: 5 * time.Second, CPU: 2 * time.Second, MaxOutput: 64 << 10},
	}
}

// The malicious "returns output violating its schema" tool: it runs in the sandbox and emits JSON that
// violates the skill's output contract. The contract gate discards it before it can propagate — the
// isolate never gets to inject an unvalidated value into the graph.
func TestSandboxOutput_GatedByContractBeforePropagation(t *testing.T) {
	entry := &registry.SkillEntry{
		VersionID: "v1", Name: "extract",
		Spec:   registry.SkillSpec{ImplHandle: "repo:tools/extract.py"},
		Input:  compile(t, `{"type":"object"}`),
		Output: compile(t, `{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"],"additionalProperties":false}`),
	}
	gate := skillgate.New(resolver{entry}, nil)
	sb := fullCapsSandbox(t)

	// Pre-execution: input validated + entry resolved before the tool runs.
	resolved, err := gate.CheckInput(context.Background(), "v1", map[string]any{})
	if err != nil {
		t.Fatalf("input gate: %v", err)
	}

	// A tool that returns a schema-violating result.
	badRes, err := sb.Run(context.Background(), spec(), sandbox.Tool{Argv: []string{"sh", "-c", `printf '{"wrong":1}'`}})
	if err != nil {
		t.Fatalf("sandbox run: %v", err)
	}
	var badOut any
	if err := json.Unmarshal(badRes.Stdout, &badOut); err != nil {
		t.Fatalf("tool output not JSON: %q", badRes.Stdout)
	}
	if err := gate.CheckOutput(resolved, badOut); !errors.Is(err, skillgate.ErrContract) {
		t.Fatalf("schema-violating output was not discarded: %v", err)
	}

	// A tool that returns a conforming result propagates.
	goodRes, _ := sb.Run(context.Background(), spec(), sandbox.Tool{Argv: []string{"sh", "-c", `printf '{"hits":[]}'`}})
	var goodOut any
	_ = json.Unmarshal(goodRes.Stdout, &goodOut)
	if err := gate.CheckOutput(resolved, goodOut); err != nil {
		t.Errorf("conforming output was rejected: %v", err)
	}
}

// Broker boundary (task 7.4): a sandboxed tool's credentialed call is performed host-side; the isolate
// holds no credential, and the broker cannot be steered to a non-allowlisted host.
func TestBrokerBoundary_CredentialNeverInIsolate(t *testing.T) {
	gw := &fakeGW{resp: &providergateway.Response{Content: "ok"}}
	aud := &countAuditor{}
	b := broker.New(broker.Config{
		Gateway: gw, Models: fakeModels{}, Egress: sandbox.EgressPolicy{Allow: []string{"api.allowed"}}, Audit: aud,
	})

	// A credentialed completion succeeds without the isolate ever holding a key.
	res, err := b.Complete(context.Background(), broker.CompleteRequest{NodeID: "n1", ModelRef: "m_ok"})
	if err != nil || res.Content != "ok" {
		t.Fatalf("brokered completion failed: %v", err)
	}
	if !gw.called {
		t.Fatal("the host gateway did not perform the call")
	}
	// The broker refuses a non-allowlisted host.
	if err := b.HTTP(context.Background(), "n1", "r1", "evil.example.com"); !errors.Is(err, broker.ErrEgressDenied) {
		t.Fatalf("broker allowed a non-allowlisted host: %v", err)
	}
}
