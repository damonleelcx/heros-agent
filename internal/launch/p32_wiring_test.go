package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// p32_wiring_test.go closes the gap between "the endpoint renders it" and "the deployment publishes it".
//
// # 🔴 The defect this exists for, found by a mutation drill
//
// `internal/api`'s health tests call `SetSourceIngestHealth` themselves and then assert `/readyz`
// carries the two documents. That proves the ENDPOINT works. It proves nothing about the wiring — and
// when `SetSourceIngestHealth(ingestMetrics, retention)` was deleted from `capabilities.go` as a drill,
// every one of those tests still passed, the build stayed green, and `served("p32_ingest_health")` kept
// printing in the boot log.
//
// That is the shape `health-signal-surface` warns about, one layer up: the job runs perfectly, the
// dashboard is empty, and an empty dashboard reads as "no problems".
//
// # Why this is a SOURCE scan and not a behavioural test
//
// `mountCapabilities` needs a live Postgres, a blob store and a skill registry, and the pgproof launch
// suite already stands those up for what it asserts. What is missing is much narrower: a claim printed
// in the boot log must have its wiring call beside it. That is a property of the FILE, and reading the
// file is the cheapest honest way to check it — the same instrument `ingress_fence_test.go` uses for
// the same class of failure, where two halves of one decision live in two places.
//
// 🚫 It deliberately does NOT check the whole registry. A generic "every capability has a mount" rule
// would need a mapping table that is itself hand-maintained, which is the artefact that fails. This
// names three pairs, and adding a fourth is a deliberate edit.

// servedCapabilityWiring pairs a capability the boot log CLAIMS with the call that makes it true.
//
// The claim is what an operator reads. The call is what makes the claim true. Nothing in the language
// connects them, which is why they are connected here.
var servedCapabilityWiring = []struct {
	capability string
	// call is the wiring that must appear in the same file. Named exactly, so a rename is a red test
	// rather than a silently unenforced pair.
	call string
	// why states what breaks when the claim ships without the call — because a fence whose failure
	// message is "expected X, got Y" teaches nobody why it exists.
	why string
}{
	{
		capability: "p32_ingest_health",
		call:       "SetSourceIngestHealth(",
		why: "the retention job and the per-forge ingest metrics would be computed and UNREADABLE. " +
			"`/readyz` would carry neither document, the job would run perfectly, and the dashboard " +
			"would be empty — which reads as `no problems`. A mutation drill removed exactly this call " +
			"and nothing anywhere went red.",
	},
	{
		capability: "p32_repo_connections",
		call:       "MountConnections(",
		why: "the boot log would announce a surface whose four routes answer 503, and a customer would " +
			"be told this deployment does not offer connections while the operator reads that it does.",
	},
	{
		capability: "p32_local_pairing",
		call:       "MountLocalPairing(",
		why: "the same, for Mode 3: the console would show the pairing tab and every code request would " +
			"answer 503.",
	},
}

// TestEveryP32CapabilityClaimHasItsWiring is the fence.
func TestEveryP32CapabilityClaimHasItsWiring(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("capabilities.go"))
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}
	body := string(src)

	checked := 0
	for _, pair := range servedCapabilityWiring {
		claim := `served("` + pair.capability
		if !strings.Contains(body, claim) {
			// The capability was renamed or removed. Reported rather than skipped: a pair that quietly
			// stops applying is a fence that quietly stops fencing, which is this file's whole subject.
			t.Errorf("capabilities.go no longer claims %q. If it was renamed, rename it here too; if it "+
				"was removed, remove this pair — but do not leave a fence pointing at nothing.", pair.capability)
			continue
		}
		checked++
		if !strings.Contains(body, pair.call) {
			t.Errorf("capabilities.go claims %q in the boot log and never calls %s.\n\nWhat breaks: %s",
				pair.capability, pair.call, pair.why)
		}
	}
	if checked != len(servedCapabilityWiring) {
		t.Errorf("only %d of %d pairs were checked — the rest named capabilities this file does not claim",
			checked, len(servedCapabilityWiring))
	}
}

// TestTheRetentionSweepIsStartedAndNotOnlyConstructed.
//
// 🔴 A `NewRetentionJob` with no `Start` is a job that never runs, and its health endpoint would report
// `never_run` forever — which is a state this phase deliberately made distinguishable from `degraded`,
// so it would look like a process that had simply not got there yet. FR17 is explicit that retention
// runs *"whether or not anything else does"*, and a constructed-but-unstarted job satisfies neither
// half of that sentence.
func TestTheRetentionSweepIsStartedAndNotOnlyConstructed(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("capabilities.go"))
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "NewRetentionJob(") {
		t.Fatal("capabilities.go does not construct a retention job — FR17 requires one that runs whether " +
			"or not anything else does")
	}
	if !strings.Contains(body, ".Start(") {
		t.Fatal("the retention job is constructed and never started. Its health endpoint would report " +
			"`never_run` forever, which is a state deliberately distinguishable from `degraded` — so it " +
			"would read as a process that simply had not got there yet, on a deployment where it never will.")
	}
}
