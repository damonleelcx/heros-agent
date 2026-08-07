// Package modelcatalog publishes the capability TIER and the cost/latency estimate for each model this
// deployment can propose, and joins them onto the registry's model entries to build a proposal.Menu.
//
// # Why this exists as its own published file
//
// `proposal.Menu.cheaperModels(tier)` is the entire input to the model-downgrade operator — the one
// operator that fires on a cost bottleneck with no diagnosis, and therefore the only one a hosted
// platform can drive from what it legitimately has. It selects on `ModelChoice.Tier` and `CostPerRun`.
//
// 🔴 The registry records NEITHER. `registry.ModelCatalogEntry` is {VersionID, Name, Provider, ModelID,
// Params}, and `ModelParams` is temperature / max_tokens / thinking_budget / seed / timeout. Every
// `proposal.Menu` anywhere in this repository is hand-written inside a `cmd/proof` binary. So without
// this file a generator would run its whole pipeline and emit zero candidates on every deployment —
// not because nothing is wrong with the workflow, but because "cheaper" is not expressible.
//
// A tier is a JUDGEMENT (which model is stronger) and a cost-per-run is a PRICE. Neither is derivable
// from a model entry, and inventing either would mean the platform proposing changes to a customer's
// code on the strength of numbers it made up. So they are published, the same way plan prices are:
// deployment configuration read from a path, never git-tracked, absent by default — and a deployment
// with no catalog reports that as a first-class state rather than as "no proposals found".
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
)

// PathEnv is the environment variable naming the published catalog, beside PLAN_CATALOG_PATH.
const PathEnv = "MODEL_CATALOG_PATH"

// ErrNoCatalog is returned when no catalog is published. It is a CONDITION, not a failure: the caller
// reports "this deployment publishes no model catalog", which names an action an operator can take.
var ErrNoCatalog = errors.New("modelcatalog: no model catalog is published")

// Entry is one model's published judgement. It is keyed by the registry entry's NAME rather than by its
// version_id: a tier is a property of the model, and re-registering the same model with a new
// temperature mints a new version_id that would silently fall out of a version-keyed catalog — leaving
// the operator with one fewer candidate and no way to notice.
type Entry struct {
	Name string `json:"name"`
	// Tier is the capability ordering; a higher tier is a stronger model. Downgrade descends it.
	Tier int `json:"tier"`
	// CostPerRun is the estimated $ per run at a node using this model. A PRE-VERIFICATION estimate
	// used only to order and describe candidates — the measured verdict replaces it, and nothing is
	// recommended on the strength of this number alone.
	CostPerRun float64 `json:"cost_per_run"`
	// LatencyMS is the estimated per-run latency, for the latency-SLA constraint check.
	LatencyMS float64 `json:"latency_ms"`

	// Provider and ModelID are the REGISTRY half of this row: what `Name` refers to.
	//
	// 🔴 Both are optional, and the reason they exist here at all is a gap this file's own header
	// describes without noticing. `Menu` JOINS published judgements onto registered models by name — so
	// a deployment whose registry has no entry for "Claude Sonnet 5" gets an empty menu, an empty
	// `/api/v1/models`, and a Studio matrix with columns and NO ROWS. The only caller of
	// `registry.RegisterModel` in this repository is a demo binary, so that was every real deployment.
	//
	// They are DECLARED here rather than inferred from the name because a provider and a model id are
	// facts about what this deployment can resolve, and guessing "Claude Sonnet 5" → `anthropic/
	// claude-sonnet-5` would be the platform inventing an upstream identifier — the same class of
	// mistake as inventing a tier. An entry that omits them publishes a judgement about a model somebody
	// else registered, which is exactly what this file did before and stays valid.
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
}

// Registrable reports whether this entry declares enough to create the registry entry it names.
func (e Entry) Registrable() bool {
	return strings.TrimSpace(e.Provider) != "" && strings.TrimSpace(e.ModelID) != ""
}

type catalogFile struct {
	Models []Entry `json:"models"`
}

// Source loads the published catalog.
type Source interface {
	// Load returns the published entries, or ErrNoCatalog when none is published.
	Load() ([]Entry, error)
	// Describe names the source — the path, never its contents.
	Describe() string
}

// FileSource reads the catalog from a path. Never git-tracked: it carries prices.
type FileSource struct{ Path string }

// NewFileSource returns a source over a published catalog file.
func NewFileSource(path string) *FileSource { return &FileSource{Path: path} }

