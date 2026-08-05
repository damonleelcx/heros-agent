package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ownership_fence_test.go holds the two P27 properties that erode by ADDITION rather than by breaking.
//
// Neither can be proved by exercising the code, because both are claims about code that must NOT exist.
// A test that calls something cannot notice a thing nobody wrote yet — so these read the source, which
// is the only place the absence is visible.

// TestTheTenantHeaderIsGoneAndStaysGone is FR16.
//
// 🔴 The header is DELETED rather than left inert, and this is what keeps it deleted. An ignored header
// that names authority is a loaded gun with the safety on: the next person to see it reads it as the
// isolation mechanism and makes it load-bearing, and at that point a request describes its own authority
// again — the exact thing ADR-008 Rule 2 forbids and the exact state P27 found the platform in.
//
// The fence covers the Go side and the console's forwarder. It deliberately allows the string inside a
// COMMENT, because the history of why it is gone is worth keeping and a fence that forbade the
// explanation would delete the reason along with the code.
func TestTheTenantHeaderIsGoneAndStaysGone(t *testing.T) {
	// 🔴 One implementation of the rule, not two.
	//
	// This test used to carry its own walker, its own exclusion list and its own comment-detection —
	// and `p27_fence_fixtures_test.go` needed the same rule to drill it against a checked-in broken
	// fixture (task 11.1). Two copies of a fence is how a fence half-erodes: somebody widens an
	// exclusion here, the drill over there keeps passing against its fixture, and the two disagree
	// about what the repository contains while both look green.
	//
	// So the detector lives in that file and this calls it. The drill proves it goes red; this proves
	// the tree is clean. Neither claim means much without the other.
	if offenders := headerFence.scan(t, repoRoot, false); len(offenders) > 0 {
		var lines []string
		for _, o := range offenders {
			lines = append(lines, o.String())
		}
		t.Fatalf("X-Console-Tenant is used as a VALUE at:\n  %s\n\n"+
			"P27 deleted this header rather than making the platform trust it. Trusting it costs one line "+
			"and lets any holder of the console's one credential name any tenant — a request describing "+
			"its own authority, which ADR-008 Rule 2 exists to forbid. Scope travels inside the "+
			"credential now: the console exchanges its session for a short-lived, tenant-scoped token at "+
			"POST /api/v1/token-exchange, and `auth` resolves the tenant from the thing the platform "+
			"verified.\n\n"+
			"If a tracing field is wanted, add one with a name that does not read as authority.",
			strings.Join(lines, "\n  "))
	}
}

// TestThereIsNoInterfaceThatChangesARunsOwner is FR19.
//
// Ownership immutability is not a property that breaks; it is a property somebody REMOVES by adding a
// convenient endpoint or an admin fix-up. A transfer moves billed usage between customers, and it does
// so silently, so the absence is worth a fence rather than a comment.
func TestThereIsNoInterfaceThatChangesARunsOwner(t *testing.T) {
	// Any statement that writes the ownership column on an EXISTING row. The insert paths use
	// `INSERT INTO run (... tenant_id ...)`, which this deliberately does not match.
	transfer := regexp.MustCompile(`(?i)UPDATE\s+(run|variant_spec|eval_run)\b[^;]*SET[^;]*tenant_id\s*=`)

	root := filepath.Join("..", "..", "internal")
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "ownership_fence_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		// The migration's own backfill would be legitimate; there is none, and if one is ever added it
		// belongs in SQL under db/migrations, which this walk does not cover.
		if transfer.MatchString(string(b)) {
			offenders = append(offenders, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("something updates a run's owning organization:\n  %s\n\n"+
			"A run's owner is written once, at creation, from the verified principal. Changing it moves "+
			"BILLED USAGE between customers, and it does so with no signal that anything happened.",
			strings.Join(offenders, "\n  "))
	}
}
