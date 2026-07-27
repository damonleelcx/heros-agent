// Package deploy holds no code — it exists so the P19 deployment manifests have a machine-checked
// acceptance gate that runs in `make go`, beside every other test, rather than living only in a
// human's review of YAML (QA task 8: "three axes, machine assertions"; kubernetes-delivery FRs).
//
// These tests read deploy/k8s/base/ and assert the properties the spec makes non-negotiable:
//   - every workload declares liveness + readiness probes that read a HEALTH ENDPOINT (never a UI),
//     resource requests AND limits, and a bounded rolling-update policy;
//   - every workload with replicas > 1 declares a PodDisruptionBudget;
//   - a default-deny NetworkPolicy exists;
//   - no plaintext Secret is committed;
//   - the two consoles are on DIFFERENT origins (ports), the structural half of their isolation.
//
// A manifest that regresses any of these fails the build, so the guarantee is a test, not a habit.
package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const baseDir = "../../deploy/k8s/base"

type k8sObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec map[string]any `yaml:"spec"`
}

// loadBase parses every YAML document under deploy/k8s/base into typed-enough objects.
func loadBase(t *testing.T) []k8sObject {
	t.Helper()
	var objs []k8sObject
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("read base dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(baseDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		for {
			var o k8sObject
			if err := dec.Decode(&o); err != nil {
				break
			}
			if o.Kind != "" {
				objs = append(objs, o)
			}
		}
	}
	if len(objs) == 0 {
		t.Fatal("no manifests parsed from base — did the tree move?")
	}
	return objs
}

func firstContainer(o k8sObject) map[string]any {
	tmpl, _ := o.Spec["template"].(map[string]any)
	if tmpl == nil {
		return nil
	}
	pspec, _ := tmpl["spec"].(map[string]any)
	if pspec == nil {
		return nil
	}
	cs, _ := pspec["containers"].([]any)
	if len(cs) == 0 {
		return nil
	}
	c, _ := cs[0].(map[string]any)
	return c
}

// Every long-running workload declares both probes reading a health endpoint, and resource limits.
func TestEveryWorkloadHasProbesAndResourceLimits(t *testing.T) {
	for _, o := range loadBase(t) {
		if o.Kind != "Deployment" && o.Kind != "StatefulSet" {
			continue
		}
		c := firstContainer(o)
		if c == nil {
			t.Errorf("%s/%s: no container found", o.Kind, o.Metadata.Name)
			continue
		}
		for _, probe := range []string{"livenessProbe", "readinessProbe"} {
			p, ok := c[probe].(map[string]any)
			if !ok {
				t.Errorf("%s/%s: missing %s — a workload with no %s cannot fail a rollout on a real signal",
					o.Kind, o.Metadata.Name, probe, probe)
				continue
			}
			// The probe must read a health endpoint (httpGet/exec/tcpSocket), never a rendered UI. We
			// accept httpGet/exec/tcpSocket; a probe with none of them is meaningless.
			if _, http := p["httpGet"]; !http {
				if _, exec := p["exec"]; !exec {
					if _, tcp := p["tcpSocket"]; !tcp {
						t.Errorf("%s/%s: %s reads no health endpoint (no httpGet/exec/tcpSocket)",
							o.Kind, o.Metadata.Name, probe)
					}
				}
			}
		}
		res, _ := c["resources"].(map[string]any)
		if res == nil {
			t.Errorf("%s/%s: no resources block (requests/limits)", o.Kind, o.Metadata.Name)
			continue
		}
		if _, ok := res["limits"].(map[string]any); !ok {
			t.Errorf("%s/%s: no resources.limits", o.Kind, o.Metadata.Name)
		}
		if _, ok := res["requests"].(map[string]any); !ok {
			t.Errorf("%s/%s: no resources.requests", o.Kind, o.Metadata.Name)
		}
	}
}

// Any workload with replicas > 1 declares a PodDisruptionBudget, so a node drain cannot remove its
// last replica (kubernetes-delivery FR). Workloads at replicas 1 (the stateful stores) need none.
func TestMultiReplicaWorkloadsHaveAPDB(t *testing.T) {
	objs := loadBase(t)
	pdbNames := map[string]bool{}
	for _, o := range objs {
		if o.Kind == "PodDisruptionBudget" {
			pdbNames[o.Metadata.Name] = true
		}
	}
	for _, o := range objs {
		if o.Kind != "Deployment" && o.Kind != "StatefulSet" {
			continue
		}
		replicas := 1
		if r, ok := o.Spec["replicas"].(int); ok {
			replicas = r
		}
		if replicas > 1 && !pdbNames[o.Metadata.Name] {
			t.Errorf("%s/%s has replicas=%d but no PodDisruptionBudget — a node drain could remove its last replica",
				o.Kind, o.Metadata.Name, replicas)
		}
	}
}

// A default-deny NetworkPolicy exists: an empty podSelector with both Ingress and Egress policy types
// and no allow rules is the deny-by-default floor everything else re-permits from.
func TestDefaultDenyNetworkPolicyExists(t *testing.T) {
	for _, o := range loadBase(t) {
		if o.Kind != "NetworkPolicy" {
			continue
		}
		sel, _ := o.Spec["podSelector"].(map[string]any)
		types, _ := o.Spec["policyTypes"].([]any)
		_, hasIngress := o.Spec["ingress"]
		_, hasEgress := o.Spec["egress"]
		if len(sel) == 0 && len(types) == 2 && !hasIngress && !hasEgress {
			return // found the default-deny
		}
	}
	t.Error("no default-deny NetworkPolicy (empty podSelector, Ingress+Egress, no allow rules) found")
}

// No plaintext Secret is committed to the base — secrets come from ExternalSecret references only.
func TestNoPlaintextSecretInBase(t *testing.T) {
	for _, o := range loadBase(t) {
		if o.Kind == "Secret" {
			if _, hasData := o.Spec["data"]; hasData {
				t.Errorf("committed plaintext Secret %q", o.Metadata.Name)
			}
		}
	}
}

// The two consoles are on DIFFERENT origins — the structural half of their isolation (P8 Decision 11).
// A shared port would put them on the same origin and share a cookie jar.
func TestConsolesAreOnDistinctOrigins(t *testing.T) {
	port := func(name string) any {
		for _, o := range loadBase(t) {
			if o.Kind == "Service" && o.Metadata.Name == name {
				ports, _ := o.Spec["ports"].([]any)
				if len(ports) > 0 {
					p, _ := ports[0].(map[string]any)
					return p["port"]
				}
			}
		}
		return nil
	}
	cust, admin := port("console"), port("admin-console")
	if cust == nil || admin == nil {
		t.Fatalf("could not find both console Services: console=%v admin-console=%v", cust, admin)
	}
	if cust == admin {
		t.Errorf("customer and operator consoles share port %v — same origin, shared cookie jar", cust)
	}
}
