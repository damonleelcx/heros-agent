package providercall_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// providercall_test.go guards the two properties this package was extracted to create. Both are the kind that
// hold today and quietly stop holding later, which is why they are tests rather than comments.

// TestProvidercallLinksNoTransport is the whole reason the package exists.
//
// It was split out of `internal/providergateway` so that `internal/telemetry` could name the value its observer
// callback receives without linking an HTTP client and the AWS SDK — an import that put `net/http` in reach of
// every package that could reach telemetry, including `internal/cli`, whose offline guarantee is structural.
//
// If this package ever grows a transport dependency, that whole chain silently comes back: nothing would fail
// here, and the failure would surface as a red `TestCLIPackageLinksNoNetworkStack` in a package five hops away,
// where nobody would connect it to a change made here.
func TestProvidercallLinksNoTransport(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := map[string]bool{
		"net/http": true, "net/smtp": true, "net/rpc": true, "net/http/httptrace": true,
	}
	for _, dep := range strings.Fields(string(out)) {
		if forbidden[dep] {
			t.Errorf("internal/providercall links %s. This package holds the vocabulary for DESCRIBING a "+
				"provider call that already happened; describing one needs no transport. Anything that MAKES a "+
				"call belongs in internal/providergateway — putting it here re-links net/http into "+
				"internal/cli's offline surface, five hops away, where the failure is unrecognisable.", dep)
		}
	}
	// It must also stay a leaf inside this repository: a heros dependency here is a future path to a transport.
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/heros-foreal/") && !strings.HasSuffix(dep, "/internal/providercall") {
			t.Errorf("internal/providercall imports %s — it is meant to be a leaf, so that no future change to "+
				"another package can give it a transport by accident", dep)
		}
	}
}

// TestGatewayAliasesAreTypeIdentity pins the property that made the extraction safe to do to a package this many
// things depend on: `providergateway.CallInfo` and `providercall.CallInfo` are ALIASES, not conversions, so every
// existing call site kept compiling and every existing observer kept satisfying the interface.
//
// The end-to-end proof already exists elsewhere and is stronger than this: telemetry/instrument_test.go builds a
// real `providergateway.New(…, providergateway.WithObserver(inst))` with an Instrument whose OnCall takes a
// `providercall.CallInfo`, and it passes. This test states the property directly so that a future change from
// alias to distinct type fails HERE, naming the reason, instead of failing as a wall of errors in six packages.
func TestGatewayAliasesAreTypeIdentity(t *testing.T) {
	// Assignable in both directions with no conversion — the definition of an alias.
	var viaLeaf providercall.CallInfo
	var viaGateway providergateway.CallInfo = viaLeaf
	viaLeaf = viaGateway
	_ = viaLeaf

	var usage providergateway.Usage = providercall.Usage{InputTokens: 1}
	if usage.InputTokens != 1 {
		t.Fatalf("Usage did not survive the alias: %+v", usage)
	}
	if providergateway.StopEndTurn != providercall.StopEndTurn {
		t.Error("StopReason constants differ across the alias")
	}

	// An observer written against the LEAF package must satisfy the GATEWAY's interface, because that is what
	// lets telemetry stay off providergateway while the server still wires it in one line.
	var _ providergateway.Observer = leafObserver{}
	var _ providercall.Observer = leafObserver{}

	// ErrTimeout must be the same error VALUE, or `errors.Is` gives different answers depending on which
	// package a caller happened to import — and an observer classifying a timeout would silently stop
	// recognising one.
	if !errors.Is(providergateway.ErrTimeout, providercall.ErrTimeout) {
		t.Error("providergateway.ErrTimeout and providercall.ErrTimeout are different values — errors.Is would " +
			"answer differently depending on which package the caller imported")
	}
}

// leafObserver implements the seam using only the leaf package's types, which is exactly what telemetry does.
type leafObserver struct{}

func (leafObserver) OnCall(context.Context, providercall.CallInfo) {}
