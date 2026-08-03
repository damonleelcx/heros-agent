package deliveryroute

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// This file is the fence migration 0026 needed and did not have. It needs no database: the question it
// answers is "do the COLUMNS carry a whole Route", and the columns are a list of names.
//
// 0026's header said "read the type before writing the table" and then dropped `base_ref` because
// base_ref "is not a field of forgedelivery.Route" — true of the struct literal, false of the type,
// since Route.Target.Base is required by Target.Validate. A column list cannot catch that; the pgproof
// fence in internal/pgmigrate asserts the columns a store NAMES exist, and a store written against the
// same misreading would have named the same wrong set. Reducing a Route to the columns and rebuilding it
// is the check that does not depend on anybody having read the type correctly.

// columnsOf is the row this table stores, as Put writes it. Written out longhand rather than by calling
// scanRoute's inverse, because a helper shared with the store would let a field that never reaches the
// database round-trip through Go and pass.
type columns struct{ target, baseRef, forge, mode string }

func store(r fd.Route) columns {
	return columns{target: r.Target.Key(), baseRef: r.Target.Base, forge: string(r.ForgeKind), mode: string(r.Mode)}
}

// load rebuilds through the SAME scanRoute the store uses, so a parse bug fails here too.
func load(c columns) (fd.Route, error) {
	return scanRoute(fakeRow{c.target, c.baseRef, c.forge, c.mode})
}

type fakeRow []string

func (f fakeRow) Scan(dst ...any) error {
	if len(dst) != len(f) {
		return errors.New("column count mismatch")
	}
	for i := range dst {
		p, ok := dst[i].(*string)
		if !ok {
			return errors.New("this table stores only text columns")
		}
		*p = f[i]
	}
	return nil
}

func TestARouteSurvivesTheRoundTripThroughItsColumns(t *testing.T) {
	routes := map[string]fd.Route{
		"repository default workflow": {
			Mode:      fd.ModeCI,
			ForgeKind: fd.ForgeGitHub,
			Target:    fd.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"},
		},
		"one workflow of several in a monorepo": {
			Mode:      fd.ModeApp,
			ForgeKind: fd.ForgeGitHub,
			Target:    fd.Target{Owner: "acme", Repo: "platform", Base: "develop", Workflow: "billing"},
		},
		"a base branch that is not the default": {
			Mode:      fd.ModeCI,
			ForgeKind: fd.ForgeGitHub,
			Target:    fd.Target{Owner: "acme", Repo: "platform", Base: "release/2026.08"},
		},
	}

	for name, want := range routes {
		t.Run(name, func(t *testing.T) {
			if err := want.Validate(); err != nil {
				t.Fatalf("the fixture is not a deliverable route: %v", err)
			}

			got, err := load(store(want))
			if err != nil {
				t.Fatalf("rebuilding from the columns: %v", err)
			}

			// The whole point: what comes back must still be DELIVERABLE. On 0026's shape this is the
			// line that fails, with "has no base branch to open a pull request against" — and on a
			// deployment it would not have failed anywhere, because Service.Pending turns a Prepare
			// error into an absent list entry.
			if err := got.Validate(); err != nil {
				t.Fatalf("the row this table can hold is not a deliverable route: %v\n"+
					"a column a valid Route needs is missing from `columns` — add it to the table, not "+
					"to this struct", err)
			}
			if got != want {
				t.Errorf("round trip changed the route:\n stored: %+v\nloaded: %+v", want, got)
			}
		})
	}
}

// The fence has to be able to fail, so this checks it does — against the exact shape 0026 shipped.
func TestTheRoundTripFenceCatchesAMissingBaseRef(t *testing.T) {
	c := store(fd.Route{
		Mode:      fd.ModeCI,
		ForgeKind: fd.ForgeGitHub,
		Target:    fd.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"},
	})
	c.baseRef = "" // 0026: the column does not exist, so every read produces this

	got, err := load(c)
	if err != nil {
		t.Fatalf("rebuilding from the columns: %v", err)
	}
	err = got.Validate()
	if err == nil {
		t.Fatal("a route with no base branch validated — the fence cannot fail, so it proves nothing")
	}
	if !strings.Contains(err.Error(), "base branch") {
		t.Errorf("expected the failure to name the base branch, got: %v", err)
	}
}

