//go:build !unix

package sourceingest

import "testing"

// mkfifo_other_test.go is the non-unix half.
//
// 🔴 It SKIPS rather than silently passing. A helper that quietly did nothing would let the
// device-node case report PASS on Windows without ever creating a device node — a green fence over an
// input that was never constructed, which is worse than an absent one because it is counted.
func mkfifo(t *testing.T, _ string) {
	t.Helper()
	t.Skip("this platform has no mkfifo; the device-node refusal is exercised through Admission.Entry instead")
}
