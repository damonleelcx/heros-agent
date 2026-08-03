package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeReg struct {
	entries []registry.ModelCatalogEntry
	err     error
}

func (f fakeReg) ModelCatalog(context.Context) ([]registry.ModelCatalogEntry, error) {
	return f.entries, f.err
}

const twoModels = `{"models":[
  {"name":"big","tier":3,"cost_per_run":0.05,"latency_ms":900},
  {"name":"small","tier":1,"cost_per_run":0.004,"latency_ms":200}
]}`

func TestMenuIsTheJoinOfPublishedAndRegistered(t *testing.T) {
	src := NewFileSource(write(t, twoModels))
	reg := fakeReg{entries: []registry.ModelCatalogEntry{
		{VersionID: strings.Repeat("1", 64), Name: "big", Provider: "anthropic", ModelID: "big-1"},
		{VersionID: strings.Repeat("2", 64), Name: "small", Provider: "anthropic", ModelID: "small-1"},
	}}

	menu, rep, err := Menu(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("menu: %v", err)
	}
	if len(menu.Models) != 2 || rep.Usable != 2 {
		t.Fatalf("menu = %+v, report = %+v", menu.Models, rep)
	}
	// The REF is the registry's, not the catalog's: an operator emits it and it must resolve.
	if menu.Models[0].Ref != strings.Repeat("2", 64) || menu.Models[0].Tier != 1 {
		t.Errorf("menu is not ordered by tier or lost the registry ref: %+v", menu.Models[0])
	}
	if menu.Models[0].ModelID != "small-1" {
		t.Errorf("the model id must come from the registry entry, got %q", menu.Models[0].ModelID)
	}
}

// 🔴 A registered model with no published tier is SKIPPED and REPORTED, never defaulted to tier 0.
// `cheaperModels` selects strictly below the current tier, so a tier-0 model is cheaper than
// everything — an unjudged model would silently become the first thing proposed.
func TestAnUnjudgedModelIsSkippedAndReported(t *testing.T) {
	src := NewFileSource(write(t, twoModels))
	reg := fakeReg{entries: []registry.ModelCatalogEntry{
		{VersionID: strings.Repeat("1", 64), Name: "big", Provider: "anthropic", ModelID: "big-1"},
		{VersionID: strings.Repeat("3", 64), Name: "experimental", Provider: "anthropic", ModelID: "x-1"},
	}}

	menu, rep, err := Menu(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("menu: %v", err)
	}
	for _, m := range menu.Models {
		if m.ModelID == "x-1" {
			t.Fatal("an unjudged model reached the menu — it would rank as cheaper than everything " +
				"published and be the first candidate proposed")
		}
		if m.Tier <= 0 {
			t.Errorf("a tier-0 model reached the menu: %+v", m)
		}
	}
	if len(rep.Unjudged) != 1 || rep.Unjudged[0] != "experimental" {
		t.Errorf("the unjudged model was not reported: %+v", rep.Unjudged)
	}
	// The other direction: `small` is published and not registered.
	if len(rep.Unregistered) != 1 || rep.Unregistered[0] != "small" {
		t.Errorf("a published model the registry cannot resolve was not reported: %+v", rep.Unregistered)
	}
}

func TestNoCatalogIsAConditionNotACrash(t *testing.T) {
	if _, _, err := Menu(context.Background(), nil, fakeReg{}); err == nil {
		t.Fatal("a nil source produced a menu")
	} else if err != ErrNoCatalog {
		t.Errorf("expected ErrNoCatalog so the caller can report a state, got %v", err)
	}
}

func TestAMalformedCatalogIsRefusedByName(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"no models":     {`{"models":[]}`, "publishes no models"},
		"nameless":      {`{"models":[{"tier":1}]}`, "entry with no name"},
		"duplicate":     {`{"models":[{"name":"a","tier":1},{"name":"a","tier":2}]}`, "twice"},
		"zero tier":     {`{"models":[{"name":"a","tier":0}]}`, "tier 0"},
		"negative cost": {`{"models":[{"name":"a","tier":1,"cost_per_run":-1}]}`, "negative"},
		"not json":      {`tier: 1`, "parse"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewFileSource(write(t, tc.body)).Load()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected the refusal to name %q, got: %v", tc.want, err)
			}
		})
	}
}

// The path is named; the contents are not. A Describe that echoed the catalog would put prices in a
// boot log.
func TestDescribeNamesThePathAndNotTheContents(t *testing.T) {
	p := write(t, twoModels)
	d := NewFileSource(p).Describe()
	if !strings.Contains(d, filepath.Base(p)) {
		t.Errorf("Describe does not name the path: %q", d)
	}
	for _, priced := range []string{"0.05", "cost_per_run", "tier"} {
		if strings.Contains(d, priced) {
			t.Errorf("Describe leaks the catalog's contents (%q): %q", priced, d)
		}
	}
}

func TestFileSourceFromEnvReportsWhenUnset(t *testing.T) {
	t.Setenv(PathEnv, "")
	if _, ok := FileSourceFromEnv(); ok {
		t.Error("an unset variable produced a source pointing at a guessed path")
	}
	t.Setenv(PathEnv, "/etc/heros/models.json")
	if s, ok := FileSourceFromEnv(); !ok || s.Path != "/etc/heros/models.json" {
		t.Errorf("FileSourceFromEnv = %+v, %v", s, ok)
	}
}
