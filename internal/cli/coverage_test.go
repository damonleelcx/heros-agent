package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
)

// ── P13 21.1 / P14 11.20 / P15 20.15 / P16 10.16 🔴 — the offline table, versioned ───────────────

// The command answers with NO account and NO network, from the engine's own table, and names the
// table's version. That version is what turns "the CLI refused what the console accepted" from a
// mystery into a one-line diagnosis.
func TestCoverageIsOfflineAndVersioned(t *testing.T) {
	var out, errbuf bytes.Buffer
	code := Main([]string{"coverage"}, Streams{Out: &out, Err: &errbuf},
		func(string) (string, bool) { return "", false }, nil)
	if code != ExitOK {
		t.Fatalf("coverage must succeed offline, got exit %d\n%s", code, errbuf.String())
	}

	var env struct {
		Data struct {
			Version   string                   `json:"coverage_version"`
			Languages []string                 `json:"registered_languages"`
			Causes    []string                 `json:"cause_classes"`
			Cells     []transform.CoverageCell `json:"cells"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("machine output must be the standard envelope: %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(env.Data.Version, "cov-") {
		t.Errorf("the coverage table must carry a self-identifying version, got %q", env.Data.Version)
	}
	if env.Data.Version != transform.CoverageTableVersion() {
		t.Errorf("the CLI reported version %q, the engine says %q — the CLI is carrying its own table",
			env.Data.Version, transform.CoverageTableVersion())
	}
	if len(env.Data.Causes) != 3 {
		t.Errorf("the three cause classes must be reported, got %v", env.Data.Causes)
	}

	// 🔴 TOTALITY, over the wire: every registered language appears on every axis. A consumer reading
	// this JSON must never have to interpret an absence.
	seen := map[string]map[string]bool{}
	for _, c := range env.Data.Cells {
		if seen[c.Axis] == nil {
			seen[c.Axis] = map[string]bool{}
		}
		seen[c.Axis][c.Language] = true
	}
	for _, axis := range transform.CoverageAxes() {
		for _, lang := range env.Data.Languages {
			if !seen[axis][lang] {
				t.Errorf("axis %q has no cell for %q in the CLI's output", axis, lang)
			}
		}
	}

	// 🚫 And the narration says the thing a reader most needs: a gap is not a plan boundary.
	if !strings.Contains(errbuf.String(), "identical on every plan") {
		t.Errorf("the narration does not state that coverage is plan-invariant:\n%s", errbuf.String())
	}
}

// The refusal suffix names the local table's version, so an offline verdict that differs from the
// hosted one is attributable.
func TestCoverageRefusalSuffixNamesTheVersion(t *testing.T) {
	got := CoverageRefusalSuffix()
	if !strings.Contains(got, transform.CoverageTableVersion()) {
		t.Errorf("the offline refusal suffix does not name the table version: %q", got)
	}
}

// `status` surfaces the same version, from the same read — an operator should not need a second command
// to know which table this binary refuses from.
func TestStatusReportsTheCoverageVersion(t *testing.T) {
	var out, errbuf bytes.Buffer
	code := Main([]string{"status"}, Streams{Out: &out, Err: &errbuf},
		func(string) (string, bool) { return "", false }, nil)
	if code != ExitOK {
		t.Fatalf("status must succeed offline, got exit %d\n%s", code, errbuf.String())
	}
	if !strings.Contains(errbuf.String(), transform.CoverageTableVersion()) {
		t.Errorf("status does not report the coverage table version:\n%s", errbuf.String())
	}
}
