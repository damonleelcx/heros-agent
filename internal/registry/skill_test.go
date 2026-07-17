package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const validInputSchema = `{
  "type": "object",
  "properties": {"query": {"type": "string"}, "limit": {"type": "integer", "minimum": 1}},
  "required": ["query"],
  "additionalProperties": false
}`

// Task 1.4: validate the schema ITSELF at registration.
func TestCompileSchema_RejectsInvalidSchemas(t *testing.T) {
	cases := []struct{ name, schema string }{
		{"unknown type", `{"type": "nonsense"}`},
		{"required is not an array", `{"type": "object", "required": "query"}`},
		{"properties is not an object", `{"type": "object", "properties": 5}`},
		{"minimum is not a number", `{"type": "integer", "minimum": "one"}`},
		{"malformed JSON", `{"type": "object"`},
		{"empty", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compileSchema("input_schema", json.RawMessage(tc.schema)); err == nil {
				t.Fatalf("compileSchema(%s) succeeded; an invalid schema must be rejected at registration", tc.schema)
			} else if !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("want ErrInvalidEntry, got %v", err)
			}
		})
	}
}

func TestCompileSchema_AcceptsValidSchema(t *testing.T) {
	if _, err := compileSchema("input_schema", json.RawMessage(validInputSchema)); err != nil {
		t.Fatalf("a valid schema was rejected: %v", err)
	}
}

// Registration must be hermetic. A remote $ref would make a version_id's meaning depend on content
// we never hashed and on a third party's uptime, and would let a registered schema drive an outbound
// request from the registration path.
func TestCompileSchema_RejectsRemoteRef(t *testing.T) {
	_, err := compileSchema("input_schema", json.RawMessage(`{"$ref": "https://example.com/schema.json"}`))
	if err == nil {
		t.Fatal("a schema with a remote $ref was accepted; registration must be hermetic")
	}
	if !strings.Contains(err.Error(), "example.com") {
		t.Errorf("error should name the refused reference, got: %v", err)
	}
}

// A self-contained internal $ref is fine — the whole contract is inside the bytes we hash.
func TestCompileSchema_AcceptsLocalRef(t *testing.T) {
	schema := `{
	  "type": "object",
	  "properties": {"a": {"$ref": "#/$defs/str"}},
	  "$defs": {"str": {"type": "string"}}
	}`
	if _, err := compileSchema("input_schema", json.RawMessage(schema)); err != nil {
		t.Fatalf("a self-contained $ref was rejected: %v", err)
	}
}

// FR8: the runtime validates argument shape against the contract BEFORE the node executes.
func TestSkillEntry_ValidateInput(t *testing.T) {
	in, err := compileSchema("input_schema", json.RawMessage(validInputSchema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := compileSchema("output_schema", json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	e := &SkillEntry{Name: "search", Input: in, Output: out}

	good := map[string]any{"query": "hello", "limit": 5}
	if err := e.ValidateInput(good); err != nil {
		t.Errorf("a conforming argument object was rejected: %v", err)
	}

	bad := []struct {
		name string
		args map[string]any
	}{
		{"wrong type", map[string]any{"query": 5}},
		{"missing required", map[string]any{"limit": 5}},
		{"additional property", map[string]any{"query": "x", "nope": true}},
		{"violates minimum", map[string]any{"query": "x", "limit": 0}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := e.ValidateInput(tc.args)
			if err == nil {
				t.Fatalf("ValidateInput(%v) succeeded; the contract must catch it before execution", tc.args)
			}
			if !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("want ErrInvalidEntry, got %v", err)
			}
		})
	}
}

// Rejection must happen BEFORE any write is attempted. The nil *sql.DB is the assertion: if
// RegisterSkill touched the database before validating, this would panic instead of returning.
func TestRegisterSkill_RejectsBeforeTouchingTheDatabase(t *testing.T) {
	s := NewStore(nil, nil)
	cases := []struct {
		name string
		spec SkillSpec
	}{
		{"empty impl handle", SkillSpec{ImplHandle: "",
			InputSchema: json.RawMessage(validInputSchema), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
		{"invalid input schema", SkillSpec{ImplHandle: "builtin:search",
			InputSchema: json.RawMessage(`{"type":"nonsense"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
		{"invalid output schema", SkillSpec{ImplHandle: "builtin:search",
			InputSchema: json.RawMessage(validInputSchema), OutputSchema: json.RawMessage(`{"required":5}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := s.RegisterSkill(context.Background(), "search", tc.spec)
			if err == nil {
				t.Fatalf("RegisterSkill succeeded with %s and returned %s", tc.name, id)
			}
			if !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("want ErrInvalidEntry, got %v", err)
			}
			if id != "" {
				t.Errorf("a rejected entry must not get a version_id, got %q", id)
			}
		})
	}
}
