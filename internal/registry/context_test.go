package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Task 1.5: the `full` policy is registered out of the box.
func TestNewStore_RegistersTheFullPolicy(t *testing.T) {
	s := NewStore(nil, nil)
	if _, ok := s.policies["full"]; !ok {
		t.Fatalf("the `full` policy is not registered; available: %v", s.policyNames())
	}
}

func TestFullPolicy_ParamsSchemaIsItselfValid(t *testing.T) {
	if _, err := compileSchema("params_schema", FullPolicy{}.ParamsSchema()); err != nil {
		t.Fatalf("the full policy's own params schema is not a valid JSON Schema: %v", err)
	}
}

// FR5's registry half: a context entry naming a policy nobody implements must never exist, so a
// Variant Spec cannot reference one. The nil *sql.DB proves the rejection precedes any write.
func TestRegisterContextPolicy_RejectsUnregisteredPolicyBeforeTouchingTheDatabase(t *testing.T) {
	s := NewStore(nil, nil)
	id, err := s.RegisterContextPolicy(context.Background(), "default", "sliding-window", nil)
	if err == nil {
		t.Fatalf("registering an unimplemented policy succeeded and returned %s", id)
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("want ErrInvalidEntry, got %v", err)
	}
	// The unhappy path must say which policy, and what was available (PRD §9 Product Designer:
	// design the unhappy path first — tell the user which dimension broke).
	if !strings.Contains(err.Error(), "sliding-window") || !strings.Contains(err.Error(), "full") {
		t.Errorf("error should name the rejected policy and the available ones, got: %v", err)
	}
}

// `full` takes no params and says so with additionalProperties:false. Passing a P3 policy's params
// to it is the mistake someone makes when they think they selected a P3 policy — it must be loud at
// registration, not a param silently ignored at runtime.
func TestRegisterContextPolicy_RejectsParamsThatViolateThePolicySchema(t *testing.T) {
	s := NewStore(nil, nil)
	id, err := s.RegisterContextPolicy(context.Background(), "default", "full", json.RawMessage(`{"window":50}`))
	if err == nil {
		t.Fatalf("params violating the policy's schema were accepted and returned %s", id)
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("want ErrInvalidEntry, got %v", err)
	}
}

func TestRegisterContextPolicy_RejectsMalformedParamsJSON(t *testing.T) {
	s := NewStore(nil, nil)
	_, err := s.RegisterContextPolicy(context.Background(), "default", "full", json.RawMessage(`{"a":`))
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("want ErrInvalidEntry for malformed params JSON, got %v", err)
	}
}

// The P3 seam: adding a policy is an AddPolicy call and a new row, not a schema change and not an
// edit to this package's types. If this test needs anything more than an implementation of Policy,
// the interface has been designed too `full`-specifically (PRD §11 risk).
func TestAddPolicy_IsEnoughToSupportANewPolicy(t *testing.T) {
	s := NewStore(nil, nil)
	s.AddPolicy(slidingWindow{})

	if _, ok := s.policies["sliding-window"]; !ok {
		t.Fatal("AddPolicy did not make the policy available")
	}
	// Its params are now validated against ITS schema, which this package knows nothing about.
	_, err := s.RegisterContextPolicy(context.Background(), "default", "sliding-window", json.RawMessage(`{"turns":"lots"}`))
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("a new policy's params must be validated against its own schema, got %v", err)
	}
	// And a params object it does accept gets past validation — reaching the DB write, which is the
	// nil handle. Panicking here would mean validation passed; that is what we want to observe.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected valid params to reach the publish step (nil *sql.DB panics); " +
					"validation appears to have rejected them")
			}
		}()
		_, _ = s.RegisterContextPolicy(context.Background(), "default", "sliding-window", json.RawMessage(`{"turns":10}`))
	}()
}

// slidingWindow stands in for a P3 policy. It exists only to prove the interface does not assume
// `full`'s shape (no params).
type slidingWindow struct{}

func (slidingWindow) Name() string { return "sliding-window" }
func (slidingWindow) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"turns":{"type":"integer","minimum":1}},"required":["turns"],"additionalProperties":false}`)
}
