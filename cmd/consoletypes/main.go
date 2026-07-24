// Command consoletypes generates the console's data contract from the Go view structs (ADR-007).
//
// It emits TWO artifacts from ONE reflection pass:
//
//	schemas/console-view.schema.json        composes with the existing schemas/ discipline
//	web/console/src/lib/types.generated.ts  the console's only view-type import
//
// One pass rather than a pipeline, because a Go -> schema -> third-party-generator -> TypeScript chain
// has an intermediate that can go stale on its own, and its failure is silent. One pass also keeps a
// third-party code generator out of the build path of the surface whose defining property is that it
// holds a credential.
//
// # Why generate at all
//
// The failure mode of a hand-written type is not a compile error. It is a BLANK CELL IN PRODUCTION: a
// field renamed in Go becomes `undefined` in TypeScript and renders as an em-dash that looks exactly
// like legitimately absent data. A generated artifact with a drift gate turns a silent wrong answer
// into a red build.
//
// # Why it refuses rather than emitting `any`
//
// A construct the emitter does not understand — an interface field, a map with a non-string key, a
// type with a custom MarshalJSON — fails LOUDLY. Emitting `any` is how a generated contract quietly
// stops being a contract: the build stays green, the type-checker stops helping, and nobody finds out
// until the blank cell.
//
//	go run ./cmd/consoletypes            # write the artifacts
//	go run ./cmd/consoletypes -check     # regenerate into memory and diff — the CI gate
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
)

func main() {
	check := flag.Bool("check", false, "regenerate and fail on any difference from the checked-in artifacts")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	g := &generator{seen: map[reflect.Type]string{}, names: map[string]bool{}}
	for _, view := range api.ConsoleViewTypes() {
		if err := g.register(reflect.TypeOf(view.Sample), view.Name, view.Endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "consoletypes: %v\n", err)
			os.Exit(2)
		}
	}

	ts := g.emitTypeScript()
	schema, err := g.emitJSONSchema()
	if err != nil {
		fmt.Fprintf(os.Stderr, "consoletypes: %v\n", err)
		os.Exit(2)
	}

	artifacts := []struct {
		path    string
		content []byte
	}{
		{filepath.Join(*root, "web", "console", "src", "lib", "types.generated.ts"), ts},
		{filepath.Join(*root, "schemas", "console-view.schema.json"), schema},
	}

	if *check {
		drift := false
		for _, a := range artifacts {
			existing, err := os.ReadFile(a.path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "consoletypes: %s is missing — run `make console-types`\n", a.path)
				drift = true
				continue
			}
			if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(a.content)) {
				fmt.Fprintf(os.Stderr, "consoletypes: %s is out of date — a Go view type changed and the artifact did not.\n", a.path)
				fmt.Fprintf(os.Stderr, "  run: make console-types\n")
				drift = true
			}
		}
		if drift {
			// The whole point of the gate. A read-model change that does not reach the browser's types
			// is a change that reaches the browser as `undefined`.
			os.Exit(1)
		}
		fmt.Println("consoletypes: artifacts are current")
		return
	}

	for _, a := range artifacts {
		if err := os.WriteFile(a.path, a.content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "consoletypes: writing %s: %v\n", a.path, err)
			os.Exit(2)
		}
		fmt.Printf("consoletypes: wrote %s\n", a.path)
	}
}

// ── The generator ────────────────────────────────────────────────────────────

type field struct {
	name     string
	tsType   string
	optional bool
	doc      string
	// jsonSchema is the field's schema fragment, built in the same pass.
	jsonSchema map[string]any
	required   bool
}

type object struct {
	name     string
	endpoint string
	fields   []field
	order    int
}

type generator struct {
	objects []*object
	seen    map[reflect.Type]string
	names   map[string]bool
	next    int
}

var timeType = reflect.TypeOf(time.Time{})

