package discovery

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SymbolKind is how an SDK entrypoint is reached at a call site (design doc 04 §2.2).
type SymbolKind string

const (
	PackageFunc         SymbolKind = "package_func"          // llms.GenerateFromSinglePrompt(...)
	ClientMethod        SymbolKind = "client_method"         // client.CreateChatCompletion(...)
	NestedServiceMethod SymbolKind = "nested_service_method" // client.Messages.New / client.Chat.Completions.New
	InterfaceMethod     SymbolKind = "interface_method"      // llms.Model.GenerateContent
)

// LocatorForm is how an argument value is addressed (design doc 04 §2.4). Four real forms plus const/opaque.
type LocatorForm string

const (
	LocPositional LocatorForm = "positional" // f(ctx, prompt) -> index 1
	LocParamName  LocatorForm = "name"       // by parameter name
	LocFieldPath  LocatorForm = "field"      // struct-field path on an options/request struct (the common case)
	LocOptionCtor LocatorForm = "option"     // variadic functional option: WithModel(x)
	LocConst      LocatorForm = "const"      // declaration asserts a literal value (llm-eval only)
	LocOpaque     LocatorForm = "opaque"     // inherently unresolvable => sentinel + flag (I5)
)

// ArgLocator says WHERE an IR field lives in a call. The extractor (§4) decides resolvable-vs-unresolved;
// the locator only names the location. Its YAML is a shorthand: {field: "x"} | {option: "WithModel"} |
// {index: 1} | {name: "prompt"} | {const: [...]} | {form: opaque}.
type ArgLocator struct {
	Form   LocatorForm
	Index  int
	Name   string
	Field  string
	Option string
	Const  any
	Unwrap []string // value-wrappers to see through: ["aws.String"], ["openai.F"], ["anthropic.F"]
}

// UnmarshalYAML infers Form from which shorthand key is present, so rows/declarations stay terse.
func (a *ArgLocator) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Index  *int     `yaml:"index"`
		Name   string   `yaml:"name"`
		Field  string   `yaml:"field"`
		Option string   `yaml:"option"`
		Const  any      `yaml:"const"`
		Form   string   `yaml:"form"`
		Unwrap []string `yaml:"unwrap"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	a.Unwrap = raw.Unwrap
	switch {
	case raw.Form == string(LocOpaque):
		a.Form = LocOpaque
	case raw.Const != nil:
		a.Form, a.Const = LocConst, raw.Const
	case raw.Index != nil:
		a.Form, a.Index = LocPositional, *raw.Index
	case raw.Name != "":
		a.Form, a.Name = LocParamName, raw.Name
	case raw.Field != "":
		a.Form, a.Field = LocFieldPath, raw.Field
	case raw.Option != "":
		a.Form, a.Option = LocOptionCtor, raw.Option
	default:
		return fmt.Errorf("arg locator: no recognized form (index/name/field/option/const/opaque)")
	}
	return nil
}

// ArgMap says where model/prompt/tools live for one entrypoint. A nil locator => that field is
// unresolved (marked, not omitted — I5). Shared by registry rows and llm-eval declarations.
type ArgMap struct {
	Model  *ArgLocator `yaml:"model"`
	Prompt *ArgLocator `yaml:"prompt"`
	Tools  *ArgLocator `yaml:"tools"`
}

// SignatureRow is one registry entry: an import-path-qualified SDK entrypoint + its arg map.
type SignatureRow struct {
	ID           string     `yaml:"id"`
	Language     string     `yaml:"language"`    // the source language this row applies to (e.g. "go", "python")
	ImportPath   string     `yaml:"import_path"` // KEY ON THIS, never the local package name (doc 04 §2.3)
	SymbolKind   SymbolKind `yaml:"symbol_kind"`
	Selector     string     `yaml:"selector"` // the call selector chain as written at the site: "Messages.New"
	Streaming    bool       `yaml:"streaming"`
	ProviderHint string     `yaml:"provider_hint"`
	Opacity      []string   `yaml:"opacity"`
	ArgMap       ArgMap     `yaml:"arg_map"`
	VersionRange string     `yaml:"version_range"`
}

// Registry is the loaded signature table (data, not code — doc 04 §2.1).
type Registry struct {
	Rows []SignatureRow
}

// ForLanguage returns a registry view containing only the rows for one language (10.2). The detector for
// a given LanguageFrontend selects rows by the frontend's language, so a Python call can never match a Go
// row and vice-versa.
func (r *Registry) ForLanguage(lang string) *Registry {
	out := &Registry{}
	for i := range r.Rows {
		if r.Rows[i].Language == lang {
			out.Rows = append(out.Rows, r.Rows[i])
		}
	}
	return out
}

//go:embed registry.yaml
var defaultRegistryYAML []byte

// DefaultRegistry returns the embedded seed registry (the five verified SDKs, doc 04 §3).
func DefaultRegistry() (*Registry, error) { return LoadRegistry(defaultRegistryYAML) }

// LoadRegistry parses and validates a registry YAML. Fails loud on a structurally bad row (no silent
// fallback — the backend "fail loud" rule): a malformed registry is a config error, not "no SDKs".
func LoadRegistry(data []byte) (*Registry, error) {
	var doc struct {
		Version         string         `yaml:"version"`
		DefaultLanguage string         `yaml:"default_language"`
		Rows            []SignatureRow `yaml:"rows"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("registry: parse: %w", err)
	}
	if len(doc.Rows) == 0 {
		return nil, fmt.Errorf("registry: no rows")
	}
	defLang := doc.DefaultLanguage
	if defLang == "" {
		defLang = "go"
	}
	seen := map[string]bool{}
	for i := range doc.Rows {
		r := &doc.Rows[i]
		if r.ID == "" || r.ImportPath == "" || r.Selector == "" {
			return nil, fmt.Errorf("registry row %d: id, import_path and selector are all required", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("registry: duplicate row id %q", r.ID)
		}
		seen[r.ID] = true
		if r.SymbolKind == "" {
			r.SymbolKind = ClientMethod // sensible default; most SDK entrypoints are client methods
		}
		if r.Language == "" {
			r.Language = defLang
		}
	}
	return &Registry{Rows: doc.Rows}, nil
}
