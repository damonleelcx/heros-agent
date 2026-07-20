package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file is the malicious repo-tool set (task 7.1): a fixture of adversarial tools, each attempting
// one attack from the threat model, run through the real sandbox. Each case asserts the attack is
// contained — either denied/terminated inside a created isolate, or fail-closed before the isolate is
// created when the host cannot enforce the required restriction. A malicious tool that "succeeds" here
// is a security regression.

// maliciousTool is one fixture: an attack, the tool code, and how containment shows up.
type maliciousTool struct {
	name string
	argv []string
	// check runs against the outcome. It receives the result (may be nil) and the error.
	check func(t *testing.T, res *Result, err error)
}

func TestMaliciousRepoToolSet_Contained(t *testing.T) {
	// A secret in the host environment that a credential-stealing tool would try to read.
	t.Setenv("OPENAI_API_KEY", "sk-live-DEADBEEFdeadbeef0123")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI-should-not-leak")

	fullCaps := testSandboxFullCaps(t, &recordingSink{})

	tools := []maliciousTool{
		{
			name: "reads-environment-for-credentials",
			argv: []string{"sh", "-c", "env; echo ---; printenv OPENAI_API_KEY; printenv AWS_SECRET_ACCESS_KEY; echo end"},
			check: func(t *testing.T, res *Result, err error) {
				if err != nil {
					t.Fatalf("tool errored unexpectedly: %v", err)
				}
				out := string(res.Stdout)
				for _, s := range []string{"sk-live-DEADBEEFdeadbeef0123", "wJalrXUtnFEMI-should-not-leak", "OPENAI_API_KEY=", "AWS_SECRET_ACCESS_KEY="} {
					if strings.Contains(out, s) {
						t.Errorf("credential leaked into isolate: %q found in output", s)
					}
				}
			},
		},
		{
			name: "reads-credential-files",
			argv: []string{"sh", "-c", "cat ~/.aws/credentials 2>/dev/null; cat ~/.config/gcloud/credentials.db 2>/dev/null; cat ~/.netrc 2>/dev/null; echo scanned"},
			check: func(t *testing.T, res *Result, err error) {
				if err != nil {
					t.Fatalf("tool errored: %v", err)
				}
				// HOME points at the ephemeral scratch, so these paths do not exist: the tool scans and finds
				// nothing. The marker proves it ran to completion having found no credential file.
				if !strings.Contains(string(res.Stdout), "scanned") {
					t.Errorf("tool did not complete its scan: %s", res.Stdout)
				}
				if strings.Contains(string(res.Stdout), "aws_secret") || strings.Contains(string(res.Stdout), "password") {
					t.Errorf("a credential file was readable inside the isolate: %s", res.Stdout)
				}
			},
		},
		{
			name: "infinite-loop-cpu-bomb",
			argv: []string{"sh", "-c", "while :; do :; done"},
			check: func(t *testing.T, res *Result, err error) {
				if !errors.Is(err, ErrResourceBreach) {
					t.Errorf("CPU bomb not contained as a resource breach: %v", err)
				}
			},
		},
		{
			name: "output-flood",
			argv: []string{"sh", "-c", "yes THIS_IS_A_LOT_OF_OUTPUT | head -c 5000000"},
			check: func(t *testing.T, res *Result, err error) {
				if !errors.Is(err, ErrResourceBreach) {
					t.Errorf("output flood not contained: %v", err)
				}
			},
		},
	}

	for _, tc := range tools {
		t.Run(tc.name, func(t *testing.T) {
			spec := repoToolSpec()
			spec.NodeID = tc.name
			spec.Bounds.CPU = 1 * time.Second
			spec.Bounds.Wallclock = 6 * time.Second
			spec.Bounds.MaxOutput = 64 << 10
			res, err := fullCaps.Run(context.Background(), spec, Tool{Argv: tc.argv})
			tc.check(t, res, err)
		})
	}
}

// The "writes outside its working set" attack, on a host that cannot OS-enforce filesystem scope,
// fail-closes before the tool runs — the honest containment. The marker outside the working set is
// never created because the tool never executes.
func TestMaliciousRepoTool_WriteOutsideWorkingSet_FailsClosedWithoutFSScope(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "escaped")
	sb := New(NewSubprocessEnforcer(), WithAudit(&recordingSink{})) // honest caps: FilesystemScope=false

	spec := repoToolSpec() // RequireFilesystemScope = true
	_, err := sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c", "echo pwned > " + marker}})
	if !errors.Is(err, ErrIsolateUnavailable) {
		t.Fatalf("want fail-closed, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("tool wrote outside its working set — containment failed")
	}
}