// register walks a struct type, emitting an object for it and for every struct it reaches.
func (g *generator) register(t reflect.Type, name, endpoint string) error {
	t = deref(t)
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("%s: only structs may be registered, got %s", name, t.Kind())
	}
	if existing, ok := g.seen[t]; ok {
		if existing != name {
			// Two registry entries pointing at one Go type would emit one interface under two names,
			// and a reader would have no way to know they are the same contract.
			return fmt.Errorf("%s is already generated as %s", t.String(), existing)
		}
		return nil
	}
	if g.names[name] {
		return fmt.Errorf("two different Go types both want the TypeScript name %s", name)
	}
	g.seen[t] = name
	g.names[name] = true

	obj := &object{name: name, endpoint: endpoint, order: g.next}
	g.next++
	// Appended before the walk so a self-referential type terminates.
	g.objects = append(g.objects, obj)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported: never serialised, so never part of the contract
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		jsonName := parts[0]
		if jsonName == "" {
			if f.Anonymous {
				// An embedded struct with no json tag is inlined by encoding/json. Inline it here too,
				// rather than emitting a nested object the wire format does not have.
				inner := deref(f.Type)
				if inner.Kind() == reflect.Struct {
					if err := g.inline(obj, inner); err != nil {
						return err
					}
					continue
				}
			}
			jsonName = f.Name
		}
		optional := contains(parts[1:], "omitempty")

		tsType, schema, err := g.typeOf(f.Type, name+"."+f.Name)
		if err != nil {
			return err
		}
		obj.fields = append(obj.fields, field{
			name:       jsonName,
			tsType:     tsType,
			optional:   optional,
			jsonSchema: schema,
			required:   !optional,
		})
	}
	return nil
}

// inline folds an embedded struct's fields into the parent, matching encoding/json.
func (g *generator) inline(obj *object, t reflect.Type) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		parts := strings.Split(f.Tag.Get("json"), ",")
		jsonName := parts[0]
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = f.Name
		}
		tsType, schema, err := g.typeOf(f.Type, t.Name()+"."+f.Name)
		if err != nil {
			return err
		}
		obj.fields = append(obj.fields, field{
			name:       jsonName,
			tsType:     tsType,
			optional:   contains(parts[1:], "omitempty"),
			jsonSchema: schema,
			required:   !contains(parts[1:], "omitempty"),
		})
	}
	return nil
}

// typeOf maps a Go type to a TypeScript type and a JSON Schema fragment, refusing what it cannot map.
func (g *generator) typeOf(t reflect.Type, path string) (string, map[string]any, error) {
	switch t.Kind() {
	case reflect.Ptr:
		// A pointer field is nullable on the wire. `| null` rather than optional: the two mean
		// different things to a consumer, and conflating them is how a UI renders "absent" for a value
		// the server explicitly said was null.
		inner, schema, err := g.typeOf(t.Elem(), path)
		if err != nil {
			return "", nil, err
		}
		return inner + " | null", map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}, nil

	case reflect.String:
		return "string", map[string]any{"type": "string"}, nil

	case reflect.Bool:
		return "boolean", map[string]any{"type": "boolean"}, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number", map[string]any{"type": "integer"}, nil

	case reflect.Float32, reflect.Float64:
		return "number", map[string]any{"type": "number"}, nil

	case reflect.Slice, reflect.Array:
		// A nil slice marshals as `null`, not `[]`, unless the producer is careful. The contract says
		// so rather than pretending otherwise — a consumer that assumes an array gets a runtime error
		// exactly once, in production.
		inner, schema, err := g.typeOf(t.Elem(), path)
		if err != nil {
			return "", nil, err
		}
		return inner + "[] | null", map[string]any{
			"anyOf": []any{map[string]any{"type": "array", "items": schema}, map[string]any{"type": "null"}},
		}, nil

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", nil, fmt.Errorf("%s: a map with a %s key has no JSON representation this generator will guess at", path, t.Key().Kind())
		}
		inner, schema, err := g.typeOf(t.Elem(), path)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Record<string, %s>", inner), map[string]any{
			"type": "object", "additionalProperties": schema,
		}, nil

	case reflect.Struct:
		if t == timeType {
			// RFC 3339 on the wire. Rendered through the console's pinned en-US formatter, never by
			// the browser's own locale.
			return "string", map[string]any{"type": "string", "format": "date-time"}, nil
		}
		name := g.nameFor(t)
		if err := g.register(t, name, ""); err != nil {
			return "", nil, err
		}
		return name, map[string]any{"$ref": "#/$defs/" + name}, nil

	case reflect.Interface:
		// Deliberately refused. An `any` here is a contract that has quietly stopped being one.
		return "", nil, fmt.Errorf("%s: an interface field has no static shape — give it a concrete type or exclude it from the console contract", path)

	default:
		return "", nil, fmt.Errorf("%s: this generator does not map %s", path, t.Kind())
	}
}