// FileSourceFromEnv builds a FileSource from PathEnv, or reports that none is configured. It returns
// (nil, false) rather than defaulting to a path nobody set: a guessed path that happens not to exist
// and a path deliberately left unset are the same failure with different remedies.
func FileSourceFromEnv() (*FileSource, bool) {
	p := strings.TrimSpace(os.Getenv(PathEnv))
	if p == "" {
		return nil, false
	}
	// 🔴 A path that names nothing is NOT a published catalog. The deploy manifests declare this
	// variable — it is where the file GOES — so a set path is a convention, not a claim that somebody
	// put one there. Reporting the two apart is what keeps "this deployment publishes no model catalog"
	// (a clean state with a next action) from becoming "read /etc/heros/models.json: no such file"
	// surfacing as a generator failure.
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return nil, false
	}
	return NewFileSource(p), true
}

func (f *FileSource) Describe() string { return "file:" + filepath.Clean(f.Path) }

// Load reads and validates the published catalog.
func (f *FileSource) Load() ([]Entry, error) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("modelcatalog: read %q: %w", f.Path, err)
	}
	var cf catalogFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("modelcatalog: parse %q: %w", f.Path, err)
	}
	if len(cf.Models) == 0 {
		// An empty catalog is refused rather than accepted as "no models". A published file with no
		// models is far more likely to be a typo in the key than a deliberate statement, and accepting it
		// produces exactly the silent-empty-menu outcome this package exists to prevent.
		return nil, fmt.Errorf("modelcatalog: %q publishes no models — an empty catalog and no catalog "+
			"are different things, and this file says neither", f.Path)
	}
	seen := map[string]bool{}
	for _, e := range cf.Models {
		if e.Name == "" {
			return nil, fmt.Errorf("modelcatalog: %q has an entry with no name", f.Path)
		}
		if seen[e.Name] {
			// Two tiers for one model is not a merge to resolve quietly: whichever wins decides which
			// candidates a customer is offered.
			return nil, fmt.Errorf("modelcatalog: %q publishes %q twice", f.Path, e.Name)
		}
		seen[e.Name] = true
		if e.Tier <= 0 {
			return nil, fmt.Errorf("modelcatalog: %q gives %q tier %d — a tier orders capability and "+
				"starts at 1; zero is what an UNPUBLISHED model already means to the operator",
				f.Path, e.Name, e.Tier)
		}
		if e.CostPerRun < 0 || e.LatencyMS < 0 {
			return nil, fmt.Errorf("modelcatalog: %q gives %q a negative cost or latency", f.Path, e.Name)
		}
	}
	return cf.Models, nil
}

// ModelLister is the registry read this package joins against.
type ModelLister interface {
	ModelCatalog(ctx context.Context) ([]registry.ModelCatalogEntry, error)
}

// Menu builds the proposal menu by joining the published judgements onto the REGISTERED models.
//
// # Why it is a join and not either half alone
//
// The registry knows which models this deployment can actually resolve — a `Ref` an operator emits must
// be a real version_id or the candidate resolves to nothing. The catalog knows which is stronger and
// what each costs. A menu from the catalog alone would name models that cannot be resolved; a menu from
// the registry alone cannot say which is cheaper. Both halves, or no menu.
//
// 🔴 A registered model with no published entry is SKIPPED and REPORTED, never defaulted to tier 0.
// Tier 0 is not a neutral placeholder: `cheaperModels` selects strictly below the current tier, so an
// unjudged model would silently become the cheapest thing on the menu and the first candidate proposed.
// The returned Unjudged list is what lets a caller say "three models are registered but not published"
// instead of offering a downgrade to a model nobody rated.
func Menu(ctx context.Context, src Source, reg ModelLister) (proposal.Menu, Report, error) {
	if src == nil {
		return proposal.Menu{}, Report{}, ErrNoCatalog
	}
	published, err := src.Load()
	if err != nil {
		return proposal.Menu{}, Report{}, err
	}
	byName := make(map[string]Entry, len(published))
	for _, e := range published {
		byName[e.Name] = e
	}

	registered, err := reg.ModelCatalog(ctx)
	if err != nil {
		return proposal.Menu{}, Report{}, fmt.Errorf("modelcatalog: read the model registry: %w", err)
	}

	rep := Report{Source: src.Describe(), Published: len(published), Registered: len(registered)}
	var models []proposal.ModelChoice
	matched := map[string]bool{}
	for _, r := range registered {
		e, ok := byName[r.Name]
		if !ok {
			rep.Unjudged = append(rep.Unjudged, r.Name)
			continue
		}
		matched[r.Name] = true
		models = append(models, proposal.ModelChoice{
			Ref: r.VersionID, Provider: r.Provider, ModelID: r.ModelID,
			Tier: e.Tier, CostPerRun: e.CostPerRun, LatencyMS: e.LatencyMS,
			Thinking: r.Params.ThinkingBudget != nil && *r.Params.ThinkingBudget > 0,
		})
	}
	// A published model nobody registered is reported too, in the other direction: it means the catalog
	// names a model this deployment cannot resolve, which is a publishing mistake rather than a gap.
	for _, e := range published {
		if !matched[e.Name] {
			rep.Unregistered = append(rep.Unregistered, e.Name)
		}
	}
	sort.Strings(rep.Unjudged)
	sort.Strings(rep.Unregistered)
	// Deterministic order: tier ascending, then ref. The operator sorts its own selections, but a menu
	// whose order depends on a map iteration makes every downstream test flaky for no reason.
	sort.Slice(models, func(i, j int) bool {
		if models[i].Tier != models[j].Tier {
			return models[i].Tier < models[j].Tier
		}
		return models[i].Ref < models[j].Ref
	})
	rep.Usable = len(models)
	return proposal.Menu{Models: models}, rep, nil
}

