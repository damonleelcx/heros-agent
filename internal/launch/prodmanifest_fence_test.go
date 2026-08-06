package launch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// prodmanifest_fence_test.go fences the two things a `kubectl apply -k overlays/prod` must not silently
// turn off.
//
// # The failure it is written against
//
// `ADMIN_IDENTITY_MODE` is empty in the base, deliberately — whether a cluster federates is the cluster's
// own fact. The prod overlay did not set it, so applying the checked-in manifest deployed agentd with an
// empty mode, which serves no admin API at all, and the operator console with an empty mode, whose
// `isFederated()` then 303s every sign-in to `/signin?reason=not_federated`.
//
// 🔴 Nothing fails when that happens. The pods are healthy, the deployment succeeds, `/readyz` reports
// `admin_idp` ABSENT — which is a correct report of a configuration nobody chose. Operator SSO had been
// configured on the live cluster out of band precisely because the manifest could not carry the values,
// so every apply reverted it, and the only symptom was an operator being unable to sign in to the
// highest-blast-radius surface in the system.
//
// The manifest now carries them. This asserts it keeps carrying them, because the reason it did not is
// exactly the kind of reason that comes back: a comment in the overlay described the gap as "a real and
// unresolved tension" and it stayed that way through several phases.
//
// # Why it reads the file rather than rendering with kustomize
//
// `kustomize` is not a dependency of this module and shelling out to a binary that may not be on the
// runner turns a fence into a skipped test. The values are asserted as they are WRITTEN, which is the
// artefact a reviewer reads and the thing that regresses.

