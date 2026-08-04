package launch

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/modelcatalog"
)

// catalogreason_test.go pins the ONE distinction every published-catalog reason has to make: a variable
// nobody set, and a variable that names a file nobody put there.
//
// 🔴 They are different deployments with different next actions — "set MODEL_CATALOG_PATH" versus "drop
// models.json at the path it already names" — and since the deploy manifests started declaring both
// paths, the SECOND is the common state on a fresh install. p55_proposal_generation reported the first
// one unconditionally: an operator whose compose file sets the variable was told to set the variable,
// which reads as a broken deployment rather than as one file away. billingAbsentReason and
// deliveryAbsentReason went through catalogHint and were right; this one did not.
//
// The fence is written over ALL THREE reasons rather than the one that was wrong, because the failure
// is a sentence drifting away from a shared helper, and it can drift in any of them.

// catalogReasons is every absent-reason that reports a published catalog, keyed by the variable it must
// name. Each is called in its no-catalog state, with a live database — the gap under test is the file.
var catalogReasons = []struct {
	capability string
	env        string
	reason     func() string
}{
	{"p7_billing", planCatalogPathEnv, func() string { return billingAbsentReason(&sql.DB{}, "", nil) }},
	{"p12_forge_delivery", planCatalogPathEnv, func() string { return deliveryAbsentReason(&sql.DB{}, "", nil) }},
	{"p55_proposal_generation", modelcatalog.PathEnv, func() string { return proposalGenAbsentReason(&sql.DB{}) }},
}

func TestCatalogAbsentReasonsTellUnsetApartFromNoFileThere(t *testing.T) {
	for _, c := range catalogReasons {
		t.Run(c.capability, func(t *testing.T) {
			// Unset: the operator has to choose a path. Saying so is the whole message.
			t.Setenv(c.env, "")
			unset := c.reason()
			if !strings.Contains(unset, c.env+" is unset") {
				t.Errorf("with %s unset the reason must say so, got %q", c.env, unset)
			}

			// Set, with nothing behind it: the path is already decided, and the remedy is the FILE. A
			// reason that still says "is unset" sends the operator to re-set a variable that is set.
			path := filepath.Join(t.TempDir(), "catalog.json")
			t.Setenv(c.env, path)
			named := c.reason()
			if !strings.Contains(named, path) {
				t.Errorf("with %s naming %s the reason must quote the path, got %q", c.env, path, named)
			}
			if strings.Contains(named, c.env+" is unset") {
				t.Errorf("%s IS set, and the reason still says it is unset — that is the sentence this "+
					"fence exists for: %q", c.env, named)
			}
		})
	}
}
