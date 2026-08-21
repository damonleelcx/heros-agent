//go:build unix

package sourceingest

import (
	"syscall"
	"testing"
)

// mkfifo_unix_test.go creates a FIFO for the hostile-tree fixtures.
//
// Build-tagged rather than guarded at runtime because `syscall.Mkfifo` does not exist on Windows —
// this is a compile-time platform difference, not a capability check. `syscall` rather than
// `golang.org/x/sys/unix` so the fixture does not promote an indirect dependency to a direct one for
// the sake of a test helper.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this platform or filesystem cannot create a FIFO: %v", err)
	}
}
