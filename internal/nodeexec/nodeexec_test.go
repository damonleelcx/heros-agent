package nodeexec

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/skillgate"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

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
	if r.e != nil && v == r.e.VersionID {
		return r.e, nil
	}
	return nil, registry.ErrNotFound
}

// isolating builds a sandbox that CAN isolate (test-only full capabilities), so the happy path runs.
func isolatingSandbox() *sandbox.Sandbox {
	e := sandbox.NewSubprocessEnforcer().WithCapabilities(sandbox.Capabilities{
		ScrubEnv: true, ResourceLimits: true, NetworkDeny: true, FilesystemScope: true,
	})
	return sandbox.New(e, sandbox.WithWarmPool(1))
}

func skillEntry(t *testing.T) *registry.SkillEntry {
	return &registry.SkillEntry{
		VersionID: "v1", Name: "extract",
		Spec:   registry.SkillSpec{ImplHandle: "repo:tools/extract.sh"},
		Input:  compile(t, `{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"],"additionalProperties":false}`),
		Output: compile(t, `{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"],"additionalProperties":false}`),
	}
}

func req(argv []string) SkillRequest {
	return SkillRequest{
		NodeID: "n1", RunID: "r1", VersionID: "v1", Args: map[string]any{"n": 3}, Argv: argv,
		Bounds: sandbox.ResourceBounds{Wallclock: 5 * time.Second, CPU: 2 * time.Second, MaxOutput: 64 << 10},
	}
}

// Happy path: input gated, tool runs in the isolate, output gated, validated result returned.
func TestRunSkill_EndToEnd(t *testing.T) {
	r := New(resolver{skillEntry(t)}, isolatingSandbox())
	res, err := r.RunSkill(context.Background(), req([]string{"sh", "-c", `printf '{"hits":[1,2]}'`}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, ok := res.Output.(map[string]any)
	if !ok || out["hits"] == nil {
		t.Fatalf("unexpected validated output: %+v", res.Output)
	}
}

// Pre-execution: a bad argument object fails closed and the tool is never run (a marker the tool would
// print never appears because CheckInput rejects first).
func TestRunSkill_BadArgs_FailsClosedBeforeTool(t *testing.T) {
	r := New(resolver{skillEntry(t)}, isolatingSandbox())
	bad := req([]string{"sh", "-c", `printf '{"hits":[]}'`})
	bad.Args = map[string]any{"n": 0} // violates minimum:1
	_, err := r.RunSkill(context.Background(), bad)
	var ce *skillgate.ContractError
	if !errors.As(err, &ce) || ce.Kind != skillgate.FailureInputSchema {
		t.Fatalf("want input_schema fail-closed, got %v", err)
	}
}

// Pre-propagation: a tool that returns schema-violating output has its result discarded.
func TestRunSkill_BadOutput_Discarded(t *testing.T) {
	r := New(resolver{skillEntry(t)}, isolatingSandbox())
	_, err := r.RunSkill(context.Background(), req([]string{"sh", "-c", `printf '{"wrong":1}'`}))
	var ce *skillgate.ContractError
	if !errors.As(err, &ce) || ce.Kind != skillgate.FailureOutputSchema {
		t.Fatalf("want output_schema fail-closed, got %v", err)
	}
}

// Non-JSON tool output cannot be validated, so it fails closed.
func TestRunSkill_NonJSONOutput_FailsClosed(t *testing.T) {
	r := New(resolver{skillEntry(t)}, isolatingSandbox())
	_, err := r.RunSkill(context.Background(), req([]string{"sh", "-c", `printf 'not json'`}))
	if !errors.Is(err, ErrToolOutputNotJSON) {
		t.Fatalf("want ErrToolOutputNotJSON, got %v", err)
	}
}

// A resource-exhausting tool is contained and the node fails closed with a resource error.
func TestRunSkill_ResourceBreach_FailsClosed(t *testing.T) {
	r := New(resolver{skillEntry(t)}, isolatingSandbox())
	rq := req([]string{"sh", "-c", "while :; do :; done"})
	rq.Bounds = sandbox.ResourceBounds{Wallclock: 6 * time.Second, CPU: 1 * time.Second, MaxOutput: 64 << 10}
	_, err := r.RunSkill(context.Background(), rq)
	if !errors.Is(err, sandbox.ErrResourceBreach) {
		t.Fatalf("want ErrResourceBreach, got %v", err)
	}
}

// Availability: on a host that cannot isolate, a repo tool is unbindable → the node fails closed
// before the tool runs (the sandbox-backed binder tying availability to real isolation capability).
func TestRunSkill_UnbindableWhenHostCannotIsolate(t *testing.T) {
	bare := sandbox.New(sandbox.NewSubprocessEnforcer()) // honest caps: NetworkDeny/FilesystemScope=false
	r := New(resolver{skillEntry(t)}, bare)
	_, err := r.RunSkill(context.Background(), req([]string{"sh", "-c", "true"}))
	var ce *skillgate.ContractError
	if !errors.As(err, &ce) || ce.Kind != skillgate.FailureUnavailable {
		t.Fatalf("repo tool should be unavailable when the host cannot isolate, got %v", err)
	}
}

// The sandbox-backed binder: builtin handles always bind; repo handles need isolation capability;
// unknown schemes never bind.
func TestSandboxBinder(t *testing.T) {
	iso := NewSandboxBinder(isolatingSandbox())
	bare := NewSandboxBinder(sandbox.New(sandbox.NewSubprocessEnforcer()))

	if err := iso.CanBind("builtin:search"); err != nil {
		t.Errorf("builtin should bind: %v", err)
	}
	if err := iso.CanBind("repo:tools/x.sh"); err != nil {
		t.Errorf("repo should bind on an isolating host: %v", err)
	}
	if err := bare.CanBind("repo:tools/x.sh"); err == nil {
		t.Error("repo must NOT bind on a host that cannot isolate")
	}
	if err := iso.CanBind("ftp://nope"); err == nil {
		t.Error("unknown scheme must not bind")
	}
}