// nameFor derives the TypeScript name for a nested struct.
func (g *generator) nameFor(t reflect.Type) string {
	if name, ok := g.seen[t]; ok {
		return name
	}
	name := t.Name()
	if name == "" {
		name = "Anonymous"
	}
	// Four of the source types are unexported (`runView`, `nodeView`, …) because they are internal to
	// the api package. Their TypeScript counterparts are not internal to anything, so the leading
	// letter is raised — a lowercase exported interface reads as a mistake and invites a "cleanup"
	// that would break every import.
	name = strings.ToUpper(name[:1]) + name[1:]
	// Two packages may both declare `View`. Qualify with the package when the bare name is taken.
	if g.names[name] {
		pkg := t.PkgPath()
		if i := strings.LastIndex(pkg, "/"); i >= 0 {
			pkg = pkg[i+1:]
		}
		name = strings.ToUpper(pkg[:1]) + pkg[1:] + name
	}
	return name
}

// ── Emitters ─────────────────────────────────────────────────────────────────

const header = `// Code generated by cmd/consoletypes. DO NOT EDIT.
//
// The console's data contract, generated from the Go view structs (docs/adr/ADR-007-console-type-generation.md).
//
// This file is CHECKED IN and regenerated in CI; a difference fails the build. That gate is the whole
// point: the failure mode of a hand-maintained type is not a compile error, it is a blank cell in
// production, because a field renamed in Go becomes ` + "`undefined`" + ` in TypeScript and renders as an
// em-dash that looks exactly like legitimately absent data.
//
// Two shapes to note, because they are contract facts rather than generator quirks:
//
//   ` + "`T[] | null`" + `   a nil Go slice marshals as null, not []. The contract says so rather than
//                  pretending otherwise; a consumer that assumes an array fails exactly once, in
//                  production.
//   ` + "`T | null`" + `     a pointer field is nullable. Null and ABSENT are different facts, and
//                  conflating them makes a UI render "absent" for a value the server called null.
//
// To change anything here: change the Go view struct, then run ` + "`make console-types`" + `.

`

func (g *generator) emitTypeScript() []byte {
	var b bytes.Buffer
	b.WriteString(header)

	objects := append([]*object(nil), g.objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].order < objects[j].order })

	for _, obj := range objects {
		if obj.endpoint != "" {
			fmt.Fprintf(&b, "/** Response of `%s`. */\n", obj.endpoint)
		}
		fmt.Fprintf(&b, "export interface %s {\n", obj.name)
		for _, f := range obj.fields {
			optional := ""
			if f.optional {
				optional = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", tsKey(f.name), optional, f.tsType)
		}
		b.WriteString("}\n\n")
	}
	return b.Bytes()
}

func (g *generator) emitJSONSchema() ([]byte, error) {
	defs := map[string]any{}
	objects := append([]*object(nil), g.objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].name < objects[j].name })

	roots := map[string]any{}
	for _, obj := range objects {
		props := map[string]any{}
		required := []string{}
		for _, f := range obj.fields {
			props[f.name] = f.jsonSchema
			if f.required {
				required = append(required, f.name)
			}
		}
		sort.Strings(required)
		def := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			def["required"] = required
		}
		if obj.endpoint != "" {
			def["description"] = "Response of " + obj.endpoint
			roots[obj.name] = map[string]any{"$ref": "#/$defs/" + obj.name}
		}
		defs[obj.name] = def
	}

	doc := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://heros.dev/schemas/console-view.schema.json",
		"title":       "Console view models",
		"description": "Generated by cmd/consoletypes from the Go view structs the P9 console renders. DO NOT EDIT.",
		"$defs":       defs,
		"properties":  roots,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// tsKey quotes a JSON key that is not a bare TypeScript identifier.
func tsKey(name string) string {
	for i, r := range name {
		valid := r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !valid {
			return `"` + name + `"`
		}
	}
	if name == "" {
		return `""`
	}
	return name
}