// Report is the account of what the join found. It is returned rather than logged: a generator that
// produced no candidates must be able to say WHY, and "every registered model is unjudged" is a
// different sentence from "this workflow has no cost bottleneck".
type Report struct {
	Source     string `json:"source"`
	Published  int    `json:"published"`
	Registered int    `json:"registered"`
	Usable     int    `json:"usable"`
	// Unjudged are registered models with no published tier. They are NOT on the menu.
	Unjudged []string `json:"unjudged,omitempty"`
	// Unregistered are published models the registry cannot resolve.
	Unregistered []string `json:"unregistered,omitempty"`
}

// ModelRegistrar is the registry write this package needs to seed. One method, so a caller can see at
// the type level that seeding cannot read, delete or deprecate anything.
type ModelRegistrar interface {
	RegisterModel(ctx context.Context, name string, spec registry.ModelSpec) (string, error)
}

// SeedReport is what a seeding pass did, for the operator log. Counts, never a spec.
type SeedReport struct {
	// Registered is how many entries were published to the registry.
	Registered int
	// Undeclared is how many published entries carry a judgement but no provider/model_id. Legitimate —
	// they describe models registered by some other route — and reported so a deployment whose matrix is
	// empty can tell "nobody declared them" from "seeding failed".
	Undeclared int
	// Failed names the entries that could not be registered, with the reason.
	Failed map[string]string
}

// SeedRegistry publishes every catalog entry that declares a provider and a model id.
//
// # Idempotent by construction, not by check
//
// `RegisterModel` is content-addressed: registering identical content returns the same version_id and
// publishes nothing. So this runs on every boot with no "have I already done this" state to get wrong —
// and an operator who edits the catalog gets a NEW version on the next boot while the old one stays
// resolvable, which is what any spec pinning it needs.
//
// 🚫 It never deletes or deprecates. A model that disappears from the published catalog stops being
// OFFERED (the join in Menu drops it) and stays RESOLVABLE, because a config_hash somewhere may pin it
// and a deployment that unregisters on edit would break specs it already accepted.
//
// A single entry failing does not abort the pass: the remaining models are still worth having, and the
// report names what did not land rather than leaving one bad row to empty the whole matrix.
func SeedRegistry(ctx context.Context, src Source, reg ModelRegistrar) (SeedReport, error) {
	rep := SeedReport{Failed: map[string]string{}}
	if src == nil || reg == nil {
		return rep, ErrNoCatalog
	}
	published, err := src.Load()
	if err != nil {
		return rep, err
	}
	for _, e := range published {
		if !e.Registrable() {
			rep.Undeclared++
			continue
		}
		if _, err := reg.RegisterModel(ctx, e.Name, registry.ModelSpec{
			Provider: strings.TrimSpace(e.Provider),
			ModelID:  strings.TrimSpace(e.ModelID),
		}); err != nil {
			rep.Failed[e.Name] = err.Error()
			continue
		}
		rep.Registered++
	}
	return rep, nil
}
