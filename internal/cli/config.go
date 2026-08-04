package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// config.go is configuration resolution with PROVENANCE (PRD FR7, task 2.6). A value resolves in a
// documented order — flags → environment → project file → defaults — and `status` reports each effective
// value together with WHERE it came from. A config that resolves from four places and explains nothing
// is a support-ticket generator (design, Product lens), so provenance is a first-class output, not a
// debug afterthought.

// Source is where a resolved value came from, in precedence order (highest first).
type Source string

const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceDefault Source = "default"
)

// EnvPrefix is the environment-variable namespace. HEROS_REPO, HEROS_SEEDS, …
const EnvPrefix = "HEROS_"

// ProjectFile is the project configuration file name, resolved from the working directory.
const ProjectFile = ".heros.json"

// Resolved is one effective setting and its provenance.
type Resolved struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source Source `json:"source"`
	// Overridden lists sources that also supplied this key but lost to precedence — so `status` can show
	// "flag (also set in: env, file)" and a user can see why a file value is being ignored.
	Overridden []Source `json:"overridden,omitempty"`
}

// Config is the resolved configuration for one invocation. It is built once from (flags, env, file,
// defaults) and then read; nothing mutates it after resolution.
type Config struct {
	values map[string]Resolved
}

// Resolver accumulates the raw inputs from each source, then resolves them in precedence order.
type Resolver struct {
	flags    map[string]string
	env      map[string]string
	file     map[string]string
	defaults map[string]string
}

// NewResolver seeds a resolver with defaults. Flags/env/file are layered on with the With* methods.
func NewResolver(defaults map[string]string) *Resolver {
	return &Resolver{
		flags:    map[string]string{},
		env:      map[string]string{},
		file:     map[string]string{},
		defaults: cloneMap(defaults),
	}
}

// SetFlag records a flag value (highest precedence). Only call for flags the user actually set — a
// flag left at its zero value must not shadow env/file, so the caller passes flag.Visit results.
func (r *Resolver) SetFlag(key, val string) { r.flags[key] = val }

// LoadEnv reads HEROS_<KEY> for every known key. environ is injected for testability.
func (r *Resolver) LoadEnv(environ func(string) (string, bool)) {
	for key := range r.defaults {
		if v, ok := environ(EnvPrefix + strings.ToUpper(key)); ok && v != "" {
			r.env[key] = v
		}
	}
}

// LoadFile reads a project JSON file of {key: value}. A missing file is not an error (the local
// workflow needs no config); an unreadable or malformed one IS — fail loud, never silently ignore
// (backend rule 1). It returns an invalid-config ExitError on malformed content.
func (r *Resolver) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return invalidConfig(fmt.Sprintf("cannot read project config %s: %v", path, err))
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return invalidConfig(fmt.Sprintf("project config %s is not valid JSON: %v", path, err))
	}
	for k, v := range raw {
		r.file[strings.ToLower(k)] = fmt.Sprint(v)
	}
	return nil
}

// Resolve produces the Config, choosing the highest-precedence source per key and recording which
// lower sources it overrode.
func (r *Resolver) Resolve() Config {
	out := map[string]Resolved{}
	keys := map[string]bool{}
	for k := range r.defaults {
		keys[k] = true
	}
	for k := range r.file {
		keys[k] = true
	}
	for k := range r.env {
		keys[k] = true
	}
	for k := range r.flags {
		keys[k] = true
	}
	for k := range keys {
		layers := []struct {
			src Source
			m   map[string]string
		}{
			{SourceFlag, r.flags},
			{SourceEnv, r.env},
			{SourceFile, r.file},
			{SourceDefault, r.defaults},
		}
		var chosen *Resolved
		var also []Source
		for _, l := range layers {
			if v, ok := l.m[k]; ok {
				if chosen == nil {
					chosen = &Resolved{Key: k, Value: v, Source: l.src}
				} else {
					also = append(also, l.src)
				}
			}
		}
		if chosen != nil {
			chosen.Overridden = also
			out[k] = *chosen
		}
	}
	return Config{values: out}
}

// Get returns the effective string value for key ("" if unset).
func (c Config) Get(key string) string { return c.values[key].Value }

// Has reports whether key resolved to a non-empty value.
func (c Config) Has(key string) bool { return c.values[key].Value != "" }

// Int returns key as an int, or def if unset/unparseable. A malformed numeric value is a caller
// concern surfaced via Require*, not silently defaulted here on the primary path.
func (c Config) Int(key string, def int) int {
	if v := c.values[key].Value; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Require returns key's value or an invalid-config error NAMING the missing input (task 2.5). It never
// prompts.
func (c Config) Require(key string) (string, error) {
	if v := c.values[key].Value; v != "" {
		return v, nil
	}
	return "", invalidConfig(fmt.Sprintf("missing required input %q — supply --%s, %s%s, or set it in %s",
		key, key, EnvPrefix, strings.ToUpper(key), ProjectFile))
}

// resolveRepo returns the repository path to analyze, defaulting to ".", and REFUSES a path that is not
// a readable directory.
//
// 🔴 Without this check the whole tool reports success over nothing. `discovery.Run` walks a tree; a path
// that is not there yields no files, no error, and an empty IR — so `heros discover --repo /typo` printed
// `ok: true`, `0 nodes`, exit 0, and wrote an EMPTY ir.json over whatever ir.json was already in the
// working directory. "This repository has no LLM call sites" and "I could not find this repository" are
// opposite conclusions with opposite remedies, and the tool was answering the first for both. Worse, the
// answer is the reassuring one: a CI step that fails to check out the repo passes discovery.
//
// It is ExitInvalidCfg, not ExitOperational: the invocation names a path that is not a repository, and
// the remedy is to fix the invocation (see the exit-code contract in exit.go). Every command that
// discovers goes through here so `eval`, `apply` and `author` cannot each answer this differently —
// which they did, because each read cfg.Get("repo") directly.
func (c Config) resolveRepo() (string, error) {
	repo := c.Get("repo")
	if repo == "" {
		repo = "."
	}
	info, err := os.Stat(repo)
	if err != nil {
		if os.IsNotExist(err) {
			return "", invalidConfig(fmt.Sprintf("--repo %q does not exist — this is not a repository with no LLM call sites, it is a path that is not there", repo))
		}
		return "", invalidConfig(fmt.Sprintf("--repo %q cannot be read: %v", repo, err))
	}
	if !info.IsDir() {
		return "", invalidConfig(fmt.Sprintf("--repo %q is a file, not a directory", repo))
	}
	return repo, nil
}

// Effective returns every resolved setting, sorted by key, for `status`.
func (c Config) Effective() []Resolved {
	out := make([]Resolved, 0, len(c.values))
	for _, v := range c.values {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
