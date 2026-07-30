package api

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDumpInstallReadModel writes the install read model to the path in $HEROS_INSTALL_DUMP, so the console's
// browser-checkable preview is seeded from the ENGINE rather than from a hand-written fixture. A hand-written
// fixture drifts, and a preview that drifts stops catching anything.
//
// It is a test rather than a cmd/ tool because the read model's types are unexported — the same reason
// consoletypes' registry lives in this package. Exporting three view structs so a generator could see them
// would widen this package's real surface for a build-time reason.
func TestDumpInstallReadModel(t *testing.T) {
	path := os.Getenv("HEROS_INSTALL_DUMP")
	if path == "" {
		t.Skip("HEROS_INSTALL_DUMP unset; nothing to write")
	}
	b, err := json.MarshalIndent(installReadModel(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(b))
}
