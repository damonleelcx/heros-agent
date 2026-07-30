package billing

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// secretfence_test.go is P21 task 3.3: **no Stripe secret exists in a git-tracked file**.
//
// It is the same discipline as plancfg's price fence and for the same reason: a rule that says "don't
// commit the key" has a demonstrated failure rate, and the failure is not recoverable by a revert —
// a secret that reached a git object is a secret that must be rotated, and by the time anyone notices
// it has been fetched by every clone.
//
// AUTO-DISCOVERING, not an allowlist: it enumerates the ENTIRE git index every run, so a key committed
// under any name, in any directory — a manifest, a fixture, a `.env.example` somebody filled in, a
// checked-in build output — is caught the first time.
//
// The log/trace/bundle halves of task 3.3 are guarded elsewhere, because they are different mechanisms
// rather than different files:
//
//	log / trace   — there is no field, method or struct in this package that returns a secret for
//	                logging or inclusion in an event (secrets.go's "what is deliberately absent"), and
//	                TestBillingSecretsComeFromTheSecretsManagerAndNeverLeak asserts it over the ledger,
//	                the Describe output and the error strings.
//	client bundle — web/console/scripts/scan-bundle.mjs runs as the last step of `npm run build` and
//	                looks at the JavaScript the browser actually downloads.

// stripeSecretShapes are the byte shapes a real Stripe credential has.
//
// Each requires a long ALPHANUMERIC run after the prefix, which is what a Stripe key is. That is not
// leniency for its own sake: this repository's own tests must be able to name a key-like placeholder
// (`sk_test_p21_fake_key_not_a_secret`) without tripping the fence, and the honest way to allow that is
// a pattern that distinguishes a credential from a word — not an allowlist of files the fence skips.
var stripeSecretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"stripe live secret key", regexp.MustCompile(`\bsk_live_[A-Za-z0-9]{16,}`)},
	{"stripe test secret key", regexp.MustCompile(`\bsk_test_[A-Za-z0-9]{16,}`)},
	{"stripe restricted key", regexp.MustCompile(`\brk_(live|test)_[A-Za-z0-9]{16,}`)},
	{"stripe webhook signing secret", regexp.MustCompile(`\bwhsec_[A-Za-z0-9]{16,}`)},
}

// TestNoStripeSecretInAGitTrackedFile walks the git index and fails on anything key-shaped.
func TestNoStripeSecretInAGitTrackedFile(t *testing.T) {
	root := gitRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")

	// Anti-vacuity: an index this fence cannot see is a fence whose assertions are trivially true.
	if len(files) < 100 {
		t.Fatalf("git ls-files returned only %d files — the fence is not seeing the repository", len(files))
	}

	scanned := 0
	for _, rel := range files {
		if rel == "" {
			continue
		}
		abs := filepath.Join(root, rel)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() || st.Size() > 2<<20 {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		scanned++
		content := string(b)
		for _, shape := range stripeSecretShapes {
			if shape.re.MatchString(content) {
				// The MATCH is deliberately not printed. Printing it would put the credential in CI logs,
				// which is the same exposure the fence exists to prevent, one system further downstream.
				t.Errorf("%s contains something shaped like a %s — a billing credential comes from the "+
					"secrets seam at the moment of use and exists in no git-tracked file (P21 design "+
					"Decision 6). If this is a real key it is already compromised: rotate it, then remove it",
					rel, shape.name)
			}
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d git-tracked files — that is not evidence", scanned)
	}
	t.Logf("scanned %d git-tracked files for Stripe credential shapes", scanned)
}

// TestStripeSecretDetectorGoesRed proves the fence can FAIL. A guard that has never been shown to fire
// is decoration — and this one guards the least recoverable mistake in the change.
func TestStripeSecretDetectorGoesRed(t *testing.T) {
	mustCatch := map[string]string{
		"live key in a manifest":     `STRIPE_KEY: ` + "s"+"k"+"_"+"l"+"i"+"v"+"e"+"_" + `51QabcdefghijklmnopqrstuvwxyZ`,
		"test key in a fixture":      `{"key":"` + "s"+"k"+"_"+"t"+"e"+"s"+"t"+"_" + `51QabcdefghijklmnopqrstuvwxyZ` + `"}`,
		"restricted key in a script": `export K=` + "r"+"k"+"_"+"l"+"i"+"v"+"e"+"_" + `51QabcdefghijklmnopqrstuvwxyZ`,
		"webhook secret in an env":   `STRIPE_WEBHOOK_SECRET=` + "w"+"h"+"s"+"e"+"c"+"_" + `AbCdEfGhIjKlMnOpQrStUvWx`,
	}
	for name, body := range mustCatch {
		if !anyStripeSecretShape(body) {
			t.Errorf("the secret detector missed %s", name)
		}
	}

	mustIgnore := map[string]string{
		// This repository's own placeholders, which must remain writable without disabling the fence.
		"test placeholder":    testStripeKey,
		"live placeholder":    liveStripeKey,
		"prefix in prose":     "Stripe keys are prefixed sk_test_ or sk_live_, and the prefix is what the mode check reads.",
		"reserved name":       SecretBillingAPIKey + " / " + SecretBillingWebhookSigning,
		"unrelated long word": "subscription_items_usage_record_summaries",
	}
	for name, body := range mustIgnore {
		if anyStripeSecretShape(body) {
			t.Errorf("the secret detector false-positived on %s (%q)", name, body)
		}
	}
}

func anyStripeSecretShape(s string) bool {
	for _, shape := range stripeSecretShapes {
		if shape.re.MatchString(s) {
			return true
		}
	}
	return false
}

func gitRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed — this fence must run inside the repository: %v", err)
	}
	return strings.TrimSpace(string(out))
}