func prodOverlay(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "deploy", "k8s", "overlays", "prod", "kustomization.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// Comments stripped, for the reason every other fence here strips them: this file DISCUSSES the
	// values it sets, at length, and a scan that cannot tell a patch from a paragraph would pass on the
	// prose alone — which is precisely how it would pass while setting nothing.
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// 🔴 Operator SSO must be ON in the manifest that is applied.
func TestTheProdOverlayDoesNotTurnOperatorSSOOff(t *testing.T) {
	overlay := prodOverlay(t)

	// `oidc` on BOTH workloads. They read separate copies of this variable, and the two disagreeing is
	// its own documented defect: agentd serving an admin API the console will not federate into, or the
	// reverse. Counted rather than merely found, so one patch satisfying the check for both cannot pass.
	if n := strings.Count(overlay, `{ name: ADMIN_IDENTITY_MODE, value: "oidc" }`); n < 2 {
		t.Errorf("ADMIN_IDENTITY_MODE=oidc appears %d time(s) in the prod overlay, want 2 (agentd AND "+
			"admin-console).\n  An empty mode does not fail: agentd serves no admin API and the operator "+
			"console 303s every sign-in to /signin?reason=not_federated. The deploy succeeds and the "+
			"operator cannot get in.", n)
	}

	// The bindings adminlaunch refuses to boot without. Asserting them here means the refusal is
	// something a reviewer sees rather than something a deploy discovers.
	for _, required := range []string{
		"ADMIN_CONSOLE_ORIGIN",
		"ADMIN_WEBAUTHN_RP_ID",
		"ADMIN_IDP_REDIRECTS",
		"ADMIN_IDP_CALLBACK_URL",
	} {
		if !strings.Contains(overlay, required) {
			t.Errorf("the prod overlay does not set %s. adminlaunch REFUSES THE BOOT without it once the "+
				"mode is federated — so this is a pod that will not start, not a subtle degradation.", required)
		}
	}

	// 🔴 The RP ID must be the OPERATOR subdomain, never the apex. An RP ID may be any registrable suffix
	// of the origin, so the apex would make operator security keys valid on EVERY *.heros-agent.space
	// origin including the customer console — giving back the origin binding that is the only thing
	// WebAuthn offers over TOTP.
	rp := regexp.MustCompile(`ADMIN_WEBAUTHN_RP_ID, value: "([^"]*)"`).FindStringSubmatch(overlay)
	if rp == nil {
		t.Fatal("ADMIN_WEBAUTHN_RP_ID is not set as a literal in the prod overlay")
	}
	if !strings.HasPrefix(rp[1], "admin.") {
		t.Errorf("ADMIN_WEBAUTHN_RP_ID is %q — it must be the operator subdomain. An RP ID may be any "+
			"registrable suffix of the origin, so an apex here makes operator security keys valid on the "+
			"customer console too.", rp[1])
	}

	// The callback must agree character for character between the platform's allowlist and the console's
	// own copy. Three values must match or sign-in fails at a different step each time; two of the three
	// are in this file, so two of the three are checkable here.
	redirects := regexp.MustCompile(`ADMIN_IDP_REDIRECTS, value: '\["([^"]*)"\]'`).FindStringSubmatch(overlay)
	callback := regexp.MustCompile(`ADMIN_IDP_CALLBACK_URL, value: "([^"]*)"`).FindStringSubmatch(overlay)
	if redirects == nil || callback == nil {
		t.Fatal("the callback URL is not set as a literal on both sides of the prod overlay")
	}
	if redirects[1] != callback[1] {
		t.Errorf("the platform allows %q and the operator console sends %q — these are matched character "+
			"for character at the IdP, and a mismatch fails sign-in at a step that names neither value",
			redirects[1], callback[1])
	}
	if !strings.HasPrefix(callback[1], "https://admin.") {
		t.Errorf("the operator callback is %q — it must be on the OPERATOR origin, or an operator's "+
			"assertion lands in the customer console's cookie jar", callback[1])
	}
}

// The packager must not ship the platform's own hosted overlays to a customer.
//
// This is the fix that dissolved the "unresolved tension" the overlay used to record: with our prod
// overlay inside every air-gapped tarball, the zero-external-origin gate scanned it, so prod could not
// carry an absolute URL — which is why operator SSO lived on the cluster out of band and got reverted by
// every apply. Two defects, one line.
func TestTheAirgappedPackageShipsOnlyWhatACustomerRuns(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "scripts", "package-airgapped.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	script := string(raw)
	// Comments stripped: this script explains the change at length and the words would satisfy a
	// substring check on their own.
	var code strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	body := code.String()

	if regexp.MustCompile(`cp -R "\$DEPLOY_DIR/k8s"\s`).MatchString(body) {
		t.Fatal("package-airgapped.sh copies the WHOLE k8s tree into the customer package.\n" +
			"  That ships the platform's own production overlay — our public hostnames, BILLING_MODE=live, " +
			"the EC2 instance-metadata egress rule that grants our node's IAM role, our image version pin — " +
			"to every air-gapped customer. It also makes the zero-external-origin gate scan our prod " +
			"overlay, which is why operator SSO could not be written into it. Ship base + overlays/airgapped.")
	}
	if !strings.Contains(body, "overlays/airgapped") {
		t.Error("package-airgapped.sh no longer copies the airgapped overlay — that is the ONE overlay a " +
			"customer needs, and without it the package has a base and nothing to apply")
	}
	for _, ours := range []string{"overlays/prod", "overlays/staging"} {
		if strings.Contains(body, `cp -R "$DEPLOY_DIR/k8s/`+ours) {
			t.Errorf("package-airgapped.sh copies %s into the customer package", ours)
		}
	}
}

// P28: self-serve sign-up is ON in prod and OFF in the base. The direction matters — an air-gapped
// install must not grow a registration form by upgrading.
func TestSelfServeIsOnInProdAndOffInTheBase(t *testing.T) {
	overlay := prodOverlay(t)
	if !strings.Contains(overlay, `{ name: HEROS_SELF_SERVE_SIGNUP, value: "1" }`) {
		t.Error("the prod overlay does not enable self-serve sign-up, so /create-account refuses every " +
			"visitor on the hosted product")
	}

	basePath := filepath.Join("..", "..", "deploy", "k8s", "base", "agentd.yaml")
	raw, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("reading %s: %v", basePath, err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	if !strings.Contains(code.String(), `{ name: HEROS_SELF_SERVE_SIGNUP, value: "0" }`) {
		t.Error("the BASE does not declare self-serve sign-up OFF. Every substrate that is not the hosted " +
			"product inherits the base, and a registration form appearing on an air-gapped install because " +
			"somebody upgraded is the failure the declared-posture rule exists to prevent.")
	}
}
