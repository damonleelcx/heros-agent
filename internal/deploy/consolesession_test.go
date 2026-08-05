package deploy

import (
	"fmt"
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

// # P27: the determinant MOVED, and this fence did not follow it
//
// 🔴 Until P27 there was one store and the question was "does the source keep a Map on globalThis?".
// P27 put TWO implementations behind a seam in `sessionStore.ts` — the original map, and a row in the
// platform's `console_session` table — and made `CONSOLE_SESSION_STORE` choose between them. The
// per-process map did not go away. It moved, and it is still the default.
//
// So the old question started returning the wrong answer for the right-looking reason: it read
// `session.ts`, found no `Map<string, Session>` there any more (it is one file over), concluded the
// store was durable, and passed `replicas: 2`. It would have passed `replicas: 2` on a manifest that
// never selected the durable store at all — which is precisely the defect it exists to prevent, with the
// fence itself now certifying it. `console.yaml`'s own comment says this test "fences the pair"; that
// sentence was false the moment the store gained a switch.
//
// The determinant is now the DEPLOYMENT's declaration, so that is what this reads. Two facts, both
// required: the code must offer a durable implementation, and this manifest must select it.
const (
	consoleSessionModule = "../../web/console/src/lib/sessionStore.ts"
	consoleSessionEnv    = "CONSOLE_SESSION_STORE"
	// durableStoreValue is the one value that selects the shared store. Anything else — including unset
	// — is the per-process map.
	durableStoreValue = "platform"
)

// consoleOffersADurableStore reports whether the console's code has a shared-store implementation at
// all. Without one, no manifest declaration can make two replicas safe.
func consoleOffersADurableStore(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile(consoleSessionModule)
	if err != nil {
		// Not a skip. Before P27 this module did not exist, and "the file is missing" is then the correct
		// answer to "is there a durable store" — but a fence that cannot read its own subject must say so
		// rather than answer from the absence.
		t.Fatalf("read %s: %v — this fence cannot judge the replica count without it", consoleSessionModule, err)
	}
	src := string(b)
	// The platform-backed implementation names itself. Both markers, not either: the string "platform"
	// appears throughout this repository, and a `kind` field alone could be anything.
	return strings.Contains(src, `kind: "platform"`) && strings.Contains(src, consoleSessionEnv)
}

// consoleSelectsTheDurableStore reads what the console Deployment actually declares. Absent is reported
// as absent, not as the default, because "nobody set it" and "somebody set it to memory" are different
// mistakes and the message should say which one happened.
func consoleSelectsTheDurableStore(t *testing.T, objs []k8sObject) (value string, declared bool) {
	t.Helper()
	for _, o := range objs {
		if o.Kind != "Deployment" || o.Metadata.Name != "console" {
			continue
		}
		for _, env := range deploymentEnv(o) {
			if env.name == consoleSessionEnv {
				return env.value, true
			}
		}
	}
	return "", false
}

type envVar struct{ name, value string }

// deploymentEnv flattens every container's env list. Values sourced from a secret or configmap carry no
// literal and are returned with an empty value — which this fence treats as NOT the durable declaration,
// deliberately: a session-store selection that a manifest reader cannot see is a selection nobody can
// review, and the whole point of this pair is that both halves are visible in the same place.
func deploymentEnv(o k8sObject) []envVar {
	var out []envVar
	tmpl, _ := o.Spec["template"].(map[string]any)
	if tmpl == nil {
		return out
	}
	spec, _ := tmpl["spec"].(map[string]any)
	if spec == nil {
		return out
	}
	containers, _ := spec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		envs, _ := cm["env"].([]any)
		for _, e := range envs {
			em, _ := e.(map[string]any)
			if em == nil {
				continue
			}
			name, _ := em["name"].(string)
			value, _ := em["value"].(string)
			out = append(out, envVar{name: name, value: strings.TrimSpace(value)})
		}
	}
	return out
}

func TestConsoleReplicasMatchItsSessionStore(t *testing.T) {
	objs := loadBase(t)

	// Per-process unless BOTH halves hold: the code can do durable, and this deployment asked for it.
	// Either half alone is the failure — a durable implementation nobody selected is the same runtime as
	// no implementation, and a manifest selecting a store the code does not have would fail at boot.
	value, declared := consoleSelectsTheDurableStore(t, objs)
	perProcess := !consoleOffersADurableStore(t) || !declared || value != durableStoreValue

	var replicas int
	var minAvailable any
	var foundDeployment, foundPDB bool
	for _, o := range objs {
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
		why := fmt.Sprintf("%s is %q in the console Deployment", consoleSessionEnv, value)
		if !declared {
			why = fmt.Sprintf("the console Deployment declares no %s, so the console falls back to the "+
				"in-process map", consoleSessionEnv)
		} else if !consoleOffersADurableStore(t) {
			why = fmt.Sprintf("%s offers no platform-backed implementation for %s to select",
				consoleSessionModule, consoleSessionEnv)
		}
		t.Errorf("the console runs %d replicas while its session store is still per-process — %s.\n"+
			"Two replicas are two disjoint session stores behind one Service: a user signs in on one pod "+
			"and roughly half their later requests reach the other, find no session, and are redirected "+
			"to /signin?reason=session_ended. It is intermittent, it logs nothing — answering `no such "+
			"session` is correct from the server's side — and it presents as `the console keeps logging "+
			"me out`.\n"+
			"Either run one replica, or set %s=%s so sessions become rows the way the OPERATOR console's "+
			"already are.", replicas, why, consoleSessionEnv, durableStoreValue)
	}

	if !perProcess && replicas == 1 {
		t.Errorf("the console declares %s=%s — a shared session store — but is still pinned to one "+
			"replica.\nThat pin was only ever a consequence of the in-process store. With a shared store "+
			"the count goes back to being a value — raise it, and restore the PodDisruptionBudget to "+
			"minAvailable: 1 at the same time.", consoleSessionEnv, durableStoreValue)
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
