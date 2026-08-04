package deploy

import (
	"os"
	"strings"
	"testing"
)

// consolesession_test.go ties the customer console's REPLICA COUNT to the shape of its session store,
// because the two are one decision and were shipped as two.
//
// The manifest said "stateless BFF, replicas 2 + PDB". `web/console/src/lib/session.ts` holds sessions
// in a Map hung off `globalThis` — one store per process — and says so in its own header. Both files
// were correct about themselves and wrong together: two replicas are two disjoint session stores behind
// one Service, so a user signs in on pod A and about half their later requests reach pod B, find no
// record, and are bounced to `/signin?reason=session_ended`.
//
// # Why this is a test and not a comment
//
// The failure never appears on Compose (one container) and never appears in a single-pod test cluster.
// It appears under load balancing, intermittently, as "the console keeps logging me out" — with no error
// logged anywhere, because from the server's side answering "no such session" is CORRECT. Nothing else
// in this suite would notice the replica count going back to 2.
//
// # It fails in BOTH directions, on purpose
//
// A shared session store is the real fix, and when it lands the replica count SHOULD go back up. So this
// asserts the pair is consistent rather than pinning one value: change the store and the test tells you
// to raise the count; raise the count without changing the store and it tells you why you cannot.

const consoleSessionStore = "../../web/console/src/lib/session.ts"

// sessionStoreIsPerProcess reports whether the console still keeps sessions in process memory.
func sessionStoreIsPerProcess(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile(consoleSessionStore)
	if err != nil {
		t.Fatalf("read %s: %v — this fence cannot judge the replica count without it", consoleSessionStore, err)
	}
	src := string(b)
	// Both markers, not either: `globalThis` alone could be any other global, and a Map alone could be a
	// cache in front of a durable store. Together they are the store this fence is about.
	return strings.Contains(src, "globalThis") && strings.Contains(src, "Map<string, Session>")
}

func TestConsoleReplicasMatchItsSessionStore(t *testing.T) {
	perProcess := sessionStoreIsPerProcess(t)

	var replicas int
	var minAvailable any
	var foundDeployment, foundPDB bool
	for _, o := range loadBase(t) {
		if o.Metadata.Name != "console" {
			continue
		}
		switch o.Kind {
		case "Deployment":
			foundDeployment = true
			replicas = 1
			if r, ok := o.Spec["replicas"].(int); ok {
				replicas = r
			}
		case "PodDisruptionBudget":
			foundPDB = true
			minAvailable = o.Spec["minAvailable"]
		}
	}
	if !foundDeployment {
		t.Fatal("no console Deployment in deploy/k8s/base — this fence found nothing to check")
	}

	if perProcess && replicas > 1 {
		t.Errorf("the console runs %d replicas while its session store is still per-process "+
			"(%s keeps a Map on globalThis).\n"+
			"Two replicas are two disjoint session stores behind one Service: a user signs in on one pod "+
			"and roughly half their later requests reach the other, find no session, and are redirected "+
			"to /signin?reason=session_ended. It is intermittent, it logs nothing — answering `no such "+
			"session` is correct from the server's side — and it presents as `the console keeps logging "+
			"me out`.\n"+
			"Either run one replica, or move sessions to a shared store the way the OPERATOR console "+
			"already does (rows in `admin_session`, which is why that Deployment can run two).",
			replicas, consoleSessionStore)
	}

	if !perProcess && replicas == 1 {
		t.Errorf("%s no longer keeps sessions in process memory, but the console is still pinned to one "+
			"replica.\nThat pin was only ever a consequence of the in-process store. With a shared store "+
			"the count goes back to being a value — raise it, and restore the PodDisruptionBudget to "+
			"minAvailable: 1 at the same time.", consoleSessionStore)
	}

	// 🔴 The drain trap. A minAvailable: 1 budget over ONE replica denies the only pod's eviction
	// forever: no replacement can start until it terminates, so the drain never completes. Reducing the
	// replica count without reducing the budget trades an intermittent sign-out for a node nobody can
	// patch, which is the worse of the two.
	if foundPDB && replicas == 1 && minAvailable != 0 {
		t.Errorf("the console PodDisruptionBudget is minAvailable: %v over %d replica(s).\n"+
			"That budget can never be satisfied: evicting the only pod would drop availability to zero, "+
			"so a node drain blocks forever waiting for a replacement that cannot start. Use "+
			"minAvailable: 0 while the count is 1 — the same value and the same reason as agentd's.",
			minAvailable, replicas)
	}
}