func TestParseTargetInvertsTargetKey(t *testing.T) {
	for _, tc := range []struct {
		key, base string
		want      fd.Target
		wantErr   bool
	}{
		{key: "owner/repo", base: "main", want: fd.Target{Owner: "owner", Repo: "repo", Base: "main"}},
		{key: "owner/repo#wf", base: "main", want: fd.Target{Owner: "owner", Repo: "repo", Base: "main", Workflow: "wf"}},
		// A row no Put could have written. Refused by name rather than returned as a zero Target, which
		// would be reported downstream as "no base branch" — the wrong problem.
		{key: "no-slash", base: "main", wantErr: true},
		{key: "/repo", base: "main", wantErr: true},
		{key: "owner/", base: "main", wantErr: true},
	} {
		got, err := parseTarget(tc.key, tc.base)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseTarget(%q): expected a refusal, got %+v", tc.key, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTarget(%q): %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseTarget(%q) = %+v, want %+v", tc.key, got, tc.want)
		}
	}
}

// Put's validation must accept a declared-but-unimplemented forge and refuse an unknown one — the split
// migration 0026 states. Checked without a database because the refusal happens before the query.
func TestPutValidatesWithoutADatabase(t *testing.T) {
	s := &PGStore{db: (*sql.DB)(nil)} // never reached: every case below is refused before any query

	valid := fd.Target{Owner: "o", Repo: "r", Base: "main"}
	for name, tc := range map[string]struct {
		tenant string
		route  fd.Route
		want   string
	}{
		"no tenant": {
			tenant: "", route: fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: valid},
			want: "must name its tenant",
		},
		"no mode": {
			tenant: "t", route: fd.Route{ForgeKind: fd.ForgeGitHub, Target: valid},
			want: "is not a delivery mode",
		},
		"no base branch": {
			tenant: "t", route: fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub,
				Target: fd.Target{Owner: "o", Repo: "r"}},
			want: "no base branch",
		},
		"unknown forge": {
			tenant: "t", route: fd.Route{Mode: fd.ModeCI, ForgeKind: "sourcehut", Target: valid},
			want: "is not a known forge",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.Put(t.Context(), tc.tenant, tc.route)
			if err == nil {
				t.Fatalf("expected a refusal naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected a refusal naming %q, got: %v", tc.want, err)
			}
		})
	}
}

// Storing a gitlab route is legitimate configuration; delivering to it is refused one layer up. This
// pins the split so a later "tidy-up" cannot collapse Put's validation into Route.Validate, which would
// make the table unable to hold a route the CHECK constraint explicitly permits.
func TestPutAcceptsADeclaredButUnimplementedForge(t *testing.T) {
	r := fd.Route{
		Mode:      fd.ModeCI,
		ForgeKind: fd.ForgeGitLab,
		Target:    fd.Target{Owner: "o", Repo: "r", Base: "main"},
	}
	if r.Validate() == nil {
		t.Fatal("gitlab became deliverable — this test's premise is stale, not its expectation")
	}
	if !knownForge(r.ForgeKind) {
		t.Error("Put would refuse a gitlab route, which migration 0026's CHECK deliberately permits")
	}
}

func TestCapabilityColumnRefusesADerivedState(t *testing.T) {
	for _, k := range []fd.RouteConditionKind{fd.RouteConfigured, fd.RouteAbsent} {
		if _, err := capabilityColumn(k); err == nil {
			t.Errorf("capabilityColumn(%q) was accepted: %q is DERIVED (from the absence of a lost "+
				"capability, or of a row) and storing it creates a second answer that can disagree", k, k)
		}
	}
	for _, k := range []fd.RouteConditionKind{fd.RouteDegraded, fd.RouteRevoked} {
		col, err := capabilityColumn(k)
		if err != nil || col == "" {
			t.Errorf("capabilityColumn(%q) = %q, %v — this one IS a stored fact", k, col, err)
		}
	}
}
