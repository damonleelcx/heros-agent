package sandbox

import (
	"strings"
	"testing"
	"time"
)

// addressspace_test.go fences the bound that could not be caught where it was written.
//
// `ulimit -v` is RLIMIT_AS — address space, not resident memory — and a 64-bit language runtime
// reserves far more of it at startup than it ever makes resident. Set too low, the child dies inside
// runtime.mallocinit before main, with "failed to reserve page summary memory".
//
// 🔴 THE REASON THIS IS A VALUE ASSERTION AND NOT A BEHAVIOURAL ONE. The failure reproduces only on
// Linux: macOS `sh` rejects `ulimit -v`, shimArgv's `|| true` swallows it, and no cap is applied. A
// test that actually ran a compiler under the shim would therefore PASS on every developer's Mac and
// fail only in CI — which is precisely what happened, and precisely the feedback loop this file
// exists to shorten. Asserting the NUMBER works everywhere, and the number is what was wrong.
//
// The floor is measured, not guessed; the measurements are recorded on MinRuntimeAddressSpace.

func TestDefaultAddressSpaceLetsALanguageRuntimeStart(t *testing.T) {
	got := DefaultBounds().Memory
	if got < MinRuntimeAddressSpace {
		t.Errorf("the default address-space bound is %d bytes, below the %d a 64-bit runtime needs to "+
			"START.\nA child under this limit dies in runtime.mallocinit before main — it is not slow "+
			"or throttled, it does not run at all. The P5.5 build gate exists to run a compiler inside "+
			"this isolate, so this bound decides whether that capability works at all on Linux.\n"+
			"⚠️ You will not reproduce it locally on macOS: `sh` rejects `ulimit -v` there and shimArgv "+
			"swallows the rejection, so the cap is silently absent. That is why this asserts the value.",
			got, MinRuntimeAddressSpace)
	}
}

// TestTheShimStillAppliesEveryBound guards the other direction: raising the limit must not become
// removing it. A bound nobody applies is not a relaxed sandbox, it is no sandbox.
func TestTheShimStillAppliesEveryBound(t *testing.T) {
	argv := shimArgv(ResourceBounds{
		CPU: 30 * time.Second, Memory: 4 << 30, Wallclock: time.Minute, MaxPIDs: 8, MaxOutput: 1 << 20,
	}, []string{"/bin/true"})

	preamble := strings.Join(argv, " ")
	for _, want := range []string{"ulimit -t ", "ulimit -f ", "ulimit -v "} {
		if !strings.Contains(preamble, want) {
			t.Errorf("the isolate shim no longer sets %q: %s", want, preamble)
		}
	}
	// The tool's own argv must still be passed positionally rather than interpolated, or a filename
	// with a space in it becomes two arguments and a filename with a `;` becomes a second command.
	if !strings.Contains(preamble, `exec "$@"`) {
		t.Errorf("the shim no longer execs the tool positionally, so the tool's argv is being "+
			"interpolated into a shell string: %s", preamble)
	}
}
